package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestGovernanceEngine(t *testing.T) *TelemetryEngine {
	t.Helper()
	cfg := DefaultTelemetryConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "telemetry_gov.db")
	eng, err := NewTelemetryEngine(cfg)
	if err != nil {
		t.Fatalf("NewTelemetryEngine: %v", err)
	}
	t.Cleanup(func() {
		_ = eng.Close()
	})
	return eng
}

func TestHITLSovereignCalibrationPrecedence(t *testing.T) {
	eng := newTestGovernanceEngine(t)
	ctx := context.Background()

	// 1. Initialize rule and decay it via repeated automated false positives into ThermalCold
	var rw *RuleWeight
	var err error
	for i := 0; i < 4; i++ {
		rw, err = eng.RecordAutomatedSignal(ctx, "RULE-SLEEP-01", "internal/worker", false, false)
		if err != nil {
			t.Fatalf("RecordAutomatedSignal: %v", err)
		}
	}
	if rw.ThermalState != ThermalCold {
		t.Fatalf("expected rule to decay into ThermalCold after 4 automated FPs, got %s (weight=%.2f)", rw.ThermalState, rw.Weight)
	}

	// 2. Record an F0 OverrideReceipt for this rule
	ovr, err := eng.RecordOverrideReceipt(ctx, OverrideReceipt{
		TaskID: "task-101",
		RuleID: "RULE-SLEEP-01",
		Actor:  "f0-supervisor",
		Reason: "believed sleep was harmless",
	})
	if err != nil || ovr.ID == "" {
		t.Fatalf("RecordOverrideReceipt: %v", err)
	}

	// 3. Human Sovereign issues a VETO (Level 3 Ground Truth): must immediately re-anchor rule to ThermalHot (>= 0.90)
	rwAfterVeto, err := eng.RecordHITLFeedback(ctx, HITLFeedback{
		RuleID:  "RULE-SLEEP-01",
		TaskID:  "task-101",
		Package: "internal/worker",
		Verdict: HITLVerdictVeto,
		Actor:   "tamld",
		Note:    "caused production race condition",
	})
	if err != nil {
		t.Fatalf("RecordHITLFeedback veto: %v", err)
	}
	if rwAfterVeto.ThermalState != ThermalHot || rwAfterVeto.Weight < 0.90 {
		t.Fatalf("expected HITL veto to anchor rule to ThermalHot (>=0.90), got state=%s weight=%.2f", rwAfterVeto.ThermalState, rwAfterVeto.Weight)
	}
	if rwAfterVeto.DormantTriggers != 1 || rwAfterVeto.DormantEscapes != 1 {
		t.Fatalf("expected dormant trigger and escape incremented on Cold veto, got triggers=%d escapes=%d", rwAfterVeto.DormantTriggers, rwAfterVeto.DormantEscapes)
	}

	// 4. Human Sovereign issues two FP verdicts on a noisy rule -> drops weight by AlphaHITL (0.50) each time
	_, _ = eng.GetOrInitRuleWeight(ctx, "RULE-NOISY-02", "internal/worker")
	rwNoisy, err := eng.RecordHITLFeedback(ctx, HITLFeedback{
		RuleID:  "RULE-NOISY-02",
		Package: "internal/worker",
		Verdict: HITLVerdictFP,
		Actor:   "tamld",
		Note:    "over-blocking creative refactor",
	})
	if err != nil {
		t.Fatalf("RecordHITLFeedback fp: %v", err)
	}
	if rwNoisy.ThermalState != ThermalCold {
		t.Fatalf("expected single HITL FP on 0.70 initial weight (0.70 - 0.50 = 0.20) to demote directly to ThermalCold, got %s (%.2f)", rwNoisy.ThermalState, rwNoisy.Weight)
	}
}

