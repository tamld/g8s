package featuremap

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
)

// validateV2 applies the DELTA-41 ownership contract:
//
//  1. every feature declares non-empty owned_paths;
//  2. owned_paths of two features never overlap;
//  3. owned_paths never overlap a hotspot, and hotspots never overlap each other;
//  4. hotspots carry a valid merge policy (serialize | append-only | owner:<FEAT>);
//  5. shared_paths are covered by a declared hotspot;
//  6. interface contracts (CLI, MCP tools, envelope kinds, Go packages) are unique;
//  7. every tracked file is matched by exactly one owned set or by a hotspot
//     (orphans are warnings, errors under Strict).
func validateV2(fm *FeatureMap, repoRoot string, opts ValidateOptions, report *ValidationReport) {
	report.HotspotCount = len(fm.Hotspots)
	featureIDs := make(map[string]bool, len(fm.Features))
	for _, f := range fm.Features {
		featureIDs[f.ID] = true
	}

	validatePatterns := func(owner, field string, entries []string) {
		for _, e := range entries {
			raw := strings.TrimPrefix(strings.TrimSpace(e), "!")
			p := NormalizePattern(raw)
			switch {
			case p == "":
				report.Errors = append(report.Errors, fmt.Sprintf("%s: empty %s entry", owner, field))
			case strings.HasPrefix(p, "/") || filepath.IsAbs(raw):
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %s entry %q must be repo-relative", owner, field, e))
			case p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../"):
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %s entry %q escapes repo root", owner, field, e))
			}
		}
	}

	// Hotspots: identity, policy, paths.
	hotspotSets := make([]PathSet, len(fm.Hotspots))
	seenHotspot := make(map[string]bool)
	for i := range fm.Hotspots {
		h := &fm.Hotspots[i]
		label := fmt.Sprintf("hotspot %s", h.ID)
		if strings.TrimSpace(h.ID) == "" {
			label = fmt.Sprintf("hotspot at index %d", i)
			report.Errors = append(report.Errors, fmt.Sprintf("%s: missing id", label))
		} else if seenHotspot[h.ID] {
			report.Errors = append(report.Errors, fmt.Sprintf("duplicate hotspot id: %s", h.ID))
		}
		seenHotspot[h.ID] = true
		validatePatterns(label, "paths", h.Paths)
		hotspotSets[i] = h.Set()
		if hotspotSets[i].Empty() {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: missing paths", label))
		}
		switch {
		case h.Policy == PolicySerialize || h.Policy == PolicyAppendOnly:
		case strings.HasPrefix(h.Policy, PolicyOwnerPrefix):
			owner := h.Owner()
			if !featureIDs[owner] {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: policy %q references unknown feature %q", label, h.Policy, owner))
			}
		default:
			report.Errors = append(report.Errors, fmt.Sprintf("%s: invalid policy %q (expected %s, %s, or %s<FEATURE-ID>)", label, h.Policy, PolicySerialize, PolicyAppendOnly, PolicyOwnerPrefix))
		}
	}
	for i := range fm.Hotspots {
		for j := i + 1; j < len(fm.Hotspots); j++ {
			if a, b, ok := hotspotSets[i].OverlapsSet(hotspotSets[j]); ok {
				report.Errors = append(report.Errors, fmt.Sprintf("hotspot overlap: %s %q intersects %s %q", fm.Hotspots[i].ID, a, fm.Hotspots[j].ID, b))
			}
		}
	}

	// Features: owned/shared paths and interfaces.
	ownedSets := make([]PathSet, len(fm.Features))
	type ifaceKey struct{ kind, name string }
	ifaceOwner := make(map[ifaceKey]string)
	for i := range fm.Features {
		f := &fm.Features[i]
		label := fmt.Sprintf("feature %s", f.ID)
		validatePatterns(label, "owned_paths", f.OwnedPaths)
		validatePatterns(label, "shared_paths", f.SharedPaths)
		ownedSets[i] = NewPathSet(f.OwnedPaths)
		if ownedSets[i].Empty() {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: missing owned_paths (required by schema %s)", label, SchemaV2))
		}
		for _, sp := range f.SharedPaths {
			sp = strings.TrimSpace(sp)
			if sp == "" || strings.HasPrefix(sp, "!") {
				continue
			}
			covered := false
			for k := range hotspotSets {
				if hotspotSets[k].CoversPattern(sp) {
					covered = true
					break
				}
			}
			if !covered {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: shared_path %q does not reference a declared hotspot", label, sp))
			}
		}
		if f.Interfaces != nil {
			check := func(kind string, names []string) {
				for _, n := range names {
					n = strings.TrimSpace(n)
					if n == "" {
						report.Errors = append(report.Errors, fmt.Sprintf("%s: empty interfaces.%s entry", label, kind))
						continue
					}
					key := ifaceKey{kind, n}
					if prev, ok := ifaceOwner[key]; ok && prev != f.ID {
						report.Errors = append(report.Errors, fmt.Sprintf("interface collision: interfaces.%s %q declared by both %s and %s", kind, n, prev, f.ID))
						continue
					}
					ifaceOwner[key] = f.ID
				}
			}
			check("cli", f.Interfaces.CLI)
			check("mcp_tools", f.Interfaces.MCPTools)
			check("envelope_kinds", f.Interfaces.EnvelopeKinds)
			check("go_packages", f.Interfaces.GoPackages)
			if repoRoot != "" {
				for _, pkg := range f.Interfaces.GoPackages {
					pkg = strings.TrimSpace(pkg)
					if pkg == "" {
						continue
					}
					if fi, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(pkg))); err != nil || !fi.IsDir() {
						report.Errors = append(report.Errors, fmt.Sprintf("%s: interfaces.go_packages %q is not a directory in repo", label, pkg))
					}
				}
			}
		}
	}

	for i := range fm.Features {
		for j := i + 1; j < len(fm.Features); j++ {
			if a, b, ok := ownedSets[i].OverlapsSet(ownedSets[j]); ok {
				report.Errors = append(report.Errors, fmt.Sprintf("owned_paths overlap: %s %q intersects %s %q", fm.Features[i].ID, a, fm.Features[j].ID, b))
			}
		}
		for k := range fm.Hotspots {
			if a, b, ok := ownedSets[i].OverlapsSet(hotspotSets[k]); ok {
				report.Errors = append(report.Errors, fmt.Sprintf("owned_paths of %s %q overlap hotspot %s %q (exclude hotspot paths with '!')", fm.Features[i].ID, a, fm.Hotspots[k].ID, b))
			}
		}
	}

	// Bidirectional CLI surface parity, applied only to the g8s root feature map
	// (identified by FEAT-PLATFORM-CLI-01 plus the root cmd/g8s/main.go) so that
	// sibling repositories and synthetic fixtures declare their own CLI surface (#678).
	if repoRoot != "" && featureIDs["FEAT-PLATFORM-CLI-01"] {
		if _, err := os.Stat(filepath.Join(repoRoot, "cmd", "g8s", "main.go")); err == nil {
			for key, owner := range ifaceOwner {
				if key.kind == "cli" && !cli.IsRegisteredCommand(key.name) {
					report.Errors = append(report.Errors, fmt.Sprintf("feature %s: interfaces.cli %q is not registered in internal/cli.CanonicalCommands()", owner, key.name))
				}
			}
			if opts.Strict && len(fm.Features) >= 15 {
				for _, cmd := range cli.CanonicalCommands() {
					if _, ok := ifaceOwner[ifaceKey{"cli", cmd}]; !ok {
						report.Errors = append(report.Errors, fmt.Sprintf("unbound CLI command %q in internal/cli.CanonicalCommands() is not claimed by any feature interfaces.cli", cmd))
					}
				}
			}
		}
	}

	if repoRoot == "" && opts.TrackedFiles == nil {
		return
	}

	files := opts.TrackedFiles
	if files == nil {
		var err error
		files, err = ListTrackedFiles(repoRoot)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("list tracked files: %v", err))
			return
		}
	}

	cov := &OwnershipCoverage{TrackedFiles: len(files)}
	for _, file := range files {
		file = NormalizePattern(file)
		if file == "" {
			continue
		}
		owned := false
		for i := range ownedSets {
			if ownedSets[i].Match(file) {
				owned = true
				break
			}
		}
		if owned {
			cov.OwnedFiles++
			continue
		}
		inHotspot := false
		for k := range hotspotSets {
			if hotspotSets[k].Match(file) {
				inHotspot = true
				break
			}
		}
		if inHotspot {
			cov.HotspotFiles++
			continue
		}
		cov.OrphanFiles = append(cov.OrphanFiles, file)
	}
	sort.Strings(cov.OrphanFiles)
	report.Coverage = cov
	for _, o := range cov.OrphanFiles {
		msg := fmt.Sprintf("orphan file: %s is not matched by any owned_paths or hotspot", o)
		if opts.Strict {
			report.Errors = append(report.Errors, msg)
		} else {
			report.Warnings = append(report.Warnings, msg)
		}
	}
}

// ListTrackedFiles returns repo-relative, slash-separated tracked files under
// repoRoot using `git ls-files`. When repoRoot is not a git work tree it falls
// back to a filesystem walk that skips `.git` directories.
func ListTrackedFiles(repoRoot string) ([]string, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "ls-files", "-z")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil {
		var files []string
		for _, f := range strings.Split(stdout.String(), "\x00") {
			if f = strings.TrimSpace(f); f != "" {
				files = append(files, filepath.ToSlash(f))
			}
		}
		sort.Strings(files)
		return files, nil
	}

	var files []string
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
