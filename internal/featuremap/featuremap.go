// Package featuremap provides declarative end-to-end feature DAG loading, validation, and blast-radius routing.
package featuremap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultFeatureMapPath is the default repo path for the feature map configuration.
const DefaultFeatureMapPath = ".g8s/feature-map.yml"

// BlastRadius captures dependencies and direct consumers for impact routing.
type BlastRadius struct {
	DependsOn       []string `yaml:"depends_on" json:"depends_on"`
	DirectConsumers []string `yaml:"direct_consumers" json:"direct_consumers"`
}

// Supported feature map schema versions. Version 2.0 (DELTA-41) is a strict
// superset of 1.0: every 1.0 document still loads and validates unchanged.
const (
	SchemaV1 = "1.0"
	SchemaV2 = "2.0"
)

// Interfaces lists the exported contracts a feature owns (DELTA-41). Each entry
// must be unique across the whole map so that contract ownership is unambiguous.
type Interfaces struct {
	CLI           []string `yaml:"cli,omitempty" json:"cli,omitempty"`
	MCPTools      []string `yaml:"mcp_tools,omitempty" json:"mcp_tools,omitempty"`
	EnvelopeKinds []string `yaml:"envelope_kinds,omitempty" json:"envelope_kinds,omitempty"`
	GoPackages    []string `yaml:"go_packages,omitempty" json:"go_packages,omitempty"`
}

