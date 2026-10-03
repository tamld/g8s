package offer

// offer_test.go — embed-bundle integrity through the package's public
// API: the binary ships the whole pull-bundle, so a broken embed or a
// malformed seed silently degrades `g8s offer init` for every sibling
// project. These checks pin the bundle's shape (the same checks the
// offer README documents) via Profiles/Seed/GateScript/Onboarding —
// the exact surface `g8s offer init` consumes.

import (
	"strings"
	"testing"
)

func TestProfiles_AllFourNonEmpty(t *testing.T) {
	profiles := Profiles()
	for _, want := range []string{"knowledge", "security", "infra", "utility"} {
		raw, ok := profiles[want]
		if !ok {
			t.Fatalf("Profiles() missing profile %q", want)
		}
		if len(strings.TrimSpace(raw)) == 0 {
			t.Errorf("profile %q is empty", want)
		}
	}
	if len(profiles) < 4 {
		t.Errorf("Profiles() returned %d entries, want >= 4", len(profiles))
	}
}

func TestSeed_KnownAndUnknown(t *testing.T) {
	for _, profile := range []string{"knowledge", "security", "infra", "utility"} {
		for _, kind := range []string{"trust", "lanes"} {
			raw, ok := Seed(profile, kind)
			if !ok {
				t.Fatalf("Seed(%s, %s) missing", profile, kind)
			}
			hasKey := false
			for _, line := range strings.Split(raw, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, ":") {
					hasKey = true
					break
				}
			}
			if !hasKey {
				t.Errorf("Seed(%s, %s) has no top-level keys — malformed?", profile, kind)
			}
		}
	}
	if _, ok := Seed("no-such-profile", "trust"); ok {
		t.Error("Seed() for an unknown profile must return ok=false")
	}
	if _, ok := Seed("knowledge", "no-such-kind"); ok {
		t.Error("Seed() for an unknown kind must return ok=false")
	}
}

func TestGateScripts_ShebangAndLookup(t *testing.T) {
	names := GateScripts()
	found := 0
	for _, want := range []string{
		"ci_spec_code_sync.sh",
		"ci_structure_sync.sh",
		"ci_link_integrity.sh",
	} {
		raw, ok := GateScript(want)
		if !ok {
			t.Fatalf("GateScript(%q) missing", want)
		}
		if !strings.HasPrefix(raw, "#!") {
			t.Errorf("GateScript(%q) has no shebang", want)
		}
		found++
	}
	if found != 3 || len(names) < 6 {
		t.Errorf("GateScripts() listed %d entries, want >= 6", len(names))
	}
	if _, ok := GateScript("no_such_script.sh"); ok {
		t.Error("GateScript() for an unknown name must return ok=false")
	}
}

func TestOnboarding_NonEmpty(t *testing.T) {
	if len(strings.TrimSpace(Onboarding())) == 0 {
		t.Fatal("Onboarding() is empty")
	}
}