func TestCarryingCapacityOneInOneOut(t *testing.T) {
	eng := newTestGovernanceEngine(t)
	ctx := context.Background()

	// Create 4 Hot rules in package "internal/verifier" with distinct weights
	rules := []struct {
		id     string
		weight float64
	}{
		{"R1", 0.95},
		{"R2", 0.88},
		{"R3", 0.76},
		{"R4", 0.68},
	}
	for _, r := range rules {
		if err := eng.saveRuleWeight(ctx, RuleWeight{
			RuleID:       r.id,
			Package:      "internal/verifier",
			Weight:       r.weight,
			ThermalState: ThermalHot,
			UpdatedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("saveRuleWeight %s: %v", r.id, err)
		}
	}

	// Enforce carrying capacity K_max = 3 ("One-In, One-Out")
	updated, err := eng.EnforceCarryingCapacity(ctx, "internal/verifier", 3)
	if err != nil {
		t.Fatalf("EnforceCarryingCapacity: %v", err)
	}
	if len(updated) != 4 {
		t.Fatalf("expected 4 rules returned, got %d", len(updated))
	}
	for i := 0; i < 3; i++ {
		if updated[i].ThermalState != ThermalHot {
			t.Fatalf("expected top-%d rule %s to remain Hot, got %s", i+1, updated[i].RuleID, updated[i].ThermalState)
		}
	}
	if updated[3].RuleID != "R4" || updated[3].ThermalState != ThermalWarm {
		t.Fatalf("expected 4th rule R4 to be demoted to ThermalWarm, got %+v", updated[3])
	}
}

func TestEvaluateHypothesesFalsificationLifecycle(t *testing.T) {
	eng := newTestGovernanceEngine(t)
	ctx := context.Background()

	// Step 1: With 0 samples, all 3 hypotheses must report EpistemicHypothesis (unproven axioms)
	rep0, err := eng.EvaluateHypotheses(ctx, 3)
	if err != nil {
		t.Fatalf("EvaluateHypotheses initial: %v", err)
	}
	for _, h := range rep0.Hypotheses {
		if h.EpistemicStatus != EpistemicHypothesis {
			t.Fatalf("expected hypothesis %s to start as 'hypothesis', got %s", h.ID, h.EpistemicStatus)
		}
	}

	// Step 2: Feed >= 5 empirical samples for H1 (bounded K<=3 has higher FPY than K>3),
	// H2 (5 dormant triggers with 0 escapes -> 0% < 5%), and H3 (5 HITL TP verdicts -> F1 = 1.0 >= 0.70)
	for i := 0; i < 5; i++ {
		if err := eng.RecordKYieldSample(ctx, "t-bounded", "internal/worker", 2, true); err != nil {
			t.Fatal(err)
		}
		if err := eng.RecordKYieldSample(ctx, "t-unbounded", "internal/worker", 6, i%2 == 0); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.RecordHITLFeedback(ctx, HITLFeedback{
			RuleID:  "RULE-PROVEN",
			Package: "internal/worker",
			Verdict: HITLVerdictTP,
			Actor:   "tamld",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Record 5 dormant catches with 0 escapes
	if err := eng.saveRuleWeight(ctx, RuleWeight{
		RuleID:          "RULE-DORMANT-OK",
		Package:         "internal/worker",
		Weight:          0.45,
		ThermalState:    ThermalWarm,
		TPCount:         5,
		DormantTriggers: 5,
		DormantEscapes:  0,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	repValidated, err := eng.EvaluateHypotheses(ctx, 3)
	if err != nil {
		t.Fatalf("EvaluateHypotheses validated: %v", err)
	}
	for _, h := range repValidated.Hypotheses {
		if h.EpistemicStatus != EpistemicValidated {
			t.Fatalf("expected hypothesis %s to be 'validated', got %s (%s)", h.ID, h.EpistemicStatus, h.Summary)
		}
	}

	// Step 3: Inject dormant escapes (>= 5% escape rate) and verify H2 transitions to EpistemicFalsified
	if err := eng.saveRuleWeight(ctx, RuleWeight{
		RuleID:          "RULE-DORMANT-OK",
		Package:         "internal/worker",
		Weight:          0.45,
		ThermalState:    ThermalWarm,
		TPCount:         5,
		DormantTriggers: 5,
		DormantEscapes:  2, // 2/5 = 40% >= 5%
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	repFalsified, err := eng.EvaluateHypotheses(ctx, 3)
	if err != nil {
		t.Fatalf("EvaluateHypotheses falsified: %v", err)
	}
	if repFalsified.Hypotheses[1].ID != "H2" || repFalsified.Hypotheses[1].EpistemicStatus != EpistemicFalsified {
		t.Fatalf("expected H2 to be falsified when escape rate is 40%%, got %+v", repFalsified.Hypotheses[1])
	}
}