// FeatureNode represents a single functional feature entry in the feature map DAG.
type FeatureNode struct {
	ID            string   `yaml:"id" json:"id"`
	Title         string   `yaml:"title" json:"title"`
	PersonaStory  string   `yaml:"persona_story" json:"persona_story"`
	SRSReqs       []string `yaml:"srs_reqs" json:"srs_reqs"`
	OpenSpecDelta string   `yaml:"openspec_delta,omitempty" json:"openspec_delta,omitempty"`
	// OpenSpecDeltas (v2) lists every delta governing the feature. The legacy
	// single-valued OpenSpecDelta is kept as an alias and folded in by Normalize.
	OpenSpecDeltas []string `yaml:"openspec_deltas,omitempty" json:"openspec_deltas,omitempty"`
	CodePaths      []string `yaml:"code_paths" json:"code_paths"`
	// OwnedPaths (v2) is the feature's exclusive write territory. Entries are
	// globs; a leading `!` excludes a sub-region. Owned sets of two features
	// MUST NOT overlap and MUST NOT overlap any hotspot.
	OwnedPaths []string `yaml:"owned_paths,omitempty" json:"owned_paths,omitempty"`
	// SharedPaths (v2) declares hotspot paths the feature legitimately edits.
	// Every entry MUST be covered by a declared top-level hotspot.
	SharedPaths []string `yaml:"shared_paths,omitempty" json:"shared_paths,omitempty"`
	// CollisionDomain (v2) groups features that must never run in parallel
	// (for example two features sharing one Go package). Defaults to ID.
	CollisionDomain string      `yaml:"collision_domain,omitempty" json:"collision_domain,omitempty"`
	Interfaces      *Interfaces `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	Invariants      []string    `yaml:"invariants" json:"invariants"`
	VisibleTests    []string    `yaml:"visible_tests" json:"visible_tests"`
	HoldoutTests    []string    `yaml:"holdout_tests,omitempty" json:"holdout_tests,omitempty"`
	VerifierClass   string      `yaml:"verifier_class" json:"verifier_class"`
	BlastRadius     BlastRadius `yaml:"blast_radius" json:"blast_radius"`
}

// Domain returns the feature's collision domain, defaulting to its ID.
func (f *FeatureNode) Domain() string {
	if d := strings.TrimSpace(f.CollisionDomain); d != "" {
		return d
	}
	return f.ID
}

// OwnedSet returns the feature's exclusive path set. For v1 nodes without
// owned_paths it falls back to code_paths so collision analysis still works.
func (f *FeatureNode) OwnedSet() PathSet {
	if len(f.OwnedPaths) > 0 {
		return NewPathSet(f.OwnedPaths)
	}
	return NewPathSet(f.CodePaths)
}

// Hotspot policies (DELTA-41).
const (
	PolicySerialize   = "serialize"
	PolicyAppendOnly  = "append-only"
	PolicyOwnerPrefix = "owner:"
)

// Hotspot is a shared, multi-writer region of the repository with an explicit
// merge policy: `serialize`, `append-only`, or `owner:<FEATURE-ID>`.
type Hotspot struct {
	ID        string   `yaml:"id" json:"id"`
	Path      string   `yaml:"path,omitempty" json:"-"`
	Paths     []string `yaml:"paths" json:"paths"`
	Policy    string   `yaml:"policy" json:"policy"`
	Rationale string   `yaml:"rationale,omitempty" json:"rationale,omitempty"`
}

// Set returns the hotspot's path set.
func (h *Hotspot) Set() PathSet { return NewPathSet(h.Paths) }

// Owner returns the owning feature ID for `owner:<FEAT>` policies.
func (h *Hotspot) Owner() string {
	if strings.HasPrefix(h.Policy, PolicyOwnerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(h.Policy, PolicyOwnerPrefix))
	}
	return ""
}

// FeatureMap represents the top-level declarative feature map schema.
type FeatureMap struct {
	SchemaVersion   string        `yaml:"schema_version" json:"schema_version"`
	InheritsSpecKit string        `yaml:"inherits_spec_kit,omitempty" json:"inherits_spec_kit,omitempty"`
	Hotspots        []Hotspot     `yaml:"hotspots,omitempty" json:"hotspots,omitempty"`
	Features        []FeatureNode `yaml:"features" json:"features"`
}

// IsV2 reports whether the map declares schema 2.0.
func (fm *FeatureMap) IsV2() bool { return fm != nil && fm.SchemaVersion == SchemaV2 }

// Normalize folds legacy aliases into their v2 forms: openspec_delta joins
// openspec_deltas (and is back-filled from it), and hotspot `path` joins `paths`.
func (fm *FeatureMap) Normalize() {
	if fm == nil {
		return
	}
	for i := range fm.Features {
		f := &fm.Features[i]
		var deltas []string
		seen := make(map[string]bool)
		add := func(d string) {
			d = strings.TrimSpace(d)
			if d != "" && !seen[d] {
				seen[d] = true
				deltas = append(deltas, d)
			}
		}
		add(f.OpenSpecDelta)
		for _, d := range f.OpenSpecDeltas {
			add(d)
		}
		f.OpenSpecDeltas = deltas
		if strings.TrimSpace(f.OpenSpecDelta) == "" && len(deltas) > 0 {
			f.OpenSpecDelta = deltas[0]
		}
	}
	for i := range fm.Hotspots {
		h := &fm.Hotspots[i]
		if p := strings.TrimSpace(h.Path); p != "" {
			h.Paths = append([]string{p}, h.Paths...)
			h.Path = ""
		}
	}
}

// ValidationReport contains the outcome of validating a FeatureMap against invariants and codebase structure.
type ValidationReport struct {
	Valid                bool     `json:"valid"`
	SchemaVersion        string   `json:"schema_version,omitempty"`
	Strict               bool     `json:"strict,omitempty"`
	FeatureCount         int      `json:"feature_count"`
	HotspotCount         int      `json:"hotspot_count,omitempty"`
	Errors               []string `json:"errors,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
	OrphanPaths          []string `json:"orphan_paths,omitempty"`
	UnverifiedInvariants []string `json:"unverified_invariants,omitempty"`
	// Ownership coverage statistics (v2 only, populated when a repo root is given).
	Coverage *OwnershipCoverage `json:"coverage,omitempty"`
}

// OwnershipCoverage summarises how tracked files partition into owned and hotspot regions.
type OwnershipCoverage struct {
	TrackedFiles int      `json:"tracked_files"`
	OwnedFiles   int      `json:"owned_files"`
	HotspotFiles int      `json:"hotspot_files"`
	OrphanFiles  []string `json:"orphan_files,omitempty"`
}

// ValidateOptions tunes ValidateWithOptions.
type ValidateOptions struct {
	// Strict promotes orphan-file warnings to errors.
	Strict bool
	// TrackedFiles overrides tracked-file discovery (tests). When nil the files
	// are discovered with `git ls-files`, falling back to a filesystem walk.
	TrackedFiles []string
}

// Load reads and parses a FeatureMap YAML file from the given path.
func Load(path string) (*FeatureMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read feature map: %w", err)
	}
	return Parse(data)
}

// Parse unmarshals FeatureMap YAML data.
func Parse(data []byte) (*FeatureMap, error) {
	var fm FeatureMap
	if err := yaml.Unmarshal(data, &fm); err != nil {
		return nil, fmt.Errorf("parse feature map yaml: %w", err)
	}
	fm.Normalize()
	return &fm, nil
}

