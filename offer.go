// Package offer carries the pull-bundle (profiles, onboarding, gate
// scripts) inside the g8s binary — `g8s offer init` scaffolds any project
// deterministically from the embedded content. Distribution model:
// pull-based (ADR-0024 addendum) — projects adopt at their own pace.
package offer

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed offer/profiles/*.md offer/ONBOARDING.md offer/README.md
//go:embed offer/profiles/seeds/*.trust.yml offer/profiles/seeds/*.lanes.yml
//go:embed tools/ci_spec_code_sync.sh tools/ci_spec_code_sync_test.sh
//go:embed tools/ci_structure_sync.sh tools/ci_structure_sync_test.sh
//go:embed tools/ci_link_integrity.sh tools/ci_link_integrity_test.sh
var bundleFS embed.FS

// Profiles returns the embedded profile markdown files keyed by profile
// name (knowledge, security, infra, utility).
func Profiles() map[string]string {
	out := map[string]string{}
	entries, err := fs.ReadDir(bundleFS, "offer/profiles")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(bundleFS, "offer/profiles/"+e.Name())
		if err == nil {
			out[strings.TrimSuffix(e.Name(), ".md")] = string(raw)
		}
	}
	return out
}

// Seed returns the machine seed (yaml) for a profile and kind
// (kind: "trust" or "lanes").
func Seed(profile, kind string) (string, bool) {
	raw, err := fs.ReadFile(bundleFS, "offer/profiles/seeds/"+profile+"."+kind+".yml")
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// GateScript returns an embedded gate script or test by its tools/ name
// (e.g. "ci_spec_code_sync.sh").
func GateScript(name string) (string, bool) {
	raw, err := fs.ReadFile(bundleFS, "tools/"+name)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// Onboarding returns the self-service onboarding guide.
func Onboarding() string {
	raw, _ := fs.ReadFile(bundleFS, "offer/ONBOARDING.md")
	return string(raw)
}

// GateScripts lists the embedded gate script names (scripts + tests).
func GateScripts() []string {
	var out []string
	entries, err := fs.ReadDir(bundleFS, "tools")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
