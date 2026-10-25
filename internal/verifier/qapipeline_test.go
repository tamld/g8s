package verifier

import (
	"strings"
	"testing"
)

func TestValidateBASpecContractAndScenarioMatrix(t *testing.T) {
	validSpec := `
# Feature Spec
- User Story: US-14
- Functional Req: REQ-FUNC-10
- Acceptance Criteria: AC-07, AC-08
- Given an empty queue at boundary limit 0, When a concurrent worker acquires a lease with timeout, Then FSM transitions QUEUED -> RUNNING -> COMPLETED and sanitizes secret tokens.
`
	res := ValidateBASpecContract(validSpec)
	if !res.Passed {
		t.Fatalf("expected valid BA spec to pass, missing: %v", res.MissingElements)
	}
	if len(res.UserStories) != 1 || res.UserStories[0] != "US-14" {
		t.Fatalf("unexpected user stories: %v", res.UserStories)
	}
	if len(res.AcceptanceCriteria) != 2 {
		t.Fatalf("expected 2 ACs, got %v", res.AcceptanceCriteria)
	}
	if len(res.CoveredDimensions) < 4 {
		t.Fatalf("expected at least 4 scenario dimensions covered, got %v", res.CoveredDimensions)
	}

	incompleteSpec := "Just a vague requirement without structured IDs."
	badRes := ValidateBASpecContract(incompleteSpec)
	if badRes.Passed {
		t.Fatalf("expected incomplete BA spec to fail")
	}
	if len(badRes.MissingElements) != 5 {
		t.Fatalf("expected all 5 missing elements reported, got %v", badRes.MissingElements)
	}
}

func TestVerifyHoldoutDisjointnessBlocksContamination(t *testing.T) {
	if err := VerifyHoldoutDisjointness([]string{"tests/holdout/**"}, []string{"internal/verifier/**", "cmd/g8s/**"}); err != nil {
		t.Fatalf("expected disjoint paths to pass, got: %v", err)
	}

	errOverlap := VerifyHoldoutDisjointness([]string{"tests/holdout/qa_test.go"}, []string{"tests/**"})
	if errOverlap == nil || !strings.Contains(errOverlap.Error(), CodeHoldoutContamination) {
		t.Fatalf("expected E_HOLDOUT_CONTAMINATION on overlapping parent prefix, got: %v", errOverlap)
	}

	errWildcard := VerifyHoldoutDisjointness([]string{"tests/holdout/**"}, []string{"**"})
	if errWildcard == nil || !strings.Contains(errWildcard.Error(), CodeHoldoutContamination) {
		t.Fatalf("expected E_HOLDOUT_CONTAMINATION on wildcard write scope, got: %v", errWildcard)
	}
}

func TestEvaluateQAPipelineEnforcesSixStagesAndEpistemicAudit(t *testing.T) {
	spec := `
- US-01 / REQ-FUNC-01
- AC-01: Given a boundary limit and empty input, When concurrent requests arrive with timeout, Then FSM transitions QUEUED -> RUNNING -> COMPLETED and unauthorized secrets are redacted.
- AC-02: Given a valid session, When telemetry is emitted, Then audit logs record latency.
`
	passReport := EvaluateQAPipeline(QAPipelineRequest{
		TaskID:            "task-qa-01",
		SpecMarkdown:      spec,
		HoldoutPaths:      []string{"holdout/qa/**"},
		AllowedWritePaths: []string{"internal/verifier/**"},
		Canary: &CanaryResult{
			Executed:     true,
			CanaryCaught: true,
			Detail:       "mutant killed by TestHoldout",
		},
		ACTestBindings: map[string]string{
			"AC-01": "TestVerifyBoundaryAndFSM",
		},
		TelemetryBindings: map[string]string{
			"AC-02": "probe:audit_latency_metric",
		},
		ChecksPassed: true,
	})
	if !passReport.Passed || passReport.Verdict != "PASS" {
		t.Fatalf("expected 6-stage QA pipeline to PASS, got verdict=%s failures=%v", passReport.Verdict, passReport.FailureReasons)
	}
	if passReport.MechanicalVerified != 1 || passReport.ObservationalCount != 1 || passReport.UnverifiedProseCount != 0 {
		t.Fatalf("unexpected epistemic counts: %+v", passReport)
	}

	// Now test fail-closed when AC-02 has no binding (UNVERIFIED_PROSE) and canary survives
	failReport := EvaluateQAPipeline(QAPipelineRequest{
		TaskID:            "task-qa-02",
		SpecMarkdown:      spec,
		HoldoutPaths:      []string{"holdout/qa/**"},
		AllowedWritePaths: []string{"holdout/**"},
		Canary: &CanaryResult{
			Executed:     true,
			CanaryCaught: false,
			VetoStatus:   VetoCanarySurvived,
			Detail:       "mutant survived",
		},
		ACTestBindings: map[string]string{
			"AC-01": "TestVerifyBoundaryAndFSM",
		},
		ChecksPassed: true,
	})
	if failReport.Passed || failReport.Verdict != "FAIL" {
		t.Fatalf("expected pipeline to FAIL on holdout contamination, surviving canary, and unverified prose")
	}
	if failReport.UnverifiedProseCount != 1 {
		t.Fatalf("expected 1 UNVERIFIED_PROSE claim, got %d", failReport.UnverifiedProseCount)
	}
	if failReport.HoldoutDisjoint {
		t.Fatalf("expected HoldoutDisjoint=false")
	}
	if failReport.CanaryGatePassed {
		t.Fatalf("expected CanaryGatePassed=false")
	}
}
