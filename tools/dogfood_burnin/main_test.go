package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "scorecard_fixture.json")
}

// writeTempFixture mutates the recorded fixture (JSON-number mutations only —
// strict encoding/json cannot carry NaN) and writes it to a temp file.
func writeTempFixture(t *testing.T, mutate func(sec map[string]any)) string {
	t.Helper()
	sec := loadFixtureSection(t)
	if mutate != nil {
		mutate(sec)
	}
	path := filepath.Join(t.TempDir(), "mutated_scorecard.json")
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"axes": map[string]any{"compliance_vs_quality": sec}}})
	if err != nil {
		t.Fatalf("marshal mutated fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write mutated fixture: %v", err)
	}
	return path
}

// TestExecuteFixturePass is the fixture-based burn-in run of the full
// harness: 3 consecutive invariant-clean runs over the recorded scorecard
// JSON must produce verdict=pass with the pass criterion met and a
// CI-consumable JSON report on disk and stdout.
func TestExecuteFixturePass(t *testing.T) {
	cfg := config{Runs: 3, Fixture: fixturePath(t), MinCohort: 10}
	report := execute(cfg)

	if report.Verdict != verdictPass {
		t.Fatalf("verdict=%s, want pass (runs=%+v cross=%+v)", report.Verdict, report.Runs, report.CrossRunViolations)
	}
	if !report.PassCriterion.Met || report.PassCriterion.ObservedConsecutiveClean != 3 {
		t.Fatalf("pass criterion not met: %+v", report.PassCriterion)
	}
	if len(report.Runs) != 3 {
		t.Fatalf("runs=%d, want 3", len(report.Runs))
	}
	for _, run := range report.Runs {
		if !run.Collected || !run.Clean || run.DataQuality != qualityFull {
			t.Fatalf("run %d not clean/full: %+v", run.Run, run)
		}
	}
	for _, inv := range report.Invariants {
		if inv.ViolationsTotal != 0 {
			t.Fatalf("invariant %s must be clean on the fixture: %+v", inv.ID, inv)
		}
	}
	// Issue #971 WP3 live F1 cohort n>=10: the opt-in cohort minimum is
	// satisfied by the fixture sandbox sample (100 attempts).
	if report.Mode != "fixture" {
		t.Fatalf("mode=%s, want fixture", report.Mode)
	}
	// The report must survive a JSON round trip for CI consumers.
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeReportFile(path, report); err != nil {
		t.Fatalf("write report: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var back burninReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("report must be valid JSON: %v", err)
	}
	if back.Verdict != verdictPass || back.Kind != reportKind || back.Issue != issueRef {
		t.Fatalf("round-tripped report drifted: %+v", back)
	}
	if !strings.Contains(report.HumanTail, "verdict=pass") || !strings.Contains(report.HumanTail, "invariants: all clean") {
		t.Fatalf("human tail incomplete:\n%s", report.HumanTail)
	}
	// The fixture exercises both honest WP2 labels: measured cohorts and the
	// insufficient_sample hotfix lane.
	joined := strings.Join(report.Runs[0].InsufficientSample, " ")
	if !strings.Contains(joined, "lane:0(n=4)") {
		t.Fatalf("insufficient_sample labels must surface the hotfix lane, got: %s", joined)
	}
}

// TestExecuteFixtureFail drives the harness end-to-end over a fixture whose
// counters no longer conserve: the verdict must be fail with the violating
// invariants tallied.
func TestExecuteFixtureFail(t *testing.T) {
	path := writeTempFixture(t, func(sec map[string]any) {
		testRows(t, sec, "lane_ledger")[3]["total"] = float64(61) // breaks Σ lanes == Σ cohorts and the escape mirror
	})
	report := execute(config{Runs: 2, Fixture: path})
	if report.Verdict != verdictFail {
		t.Fatalf("verdict=%s, want fail", report.Verdict)
	}
	if report.PassCriterion.Met || report.PassCriterion.ObservedConsecutiveClean != 0 {
		t.Fatalf("pass criterion must not be met on fail: %+v", report.PassCriterion)
	}
	found := map[string]bool{}
	for _, inv := range report.Invariants {
		if inv.ViolationsTotal > 0 {
			found[inv.ID] = true
		}
	}
	if !found[invConservation] {
		t.Fatalf("I_CONSERVATION must be tallied, got %+v", report.Invariants)
	}
	if exitCodeFor(report.Verdict) != 1 {
		t.Fatal("fail verdict must exit 1")
	}
}

// TestExecuteDegradedCollection asserts explicit degradation when the
// scorecard output cannot be collected at all (missing fixture = missing
// binary/data in live mode).
func TestExecuteDegradedCollection(t *testing.T) {
	report := execute(config{Runs: 2, Fixture: filepath.Join(t.TempDir(), "missing.json")})
	if report.Verdict != verdictDegraded {
		t.Fatalf("verdict=%s, want degraded", report.Verdict)
	}
	if len(report.Runs) != 1 || report.Runs[0].Collected {
		t.Fatalf("run must be uncollected: %+v", report.Runs)
	}
	if report.Runs[0].CollectionError == "" {
		t.Fatal("collection error must be explicit")
	}
	if report.PassCriterion.Met {
		t.Fatal("degraded burn-in must not meet the pass criterion")
	}
	if exitCodeFor(report.Verdict) != 2 {
		t.Fatal("degraded verdict must exit 2")
	}
}

// TestExecuteDegradedEmptyLandingSet asserts the harness refuses to pass on
// nothing: a collected section with zero recorded landings degrades
// explicitly (missing/insufficient data is never silently a pass).
func TestExecuteDegradedEmptyLandingSet(t *testing.T) {
	path := writeTempFixture(t, func(sec map[string]any) {
		for _, field := range []string{"lane_ledger", "escape_rate_by_lane"} {
			for _, row := range testRows(t, sec, field) {
				row["total"] = float64(0)
				row["revert_fixup_commits"] = float64(0)
				row["receipt_backed"] = float64(0)
				row["f0_direct"] = float64(0)
				row["unclassified"] = float64(0)
				row["rate_label"] = "insufficient_sample"
				delete(row, "escape_rate")
			}
		}
		for _, row := range testRows(t, sec, "cohort_metrics") {
			row["commits"] = float64(0)
			row["revert_fixup_commits"] = float64(0)
			row["rate_label"] = "insufficient_sample"
			delete(row, "revert_fixup_rate")
		}
	})
	report := execute(config{Runs: 2, Fixture: path})
	if report.Verdict != verdictDegraded {
		t.Fatalf("verdict=%s, want degraded (empty landing set must not pass)", report.Verdict)
	}
	if report.Runs[0].DataQuality != qualityEmpty {
		t.Fatalf("data_quality=%s, want empty", report.Runs[0].DataQuality)
	}
}

// TestExecutePartialDegradeIsClean asserts the WP2 explicit-degrade contract
// end to end: a git_log_error with empty arrays plus an explicit sandbox
// error is honest partial data — invariant-clean, verdict pass with the
// degraded quality surfaced (not hidden, not a failure).
func TestExecutePartialDegradeIsClean(t *testing.T) {
	path := writeTempFixture(t, func(sec map[string]any) {
		sec["git_log_error"] = "git log provenance window: exit 128"
		sec["sandbox_enforcement_error"] = "tasks database missing"
		delete(sec, "sandbox_enforcement")
		sec["lane_ledger"] = []any{}
		sec["cohort_metrics"] = []any{}
		sec["escape_rate_by_lane"] = []any{}
	})
	report := execute(config{Runs: 2, Fixture: path})
	if report.Verdict != verdictPass {
		t.Fatalf("verdict=%s, want pass for explicit partial degrade (runs=%+v)", report.Verdict, report.Runs)
	}
	if report.Runs[0].DataQuality != qualityPartial {
		t.Fatalf("data_quality=%s, want partial", report.Runs[0].DataQuality)
	}
	if report.Runs[0].GitLogError == "" || report.Runs[0].SandboxError == "" {
		t.Fatal("explicit errors must be surfaced in the run record")
	}
}

// TestExecuteMinCohortGate exercises the opt-in live F1 cohort minimum
// (issue #971 WP3: live isolated F1 burn-in cohort n >= 10).
func TestExecuteMinCohortGate(t *testing.T) {
	t.Run("cohort satisfied", func(t *testing.T) {
		report := execute(config{Runs: 1, Fixture: fixturePath(t), MinCohort: 10})
		if report.Verdict != verdictPass {
			t.Fatalf("verdict=%s, want pass (fixture attempts=100 >= 10)", report.Verdict)
		}
	})
	t.Run("cohort too small", func(t *testing.T) {
		path := writeTempFixture(t, func(sec map[string]any) {
			sandbox, ok := sec["sandbox_enforcement"].(map[string]any)
			if !ok {
				t.Fatalf("fixture sandbox_enforcement not an object")
			}
			sandbox["workspace_write_attempts"] = float64(9)
			sandbox["kernel_enforced"] = float64(4)
			sandbox["unclassified"] = float64(5)
		})
		report := execute(config{Runs: 1, Fixture: path, MinCohort: 10})
		if report.Verdict != verdictFail {
			t.Fatalf("verdict=%s, want fail (attempts=9 < 10)", report.Verdict)
		}
		found := false
		for _, inv := range report.Invariants {
			if inv.ID == invCohortMin && inv.ViolationsTotal > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("I_COHORT_MIN must be tallied: %+v", report.Invariants)
		}
	})
	t.Run("cohort unverifiable without sandbox metric", func(t *testing.T) {
		path := writeTempFixture(t, func(sec map[string]any) {
			delete(sec, "sandbox_enforcement")
			sec["sandbox_enforcement_error"] = "tasks database missing"
		})
		report := execute(config{Runs: 1, Fixture: path, MinCohort: 10})
		if report.Verdict != verdictFail {
			t.Fatalf("verdict=%s, want fail (opt-in minimum unverifiable)", report.Verdict)
		}
	})
}

// TestParseFlagsClamps guards the bounded-loop contract.
func TestParseFlagsClamps(t *testing.T) {
	cfg, err := parseFlags([]string{"--runs", "0"})
	if err != nil || cfg.Runs != 1 {
		t.Fatalf("runs 0 must clamp to 1: %+v err=%v", cfg, err)
	}
	cfg, err = parseFlags([]string{"--runs", "500"})
	if err != nil || cfg.Runs != maxRuns {
		t.Fatalf("runs 500 must clamp to %d: %+v err=%v", maxRuns, cfg, err)
	}
	cfg, err = parseFlags([]string{"--min-cohort", "-3"})
	if err != nil || cfg.MinCohort != 0 {
		t.Fatalf("negative min-cohort must clamp to 0: %+v err=%v", cfg, err)
	}
	cfg, err = parseFlags([]string{"--runs", "2", "--min-cohort", "10"})
	if err != nil || cfg.Runs != 2 || cfg.MinCohort != 10 {
		t.Fatalf("flags not honored: %+v err=%v", cfg, err)
	}
}

// TestResolveG8sBinPriority covers binary resolution: flag beats env beats
// <repo>/bin/g8s, and an unresolvable binary yields an explicit collection
// error (degraded, not a silent skip).
func TestResolveG8sBinPriority(t *testing.T) {
	repo := t.TempDir()
	binDir := filepath.Join(repo, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	localBin := filepath.Join(binDir, "g8s")
	if err := os.WriteFile(localBin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("repo bin", func(t *testing.T) {
		if got := resolveG8sBin(config{RepoDir: repo}); got != localBin {
			t.Fatalf("repo bin resolution: got %q", got)
		}
	})
	t.Run("env beats repo bin", func(t *testing.T) {
		t.Setenv("G8S_BIN", "/env/path/g8s")
		if got := resolveG8sBin(config{RepoDir: repo}); got != "/env/path/g8s" {
			t.Fatalf("env resolution: got %q", got)
		}
	})
	t.Run("flag beats env", func(t *testing.T) {
		t.Setenv("G8S_BIN", "/env/path/g8s")
		if got := resolveG8sBin(config{RepoDir: repo, G8sBin: "/flag/path/g8s"}); got != "/flag/path/g8s" {
			t.Fatalf("flag resolution: got %q", got)
		}
	})
	t.Run("unresolvable degrades explicitly", func(t *testing.T) {
		t.Setenv("G8S_BIN", "")
		empty := t.TempDir()
		if got := resolveG8sBin(config{RepoDir: empty}); got != "" {
			t.Fatalf("empty repo without env must yield empty, got %q", got)
		}
		missing := execute(config{Runs: 1, RepoDir: empty})
		if missing.Verdict != verdictDegraded || missing.Runs[0].CollectionError == "" {
			t.Fatalf("missing binary must degrade explicitly: %+v", missing.Runs)
		}
	})
}

// TestExitCodeContract pins the CI-gateable exit codes.
func TestExitCodeContract(t *testing.T) {
	if exitCodeFor(verdictPass) != 0 || exitCodeFor(verdictFail) != 1 || exitCodeFor(verdictDegraded) != 2 {
		t.Fatal("exit codes must be pass=0 fail=1 degraded=2")
	}
}

// TestFixtureCohortLabelsDocumentCurrentLedgerState asserts the recorded
// fixture mirrors the honest WP2 labels: measured cohorts with emitted rates
// and an insufficient_sample hotfix lane without one.
func TestFixtureCohortLabelsDocumentCurrentLedgerState(t *testing.T) {
	sec := loadFixtureSection(t)
	labels := map[string]string{}
	for _, row := range testRows(t, sec, "cohort_metrics") {
		labels[rowCohort(row)] = rowRateLabel(row)
	}
	for _, cohort := range canonicalCohorts {
		if labels[cohort] != "measured" {
			t.Fatalf("fixture cohort %s label=%q, want measured", cohort, labels[cohort])
		}
	}
	hotfix := testRows(t, sec, "escape_rate_by_lane")[0]
	if hotfix["rate_label"] != "insufficient_sample" {
		t.Fatalf("hotfix lane label=%v, want insufficient_sample", hotfix["rate_label"])
	}
	if _, has := hotfix["escape_rate"]; has {
		t.Fatal("insufficient_sample lane must not carry a rate")
	}
	if _, has := hotfix["lane_name"]; !has {
		t.Fatal("fixture rows must carry lane_name")
	}
	// Guard the zero-rate-measured case the merged producer emits via omitempty:
	// the security lane has 0 reverts at n=24, so no rate key is present.
	security := testRows(t, sec, "escape_rate_by_lane")[4]
	if security["rate_label"] != "measured" {
		t.Fatalf("security lane label=%v, want measured", security["rate_label"])
	}
	if _, has := security["escape_rate"]; has {
		t.Fatal("zero-rate measured lane must omit the rate (omitempty semantic)")
	}
}
