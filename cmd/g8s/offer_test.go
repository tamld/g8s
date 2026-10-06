package main

// #420 distribution model: `g8s offer` — the pull-bundle made deterministic.
// RED-first: these tests fail (runOffer undefined) before implementation.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/lane"
)

func TestOfferInitScaffoldsFromProfile(t *testing.T) {
	dir := t.TempDir()
	cmd := execOffer(t, dir, "init", "--profile", "security")
	if cmd.exitCode != 0 {
		t.Fatalf("offer init exit = %d, output:\n%s", cmd.exitCode, cmd.output)
	}
	for _, f := range []string{".g8s/trust-boundaries.yml", ".g8s/lane-bundles.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("scaffold missing %s: %v", f, err)
		}
	}
	for _, f := range []string{
		"tools/ci_spec_code_sync.sh", "tools/ci_structure_sync.sh", "tools/ci_link_integrity.sh",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("gate script missing %s: %v", f, err)
		}
	}
}

func TestOfferInitSecuritySeedContainsDenyAll(t *testing.T) {
	dir := t.TempDir()
	cmd := execOffer(t, dir, "init", "--profile", "security")
	if cmd.exitCode != 0 {
		t.Fatalf("init exit %d", cmd.exitCode)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".g8s/trust-boundaries.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"**"`) {
		t.Fatalf("security profile must seed deny-all boundary, got:\n%s", raw)
	}
}

func TestOfferInitRefusesExistingScaffold(t *testing.T) {
	dir := t.TempDir()
	execOffer(t, dir, "init", "--profile", "utility")
	cmd := execOffer(t, dir, "init", "--profile", "utility")
	if cmd.exitCode != 2 {
		t.Fatalf("re-init without --force must exit 2 (usage), got %d", cmd.exitCode)
	}
	if !strings.Contains(cmd.output, "already") {
		t.Fatalf("expected 'already scaffolded' message, got:\n%s", cmd.output)
	}
}

func TestOfferInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	execOffer(t, dir, "init", "--profile", "knowledge")
	cmd := execOffer(t, dir, "init", "--profile", "knowledge", "--force")
	if cmd.exitCode != 0 {
		t.Fatalf("--force re-init must succeed, got %d:\n%s", cmd.exitCode, cmd.output)
	}
}

func TestOfferInitUnknownProfileExit2(t *testing.T) {
	cmd := execOffer(t, t.TempDir(), "init", "--profile", "blockchain")
	if cmd.exitCode != 2 {
		t.Fatalf("unknown profile must exit 2 (usage), got %d", cmd.exitCode)
	}
}

func TestOfferCheckRunsGates(t *testing.T) {
	dir := t.TempDir()
	execOffer(t, dir, "init", "--profile", "utility")
	// a clean utility scaffold has no spec dir + no markers — check must
	// degrade gracefully (the gates skip absent surfaces) and exit 0.
	cmd := execOffer(t, dir, "check")
	if cmd.exitCode != 0 {
		t.Fatalf("offer check on clean scaffold must exit 0, got %d:\n%s", cmd.exitCode, cmd.output)
	}
}

func TestOfferVersionPrintsBundleVersion(t *testing.T) {
	cmd := execOffer(t, t.TempDir(), "version")
	if cmd.exitCode != 0 || !strings.Contains(cmd.output, "offer") {
		t.Fatalf("offer version must print the bundle version, got %d:\n%s", cmd.exitCode, cmd.output)
	}
}

// execOffer runs the built g8s binary with `offer` args inside dir.
type offerResult struct {
	exitCode int
	output   string
}

func execOffer(t *testing.T, dir string, args ...string) offerResult {
	t.Helper()
	bin := buildG8sBinary(t)
	cmd := exec.Command(bin, append([]string{"offer"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return offerResult{exitCode: code, output: string(out)}
}

// TestOfferKnowledgeEffortClassesSeed_Parses validates that the knowledge profile
// effort-classes seed parses with ParseEffortClasses and contains the pilot-proven comment,
// docs->low, test->medium, and unregistered floor medium.
func TestOfferKnowledgeEffortClassesSeed_Parses(t *testing.T) {
	if !strings.Contains(knowledgeEffortClassesSeed, "pilot-proven") {
		t.Errorf("knowledge effort classes seed must include 'pilot-proven' comment")
	}

	ec, err := lane.ParseEffortClasses([]byte(knowledgeEffortClassesSeed))
	if err != nil {
		t.Fatalf("ParseEffortClasses failed on knowledgeEffortClassesSeed: %v", err)
	}

	cls, eff := ec.ResolveClass([]string{"docs/intro.md"})
	if cls != "docs" || eff != config.EffortLow {
		t.Errorf("docs/intro.md = (%q, %q), want (docs, low)", cls, eff)
	}

	cls, eff = ec.ResolveClass([]string{"internal/pkg/foo_test.go"})
	if cls != "test" || eff != config.EffortMedium {
		t.Errorf("foo_test.go = (%q, %q), want (test, medium)", cls, eff)
	}

	cls, eff = ec.ResolveClass([]string{"main.go"})
	if cls != lane.UnregisteredClassName || eff != config.EffortMedium {
		t.Errorf("main.go = (%q, %q), want (unregistered, medium)", cls, eff)
	}
}

// TestOfferInitKnowledgeProfile_ScaffoldsEffortClasses asserts that `g8s offer init --profile knowledge`
// writes .g8s/effort-classes.yml with mode 0o600 and includes it in the scaffold list.
func TestOfferInitKnowledgeProfile_ScaffoldsEffortClasses(t *testing.T) {
	dir := t.TempDir()
	res := execOffer(t, dir, "init", "--profile", "knowledge", "--json")
	if res.exitCode != 0 {
		t.Fatalf("offer init exit = %d, output:\n%s", res.exitCode, res.output)
	}

	targetFile := filepath.Join(dir, ".g8s", "effort-classes.yml")
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("expected .g8s/effort-classes.yml to be created: %v", err)
	}
	// POSIX file modes are not honored on Windows (os.Stat reports 0666 for
	// any created file) — the 0600 assertion is darwin/linux semantics only.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf(".g8s/effort-classes.yml permissions = %#o, want 0600", perm)
		}
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read scaffolded effort-classes.yml: %v", err)
	}
	if _, err := lane.ParseEffortClasses(data); err != nil {
		t.Fatalf("scaffolded effort-classes.yml failed to parse: %v", err)
	}

	var env cli.Envelope
	if err := json.Unmarshal([]byte(res.output), &env); err != nil {
		t.Fatalf("failed to parse offer init json response: %v\nraw: %s", err, res.output)
	}
	dataMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("envelope Data is not a map: %v", env.Data)
	}
	scaffoldList, ok := dataMap["scaffold"].([]any)
	if !ok {
		t.Fatalf("envelope Data.scaffold is not an array: %v", dataMap["scaffold"])
	}
	found := false
	for _, item := range scaffoldList {
		if s, ok := item.(string); ok && s == ".g8s/effort-classes.yml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("scaffold list %v does not contain .g8s/effort-classes.yml", scaffoldList)
	}
}
