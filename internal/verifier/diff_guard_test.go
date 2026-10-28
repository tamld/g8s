package verifier

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/vault"
)

func TestParseUnifiedDiffAddedLines(t *testing.T) {
	diff := `diff --git a/internal/worker/worker.go b/internal/worker/worker.go
index 1111111..2222222 100644
--- a/internal/worker/worker.go
+++ b/internal/worker/worker.go
@@ -40,2 +40,3 @@ func run() {
-	oldRemovedLine()
 	existingContextLine()
+	addedLineOne()
+	addedLineTwo()
`
	parsed := ParseUnifiedDiffAddedLines(diff)
	lines, ok := parsed["internal/worker/worker.go"]
	if !ok {
		t.Fatalf("expected internal/worker/worker.go in parsed diff, got %+v", parsed)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 added lines, got %d (%+v)", len(lines), lines)
	}
	if lines[0].Line != 41 || strings.TrimSpace(lines[0].Text) != "addedLineOne()" {
		t.Fatalf("unexpected first added line: %+v", lines[0])
	}
	if lines[1].Line != 42 || strings.TrimSpace(lines[1].Text) != "addedLineTwo()" {
		t.Fatalf("unexpected second added line: %+v", lines[1])
	}
}

func TestDiffGuardHardVsAdvisoryAndDormantReawakening(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	vdbPath := filepath.Join(tempDir, "vault.db")

	v, err := vault.NewVault(vdbPath, nil)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	// Store 1 cold/dormant rule (Status = DEPRECATED) to test Hypothesis H2 re-awakening
	_, err = v.Store(ctx, vault.DistillationRecord{
		ID:        "RULE-COLD-SLEEP",
		Title:     "Dormant raw sleep prohibition",
		Status:    "DEPRECATED",
		Milestone: "v0.16.0",
		Causality: vault.CausalityAnchor{
			Problem:  "Flaky timer waits",
			TradeOff: "Use injectable clock",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package:         "internal/worker",
			File:            "internal/worker/worker.go",
			DeniedFragments: []string{"time.Sleep("},
		},
	})
	v.Close()
	if err != nil {
		t.Fatalf("Store cold rule: %v", err)
	}

	t.Setenv("G8S_VAULT_DB", vdbPath)

	regYAML := []byte(`
classes:
  - name: go-diff-guarded
    status: hard
    founding_catch: "#595 net-delta diff guard"
    priority: 10
    paths:
      - "internal/worker/**"
    checks:
      - type: diff-guard
`)

	// Case 1: Advisory Signal from Dormant (Cold) Rule + Tier-0 empty err branch -> fails without OverrideReason, reports DormantReawakened
	advisoryDiff := `--- a/internal/worker/worker.go
+++ b/internal/worker/worker.go
@@ -10,0 +11,2 @@
+	time.Sleep(5)
+	if err != nil {}
`
	vf := NewVerifier(
		WithRegistryData(regYAML),
		WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
			return []byte(advisoryDiff), nil, 0, nil
		}),
		WithTelemetrySink(func(taskID string, verdict Verdict) {}),
	)

	verdict, err := vf.Verify(TaskRef{
		ID:           "task-adv-1",
		ReceiptID:    "rc-1",
		AllowedPaths: []string{"internal/worker/worker.go"},
	}, TaskRef{ID: "f0-supervisor"})
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if verdict.Outcome != OutcomeFail {
		t.Fatalf("expected OutcomeFail on un-overridden advisory signal, got %s", verdict.Outcome)
	}
	if len(verdict.DormantReawakened) != 1 || verdict.DormantReawakened[0] != "RULE-COLD-SLEEP" {
		t.Fatalf("expected RULE-COLD-SLEEP in DormantReawakened, got %+v", verdict.DormantReawakened)
	}
	if len(verdict.Advisories) != 2 {
		t.Fatalf("expected 2 advisories (denied fragment + empty err branch), got %+v", verdict.Advisories)
	}

	// Case 2: Same Advisory Diff WITH F0 OverrideReason -> passes with recorded advisories
	verdictOverridden, err := vf.Verify(TaskRef{
		ID:             "task-adv-1",
		ReceiptID:      "rc-1",
		AllowedPaths:   []string{"internal/worker/worker.go"},
		OverrideReason: "F0 approved bounded sleep in test harness adapter",
	}, TaskRef{ID: "f0-supervisor"})
	if err != nil {
		t.Fatalf("Verify override error: %v", err)
	}
	if verdictOverridden.Outcome != OutcomePass {
		t.Fatalf("expected OutcomePass when F0 provides OverrideReason for advisory, got %s (%+v)", verdictOverridden.Outcome, verdictOverridden.Checks)
	}

	// Case 3: Hard Invariant (secret literal in added line) -> CANNOT be overridden even with OverrideReason
	hardSecretDiff := `--- a/internal/worker/worker.go
+++ b/internal/worker/worker.go
@@ -20,0 +21,1 @@
+	apiKey := "sk-proj1234567890abcdefghijklmnop"
`
	vfHard := NewVerifier(
		WithRegistryData(regYAML),
		WithCommandRunner(func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
			return []byte(hardSecretDiff), nil, 0, nil
		}),
		WithTelemetrySink(func(taskID string, verdict Verdict) {}),
	)

	verdictHard, err := vfHard.Verify(TaskRef{
		ID:             "task-hard-1",
		ReceiptID:      "rc-2",
		AllowedPaths:   []string{"internal/worker/worker.go"},
		OverrideReason: "attempting to override secret check",
	}, TaskRef{ID: "f0-supervisor"})
	if err != nil {
		t.Fatalf("Verify hard error: %v", err)
	}
	if verdictHard.Outcome != OutcomeFail {
		t.Fatalf("expected Hard Invariant to refuse OverrideReason and fail, got %s", verdictHard.Outcome)
	}
	if len(verdictHard.Checks) != 1 || !strings.Contains(verdictHard.Checks[0].Detail, "hard invariant violation") {
		t.Fatalf("expected hard invariant violation detail, got %+v", verdictHard.Checks)
	}
}

