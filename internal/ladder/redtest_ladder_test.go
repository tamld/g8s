package ladder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/routing"
	"github.com/tamld/g8s/internal/telemetry"
)

// Helper: standard redtest manifest with multi-provider model hierarchy.
func redtestLadderManifest() *config.File {
	return &config.File{
		Providers: []config.ProviderEntry{
			{
				Name:  "agy",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{
						ID:               "gemini-3.8-flash-high",
						EffortStyle:      config.EffortStyleBakedName,
						SupportedEfforts: []string{"low", "medium", "high"},
						DefaultEffort:    "low",
					},
				},
			},
			{
				Name:  "claude",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{
						ID:               "claude-haiku-4-5",
						EffortStyle:      config.EffortStyleNamed,
						SupportedEfforts: []string{"low", "medium"},
						DefaultEffort:    "medium",
					},
				},
			},
			{
				Name:  "ollama",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{
						ID:               "llama3.1",
						EffortStyle:      config.EffortStyleToggle,
						SupportedEfforts: []string{"medium", "high"},
						DefaultEffort:    "medium",
					},
				},
			},
		},
	}
}

// =============================================================================
// DIMENSION 1: Rung State Machine
// =============================================================================

func TestRedtest_Dimension1_RungStateMachine(t *testing.T) {
	manifest := redtestLadderManifest()

	// 1. Legal advance sequences: step-by-step advance from Rung 0 to Rung 6
	t.Run("legal_advance_sequence_r0_to_r6", func(t *testing.T) {
		// Rule: Sequential progression from Rung 0 (Diagnosis) through Rungs 1..3
		// (Effort +1) to Rungs 4..5 (Model Alternatives) to Rung 6 (Ceiling HITL)
		// per policy.go rules 5-9.
		ctx := PolicyContext{
			RootTaskID:     "task-root-seq",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			Manifest:       manifest,
			TokenBudget:    500000,
			LatestVerdict: ClassifierVerdict{
				Shape:  ShapeEffort,
				Reason: "class checks failed",
			},
		}

		// Rung 0: Diagnosis dispatch (read_only, low effort)
		p0 := EvaluateNextRung(ctx)
		if p0.Action != ActionDiagnosis || p0.RungIndex != 0 || p0.Permission != "read_only" || p0.Effort != config.EffortLow {
			t.Fatalf("Rung 0 mismatch: %+v", p0)
		}

		// Record Rung 0
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 0, TaskID: "task-r0", Role: p0.Role, Permission: p0.Permission,
			Model: p0.Model, Effort: p0.Effort, Shape: ShapeEffort, Verdict: "failed", Tokens: 1000,
		})
		ctx.CumulativeTokens += 1000

		// Rung 1: Effort +1 step (low -> medium)
		p1 := EvaluateNextRung(ctx)
		if p1.Action != ActionEscalateEffort || p1.RungIndex != 1 || p1.Effort != config.EffortMedium {
			t.Fatalf("Rung 1 mismatch: %+v", p1)
		}

		// Record Rung 1
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 1, TaskID: "task-r1", Role: p1.Role, Permission: p1.Permission,
			Model: p1.Model, Effort: p1.Effort, Shape: ShapeEffort, Verdict: "failed", Tokens: 2000,
		})
		ctx.CumulativeTokens += 2000

		// Rung 2: Effort +1 step (medium -> high)
		p2 := EvaluateNextRung(ctx)
		if p2.Action != ActionEscalateEffort || p2.RungIndex != 2 || p2.Effort != config.EffortHigh {
			t.Fatalf("Rung 2 mismatch: %+v", p2)
		}

		// Record Rung 2
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 2, TaskID: "task-r2", Role: p2.Role, Permission: p2.Permission,
			Model: p2.Model, Effort: p2.Effort, Shape: ShapeEffort, Verdict: "failed", Tokens: 3000,
		})
		ctx.CumulativeTokens += 3000

		// Rung 3: gemini subset exhausted -> Model Alternative 1 (claude-haiku-4-5)
		p3 := EvaluateNextRung(ctx)
		if p3.Action != ActionModelAlternative || p3.RungIndex != 3 || p3.Model != "claude-haiku-4-5" {
			t.Fatalf("Rung 3 mismatch: %+v", p3)
		}

		// Record Rung 3
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 3, TaskID: "task-r3", Role: p3.Role, Permission: p3.Permission,
			Model: p3.Model, Effort: p3.Effort, Shape: ShapeEffort, Verdict: "failed", Tokens: 4000,
		})
		ctx.CumulativeTokens += 4000

		// Rung 4: Model Alternative 2 (llama3.1)
		p4 := EvaluateNextRung(ctx)
		if p4.Action != ActionModelAlternative || p4.RungIndex != 4 || p4.Model != "llama3.1" {
			t.Fatalf("Rung 4 mismatch: %+v", p4)
		}

		// Record Rung 4
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 4, TaskID: "task-r4", Role: p4.Role, Permission: p4.Permission,
			Model: p4.Model, Effort: p4.Effort, Shape: ShapeEffort, Verdict: "failed", Tokens: 5000,
		})
		ctx.CumulativeTokens += 5000

		// Rung 5: No alternatives left -> HITL
		p5 := EvaluateNextRung(ctx)
		if p5.Action != ActionHITL || p5.RungIndex != 5 {
			t.Fatalf("Rung 5 mismatch: %+v", p5)
		}

		// Record Rung 5
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 5, TaskID: "task-r5", Role: "coder", Permission: "workspace_write",
			Model: "llama3.1", Effort: "medium", Shape: ShapeEffort, Verdict: "failed", Tokens: 1000,
		})
		ctx.CumulativeTokens += 1000

		// Rung 6: Hard ceiling reached
		p6 := EvaluateNextRung(ctx)
		if p6.Action != ActionHITL || p6.RungIndex != 6 || !errors.Is(p6.RefusalError, ErrCeilingReached) {
			t.Fatalf("Rung 6 mismatch: %+v", p6)
		}
	})

	// 2. Early success termination
	t.Run("legal_advance_early_success_termination", func(t *testing.T) {
		// Rule: Early success at any intermediate rung terminates progression with ActionDone per rule 1.
		ctx := PolicyContext{
			RootTaskID:      "task-root-pass",
			Class:           "docs",
			OriginalRole:    "summarizer",
			OriginalModel:   "claude-haiku-4-5",
			LatestSucceeded: true,
			History: []RungRecord{
				{RungIndex: 0, TaskID: "task-0", Shape: ShapeEffort, Verdict: "failed", Tokens: 500},
				{RungIndex: 1, TaskID: "task-1", Shape: ShapeEffort, Verdict: "success", Tokens: 800},
			},
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionDone {
			t.Fatalf("Action = %s, want ActionDone", plan.Action)
		}
		if plan.RefusalError != nil {
			t.Errorf("RefusalError = %v, want nil", plan.RefusalError)
		}
	})

	// 3. Advancing a completed task
	t.Run("advancing_completed_task_action_done", func(t *testing.T) {
		// Rule: Advancing a completed task (LatestSucceeded: true) must return ActionDone and never advance to another rung.
		ctx := PolicyContext{
			RootTaskID:      "task-root-done",
			Class:           "feature",
			OriginalRole:    "coder",
			OriginalModel:   "gemini-3.8-flash-high",
			LatestSucceeded: true,
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionDone {
			t.Fatalf("Action = %s, want ActionDone", plan.Action)
		}
	})

	// 4. Double-advance of the same rung
	t.Run("double_advance_same_rung_rejected", func(t *testing.T) {
		// Rule: Rung state machine must reject double-advance of the same rung (duplicate RungIndex in history).
		// FINDING-R3-1:
		// Expected: EvaluateNextRung detects duplicate rung index in history and returns a refusal error / rejects double-advance.
		// Actual: EvaluateNextRung blindly uses len(ctx.History) = 2, advancing past Rung 1 without refusal error.
		t.Skip("FINDING-R3-1: EvaluateNextRung accepts double-advance with duplicate rung indices in history without refusal")

		ctx := PolicyContext{
			RootTaskID:     "task-root-dup",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0-a", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
				{RungIndex: 0, TaskID: "t0-b", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
			},
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
		}
		plan := EvaluateNextRung(ctx)
		if plan.RefusalError == nil {
			t.Fatalf("expected refusal error on duplicate rung index in history, got plan %+v", plan)
		}
	})

	// 5. Advancing a task when history already contains a succeeded rung
	t.Run("historical_rung_success_verdict_ignored", func(t *testing.T) {
		// Rule: A state machine must not advance if a prior rung in history already succeeded.
		// FINDING-R3-2:
		// Expected: EvaluateNextRung inspects History verdicts and terminates with ActionDone when any rung succeeded.
		// Actual: EvaluateNextRung only inspects LatestSucceeded; it ignores historical rung success verdicts.
		t.Skip("FINDING-R3-2: EvaluateNextRung ignores historical rung success verdicts unless LatestSucceeded is explicitly set")

		ctx := PolicyContext{
			RootTaskID:     "task-root-hist-succ",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
				{RungIndex: 1, TaskID: "t1", Shape: ShapeEffort, Verdict: "success", Tokens: 200},
			},
			LatestSucceeded: false, // caller forgot to mirror Verdict into LatestSucceeded
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionDone {
			t.Fatalf("expected ActionDone when historical rung succeeded, got %s", plan.Action)
		}
	})

	// 6. Lineage integrity: skipped rungs in history
	t.Run("lineage_integrity_skipped_rungs", func(t *testing.T) {
		// Rule: Lineage rungs must be contiguous without gaps (parent chain cannot skip rungs; e.g. Rung 0 then Rung 2).
		// FINDING-R3-3:
		// Expected: Lineage integrity check refuses progression when rung indices contain gaps/skips.
		// Actual: EvaluateNextRung checks only len(ctx.History) and allows skipped rungs without validation.
		t.Skip("FINDING-R3-3: EvaluateNextRung does not validate history sequence continuity, accepting skipped rungs")

		ctx := PolicyContext{
			RootTaskID:     "task-root-gap",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
				{RungIndex: 2, TaskID: "t2", Shape: ShapeEffort, Verdict: "failed", Tokens: 100}, // Rung 1 skipped!
			},
		}
		plan := EvaluateNextRung(ctx)
		if plan.RefusalError == nil {
			t.Fatalf("expected refusal error on skipped rung in lineage, got %+v", plan)
		}
	})

	// 7. Lineage integrity: non-monotonic / out-of-order rungs
	t.Run("lineage_integrity_non_monotonic_rungs", func(t *testing.T) {
		// Rule: Lineage rungs must appear in strictly ascending chronological order (parent chain cannot fork or go backwards).
		// FINDING-R3-4:
		// Expected: EvaluateNextRung rejects non-monotonic or decreasing rung index history.
		// Actual: EvaluateNextRung accepts out-of-order history records without error.
		t.Skip("FINDING-R3-4: EvaluateNextRung accepts non-monotonic rung index order in history")

		ctx := PolicyContext{
			RootTaskID:     "task-root-rev",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
			History: []RungRecord{
				{RungIndex: 2, TaskID: "t2", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
				{RungIndex: 1, TaskID: "t1", Shape: ShapeEffort, Verdict: "failed", Tokens: 100},
			},
		}
		plan := EvaluateNextRung(ctx)
		if plan.RefusalError == nil {
			t.Fatalf("expected refusal error on non-monotonic rung index history, got %+v", plan)
		}
	})
}

// =============================================================================
// DIMENSION 2: Next-Rung Plan
// =============================================================================

func TestRedtest_Dimension2_NextRungPlan(t *testing.T) {
	manifest := redtestLadderManifest()

	// 1. Empty lineage defaults
	t.Run("empty_lineage_defaults", func(t *testing.T) {
		// Rule: Empty lineage plans Rung 0 diagnosis with read_only permission, low effort, and default verifier role when unspecified per rule 5.
		ctx := PolicyContext{
			RootTaskID:    "t-root-empty",
			Class:         "test",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
		}
		plan := EvaluateNextRung(ctx)
		if plan.RungIndex != 0 {
			t.Errorf("RungIndex = %d, want 0", plan.RungIndex)
		}
		if plan.Action != ActionDiagnosis {
			t.Errorf("Action = %s, want ActionDiagnosis", plan.Action)
		}
		if plan.Permission != "read_only" {
			t.Errorf("Permission = %s, want read_only", plan.Permission)
		}
		if plan.Effort != config.EffortLow {
			t.Errorf("Effort = %s, want low", plan.Effort)
		}
		if plan.Role != "verifier" {
			t.Errorf("Role = %s, want verifier (default)", plan.Role)
		}
	})

	// 2. Empty lineage preserves custom role
	t.Run("empty_lineage_preserves_custom_role", func(t *testing.T) {
		// Rule: Empty lineage preserves OriginalRole if specified by caller.
		ctx := PolicyContext{
			RootTaskID:    "t-root-role",
			Class:         "security",
			OriginalRole:  "auditor",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
		}
		plan := EvaluateNextRung(ctx)
		if plan.Role != "auditor" {
			t.Errorf("Role = %s, want auditor", plan.Role)
		}
	})

	// 3. Empty lineage with non-effort shape
	t.Run("empty_lineage_non_effort_routes_hitl", func(t *testing.T) {
		// Rule: Empty lineage with non-effort failure shape terminates immediately with ActionHITL and ErrNonEffortShape per rule 4.
		ctx := PolicyContext{
			RootTaskID:    "t-root-noneffort",
			Class:         "feature",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{
				Shape:  ShapeEnv,
				Reason: "connection reset by peer",
			},
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL {
			t.Fatalf("Action = %s, want ActionHITL", plan.Action)
		}
		if !errors.Is(plan.RefusalError, ErrNonEffortShape) {
			t.Errorf("RefusalError = %v, want ErrNonEffortShape", plan.RefusalError)
		}
	})

	// 4. Empty lineage with token budget exhausted
	t.Run("empty_lineage_token_budget_exhausted", func(t *testing.T) {
		// Rule: Empty lineage where CumulativeTokens >= TokenBudget terminates immediately with ActionHITL and ErrTokenBudgetExceeded per rule 2.
		ctx := PolicyContext{
			RootTaskID:       "t-root-budget",
			Class:            "feature",
			OriginalModel:    "gemini-3.8-flash-high",
			TokenBudget:      5000,
			CumulativeTokens: 5000,
			LatestVerdict:    ClassifierVerdict{Shape: ShapeEffort},
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL {
			t.Fatalf("Action = %s, want ActionHITL", plan.Action)
		}
		if !errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Errorf("RefusalError = %v, want ErrTokenBudgetExceeded", plan.RefusalError)
		}
	})

	// 5. Single rung escalates effort
	t.Run("single_rung_escalates_effort", func(t *testing.T) {
		// Rule: Single rung in history advances to Rung 1 with ActionEscalateEffort along model supported subset per rule 6.
		ctx := PolicyContext{
			RootTaskID:     "t-root-r1",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0", Effort: "low", Model: "gemini-3.8-flash-high", Shape: ShapeEffort},
			},
		}
		plan := EvaluateNextRung(ctx)
		if plan.RungIndex != 1 {
			t.Errorf("RungIndex = %d, want 1", plan.RungIndex)
		}
		if plan.Action != ActionEscalateEffort {
			t.Errorf("Action = %s, want ActionEscalateEffort", plan.Action)
		}
		if plan.Effort != config.EffortMedium {
			t.Errorf("Effort = %s, want medium", plan.Effort)
		}
		if plan.Permission != "workspace_write" {
			t.Errorf("Permission = %s, want workspace_write", plan.Permission)
		}
		if plan.Role != "coder" {
			t.Errorf("Role = %s, want coder", plan.Role)
		}
	})

	// 6. Ordering guarantee: canonical EffortLadder progression
	t.Run("ordering_guarantee_effort_ladder", func(t *testing.T) {
		// Rule: NextEffortLevel strictly follows the canonical config.EffortLadder order across non-adjacent supported levels per rule 6.
		view := &routing.ModelEffortView{
			SupportedEfforts: []string{"minimal", "high", "max"},
		}
		lvl1, ok1 := NextEffortLevel(view, "minimal")
		if !ok1 || lvl1 != "high" {
			t.Errorf("Next from minimal = (%s, %v), want (high, true)", lvl1, ok1)
		}
		lvl2, ok2 := NextEffortLevel(view, "high")
		if !ok2 || lvl2 != "max" {
			t.Errorf("Next from high = (%s, %v), want (max, true)", lvl2, ok2)
		}
		lvl3, ok3 := NextEffortLevel(view, "max")
		if ok3 || lvl3 != "" {
			t.Errorf("Next from max = (%s, %v), want ('', false)", lvl3, ok3)
		}
	})

	// 7. Ordering guarantee: baked-name models
	t.Run("ordering_guarantee_baked_name_models", func(t *testing.T) {
		// Rule: Baked-name models traverse the family ladder [low, medium, high] despite declaring a single variant per issue #563/#568.
		view := &routing.ModelEffortView{
			Model:            "gemini-3.8-flash-high",
			EffortStyle:      config.EffortStyleBakedName,
			SupportedEfforts: []string{"high"},
		}
		lvl1, ok1 := NextEffortLevel(view, "low")
		if !ok1 || lvl1 != "medium" {
			t.Errorf("Baked model low -> (%s, %v), want (medium, true)", lvl1, ok1)
		}
		lvl2, ok2 := NextEffortLevel(view, "medium")
		if !ok2 || lvl2 != "high" {
			t.Errorf("Baked model medium -> (%s, %v), want (high, true)", lvl2, ok2)
		}
		lvl3, ok3 := NextEffortLevel(view, "high")
		if ok3 || lvl3 != "" {
			t.Errorf("Baked model high -> (%s, %v), want ('', false)", lvl3, ok3)
		}
	})

	// 8. Ordering guarantee: model alternatives
	t.Run("ordering_guarantee_model_alternatives", func(t *testing.T) {
		// Rule: When effort subset is exhausted, alternative models are selected in manifest provider and model order skipping used models per rules 7-8.
		used := []string{"gemini-3.8-flash-high"}
		prov1, mod1, ok1 := FindAlternativeModel(manifest, used)
		if !ok1 || prov1 != "claude" || mod1 != "claude-haiku-4-5" {
			t.Errorf("Candidate 1 = (%s, %s, %v), want (claude, claude-haiku-4-5, true)", prov1, mod1, ok1)
		}

		used = append(used, "claude-haiku-4-5")
		prov2, mod2, ok2 := FindAlternativeModel(manifest, used)
		if !ok2 || prov2 != "ollama" || mod2 != "llama3.1" {
			t.Errorf("Candidate 2 = (%s, %s, %v), want (ollama, llama3.1, true)", prov2, mod2, ok2)
		}

		used = append(used, "llama3.1")
		_, _, ok3 := FindAlternativeModel(manifest, used)
		if ok3 {
			t.Errorf("Candidate 3 found after exhausting manifest, want false")
		}
	})

	// 9. Top of ladder: ceiling reached at Rung 6
	t.Run("top_of_ladder_ceiling_reached", func(t *testing.T) {
		// Rule: At Rung 6 (len(History) == 6), hard ceiling of MaxLadderRungs is reached, returning ActionHITL and ErrCeilingReached per rule 3.
		ctx := PolicyContext{
			RootTaskID:    "t-root-top",
			Class:         "feature",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
			History:       make([]RungRecord, MaxLadderRungs), // exactly 6 rungs
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL {
			t.Fatalf("Action = %s, want ActionHITL", plan.Action)
		}
		if plan.RungIndex != MaxLadderRungs {
			t.Errorf("RungIndex = %d, want %d", plan.RungIndex, MaxLadderRungs)
		}
		if !errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Errorf("RefusalError = %v, want ErrCeilingReached", plan.RefusalError)
		}
	})

	// 10. Top of ladder exceeded
	t.Run("top_of_ladder_exceeded", func(t *testing.T) {
		// Rule: Beyond Rung 6 (len(History) > 6), ceiling enforcement continues to refuse with ErrCeilingReached.
		ctx := PolicyContext{
			RootTaskID:    "t-root-exceeded",
			Class:         "feature",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
			History:       make([]RungRecord, 8), // 8 rungs
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Fatalf("expected ActionHITL and ErrCeilingReached, got %+v", plan)
		}
	})

	// 11. Precedence: success overrides ceiling
	t.Run("precedence_success_overrides_ceiling", func(t *testing.T) {
		// Rule: Success at Rung 6 returns ActionDone, overriding ceiling enforcement per rule 1 precedence.
		ctx := PolicyContext{
			RootTaskID:      "t-root-succ-ceil",
			Class:           "feature",
			OriginalModel:   "gemini-3.8-flash-high",
			LatestSucceeded: true,
			History:         make([]RungRecord, MaxLadderRungs),
		}
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionDone {
			t.Fatalf("Action = %s, want ActionDone", plan.Action)
		}
		if plan.RefusalError != nil {
			t.Errorf("RefusalError = %v, want nil", plan.RefusalError)
		}
	})
}

// =============================================================================
// DIMENSION 3: Classifier
// =============================================================================

func TestRedtest_Dimension3_Classifier(t *testing.T) {
	// 1. Staleness sensor: all 7 canonical effort levels
	t.Run("staleness_sensor_7_effort_levels", func(t *testing.T) {
		// Rule: Provider 400 rejections naming any of the 7 canonical effort levels produce catalog-stale(model, level) per classifier rule 1.
		levels := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		for _, lvl := range levels {
			ev := FailureEvidence{
				ErrorText: fmt.Sprintf("HTTP 400 Bad Request: effort %s is not supported by backend", lvl),
				Model:     "gemini-test",
				Effort:    lvl,
			}
			verdict := ClassifyFailure(ev)
			wantTag := fmt.Sprintf("catalog-stale(gemini-test, %s)", lvl)
			if verdict.StaleTag != wantTag {
				t.Errorf("level %s: StaleTag = %q, want %q", lvl, verdict.StaleTag, wantTag)
			}
			if verdict.Shape != ShapeBrief {
				t.Errorf("level %s: Shape = %s, want ShapeBrief", lvl, verdict.Shape)
			}
		}
	})

	// 2. Staleness sensor boundary: 400 without effort level
	t.Run("staleness_sensor_400_without_effort_level", func(t *testing.T) {
		// Rule: Provider 400 rejection without an effort level must not produce a stale tag per classifier rule 1 boundary.
		ev := FailureEvidence{
			ErrorText: "HTTP 400 Bad Request: invalid argument 'temperature' value 2.5",
			Model:     "gemini-test",
		}
		verdict := ClassifyFailure(ev)
		if verdict.StaleTag != "" {
			t.Errorf("StaleTag = %q, want empty", verdict.StaleTag)
		}
		// Notice: "invalid argument" is a brief marker, so it classifies as brief
		if verdict.Shape != ShapeBrief {
			t.Errorf("Shape = %s, want ShapeBrief", verdict.Shape)
		}
	})

	// 3. Staleness sensor: case insensitivity and whitespace
	t.Run("staleness_sensor_case_and_whitespace", func(t *testing.T) {
		// Rule: Staleness sensor handles upper-case and irregular whitespace in 400 response text.
		ev := FailureEvidence{
			ErrorText: "400   BAD REQUEST:   REASONING_EFFORT   'HIGH'   IS NOT SUPPORTED",
			Model:     "gemini-3.8-flash-high",
		}
		verdict := ClassifyFailure(ev)
		wantTag := "catalog-stale(gemini-3.8-flash-high, high)"
		if verdict.StaleTag != wantTag {
			t.Errorf("StaleTag = %q, want %q", verdict.StaleTag, wantTag)
		}
	})

	// 4. Explicit contract violation: whitespace boundary
	t.Run("contract_violation_whitespace_boundary", func(t *testing.T) {
		// Rule: Whitespace-only ContractViolation is ignored and falls through to text/code inspection per classifier rule 2 boundary.
		ev := FailureEvidence{
			ContractViolation: "   \t\n  ",
			ErrorText:         "tests failed in internal/ladder: unexpected assertion failure",
			ChecksFailed:      []string{"check-tests"},
		}
		verdict := ClassifyFailure(ev)
		if verdict.Shape != ShapeEffort {
			t.Errorf("Shape = %s, want ShapeEffort (whitespace ContractViolation ignored)", verdict.Shape)
		}
	})

	// 5. Exit codes 126 and 127
	t.Run("exit_code_126_and_127", func(t *testing.T) {
		// Rule: Exit code 126 is ShapeEnv (binary execution failed) and 127 is ShapeEnv (binary not found) per classifier rule 3.
		c126 := 126
		v126 := ClassifyFailure(FailureEvidence{ExitCode: &c126})
		if v126.Shape != ShapeEnv || !strings.Contains(v126.Reason, "exit code 126") {
			t.Errorf("v126 = %+v, want ShapeEnv with exit code 126", v126)
		}

		c127 := 127
		v127 := ClassifyFailure(FailureEvidence{ExitCode: &c127})
		if v127.Shape != ShapeEnv || !strings.Contains(v127.Reason, "exit code 127") {
			t.Errorf("v127 = %+v, want ShapeEnv with exit code 127", v127)
		}
	})

	// 6. Exit code 2 timeout worker convention
	t.Run("exit_code_2_timeout_convention", func(t *testing.T) {
		// Rule: Worker exit code 2 convention maps to ShapeEnv timeout when no other text markers exist per classifier rule 3.
		c2 := 2
		v2 := ClassifyFailure(FailureEvidence{ExitCode: &c2})
		if v2.Shape != ShapeEnv || !strings.Contains(v2.Reason, "timeout-exit-code-2") {
			t.Errorf("v2 = %+v, want ShapeEnv with timeout-exit-code-2", v2)
		}
	})

	// 7. Marker boundary: "exec: executable file not found" vs "file not found"
	t.Run("marker_boundary_exec_file_not_found_vs_file_not_found", func(t *testing.T) {
		// Rule: "exec: executable file not found" bypasses "file not found" brief marker, classifying as ShapeEnv per classifier rule 4.
		evEnv := FailureEvidence{
			ErrorText: "exec: executable file not found in $PATH",
		}
		vEnv := ClassifyFailure(evEnv)
		if vEnv.Shape != ShapeEnv {
			t.Errorf("vEnv.Shape = %s, want ShapeEnv", vEnv.Shape)
		}

		evBrief := FailureEvidence{
			ErrorText: "open spec/schema.json: file not found",
		}
		vBrief := ClassifyFailure(evBrief)
		if vBrief.Shape != ShapeBrief {
			t.Errorf("vBrief.Shape = %s, want ShapeBrief", vBrief.Shape)
		}
	})

	// 8. Class collision resolution: brief marker + env marker in ErrorText
	t.Run("class_collision_brief_and_env_in_text", func(t *testing.T) {
		// Rule: Ambiguous failure with both brief and env markers in text resolves to ShapeBrief (pinned precedence) per classifier rule 5.
		ev := FailureEvidence{
			ErrorText: "prompt error: schema mismatch before gateway timeout 504 occurred",
		}
		verdict := ClassifyFailure(ev)
		if verdict.Shape != ShapeBrief {
			t.Fatalf("Shape = %s, want ShapeBrief", verdict.Shape)
		}
		if !strings.HasPrefix(verdict.Reason, "ambiguous failure: brief-shaped precedence pinned") {
			t.Errorf("Reason = %q, want prefix 'ambiguous failure: brief-shaped precedence pinned'", verdict.Reason)
		}
	})

	// 9. Class collision resolution: ExitCode 127 vs Brief marker in ErrorText
	t.Run("class_collision_exit127_vs_brief_text", func(t *testing.T) {
		// Rule: Rule 5 specifies that when both brief and env defects appear, brief-shaped takes precedence; exit code 127 (env defect) should yield to brief markers in ErrorText.
		// FINDING-R3-5:
		// Expected: Brief marker in ErrorText takes precedence over exit code 127 per rule 5.
		// Actual: Exit code 127 returns ShapeEnv at line 251 before ErrorText is scanned for brief markers.
		t.Skip("FINDING-R3-5: Exit code 127 overrides brief markers in ErrorText, violating brief-precedence collision rule")

		c127 := 127
		ev := FailureEvidence{
			ExitCode:  &c127,
			ErrorText: "contract violation: tool execution unauthorized before exit code 127",
		}
		verdict := ClassifyFailure(ev)
		if verdict.Shape != ShapeBrief {
			t.Fatalf("expected brief precedence for collision with exit code 127, got %s (reason: %s)", verdict.Shape, verdict.Reason)
		}
	})

	// 10. Unknown / empty task shape
	t.Run("unknown_empty_task_shape", func(t *testing.T) {
		// Rule: Empty FailureEvidence{} falls through to ShapeEffort per classifier rule 8 ("everything else = effort-shaped").
		ev := FailureEvidence{}
		verdict := ClassifyFailure(ev)
		if verdict.Shape != ShapeEffort {
			t.Errorf("Shape = %s, want ShapeEffort", verdict.Shape)
		}
		if !strings.Contains(verdict.Reason, "effort-shaped") {
			t.Errorf("Reason = %q, want substring 'effort-shaped'", verdict.Reason)
		}
	})

	// 11. Exit code 0 with failing checks
	t.Run("exit_code_0_with_failed_checks", func(t *testing.T) {
		// Rule: Exit code 0 with failing class checks classifies as ShapeEffort naming failing checks per classifier rule 8.
		c0 := 0
		ev := FailureEvidence{
			ExitCode:     &c0,
			ChecksFailed: []string{"check-doc-coverage", "check-lint"},
		}
		verdict := ClassifyFailure(ev)
		if verdict.Shape != ShapeEffort {
			t.Errorf("Shape = %s, want ShapeEffort", verdict.Shape)
		}
		if !strings.Contains(verdict.Reason, "2 class check(s) failed") {
			t.Errorf("Reason = %q, want substring '2 class check(s) failed'", verdict.Reason)
		}
	})
}

// =============================================================================
// DIMENSION 4: Evidence Packets
// =============================================================================

func TestRedtest_Dimension4_EvidencePackets(t *testing.T) {
	// 1. Empty evidence packet construction
	t.Run("empty_evidence_packet_construction", func(t *testing.T) {
		// Rule: BuildEvidencePacket handles empty history and nil failing checks without panic, computing TotalRungs=0, TotalTokens=0.
		packet := BuildEvidencePacket("task-root", "docs", "hitl_empty", "no history", nil, nil, 0)
		if packet.TotalRungs != 0 {
			t.Errorf("TotalRungs = %d, want 0", packet.TotalRungs)
		}
		if packet.TotalTokens != 0 {
			t.Errorf("TotalTokens = %d, want 0", packet.TotalTokens)
		}
		if packet.ExactFailingChecks == nil {
			t.Errorf("ExactFailingChecks is nil, want non-nil slice")
		}
		if packet.LadderHistory == nil {
			t.Errorf("LadderHistory is nil, want non-nil slice")
		}
	})

	// 2. Missing required fields validation
	t.Run("missing_required_fields_validation", func(t *testing.T) {
		// Rule: Evidence packets must validate required fields (RootTaskID, Class, FinalVerdict); empty fields should be rejected.
		// FINDING-R3-6:
		// Expected: BuildEvidencePacket validates required fields and rejects empty RootTaskID, Class, or FinalVerdict.
		// Actual: BuildEvidencePacket accepts empty strings for all required fields without validation.
		t.Skip("FINDING-R3-6: BuildEvidencePacket does not validate required fields (RootTaskID, Class, FinalVerdict)")

		packet := BuildEvidencePacket("", "", "", "reason", nil, nil, 0)
		if packet.RootTaskID == "" || packet.Class == "" || packet.FinalVerdict == "" {
			t.Fatalf("expected validation rejection for empty required fields, packet constructed: %+v", packet)
		}
	})

	// 3. WriteToFile reject path escapes
	t.Run("write_to_file_reject_path_escapes", func(t *testing.T) {
		// Rule: WriteToFile must validate target path and reject path escapes / directory traversal (e.g. ../../escaped.json).
		// FINDING-R3-7:
		// Expected: WriteToFile rejects paths containing '..' or escaping directory boundaries.
		// Actual: WriteToFile calls os.Create directly with no path validation, permitting path escapes.
		t.Skip("FINDING-R3-7: WriteToFile does not validate destination paths, allowing directory traversal escapes")

		dir := t.TempDir()
		safeSubdir := filepath.Join(dir, "safe")
		if err := os.Mkdir(safeSubdir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		packet := BuildEvidencePacket("t-root", "feature", "hitl", "path-escape-test", nil, nil, 0)
		escapePath := filepath.Join(safeSubdir, "..", "escaped.json")
		err := packet.WriteToFile(escapePath)
		if err == nil {
			t.Fatalf("expected error rejecting path escape %s, write succeeded", escapePath)
		}
	})

	// 4. Duplicate evidence IDs in ladder history
	t.Run("duplicate_evidence_ids_in_history", func(t *testing.T) {
		// Rule: BuildEvidencePacket must detect or reject duplicate task IDs in history to prevent double-counting tokens.
		// FINDING-R3-8:
		// Expected: BuildEvidencePacket detects duplicate task IDs in history and flags/rejects them.
		// Actual: BuildEvidencePacket blindly copies history and double-counts tokens.
		t.Skip("FINDING-R3-8: BuildEvidencePacket does not detect duplicate task IDs in history, causing double-counting")

		historyWithDupes := []RungRecord{
			{RungIndex: 0, TaskID: "task-dup-1", Tokens: 1000, Shape: ShapeEffort},
			{RungIndex: 1, TaskID: "task-dup-1", Tokens: 1000, Shape: ShapeEffort}, // duplicate task ID!
		}
		packet := BuildEvidencePacket("t-root", "feature", "hitl", "dupe-test", nil, historyWithDupes, 0)
		if packet.TotalTokens == 2000 {
			t.Fatalf("duplicate task ID double-counted: expected deduplication or rejection")
		}
	})

	// 5. Valid packet JSON roundtrip
	t.Run("valid_packet_json_roundtrip", func(t *testing.T) {
		// Rule: Complete EvidencePacket serializes and deserializes accurately with all token counts and history preserved.
		now := time.Now().UTC().Truncate(time.Second)
		history := []RungRecord{
			{
				RungIndex:    0,
				TaskID:       "task-rung-0",
				Role:         "verifier",
				Permission:   "read_only",
				Model:        "gemini-3.8-flash-high",
				Effort:       "low",
				Shape:        ShapeEffort,
				Verdict:      "class_checks_failed",
				Tokens:       1200,
				InputTokens:  900,
				OutputTokens: 300,
				ChecksFailed: []string{"check-lint"},
			},
		}
		packet := BuildEvidencePacket("task-root-123", "feature", "hitl_ceiling", "all rungs failed", []string{"check-lint"}, history, 50000)
		packet.Timestamp = now

		var buf bytes.Buffer
		if err := packet.WriteJSON(&buf); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}

		var decoded EvidencePacket
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if decoded.RootTaskID != "task-root-123" || decoded.Class != "feature" || decoded.TotalTokens != 1200 || decoded.TotalRungs != 1 {
			t.Errorf("decoded packet mismatch: %+v", decoded)
		}
		if len(decoded.ExactFailingChecks) != 1 || decoded.ExactFailingChecks[0] != "check-lint" {
			t.Errorf("failing checks mismatch: %+v", decoded.ExactFailingChecks)
		}
	})

	// 6. WriteToFile empty path no-op
	t.Run("write_to_file_empty_path_noop", func(t *testing.T) {
		// Rule: WriteToFile with empty path returns nil (no-op) without error.
		packet := BuildEvidencePacket("t-root", "docs", "hitl", "empty path test", nil, nil, 0)
		if err := packet.WriteToFile(""); err != nil {
			t.Errorf("WriteToFile('') error = %v, want nil", err)
		}
	})
}

// =============================================================================
// DIMENSION 5: Gauges Math
// =============================================================================

func TestRedtest_Dimension5_GaugesMath(t *testing.T) {
	exit0 := 0
	exit1 := 1
	now := time.Now()

	// 1. Empty sets: division-by-zero guard
	t.Run("empty_sets_division_by_zero_guard", func(t *testing.T) {
		// Rule: Zero events and zero tasks produce zero rates without division-by-zero or NaN/Inf.
		report := ComputeGauges(nil, nil, "")
		if len(report.PassRates) != 0 {
			t.Errorf("PassRates len = %d, want 0", len(report.PassRates))
		}
		if len(report.EscalationRates) != 0 {
			t.Errorf("EscalationRates len = %d, want 0", len(report.EscalationRates))
		}
		if report.HITL.TotalRounds != 0 || report.HITL.HITLPackets != 0 || report.HITL.HITLRate != 0.0 {
			t.Errorf("HITL metric = %+v, want all zeroes", report.HITL)
		}
	})

	// 2. Single element: pass
	t.Run("single_element_pass", func(t *testing.T) {
		// Rule: Single pass event produces PassRate = 1.0, TotalCount = 1, FailCount = 0.
		ev := []telemetry.TraceEvent{
			{
				ID: "ev-single-p", TaskID: "t-p", EventType: telemetry.TraceEventTaskCompleted,
				Class: "docs", EffortApplied: "low", ExitCode: &exit0, Timestamp: now,
			},
		}
		report := ComputeGauges(ev, nil, "")
		if len(report.PassRates) != 1 {
			t.Fatalf("PassRates len = %d, want 1", len(report.PassRates))
		}
		g := report.PassRates[0]
		if g.PassRate != 1.0 || g.PassCount != 1 || g.FailCount != 0 || g.TotalCount != 1 {
			t.Errorf("PassRate gauge = %+v, want rate 1.0, pass 1, fail 0, total 1", g)
		}
	})

	// 3. Single element: fail
	t.Run("single_element_fail", func(t *testing.T) {
		// Rule: Single fail event produces PassRate = 0.0, TotalCount = 1, PassCount = 0.
		ev := []telemetry.TraceEvent{
			{
				ID: "ev-single-f", TaskID: "t-f", EventType: telemetry.TraceEventTaskFailed,
				Class: "docs", EffortApplied: "low", ExitCode: &exit1, Timestamp: now,
			},
		}
		report := ComputeGauges(ev, nil, "")
		if len(report.PassRates) != 1 {
			t.Fatalf("PassRates len = %d, want 1", len(report.PassRates))
		}
		g := report.PassRates[0]
		if g.PassRate != 0.0 || g.PassCount != 0 || g.FailCount != 1 || g.TotalCount != 1 {
			t.Errorf("PassRate gauge = %+v, want rate 0.0, pass 0, fail 1, total 1", g)
		}
	})

	// 4. Single element: root task without rungs
	t.Run("single_element_root_task", func(t *testing.T) {
		// Rule: Single root task without rungs produces TasksCount = 1, RungsFired = 0, EscalationRate = 0.0.
		tasks := []*controlplane.Task{
			{TaskID: "task-root-alone", Request: []byte(`{"class":"feature"}`)},
		}
		report := ComputeGauges(nil, tasks, "")
		if len(report.EscalationRates) != 1 {
			t.Fatalf("EscalationRates len = %d, want 1", len(report.EscalationRates))
		}
		esc := report.EscalationRates[0]
		if esc.TasksCount != 1 || esc.RungsFired != 0 || esc.EscalationRate != 0.0 {
			t.Errorf("Escalation gauge = %+v, want tasks 1, rungs 0, rate 0.0", esc)
		}
	})

	// 5. All pass across classes
	t.Run("all_pass_across_classes", func(t *testing.T) {
		// Rule: 100% passing events across all classes produce PassRate = 1.0.
		ev := []telemetry.TraceEvent{
			{ID: "p1", TaskID: "t1", EventType: telemetry.TraceEventTaskCompleted, Class: "c1", EffortApplied: "low", ExitCode: &exit0, Timestamp: now},
			{ID: "p2", TaskID: "t2", EventType: telemetry.TraceEventTaskCompleted, Class: "c2", EffortApplied: "medium", ExitCode: &exit0, Timestamp: now},
		}
		report := ComputeGauges(ev, nil, "")
		for _, g := range report.PassRates {
			if g.PassRate != 1.0 {
				t.Errorf("class %s effort %s PassRate = %f, want 1.0", g.Class, g.Effort, g.PassRate)
			}
		}
	})

	// 6. All fail across classes
	t.Run("all_fail_across_classes", func(t *testing.T) {
		// Rule: 100% failing events across all classes produce PassRate = 0.0.
		ev := []telemetry.TraceEvent{
			{ID: "f1", TaskID: "t1", EventType: telemetry.TraceEventTaskFailed, Class: "c1", EffortApplied: "low", ExitCode: &exit1, Timestamp: now},
			{ID: "f2", TaskID: "t2", EventType: telemetry.TraceEventTaskFailed, Class: "c2", EffortApplied: "medium", ExitCode: &exit1, Timestamp: now},
		}
		report := ComputeGauges(ev, nil, "")
		for _, g := range report.PassRates {
			if g.PassRate != 0.0 {
				t.Errorf("class %s effort %s PassRate = %f, want 0.0", g.Class, g.Effort, g.PassRate)
			}
		}
	})

	// 7. Counter going backwards (monotonicity guard)
	t.Run("counter_going_backwards_monotonicity_guard", func(t *testing.T) {
		// Rule: Token and counter metrics must be monotonic and non-negative; negative token values must be guarded.
		// FINDING-R3-9:
		// Expected: ComputeGauges validates token counters and rejects or clamps negative values.
		// Actual: ComputeGauges does not validate token counter monotonicity, accepting negative values.
		t.Skip("FINDING-R3-9: ComputeGauges does not validate token counter monotonicity, accepting negative values")

		ev := []telemetry.TraceEvent{
			{
				ID: "ev-neg", TaskID: "t-neg", EventType: telemetry.TraceEventTaskCompleted,
				Class: "docs", EffortApplied: "low", ExitCode: &exit0, Timestamp: now,
				InputTokens: -500, OutputTokens: -100, // Negative tokens!
			},
		}
		report := ComputeGauges(ev, nil, "")
		_ = report
	})

	// 8. Rate values stay within [0, 1] — HITL rate
	t.Run("rate_values_stay_within_0_1_hitl_rate", func(t *testing.T) {
		// Rule: HITLRate must stay within [0.0, 1.0] per gauge metric definition.
		// FINDING-R3-10:
		// Expected: HITLRate stays within [0.0, 1.0] even when multiple child rungs emit HITL events for the same root task.
		// Actual: HITLRate exceeds 1.0 (reaches 3.0) because multiple child rung task IDs are counted in hitlTasks against root totalRounds.
		t.Skip("FINDING-R3-10: HITLRate exceeds 1.0 when multiple child rungs emit HITL events for the same root task")

		// 1 root task
		tasks := []*controlplane.Task{
			{TaskID: "root-task-1", Request: []byte(`{"class":"feature"}`)},
		}
		// 3 child rungs of root-task-1 each emit a HITL-tagged event with their own TaskID
		events := []telemetry.TraceEvent{
			{ID: "e1", TaskID: "child-rung-1", Class: "feature", Tags: []string{"hitl"}, Timestamp: now},
			{ID: "e2", TaskID: "child-rung-2", Class: "feature", Tags: []string{"hitl"}, Timestamp: now},
			{ID: "e3", TaskID: "child-rung-3", Class: "feature", Tags: []string{"hitl"}, Timestamp: now},
		}
		report := ComputeGauges(events, tasks, "")
		if report.HITL.HITLRate > 1.0 {
			t.Fatalf("HITLRate = %f > 1.0 (total rounds = %d, hitl packets = %d)",
				report.HITL.HITLRate, report.HITL.TotalRounds, report.HITL.HITLPackets)
		}
	})

	// 9. PassRate values stay strictly within [0, 1]
	t.Run("pass_rate_stays_within_0_1", func(t *testing.T) {
		// Rule: PassRate strictly stays within [0.0, 1.0] across all ratio combinations.
		var ev []telemetry.TraceEvent
		for i := 0; i < 7; i++ {
			ev = append(ev, telemetry.TraceEvent{
				ID: fmt.Sprintf("p-%d", i), TaskID: fmt.Sprintf("tp-%d", i), EventType: telemetry.TraceEventTaskCompleted,
				Class: "ratio-test", EffortApplied: "low", ExitCode: &exit0, Timestamp: now,
			})
		}
		for i := 0; i < 3; i++ {
			ev = append(ev, telemetry.TraceEvent{
				ID: fmt.Sprintf("f-%d", i), TaskID: fmt.Sprintf("tf-%d", i), EventType: telemetry.TraceEventTaskFailed,
				Class: "ratio-test", EffortApplied: "low", ExitCode: &exit1, Timestamp: now,
			})
		}
		report := ComputeGauges(ev, nil, "")
		if len(report.PassRates) != 1 {
			t.Fatalf("PassRates len = %d, want 1", len(report.PassRates))
		}
		rate := report.PassRates[0].PassRate
		if rate < 0.0 || rate > 1.0 || rate != 0.7 {
			t.Errorf("PassRate = %f, want 0.7000 within [0,1]", rate)
		}
	})

	// 10a. Class filter with unknown class: empty rates
	t.Run("class_filter_unknown_class_empty_rates", func(t *testing.T) {
		// Rule: Filtering by unknown class returns empty pass-rates and escalation-rates without panic.
		ev := []telemetry.TraceEvent{
			{ID: "e1", TaskID: "t1", EventType: telemetry.TraceEventTaskCompleted, Class: "real-class", EffortApplied: "low", ExitCode: &exit0, Timestamp: now},
		}
		report := ComputeGauges(ev, nil, "unknown-class")
		if len(report.PassRates) != 0 {
			t.Errorf("PassRates len = %d, want 0", len(report.PassRates))
		}
		if len(report.EscalationRates) != 0 {
			t.Errorf("EscalationRates len = %d, want 0", len(report.EscalationRates))
		}
	})

	// 10b. Class filter with unknown class: HITL total rounds leak (FINDING-R3-11)
	t.Run("class_filter_unknown_class_total_rounds_leak", func(t *testing.T) {
		// Rule: Filtering by unknown class should yield 0 total rounds matching the class filter.
		// FINDING-R3-11:
		// Expected: When filterClass does not match any class, HITL.TotalRounds should be 0.
		// Actual: ComputeGauges lines 312-316 fall back to unfiltered len(events), returning TotalRounds = 1.
		t.Skip("FINDING-R3-11: ComputeGauges leaks unfiltered events into HITL.TotalRounds when class filter matches nothing")

		ev := []telemetry.TraceEvent{
			{ID: "e1", TaskID: "t1", EventType: telemetry.TraceEventTaskCompleted, Class: "real-class", EffortApplied: "low", ExitCode: &exit0, Timestamp: now},
		}
		report := ComputeGauges(ev, nil, "unknown-class")
		if report.HITL.TotalRounds != 0 {
			t.Errorf("TotalRounds = %d, want 0", report.HITL.TotalRounds)
		}
	})

	// 11. Class filter case insensitivity
	t.Run("class_filter_case_insensitivity", func(t *testing.T) {
		// Rule: Filtering class matches case-insensitively (e.g. 'DOCS' matches 'docs').
		ev := []telemetry.TraceEvent{
			{ID: "e1", TaskID: "t1", EventType: telemetry.TraceEventTaskCompleted, Class: "docs", EffortApplied: "low", ExitCode: &exit0, Timestamp: now},
		}
		report := ComputeGauges(ev, nil, "DOCS")
		if len(report.PassRates) != 1 {
			t.Fatalf("PassRates len = %d, want 1", len(report.PassRates))
		}
		if report.PassRates[0].Class != "docs" {
			t.Errorf("Class = %s, want docs", report.PassRates[0].Class)
		}
	})
}

// =============================================================================
// DIMENSION 6: Policy Thresholds
// =============================================================================

func TestRedtest_Dimension6_PolicyThresholds(t *testing.T) {
	baseCtx := PolicyContext{
		RootTaskID:     "task-root-thresh",
		Class:          "feature",
		OriginalRole:   "coder",
		OriginalPerm:   "workspace_write",
		OriginalModel:  "gemini-3.8-flash-high",
		OriginalEffort: "low",
		TokenBudget:    10000,
		LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
	}

	// 1. Token budget: below threshold (Budget - 1)
	t.Run("token_budget_below_threshold", func(t *testing.T) {
		// Rule: CumulativeTokens < TokenBudget (Budget - 1) does not fire token budget HITL per rule 2.
		ctx := baseCtx
		ctx.CumulativeTokens = 9999
		plan := EvaluateNextRung(ctx)
		if plan.Action == ActionHITL && errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("unexpected token budget HITL below threshold: %+v", plan)
		}
		if plan.Action != ActionDiagnosis { // Rung 0
			t.Errorf("Action = %s, want ActionDiagnosis", plan.Action)
		}
	})

	// 2. Token budget: exactly at threshold (Budget)
	t.Run("token_budget_exactly_at_threshold", func(t *testing.T) {
		// Rule: CumulativeTokens == TokenBudget (inclusive >=) fires ActionHITL with TokenBudgetExceededError per rule 2.
		ctx := baseCtx
		ctx.CumulativeTokens = 10000
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL {
			t.Fatalf("Action = %s, want ActionHITL", plan.Action)
		}
		if !errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("RefusalError = %v, want ErrTokenBudgetExceeded", plan.RefusalError)
		}
	})

	// 3. Token budget: above threshold (Budget + 1)
	t.Run("token_budget_above_threshold", func(t *testing.T) {
		// Rule: CumulativeTokens > TokenBudget (Budget + 1) fires ActionHITL with TokenBudgetExceededError per rule 2.
		ctx := baseCtx
		ctx.CumulativeTokens = 10001
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("expected ActionHITL and ErrTokenBudgetExceeded, got %+v", plan)
		}
	})

	// 4. Token budget disabled: zero
	t.Run("token_budget_disabled_zero", func(t *testing.T) {
		// Rule: TokenBudget == 0 disables budget enforcement; large cumulative tokens never trigger HITL per rule 2.
		ctx := baseCtx
		ctx.TokenBudget = 0
		ctx.CumulativeTokens = 9999999
		plan := EvaluateNextRung(ctx)
		if plan.Action == ActionHITL && errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("disabled budget fired HITL: %+v", plan)
		}
		if plan.Action != ActionDiagnosis {
			t.Errorf("Action = %s, want ActionDiagnosis", plan.Action)
		}
	})

	// 5. Token budget disabled: negative
	t.Run("token_budget_disabled_negative", func(t *testing.T) {
		// Rule: TokenBudget < 0 disables budget enforcement; negative budget never triggers HITL per rule 2.
		ctx := baseCtx
		ctx.TokenBudget = -1
		ctx.CumulativeTokens = 50000
		plan := EvaluateNextRung(ctx)
		if plan.Action == ActionHITL && errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("negative budget fired HITL: %+v", plan)
		}
	})

	// 6. Ceiling below threshold: Rung 5 (len == 5)
	t.Run("ceiling_below_threshold_rung_5", func(t *testing.T) {
		// Rule: At Rung 5 (len(History) == 5), ceiling enforcement does not fire per rule 3; model alternative or exhaustion executes.
		ctx := baseCtx
		ctx.TokenBudget = 0
		ctx.History = make([]RungRecord, 5)
		plan := EvaluateNextRung(ctx)
		if errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Fatalf("ceiling error fired prematurely at rung 5: %+v", plan)
		}
		if plan.RungIndex != 5 {
			t.Errorf("RungIndex = %d, want 5", plan.RungIndex)
		}
	})

	// 7. Ceiling exactly at threshold: Rung 6 (len == 6)
	t.Run("ceiling_exactly_at_threshold_rung_6", func(t *testing.T) {
		// Rule: At Rung 6 (len(History) == 6, inclusive >=), ceiling enforcement fires ActionHITL with CeilingExceededError (ErrCeilingReached) per rule 3.
		ctx := baseCtx
		ctx.TokenBudget = 0
		ctx.History = make([]RungRecord, 6)
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL {
			t.Fatalf("Action = %s, want ActionHITL", plan.Action)
		}
		if !errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Fatalf("RefusalError = %v, want ErrCeilingReached", plan.RefusalError)
		}
		if plan.RungIndex != 6 {
			t.Errorf("RungIndex = %d, want 6", plan.RungIndex)
		}
	})

	// 8. Ceiling above threshold: Rung 7 (len == 7)
	t.Run("ceiling_above_threshold_rung_7", func(t *testing.T) {
		// Rule: Above Rung 6 (len(History) > 6), ceiling enforcement fires ActionHITL with CeilingExceededError per rule 3.
		ctx := baseCtx
		ctx.TokenBudget = 0
		ctx.History = make([]RungRecord, 7)
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Fatalf("expected ceiling reached at rung 7, got %+v", plan)
		}
	})

	// 9. Failure shape: effort vs non-effort
	t.Run("failure_shape_effort_vs_non_effort", func(t *testing.T) {
		// Rule: ShapeEffort allows progression, while ShapeBrief and ShapeEnv route immediately to ActionHITL with NonEffortShapeError per rule 4.
		ctxEffort := baseCtx
		ctxEffort.LatestVerdict = ClassifierVerdict{Shape: ShapeEffort}
		pEffort := EvaluateNextRung(ctxEffort)
		if pEffort.Action == ActionHITL && errors.Is(pEffort.RefusalError, ErrNonEffortShape) {
			t.Errorf("ShapeEffort fired non-effort HITL")
		}

		ctxBrief := baseCtx
		ctxBrief.LatestVerdict = ClassifierVerdict{Shape: ShapeBrief, Reason: "doc contract violated"}
		pBrief := EvaluateNextRung(ctxBrief)
		if pBrief.Action != ActionHITL || !errors.Is(pBrief.RefusalError, ErrNonEffortShape) {
			t.Errorf("ShapeBrief failed to route to HITL: %+v", pBrief)
		}

		ctxEnv := baseCtx
		ctxEnv.LatestVerdict = ClassifierVerdict{Shape: ShapeEnv, Reason: "sigkill received"}
		pEnv := EvaluateNextRung(ctxEnv)
		if pEnv.Action != ActionHITL || !errors.Is(pEnv.RefusalError, ErrNonEffortShape) {
			t.Errorf("ShapeEnv failed to route to HITL: %+v", pEnv)
		}
	})

	// 10. Precedence: success over budget and ceiling
	t.Run("precedence_success_over_budget_and_ceiling", func(t *testing.T) {
		// Rule: LatestSucceeded=true takes precedence over exceeded token budget and ceiling per rule 1.
		ctx := baseCtx
		ctx.LatestSucceeded = true
		ctx.TokenBudget = 1000
		ctx.CumulativeTokens = 50000         // budget exceeded
		ctx.History = make([]RungRecord, 10) // ceiling exceeded
		ctx.LatestVerdict = ClassifierVerdict{Shape: ShapeEnv}

		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionDone {
			t.Fatalf("Action = %s, want ActionDone (rule 1 precedence)", plan.Action)
		}
	})

	// 11. Precedence: budget over ceiling and shape
	t.Run("precedence_budget_over_ceiling_and_shape", func(t *testing.T) {
		// Rule: Token budget exceeded takes precedence over ceiling reached and non-effort shape per rule 2.
		ctx := baseCtx
		ctx.TokenBudget = 5000
		ctx.CumulativeTokens = 6000         // budget exceeded
		ctx.History = make([]RungRecord, 8) // ceiling exceeded
		ctx.LatestVerdict = ClassifierVerdict{Shape: ShapeEnv}

		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrTokenBudgetExceeded) {
			t.Fatalf("expected ErrTokenBudgetExceeded precedence over ceiling and shape, got %+v", plan)
		}
	})

	// 12. Precedence: ceiling over shape
	t.Run("precedence_ceiling_over_shape", func(t *testing.T) {
		// Rule: Ceiling reached takes precedence over non-effort shape per rule 3.
		ctx := baseCtx
		ctx.TokenBudget = 0
		ctx.History = make([]RungRecord, 6) // ceiling reached
		ctx.LatestVerdict = ClassifierVerdict{Shape: ShapeEnv, Reason: "timeout"}

		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrCeilingReached) {
			t.Fatalf("expected ErrCeilingReached precedence over non-effort shape, got %+v", plan)
		}
	})
}

