package offer

// offer_test.go — embed-bundle integrity: the binary ships the whole
// pull-bundle, so a broken embed or a malformed seed silently degrades
// `g8s offer init` for every sibling project. These checks pin the
// bundle's shape (the same checks the offer README documents).

import (
	"strings"
	"testing"
)

func TestBundleShape_Integrity(t *testing.T) {
	for _, required := range []string{
		"offer/ONBOARDING.md",
		"offer/README.md",
		"offer/profiles/knowledge.md",
		"offer/profiles/security.md",
		"offer/profiles/infra.md",
		"offer/profiles/utility.md",
	} {
		raw, err := bundleFS.ReadFile(required)
		if err != nil {
			t.Fatalf("embedded bundle missing %s: %v", required, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			t.Errorf("embedded %s is empty", required)
		}
	}
}

func TestBundleSeeds_ParseShape(t *testing.T) {
	// Every seed must be non-empty YAML-ish with at least one top-level
	// key line (structural check, not a full YAML parse — stdlib only).
	for _, kind := range []string{"lanes", "trust"} {
		for _, profile := range []string{"knowledge", "security", "infra", "utility"} {
			path := "offer/profiles/seeds/" + profile + "." + kind + ".yml"
			raw, err := bundleFS.ReadFile(path)
			if err != nil {
				t.Fatalf("embedded seed missing %s: %v", path, err)
			}
			hasKey := false
			for _, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, ":") {
					hasKey = true
					break
				}
			}
			if !hasKey {
				t.Errorf("seed %s has no top-level keys — malformed?", path)
			}
		}
	}
}

func TestBundleGateScripts_Embedded(t *testing.T) {
	for _, script := range []string{
		"tools/ci_spec_code_sync.sh",
		"tools/ci_spec_code_sync_test.sh",
		"tools/ci_structure_sync.sh",
		"tools/ci_structure_sync_test.sh",
		"tools/ci_link_integrity.sh",
		"tools/ci_link_integrity_test.sh",
	} {
		raw, err := bundleFS.ReadFile(script)
		if err != nil {
			t.Fatalf("embedded gate script missing %s: %v", script, err)
		}
		if !strings.HasPrefix(string(raw), "#!") {
			t.Errorf("embedded %s does not look like a script (no shebang)", script)
		}
	}
}