func TestEvaluateNetDeltaTheaterPlaceholdersAndTrivialAsserts(t *testing.T) {
	diff := `--- a/internal/worker/worker.go
+++ b/internal/worker/worker.go
@@ -10,0 +11,3 @@
+	// TODO(#644): legitimate owned issue tracking comment must pass
+	panic("TODO: implement later")
+	msg := "FILL IN HERE"
--- a/internal/worker/worker_test.go
+++ b/internal/worker/worker_test.go
@@ -20,0 +21,2 @@
+	assert.True(t, true)
+	assert.Nil(t, nil)
`
	hard, advs := EvaluateNetDelta(context.Background(), diff, []string{"internal/worker/**"}, nil)
	if len(hard) != 0 {
		t.Fatalf("expected 0 hard violations, got %+v", hard)
	}
	if len(advs) != 4 {
		t.Fatalf("expected 4 advisories (2 theater placeholders + 2 trivial asserts, ignoring owned TODO comment), got %d: %+v", len(advs), advs)
	}
	var theaterCount, trivialCount int
	for _, a := range advs {
		switch a.RuleID {
		case "TIER0-THEATER-PLACEHOLDER":
			theaterCount++
		case "TIER0-TRIVIAL-ASSERT":
			trivialCount++
		}
	}
	if theaterCount != 2 || trivialCount != 2 {
		t.Fatalf("expected 2 TIER0-THEATER-PLACEHOLDER and 2 TIER0-TRIVIAL-ASSERT, got theater=%d trivial=%d (%+v)", theaterCount, trivialCount, advs)
	}
}

func TestEvaluateNetDeltaReawakensDormantRuleInVault(t *testing.T) {
	ctx := context.Background()
	v, err := vault.NewVault(":memory:", time.Now)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	defer v.Close()

	_, err = v.Store(ctx, vault.DistillationRecord{
		ID:        "RULE-DORMANT-685",
		Title:     "Forbid raw time.Sleep in worker loop",
		Milestone: "v0.22.0",
		Status:    "DORMANT",
		Causality: vault.CausalityAnchor{
			Problem:  "Polling sleep wastes CPU",
			TradeOff: "Use context timer",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package:         "internal/worker",
			File:            "internal/worker/worker.go",
			DeniedFragments: []string{"time.Sleep("},
		},
	})
	if err != nil {
		t.Fatalf("v.Store: %v", err)
	}

	diff := `--- a/internal/worker/worker.go
+++ b/internal/worker/worker.go
@@ -10,0 +11,1 @@
+	time.Sleep(5 * time.Second)
`
	_, advs := EvaluateNetDelta(ctx, diff, []string{"internal/worker/**"}, v)
	if len(advs) != 1 || !advs[0].DormantReawakened {
		t.Fatalf("expected 1 dormant re-awakened advisory, got %+v", advs)
	}

	// Verify the Vault record itself transitioned from DORMANT -> APPLIED (#685 Hypothesis H2)
	rec, err := v.Get(ctx, "RULE-DORMANT-685")
	if err != nil {
		t.Fatalf("v.Get: %v", err)
	}
	if rec.Status != "APPLIED" {
		t.Fatalf("expected DORMANT rule to be re-awakened to APPLIED in Vault DB (#685), got status=%q", rec.Status)
	}
}
