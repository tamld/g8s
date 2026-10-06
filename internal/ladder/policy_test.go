package ladder

import (
	"errors"
	"testing"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/routing"
)

func TestLadderPolicyStateProgression(t *testing.T) {
	manifest := &config.File{
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

	baseCtx := PolicyContext{
		RootTaskID:     "task-root-1",
		Class:          "feature",
		OriginalRole:   "coder",
		OriginalPerm:   "workspace_write",
		OriginalModel:  "gemini-3.8-flash-high",
		OriginalEffort: "low",
		Manifest:       manifest,
		TokenBudget:    100000,
		LatestVerdict: ClassifierVerdict{
			Shape:  ShapeEffort,
			Reason: "class checks failed",
		},
	}

	// Step 0: Rung 0 (Diagnosis dispatch)
	plan0 := EvaluateNextRung(baseCtx)
	if plan0.Action != ActionDiagnosis {
		t.Fatalf("Rung 0 Action = %s, want %s", plan0.Action, ActionDiagnosis)
	}
	if plan0.Permission != "read_only" {
		t.Errorf("Rung 0 Permission = %s, want read_only", plan0.Permission)
	}
	if plan0.Effort != config.EffortLow {
		t.Errorf("Rung 0 Effort = %s, want low", plan0.Effort)
	}
	if plan0.RungIndex != 0 {
		t.Errorf("Rung 0 RungIndex = %d, want 0", plan0.RungIndex)
	}

	// Step 1: Rung 1 (Effort +1: low -> medium)
	ctx1 := baseCtx
	ctx1.History = append(ctx1.History, RungRecord{
		RungIndex:  0,
		TaskID:     "task-rung-0",
		Role:       "diagnostician",
		Permission: "read_only",
		Model:      "gemini-3.8-flash-high",
		Effort:     "low",
		Shape:      ShapeEffort,
		Verdict:    "failed",
		Tokens:     1000,
	})
	ctx1.CumulativeTokens = 1000

	plan1 := EvaluateNextRung(ctx1)
	if plan1.Action != ActionEscalateEffort {
		t.Fatalf("Rung 1 Action = %s, want %s", plan1.Action, ActionEscalateEffort)
	}
	if plan1.Effort != config.EffortMedium {
		t.Errorf("Rung 1 Effort = %s, want medium", plan1.Effort)
	}

	// Step 2: Rung 2 (Effort +1: medium -> high)
	ctx2 := ctx1
	ctx2.History = append(ctx2.History, RungRecord{
		RungIndex:  1,
		TaskID:     "task-rung-1",
		Role:       "coder",
		Permission: "workspace_write",
		Model:      "gemini-3.8-flash-high",
		Effort:     "medium",
		Shape:      ShapeEffort,
		Verdict:    "failed",
		Tokens:     3000,
	})
	ctx2.CumulativeTokens += 3000

	plan2 := EvaluateNextRung(ctx2)
	if plan2.Action != ActionEscalateEffort {
		t.Fatalf("Rung 2 Action = %s, want %s", plan2.Action, ActionEscalateEffort)
	}
	if plan2.Effort != config.EffortHigh {
		t.Errorf("Rung 2 Effort = %s, want high", plan2.Effort)
	}

	// Step 3: Rung 3 (Subset exhausted on agy model -> falls through to Model Alternative candidate 1: claude-haiku-4-5)
	ctx3 := ctx2
	ctx3.History = append(ctx3.History, RungRecord{
		RungIndex:  2,
		TaskID:     "task-rung-2",
		Role:       "coder",
		Permission: "workspace_write",
		Model:      "gemini-3.8-flash-high",
		Effort:     "high",
		Shape:      ShapeEffort,
		Verdict:    "failed",
		Tokens:     4000,
	})
	ctx3.CumulativeTokens += 4000

	plan3 := EvaluateNextRung(ctx3)
	// Because gemini-3.8-flash-high cannot escalate past high, subset is exhausted!
	if plan3.Action != ActionModelAlternative {
		t.Fatalf("Rung 3 Action = %s, want %s", plan3.Action, ActionModelAlternative)
	}
	if plan3.Model != "claude-haiku-4-5" {
		t.Errorf("Rung 3 Model = %s, want claude-haiku-4-5", plan3.Model)
	}

	// Step 4: Rung 4 (Model Alternative candidate 2: llama3.1)
	ctx4 := ctx3
	ctx4.History = append(ctx4.History, RungRecord{
		RungIndex:  3,
		TaskID:     "task-rung-3",
		Role:       "coder",
		Permission: "workspace_write",
		Model:      "claude-haiku-4-5",
		Effort:     "medium",
		Shape:      ShapeEffort,
		Verdict:    "failed",
		Tokens:     3500,
	})
	ctx4.CumulativeTokens += 3500

	plan4 := EvaluateNextRung(ctx4)
	if plan4.Action != ActionModelAlternative {
		t.Fatalf("Rung 4 Action = %s, want %s", plan4.Action, ActionModelAlternative)
	}
	if plan4.Model != "llama3.1" {
		t.Errorf("Rung 4 Model = %s, want llama3.1", plan4.Model)
	}

	// Step 5: Rung 5 (All alternatives exhausted -> HITL)
	ctx5 := ctx4
	ctx5.History = append(ctx5.History, RungRecord{
		RungIndex:  4,
		TaskID:     "task-rung-4",
		Role:       "coder",
		Permission: "workspace_write",
		Model:      "llama3.1",
		Effort:     "medium",
		Shape:      ShapeEffort,
		Verdict:    "failed",
		Tokens:     2500,
	})
	ctx5.CumulativeTokens += 2500

	plan5 := EvaluateNextRung(ctx5)
	if plan5.Action != ActionHITL {
		t.Fatalf("Rung 5 Action = %s, want %s", plan5.Action, ActionHITL)
	}

	// Step 6: Ceiling enforcement (6 rungs maximum cap)
	ctx6 := ctx5
	ctx6.History = append(ctx6.History, RungRecord{
		RungIndex: 5,
		TaskID:    "task-rung-5",
		Shape:     ShapeEffort,
		Verdict:   "failed",
		Tokens:    1000,
	})
	plan6 := EvaluateNextRung(ctx6)
	if plan6.Action != ActionHITL {
		t.Fatalf("Rung 6 Action = %s, want %s", plan6.Action, ActionHITL)
	}
	if !errors.Is(plan6.RefusalError, ErrCeilingReached) {
		t.Fatalf("Rung 6 RefusalError = %v, want ErrCeilingReached", plan6.RefusalError)
	}
}

func TestLadderEarlyTerminationChecks(t *testing.T) {
	baseCtx := PolicyContext{
		RootTaskID:       "task-root-1",
		Class:            "feature",
		OriginalRole:     "coder",
		OriginalPerm:     "workspace_write",
		OriginalModel:    "gemini-3.8-flash-high",
		OriginalEffort:   "low",
		TokenBudget:      5000,
		CumulativeTokens: 2000,
	}

	// 1. Success early termination
	ctxSuccess := baseCtx
	ctxSuccess.LatestSucceeded = true
	planPass := EvaluateNextRung(ctxSuccess)
	if planPass.Action != ActionDone {
		t.Errorf("Success Action = %s, want %s", planPass.Action, ActionDone)
	}

	// 2. Token budget early HITL
	ctxBudget := baseCtx
	ctxBudget.CumulativeTokens = 6000 // exceeds 5000
	ctxBudget.LatestVerdict = ClassifierVerdict{Shape: ShapeEffort}
	planBudget := EvaluateNextRung(ctxBudget)
	if planBudget.Action != ActionHITL {
		t.Fatalf("Budget exceeded Action = %s, want %s", planBudget.Action, ActionHITL)
	}
	if !errors.Is(planBudget.RefusalError, ErrTokenBudgetExceeded) {
		t.Errorf("Budget exceeded RefusalError = %v, want ErrTokenBudgetExceeded", planBudget.RefusalError)
	}

	// 3. Non-effort shape early HITL (env-shaped)
	ctxEnv := baseCtx
	ctxEnv.LatestVerdict = ClassifierVerdict{
		Shape:  ShapeEnv,
		Reason: "transport connection reset",
	}
	planEnv := EvaluateNextRung(ctxEnv)
	if planEnv.Action != ActionHITL {
		t.Fatalf("Env shape Action = %s, want %s", planEnv.Action, ActionHITL)
	}
	if !errors.Is(planEnv.RefusalError, ErrNonEffortShape) {
		t.Errorf("Env shape RefusalError = %v, want ErrNonEffortShape", planEnv.RefusalError)
	}

	// 4. Non-effort shape early HITL (brief-shaped)
	ctxBrief := baseCtx
	ctxBrief.LatestVerdict = ClassifierVerdict{
		Shape:  ShapeBrief,
		Reason: "contract violation: tool outside scope",
	}
	planBrief := EvaluateNextRung(ctxBrief)
	if planBrief.Action != ActionHITL {
		t.Fatalf("Brief shape Action = %s, want %s", planBrief.Action, ActionHITL)
	}
	if !errors.Is(planBrief.RefusalError, ErrNonEffortShape) {
		t.Errorf("Brief shape RefusalError = %v, want ErrNonEffortShape", planBrief.RefusalError)
	}
}

func TestNextEffortLevel(t *testing.T) {
	view := &routing.ModelEffortView{
		Provider:         "openrouter",
		Model:            "deepseek-r1",
		SupportedEfforts: []string{"minimal", "medium", "max"},
	}

	next, ok := NextEffortLevel(view, "minimal")
	if !ok || next != "medium" {
		t.Errorf("NextEffortLevel minimal -> got (%s, %v), want (medium, true)", next, ok)
	}

	next, ok = NextEffortLevel(view, "medium")
	if !ok || next != "max" {
		t.Errorf("NextEffortLevel medium -> got (%s, %v), want (max, true)", next, ok)
	}

	next, ok = NextEffortLevel(view, "max")
	if ok || next != "" {
		t.Errorf("NextEffortLevel max -> got (%s, %v), want ('', false)", next, ok)
	}
}

func TestParseTokenBudgets(t *testing.T) {
	yamlData := []byte(`
schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 30
    ladder_token_budget: 25000
    paths:
      - "*.md"
  - name: feature
    default_effort: high
    priority: 15
    ladder_token_budget: 120000
    paths:
      - "tools/**"
  - name: test
    default_effort: medium
    priority: 20
    paths:
      - "*_test.go"
`)

	budgets := ParseTokenBudgets(yamlData)
	if budgets["docs"] != 25000 {
		t.Errorf("docs budget = %d, want 25000", budgets["docs"])
	}
	if budgets["feature"] != 120000 {
		t.Errorf("feature budget = %d, want 120000", budgets["feature"])
	}
	if val, ok := budgets["test"]; ok && val != 0 {
		t.Errorf("test budget = %d, want 0/absent (fail-open)", val)
	}
}