// GetFeature returns a pointer to the FeatureNode with the specified ID and true, or nil and false if not found.
func (fm *FeatureMap) GetFeature(id string) (*FeatureNode, bool) {
	if fm == nil {
		return nil, false
	}
	for i := range fm.Features {
		if fm.Features[i].ID == id {
			return &fm.Features[i], true
		}
	}
	return nil, false
}

// isCommandTest checks if a visible test entry is a command line rather than a path/glob.
func isCommandTest(t string) bool {
	trimmed := strings.TrimSpace(t)
	prefixes := []string{"go test", "npm ", "node ", "bash ", "sh ", "pytest"}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// DeriveAllowedPaths returns deduplicated CodePaths plus any entries in VisibleTests
// that are file/glob paths (not command lines), while strictly excluding any path
// in HoldoutTests or containing "holdout".
func (fm *FeatureMap) DeriveAllowedPaths(featureID string) ([]string, error) {
	f, ok := fm.GetFeature(featureID)
	if !ok {
		return nil, fmt.Errorf("feature not found: %s", featureID)
	}

	holdoutSet := make(map[string]bool)
	for _, ht := range f.HoldoutTests {
		holdoutSet[filepath.ToSlash(filepath.Clean(ht))] = true
	}

	isHoldout := func(p string) bool {
		clean := filepath.ToSlash(filepath.Clean(p))
		if holdoutSet[clean] {
			return true
		}
		if strings.Contains(strings.ToLower(clean), "holdout") {
			return true
		}
		return false
	}

	seen := make(map[string]bool)
	var allowed []string

	addPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || isHoldout(p) {
			return
		}
		clean := filepath.ToSlash(filepath.Clean(p))
		// retain /** glob convention if present in original
		if strings.HasSuffix(p, "/**") && !strings.HasSuffix(clean, "/**") {
			clean = clean + "/**"
		}
		if !seen[clean] {
			seen[clean] = true
			allowed = append(allowed, clean)
		}
	}

	for _, cp := range f.CodePaths {
		addPath(cp)
	}

	for _, vt := range f.VisibleTests {
		if isCommandTest(vt) {
			continue
		}
		addPath(vt)
	}

	return allowed, nil
}

// ResolveTransitiveFeatures returns a slice starting with the target FeatureNode
// followed by all transitive BlastRadius.DirectConsumers in deterministic BFS order.
func (fm *FeatureMap) ResolveTransitiveFeatures(featureID string) ([]FeatureNode, error) {
	root, ok := fm.GetFeature(featureID)
	if !ok {
		return nil, fmt.Errorf("feature not found: %s", featureID)
	}

	var result []FeatureNode
	result = append(result, *root)

	visited := make(map[string]bool)
	visited[featureID] = true

	queue := []string{featureID}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		currNode, ok := fm.GetFeature(currID)
		if !ok {
			continue
		}

		for _, consumerID := range currNode.BlastRadius.DirectConsumers {
			if !visited[consumerID] {
				visited[consumerID] = true
				if consumerNode, exists := fm.GetFeature(consumerID); exists {
					result = append(result, *consumerNode)
					queue = append(queue, consumerID)
				}
			}
		}
	}

	return result, nil
}

// Validate checks the feature map structure, graph acyclicity, and optional codebase anchors under repoRoot.
func Validate(fm *FeatureMap, repoRoot string) ValidationReport {
	return ValidateWithOptions(fm, repoRoot, ValidateOptions{})
}

