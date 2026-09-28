package main

// #420 distribution model: `g8s offer` — the pull-bundle made deterministic.
// RED-first: these tests fail (runOffer undefined) before implementation.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
