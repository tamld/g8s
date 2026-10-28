package verifier

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyPlanDiff_DetectsMissingMismatchAndUnplanned(t *testing.T) {
	plan := `
## Proposed Changes
- [NEW] internal/verifier/aegis_guards.go
- [MODIFY] internal/verifier/diff_guard.go
- [DELETE] legacy/old_hook.sh
`
	// Case 1: Exact match
	diffExact := `
A	internal/verifier/aegis_guards.go
M	internal/verifier/diff_guard.go
D	legacy/old_hook.sh
`
	repExact := VerifyPlanDiff(plan, diffExact)
	if !repExact.OK || len(repExact.Violations) != 0 {
		t.Fatalf("expected exact plan-diff match to pass, got %+v", repExact)
	}

	// Case 2: Missing planned change + Action mismatch + Unplanned change
	diffDiverged := `
M	internal/verifier/diff_guard.go
M	legacy/old_hook.sh
A	cmd/g8s/sneaky_unplanned.go
`
	repDiv := VerifyPlanDiff(plan, diffDiverged)
	if repDiv.OK {
		t.Fatalf("expected diverged plan-diff to fail, got OK")
	}
	if len(repDiv.Violations) != 3 {
		t.Fatalf("expected 3 violations (missing, mismatch, unplanned), got %d: %+v", len(repDiv.Violations), repDiv.Violations)
	}
	if !strings.Contains(repDiv.JITBaton, "[jit:baton:PLAN_DIFF_MISMATCH]") {
		t.Fatalf("expected PLAN_DIFF_MISMATCH baton, got %q", repDiv.JITBaton)
	}
}

func TestVerifyTestAssertionsNotWeakened_AndDiffGuardIntegration(t *testing.T) {
	// Scenario A: Worker deletes t.Fatalf assertion to make test pass (pure deletion diff!)
	pureDeletionDiff := `diff --git a/internal/foo/foo_test.go b/internal/foo/foo_test.go
--- a/internal/foo/foo_test.go
+++ b/internal/foo/foo_test.go
@@ -14,2 +14,0 @@ func TestFoo(t *testing.T) {
-	if got != want {
-		t.Fatalf("got %d, want %d", got, want)
-	}
`
	repDel := VerifyTestAssertionsNotWeakened(pureDeletionDiff)
	if repDel.OK {
		t.Fatalf("expected assertion deletion to be blocked")
	}
	if len(repDel.Findings) != 1 || repDel.Findings[0].RuleID != "TIER0-TEST-ASSERTION-WEAKENED" {
		t.Fatalf("unexpected findings: %+v", repDel.Findings)
	}

	// Ensure EvaluateNetDelta also catches pure deletion diffs in test files (#694)
	_, advisories := EvaluateNetDelta(context.Background(), pureDeletionDiff, []string{"internal/foo/**"}, nil)
	foundGuard := false
	for _, adv := range advisories {
		if adv.RuleID == "TIER0-TEST-ASSERTION-WEAKENED" {
			foundGuard = true
		}
	}
	if !foundGuard {
		t.Fatalf("expected EvaluateNetDelta to emit TIER0-TEST-ASSERTION-WEAKENED on pure deletion diff, got %+v", advisories)
	}

	// Scenario B: Worker injects t.Skip("flaky") to bypass test
	skipDiff := `diff --git a/src/bar.test.ts b/src/bar.test.ts
--- a/src/bar.test.ts
+++ b/src/bar.test.ts
@@ -5,1 +5,2 @@
+it.skip("should validate token", () => {
+  expect(1).toBe(1);
`
	repSkip := VerifyTestAssertionsNotWeakened(skipDiff)
	if repSkip.OK || len(repSkip.Findings) == 0 || repSkip.Findings[0].RuleID != "TIER0-TEST-SKIP-INJECTED" {
		t.Fatalf("expected TIER0-TEST-SKIP-INJECTED, got %+v", repSkip)
	}
}

func TestVerifyEvidenceProvenance(t *testing.T) {
	badReport := "Status: Fixed\nRoot Cause: race condition in worker loop."
	repBad := VerifyEvidenceProvenance(badReport)
	if repBad.OK {
		t.Fatalf("expected conclusion without provenance tag and file:line to fail")
	}
	if !strings.Contains(repBad.JITBaton, "[jit:baton:EVIDENCE_PROVENANCE_REQUIRED]") {
		t.Fatalf("expected EVIDENCE_PROVENANCE_REQUIRED baton, got %q", repBad.JITBaton)
	}

	goodReport := "Status: Fixed\n[RUNTIME] Verified in internal/verifier/aegis_guards.go:42 with exit code 0."
	repGood := VerifyEvidenceProvenance(goodReport)
	if !repGood.OK {
		t.Fatalf("expected tagged and cited conclusion to pass, got %+v", repGood)
	}
}

func TestVerifyDocConsumerFrontmatter(t *testing.T) {
	tmp := t.TempDir()
	consumerFile := filepath.Join(tmp, "internal", "verifier", "aegis_guards.go")
	if err := os.MkdirAll(filepath.Dir(consumerFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(consumerFile, []byte("package verifier\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	docValid := `---
title: "Plan Diff Spec"
consumer: "internal/verifier/aegis_guards.go"
---
# Spec
`
	repOK := VerifyDocConsumerFrontmatter(docValid, tmp)
	if !repOK.OK {
		t.Fatalf("expected valid consumer frontmatter to pass, got %+v", repOK)
	}

	docDead := `---
title: "Dead Spec"
consumer: "internal/verifier/nonexistent_ghost.go"
---
# Dead Prose
`
	repDead := VerifyDocConsumerFrontmatter(docDead, tmp)
	if repDead.OK || len(repDead.MissingConsumers) != 1 {
		t.Fatalf("expected missing consumer file to fail, got %+v", repDead)
	}
}
