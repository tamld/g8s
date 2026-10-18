package main

// #420 distribution model: `g8s offer` — the pull-bundle made deterministic.
// RED-first: these tests fail (runOffer undefined) before implementation.

import (
	"encoding/json"
	"fmt"
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
	for _, f := range []string{".g8s/trust-boundaries.yml", ".g8s/lane-bundles.yml", ".g8s/effort-classes.yml", ".g8s/verifier-classes.yml"} {
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

func TestOfferInitGatesOnlyPreservesExistingG8sDir(t *testing.T) {
	dir := t.TempDir()
	g8sDir := filepath.Join(dir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o700); err != nil {
		t.Fatal(err)
	}
	customContent := "# custom sibling effort classes\nschema_version: \"effort-classes.v1\"\n"
	if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(customContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := execOffer(t, dir, "init", "--gates-only")
	if cmd.exitCode != 0 {
		t.Fatalf("--gates-only must exit 0 even when .g8s/ exists, got %d:\n%s", cmd.exitCode, cmd.output)
	}
	got, err := os.ReadFile(filepath.Join(g8sDir, "effort-classes.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != customContent {
		t.Errorf("--gates-only overwrote custom .g8s/effort-classes.yml: got %q, want %q", string(got), customContent)
	}
	for _, f := range []string{
		"tools/ci_spec_code_sync.sh", "tools/ci_structure_sync.sh", "tools/ci_link_integrity.sh",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("--gates-only missing gate script %s: %v", f, err)
		}
	}
}

func TestOfferCheckRunsGates(t *testing.T) {
	dir := t.TempDir()
	execOffer(t, dir, "init", "--profile", "utility")
	// a clean utility scaffold has no spec dir + no markers — check must
	// degrade gracefully (the gates skip absent surfaces) and exit 0 with overall: PASS.
	cmd := execOffer(t, dir, "check")
	if cmd.exitCode != 0 || !strings.Contains(cmd.output, "overall: PASS") {
		t.Fatalf("offer check on clean scaffold must exit 0 with overall: PASS, got %d:\n%s", cmd.exitCode, cmd.output)
	}
}

func TestOfferCheckEmbeddedFallbackWithoutToolsDir(t *testing.T) {
	// L1-domain-locked repos (e.g. aegis) keep .g8s/ only and forbid top-level tools/*.sh.
	dir := t.TempDir()
	cmd := execOffer(t, dir, "check")
	if cmd.exitCode != 0 || !strings.Contains(cmd.output, "overall: PASS") {
		t.Fatalf("offer check without tools/ on disk must fall back to embedded gates and PASS, got %d:\n%s", cmd.exitCode, cmd.output)
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

// TestOfferCheck_FailsExit1AndSurfacesDiagnostic verifies (a):
// 'g8s offer check' exits 1 and surfaces diagnostic output when a gate fails.
func TestOfferCheck_FailsExit1AndSurfacesDiagnostic(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	failingScript := "#!/usr/bin/env bash\necho \"diagnostic: spec validation failed due to missing invariant\"\nexit 1\n"
	scriptPath := filepath.Join(toolsDir, "ci_spec_code_sync.sh")
	if err := os.WriteFile(scriptPath, []byte(failingScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Human mode: verify exit code 1, indented diagnostic output, and overall FAIL.
	cmd := execOffer(t, dir, "check", "--gates", "spec")
	if cmd.exitCode != 1 {
		t.Fatalf("offer check with failing gate must exit 1, got %d:\n%s", cmd.exitCode, cmd.output)
	}
	if !strings.Contains(cmd.output, "diagnostic: spec validation failed due to missing invariant") {
		t.Fatalf("expected diagnostic output in output, got:\n%s", cmd.output)
	}
	if !strings.Contains(cmd.output, "    diagnostic: spec validation failed due to missing invariant") {
		t.Fatalf("expected diagnostic output to be indented below status line, got:\n%s", cmd.output)
	}
	if !strings.Contains(cmd.output, "overall: FAIL") {
		t.Fatalf("expected overall: FAIL, got:\n%s", cmd.output)
	}

	// JSON mode: verify exit code 1 and envelope data contains failure details.
	cmdJSON := execOffer(t, dir, "check", "--gates", "spec", "--json")
	if cmdJSON.exitCode != 1 {
		t.Fatalf("offer check --json with failing gate must exit 1, got %d:\n%s", cmdJSON.exitCode, cmdJSON.output)
	}
	var env cli.Envelope
	if err := json.Unmarshal([]byte(cmdJSON.output), &env); err != nil {
		t.Fatalf("failed to parse offer check json: %v\nraw: %s", err, cmdJSON.output)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("envelope Data is not a map: %v", env.Data)
	}
	if data["overall"] != "FAIL" {
		t.Errorf("data.overall = %v, want FAIL", data["overall"])
	}
}

// TestOfferCheck_WarnsOnStaleGateScript verifies (b):
// 'g8s offer check' warns when a vendored gate script is stale compared to the embedded script.
func TestOfferCheck_WarnsOnStaleGateScript(t *testing.T) {
	dir := t.TempDir()
	resInit := execOffer(t, dir, "init", "--profile", "utility")
	if resInit.exitCode != 0 {
		t.Fatalf("offer init exit %d: %s", resInit.exitCode, resInit.output)
	}

	// 1. Freshly initialized scripts should not trigger stale warning
	cmdClean := execOffer(t, dir, "check")
	if cmdClean.exitCode != 0 {
		t.Fatalf("clean check must exit 0, got %d:\n%s", cmdClean.exitCode, cmdClean.output)
	}
	if strings.Contains(cmdClean.output, "[warn] stale gate script") {
		t.Fatalf("clean check must not warn about stale scripts, got:\n%s", cmdClean.output)
	}

	// 2. Modify one script so its content differs from embedded bundle
	specScript := filepath.Join(dir, "tools", "ci_spec_code_sync.sh")
	origContent, err := os.ReadFile(specScript)
	if err != nil {
		t.Fatal(err)
	}
	modifiedContent := string(origContent) + "\n# local modification\n"
	if err := os.WriteFile(specScript, []byte(modifiedContent), 0o755); err != nil {
		t.Fatal(err)
	}

	// 3. Human mode check should print stale warning
	cmdStale := execOffer(t, dir, "check")
	if cmdStale.exitCode != 0 {
		t.Fatalf("check with modified script should still pass if script passes, got %d:\n%s", cmdStale.exitCode, cmdStale.output)
	}
	wantWarn := fmt.Sprintf("  [warn] stale gate script %s differs from embedded bundle (run 'g8s offer init --gates-only --dir .')", filepath.Join("tools", "ci_spec_code_sync.sh"))
	if !strings.Contains(cmdStale.output, wantWarn) {
		t.Fatalf("expected warning line %q in output, got:\n%s", wantWarn, cmdStale.output)
	}

	// Also verify warning format when --dir is passed explicitly
	cmdStaleDir := execOffer(t, dir, "check", "--dir", dir)
	if cmdStaleDir.exitCode != 0 {
		t.Fatalf("check --dir exit = %d, output:\n%s", cmdStaleDir.exitCode, cmdStaleDir.output)
	}
	wantWarnDir := fmt.Sprintf("  [warn] stale gate script %s differs from embedded bundle (run 'g8s offer init --gates-only --dir %s')", specScript, dir)
	if !strings.Contains(cmdStaleDir.output, wantWarnDir) {
		t.Fatalf("expected warning line %q in output, got:\n%s", wantWarnDir, cmdStaleDir.output)
	}

	// 4. JSON mode check should include "stale": true
	cmdJSON := execOffer(t, dir, "check", "--json")
	if cmdJSON.exitCode != 0 {
		t.Fatalf("check --json exit = %d, output:\n%s", cmdJSON.exitCode, cmdJSON.output)
	}
	var env cli.Envelope
	if err := json.Unmarshal([]byte(cmdJSON.output), &env); err != nil {
		t.Fatalf("unmarshal json: %v\nraw: %s", err, cmdJSON.output)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("env.Data is not map[string]any: %T", env.Data)
	}
	gatesList, ok := data["gates"].([]any)
	if !ok {
		t.Fatalf("gates is not []any: %v", data["gates"])
	}
	foundStale := false
	for _, g := range gatesList {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if gm["gate"] == "spec" {
			if st, ok := gm["stale"].(bool); ok && st {
				foundStale = true
			}
		}
	}
	if !foundStale {
		t.Errorf("expected gate 'spec' to have stale: true in JSON output: %s", cmdJSON.output)
	}

	// 5. Run 'offer init --gates-only' to refresh scripts and verify warning disappears
	resRefresh := execOffer(t, dir, "init", "--gates-only")
	if resRefresh.exitCode != 0 {
		t.Fatalf("offer init --gates-only exit %d: %s", resRefresh.exitCode, resRefresh.output)
	}
	cmdRefreshed := execOffer(t, dir, "check")
	if strings.Contains(cmdRefreshed.output, "[warn] stale gate script") {
		t.Fatalf("after refresh, stale warning must disappear, got:\n%s", cmdRefreshed.output)
	}
}

// TestOfferGatesDir_OpsGatesAutoDetect verifies (c):
// 'g8s offer init --gates-only --gates-dir ops/gates' and 'g8s offer check' auto-detecting ops/gates work cleanly.
func TestOfferGatesDir_OpsGatesAutoDetect(t *testing.T) {
	dir := t.TempDir()

	cmdInit := execOffer(t, dir, "init", "--gates-only", "--gates-dir", "ops/gates")
	if cmdInit.exitCode != 0 {
		t.Fatalf("offer init with custom gates-dir failed: %d\n%s", cmdInit.exitCode, cmdInit.output)
	}

	for _, scriptName := range []string{"ci_spec_code_sync.sh", "ci_structure_sync.sh", "ci_link_integrity.sh"} {
		if _, err := os.Stat(filepath.Join(dir, "ops", "gates", scriptName)); err != nil {
			t.Errorf("expected script in ops/gates: %s: %v", scriptName, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tools")); !os.IsNotExist(err) {
		t.Errorf("tools directory should not have been created, err: %v", err)
	}

	cmdCheck := execOffer(t, dir, "check")
	if cmdCheck.exitCode != 0 {
		t.Fatalf("offer check auto-detecting ops/gates must exit 0, got %d:\n%s", cmdCheck.exitCode, cmdCheck.output)
	}
	if !strings.Contains(cmdCheck.output, "overall: PASS") {
		t.Fatalf("expected overall: PASS, got:\n%s", cmdCheck.output)
	}
	if !strings.Contains(cmdCheck.output, filepath.Join("ops", "gates", "ci_spec_code_sync.sh")) {
		t.Fatalf("expected check output to reference ops/gates/ci_spec_code_sync.sh, got:\n%s", cmdCheck.output)
	}
	if strings.Contains(cmdCheck.output, "[warn] stale") {
		t.Fatalf("freshly scaffolded scripts in ops/gates must not be stale, got:\n%s", cmdCheck.output)
	}

	cmdCheckExplicit := execOffer(t, dir, "check", "--gates-dir", "ops/gates")
	if cmdCheckExplicit.exitCode != 0 || !strings.Contains(cmdCheckExplicit.output, "overall: PASS") {
		t.Fatalf("offer check with explicit --gates-dir ops/gates failed: %d\n%s", cmdCheckExplicit.exitCode, cmdCheckExplicit.output)
	}
}