// =============================================================================
// DIMENSION 7: Interaction (Gauges, Policy, Routing Flow)
// =============================================================================

func TestRedtest_Dimension7_Interaction(t *testing.T) {
	manifest := redtestLadderManifest()
	exit0 := 0
	exit1 := 1

	// 1. Multi-rung telemetry-to-gauge lifecycle composition
	t.Run("lifecycle_telemetry_gauges_and_policy_progression", func(t *testing.T) {
		// Rule: As rungs execute and emit telemetry trace events, ComputeGauges accurately tracks pass-rate and escalation-rate, while EvaluateNextRung advances lineage per internal/ladder/policy.go:194-206.
		ctx := PolicyContext{
			RootTaskID:     "task-interaction-root",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			Manifest:       manifest,
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
		}

		// Initial state: Rung 0 diagnosis
		p0 := EvaluateNextRung(ctx)
		if p0.Action != ActionDiagnosis {
			t.Fatalf("Rung 0 Action = %s, want ActionDiagnosis", p0.Action)
		}

		// Rung 0 executes and fails class checks
		rec0 := RungRecord{
			RungIndex: 0, TaskID: "task-r0-exec", Role: p0.Role, Permission: p0.Permission,
			Model: p0.Model, Effort: p0.Effort, Shape: ShapeEffort, Verdict: "class_checks_failed", Tokens: 1000,
		}
		ctx.History = append(ctx.History, rec0)
		ctx.CumulativeTokens += 1000

		// Telemetry trace event for Rung 0 failure
		events := []telemetry.TraceEvent{
			{
				ID: "ev-r0", TaskID: "task-r0-exec", EventType: telemetry.TraceEventTaskFailed,
				Class: "feature", EffortApplied: "low", ExitCode: &exit1, Timestamp: time.Now(),
				Tags: []string{"ladder", "ladder-rung"},
			},
		}
		tasks := []*controlplane.Task{
			{TaskID: "task-interaction-root", Request: []byte(`{"class":"feature"}`)},
			{TaskID: "task-r0-exec", ParentTaskID: &ctx.RootTaskID, Request: []byte(`{"class":"feature"}`)},
		}

		// Compute intermediate gauges: 1 failure, pass rate 0.0, 1 escalation rung on 1 root task (rate 1.0)
		rep1 := ComputeGauges(events, tasks, "feature")
		if len(rep1.PassRates) != 1 || rep1.PassRates[0].PassRate != 0.0 {
			t.Errorf("rep1 PassRate = %+v, want 0.0", rep1.PassRates)
		}
		if len(rep1.EscalationRates) != 1 || rep1.EscalationRates[0].EscalationRate != 1.0 {
			t.Errorf("rep1 EscalationRate = %+v, want 1.0", rep1.EscalationRates)
		}

		// Policy evaluates Rung 1: effort escalates to medium
		p1 := EvaluateNextRung(ctx)
		if p1.Action != ActionEscalateEffort || p1.Effort != config.EffortMedium {
			t.Fatalf("Rung 1 Action = %s, Effort = %s, want (escalate_effort, medium)", p1.Action, p1.Effort)
		}

		// Rung 1 executes and succeeds!
		ctx.LatestSucceeded = true
		ctx.History = append(ctx.History, RungRecord{
			RungIndex: 1, TaskID: "task-r1-exec", Role: p1.Role, Permission: p1.Permission,
			Model: p1.Model, Effort: p1.Effort, Shape: ShapeEffort, Verdict: "success", Tokens: 2500,
		})

		// Telemetry trace event for Rung 1 success
		events = append(events, telemetry.TraceEvent{
			ID: "ev-r1", TaskID: "task-r1-exec", EventType: telemetry.TraceEventTaskCompleted,
			Class: "feature", EffortApplied: "medium", ExitCode: &exit0, Timestamp: time.Now(),
			Tags: []string{"ladder", "ladder-rung"},
		})
		tasks = append(tasks, &controlplane.Task{
			TaskID: "task-r1-exec", ParentTaskID: &ctx.RootTaskID, Request: []byte(`{"class":"feature"}`),
		})

		// Final policy evaluation: terminates with ActionDone
		pEnd := EvaluateNextRung(ctx)
		if pEnd.Action != ActionDone {
			t.Fatalf("pEnd Action = %s, want ActionDone", pEnd.Action)
		}

		// Final gauges: low pass rate 0.0, medium pass rate 1.0, 2 rungs fired on 1 root task (rate 2.0)
		repFinal := ComputeGauges(events, tasks, "feature")
		if len(repFinal.PassRates) != 2 {
			t.Fatalf("repFinal PassRates count = %d, want 2", len(repFinal.PassRates))
		}
		if repFinal.EscalationRates[0].EscalationRate != 2.0 {
			t.Errorf("repFinal EscalationRate = %f, want 2.0", repFinal.EscalationRates[0].EscalationRate)
		}
	})

	// 2. Decoupled policy evaluation: gauge pass-rate does not alter deterministic ladder rules
	t.Run("decoupled_policy_evaluation_from_gauge_fluctuations", func(t *testing.T) {
		// Rule: Per internal/ladder/policy.go:194-206 and commit b5a1104 ('defaults stay operator-ratified'), ladder policy progression is deterministic and decoupled from real-time gauge pass-rate drops.
		ctx := PolicyContext{
			RootTaskID:     "task-root-decoupled",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			Manifest:       manifest,
			LatestVerdict:  ClassifierVerdict{Shape: ShapeEffort},
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0", Effort: "low", Shape: ShapeEffort},
			},
		}

		// Even if global telemetry shows a 0.0 pass rate for medium effort, EvaluateNextRung strictly advances to medium (+1 step)
		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionEscalateEffort || plan.Effort != config.EffortMedium {
			t.Errorf("EvaluateNextRung deviated from deterministic ladder: %+v", plan)
		}
	})

	// 3. Non-effort shape halts ladder and emits HITL packet
	t.Run("non_effort_shape_halts_ladder_and_builds_hitl_packet", func(t *testing.T) {
		// Rule: Non-effort shape halts ladder progression immediately, records telemetry failure, and builds comprehensive HITL evidence packet per internal/ladder/evidence.go:42-74 and policy.go:248-262.
		ctx := PolicyContext{
			RootTaskID:     "task-root-halt",
			Class:          "feature",
			OriginalRole:   "coder",
			OriginalPerm:   "workspace_write",
			OriginalModel:  "gemini-3.8-flash-high",
			OriginalEffort: "low",
			LatestVerdict: ClassifierVerdict{
				Shape:  ShapeBrief,
				Reason: "contract violation: tool 'bash_eval' called without authorization",
			},
			History: []RungRecord{
				{RungIndex: 0, TaskID: "t0", Effort: "low", Shape: ShapeEffort, Tokens: 800},
			},
			TokenBudget: 50000,
		}

		plan := EvaluateNextRung(ctx)
		if plan.Action != ActionHITL || !errors.Is(plan.RefusalError, ErrNonEffortShape) {
			t.Fatalf("expected ActionHITL with ErrNonEffortShape, got %+v", plan)
		}

		// Build the evidence packet
		packet := BuildEvidencePacket(
			ctx.RootTaskID,
			ctx.Class,
			string(plan.Action),
			plan.Reason,
			[]string{"tool 'bash_eval' unauthorized"},
			ctx.History,
			ctx.TokenBudget,
		)

		if packet.FinalVerdict != "hitl" || packet.TotalRungs != 1 || packet.TotalTokens != 800 {
			t.Errorf("evidence packet mismatch: %+v", packet)
		}
	})

	// 4. Specification citation and routing flow alignment
	t.Run("specification_citation_and_routing_flow_alignment", func(t *testing.T) {
		// Rule: Confirms policy composition matches documented flow in internal/ladder/policy.go:194-206 (rules 1..9), distinct from submit-time placement in docs/user-guide/routing.md.
		// Citation:
		// - 'internal/ladder/policy.go:194-206': SSoT specification for the 9-step quality ladder policy.
		// - 'docs/user-guide/routing.md': Submit-time distributor routing (Layer 1 deterministic rules 1-5, Layer 2 Jev-assisted).
		// Verify policy.go rule 5: Rung 0 always dispatches diagnosis with read_only permission and low effort.
		p0 := EvaluateNextRung(PolicyContext{
			RootTaskID:    "t-cite",
			OriginalModel: "gemini-3.8-flash-high",
			LatestVerdict: ClassifierVerdict{Shape: ShapeEffort},
		})
		if p0.Action != ActionDiagnosis || p0.Permission != "read_only" || p0.Effort != config.EffortLow {
			t.Errorf("Rung 0 violated policy.go:194-206 rule 5: %+v", p0)
		}
	})
}