// ValidateWithOptions is Validate with explicit options (strict orphan-file mode,
// injected tracked-file list). Schema 2.0 maps additionally get DELTA-41
// ownership, hotspot, interface, and orphan-file validation.
func ValidateWithOptions(fm *FeatureMap, repoRoot string, opts ValidateOptions) ValidationReport {
	var report ValidationReport
	report.Strict = opts.Strict

	if fm == nil {
		report.Errors = append(report.Errors, "feature map is nil")
		return report
	}
	report.SchemaVersion = fm.SchemaVersion

	if fm.SchemaVersion != SchemaV1 && fm.SchemaVersion != SchemaV2 {
		report.Errors = append(report.Errors, fmt.Sprintf("unsupported schema_version: %q (expected %s or %s)", fm.SchemaVersion, SchemaV1, SchemaV2))
	}

	if len(fm.Features) == 0 {
		report.Errors = append(report.Errors, "features list is empty")
		return report
	}

	report.FeatureCount = len(fm.Features)
	featureMapByID := make(map[string]*FeatureNode, len(fm.Features))
	seenIDs := make(map[string]bool, len(fm.Features))

	for i := range fm.Features {
		f := &fm.Features[i]
		if strings.TrimSpace(f.ID) == "" {
			report.Errors = append(report.Errors, fmt.Sprintf("feature at index %d: missing id", i))
			continue
		}
		if seenIDs[f.ID] {
			report.Errors = append(report.Errors, fmt.Sprintf("duplicate feature id: %s", f.ID))
		}
		seenIDs[f.ID] = true
		featureMapByID[f.ID] = f

		if strings.TrimSpace(f.Title) == "" {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing title", f.ID))
		}
		if strings.TrimSpace(f.PersonaStory) == "" {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing persona_story", f.ID))
		}
		if len(f.SRSReqs) == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing srs_reqs", f.ID))
		}
		if len(f.CodePaths) == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing code_paths", f.ID))
		}
		if len(f.Invariants) == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing invariants", f.ID))
		}
		if len(f.VisibleTests) == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing visible_tests", f.ID))
		}
		if strings.TrimSpace(f.VerifierClass) == "" {
			report.Errors = append(report.Errors, fmt.Sprintf("feature %s: missing verifier_class", f.ID))
		}

		if len(f.Invariants) > 0 && len(f.VisibleTests) == 0 {
			report.UnverifiedInvariants = append(report.UnverifiedInvariants, f.ID)
		}
	}

	// Validate references and bidirectional edge symmetry in blast radius (#665)
	hasString := func(slice []string, target string) bool {
		for _, item := range slice {
			if item == target {
				return true
			}
		}
		return false
	}
	for _, f := range fm.Features {
		for _, dep := range f.BlastRadius.DependsOn {
			depNode, ok := featureMapByID[dep]
			if !ok {
				report.Errors = append(report.Errors, fmt.Sprintf("feature %s depends_on unknown feature: %s", f.ID, dep))
				continue
			}
			if !hasString(depNode.BlastRadius.DirectConsumers, f.ID) {
				report.Errors = append(report.Errors, fmt.Sprintf("asymmetric blast_radius edge: %s depends_on %s, but %s.direct_consumers omits %s", f.ID, dep, dep, f.ID))
			}
		}
		for _, cons := range f.BlastRadius.DirectConsumers {
			consNode, ok := featureMapByID[cons]
			if !ok {
				report.Errors = append(report.Errors, fmt.Sprintf("feature %s direct_consumers unknown feature: %s", f.ID, cons))
				continue
			}
			if !hasString(consNode.BlastRadius.DependsOn, f.ID) {
				report.Errors = append(report.Errors, fmt.Sprintf("asymmetric blast_radius edge: %s lists %s in direct_consumers, but %s.depends_on omits %s", f.ID, cons, cons, f.ID))
			}
		}
	}

	// Cycle detection for DependsOn and DirectConsumers
	checkCycles := func(graphName string, getEdges func(node *FeatureNode) []string) {
		visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
		var path []string

		var dfs func(id string) bool
		dfs = func(id string) bool {
			visited[id] = 1
			path = append(path, id)

			node := featureMapByID[id]
			if node != nil {
				for _, next := range getEdges(node) {
					if visited[next] == 1 {
						// Cycle detected
						cyclePath := append(path, next)
						report.Errors = append(report.Errors, fmt.Sprintf("cycle detected in %s: %s", graphName, strings.Join(cyclePath, " -> ")))
						return true
					}
					if visited[next] == 0 {
						if dfs(next) {
							return true
						}
					}
				}
			}

			path = path[:len(path)-1]
			visited[id] = 2
			return false
		}

		for _, f := range fm.Features {
			if visited[f.ID] == 0 {
				dfs(f.ID)
			}
		}
	}

	checkCycles("depends_on", func(n *FeatureNode) []string { return n.BlastRadius.DependsOn })
	checkCycles("direct_consumers", func(n *FeatureNode) []string { return n.BlastRadius.DirectConsumers })

	// Check codebase anchors if repoRoot is provided
	if repoRoot != "" {
		cleanRepoRoot := filepath.Clean(repoRoot)
		absRepoRoot, err := filepath.Abs(cleanRepoRoot)
		if err != nil {
			absRepoRoot = cleanRepoRoot
		}

		// 1. Code paths existence check
		for _, f := range fm.Features {
			for _, cp := range f.CodePaths {
				cpClean := filepath.Clean(cp)
				target := filepath.Join(absRepoRoot, cpClean)
				rel, err := filepath.Rel(absRepoRoot, target)
				if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
					report.Errors = append(report.Errors, fmt.Sprintf("feature %s: code_path %q escapes repoRoot", f.ID, cp))
					continue
				}

				if strings.HasSuffix(cp, "/**") || strings.HasSuffix(cp, "/*") {
					prefix := strings.TrimSuffix(strings.TrimSuffix(cp, "/**"), "/*")
					prefixTarget := filepath.Join(absRepoRoot, filepath.Clean(prefix))
					if _, err := os.Stat(prefixTarget); os.IsNotExist(err) {
						report.Errors = append(report.Errors, fmt.Sprintf("feature %s: directory prefix for code_path %q does not exist: %s", f.ID, cp, prefix))
					}
				} else if strings.Contains(cp, "*") {
					matches, err := filepath.Glob(target)
					if err != nil || len(matches) == 0 {
						report.Errors = append(report.Errors, fmt.Sprintf("feature %s: glob code_path %q matched no files", f.ID, cp))
					}
				} else {
					if _, err := os.Stat(target); os.IsNotExist(err) {
						report.Errors = append(report.Errors, fmt.Sprintf("feature %s: code_path %q does not exist", f.ID, cp))
					}
				}
			}
		}

		// 2. SRS requirements validation
		srsPath := filepath.Join(absRepoRoot, "docs", "SRS.md")
		if srsBytes, err := os.ReadFile(srsPath); err == nil {
			srsContent := string(srsBytes)
			for _, f := range fm.Features {
				for _, req := range f.SRSReqs {
					if !strings.Contains(srsContent, req) {
						report.Errors = append(report.Errors, fmt.Sprintf("feature %s: srs_req %q not found in docs/SRS.md", f.ID, req))
					}
				}
			}
		}

		// 3. OpenSpec delta validation
		manifestPath := filepath.Join(absRepoRoot, "manifest.json")
		if manifestBytes, err := os.ReadFile(manifestPath); err == nil {
			manifestContent := string(manifestBytes)
			for _, f := range fm.Features {
				deltas := f.OpenSpecDeltas
				if len(deltas) == 0 && f.OpenSpecDelta != "" {
					deltas = []string{f.OpenSpecDelta}
				}
				for _, d := range deltas {
					if !strings.Contains(manifestContent, d) {
						report.Errors = append(report.Errors, fmt.Sprintf("feature %s: openspec_delta %q not found in manifest.json", f.ID, d))
					}
				}
			}
		}

		// 4. Orphan internal package detection
		internalDir := filepath.Join(absRepoRoot, "internal")
		if fi, err := os.Stat(internalDir); err == nil && fi.IsDir() {
			entries, err := os.ReadDir(internalDir)
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					pkgName := entry.Name()
					pkgRel := filepath.ToSlash(filepath.Join("internal", pkgName))

					covered := false
					for _, f := range fm.Features {
						for _, cp := range f.CodePaths {
							cpSlash := filepath.ToSlash(filepath.Clean(cp))
							if cpSlash == "internal" || cpSlash == "internal/**" || cpSlash == "internal/*" {
								covered = true
								break
							}
							prefix := strings.TrimSuffix(strings.TrimSuffix(cpSlash, "/**"), "/*")
							if prefix == pkgRel || strings.HasPrefix(pkgRel, prefix+"/") {
								covered = true
								break
							}
							if strings.HasPrefix(prefix, pkgRel+"/") {
								covered = true
								break
							}
						}
						if covered {
							break
						}
					}

					if !covered {
						report.OrphanPaths = append(report.OrphanPaths, pkgRel)
						report.Errors = append(report.Errors, fmt.Sprintf("orphan package detected: %s is not covered by any feature", pkgRel))
					}
				}
			}
		}
	}

	if fm.IsV2() {
		validateV2(fm, repoRoot, opts, &report)
	}

	sort.Strings(report.Errors)
	sort.Strings(report.Warnings)
	sort.Strings(report.OrphanPaths)
	sort.Strings(report.UnverifiedInvariants)

	report.Valid = len(report.Errors) == 0 && len(report.OrphanPaths) == 0 && len(report.UnverifiedInvariants) == 0
	return report
}
