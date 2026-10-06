package ladder

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/routing"
)

// LadderAction describes the decision reached for the next step of the ladder.
type LadderAction string

const (
	ActionDiagnosis        LadderAction = "diagnosis"
	ActionEscalateEffort   LadderAction = "escalate_effort"
	ActionModelAlternative LadderAction = "model_alternative"
	ActionHITL             LadderAction = "hitl"
	ActionDone             LadderAction = "done"
)

// RungPlan represents the planned parameters for executing a ladder rung.
type RungPlan struct {
	RungIndex    int          `json:"rung_index"`
	Action       LadderAction `json:"action"`
	Effort       string       `json:"effort,omitempty"`
	Model        string       `json:"model,omitempty"`
	Provider     string       `json:"provider,omitempty"`
	Permission   string       `json:"permission,omitempty"`
	Role         string       `json:"role,omitempty"`
	Reason       string       `json:"reason"`
	StaleTag     string       `json:"stale_tag,omitempty"`
	RefusalError error        `json:"-"`
}

// NextEffortLevel finds the next higher effort level in the model's supported subset
// using the canonical config.EffortLadder order.
// Returns next level, or ("", false) if subset is exhausted.
func NextEffortLevel(view *routing.ModelEffortView, currentEffort string) (string, bool) {
	if view == nil {
		// Baked agy fallback: low -> medium -> high
		switch currentEffort {
		case config.EffortLow, "":
			return config.EffortMedium, true
		case config.EffortMedium:
			return config.EffortHigh, true
		default:
			return "", false
		}
	}

	supported := view.SupportedEfforts
	if len(supported) == 0 {
		if view.EffortStyle == config.EffortStyleBakedName || view.Provider == "agy" ||
			strings.HasPrefix(view.Model, "gemini-") || strings.Contains(view.Model, "agy") {
			supported = []string{config.EffortLow, config.EffortMedium, config.EffortHigh}
		} else {
			return "", false
		}
	}

	currIdx := ladderIndex(currentEffort)

	// Find the next level in supported with the smallest index strictly > currIdx
	bestLvl := ""
	bestIdx := 1000

	for _, lvl := range supported {
		idx := ladderIndex(lvl)
		if idx > currIdx && idx < bestIdx {
			bestIdx = idx
			bestLvl = lvl
		}
	}

	if bestLvl != "" {
		return bestLvl, true
	}
	return "", false
}

func ladderIndex(lvl string) int {
	for i, l := range config.EffortLadder {
		if strings.EqualFold(l, lvl) {
			return i
		}
	}
	return -1
}

// FindAlternativeModel finds the next candidate model from the manifest that has not been used yet.
func FindAlternativeModel(manifest *config.File, usedModels []string) (provider string, model string, found bool) {
	usedMap := make(map[string]bool)
	for _, m := range usedModels {
		usedMap[strings.ToLower(m)] = true
	}

	var providers []config.ProviderEntry
	if manifest != nil && len(manifest.Providers) > 0 {
		providers = manifest.Providers
	} else {
		providers = routing.DefaultManifest().Providers
	}

	for _, p := range providers {
		for _, m := range p.Models {
			mID := strings.ToLower(m.ID)
			if !usedMap[mID] {
				return p.Name, m.ID, true
			}
		}
	}

	return "", "", false
}

// ParseTokenBudgets extracts optional ladder_token_budget integers per class from effort-classes.yml.
func ParseTokenBudgets(data []byte) map[string]int {
	budgets := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var currentClass string
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.IndexByte(line, '#'); idx != -1 {
			line = line[:idx]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "- name:") {
			currentClass = strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
			currentClass = strings.Trim(currentClass, `"'`)
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			currentClass = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			currentClass = strings.Trim(currentClass, `"'`)
			continue
		}

		if strings.HasPrefix(trimmed, "ladder_token_budget:") {
			valStr := strings.TrimSpace(strings.TrimPrefix(trimmed, "ladder_token_budget:"))
			valStr = strings.Trim(valStr, `"'`)
			if val, err := strconv.Atoi(valStr); err == nil && val > 0 && currentClass != "" {
				budgets[currentClass] = val
			}
		}
	}
	return budgets
}

// LoadTokenBudgets loads ladder_token_budget mappings from the given path (fails open if file is missing).
func LoadTokenBudgets(path string) map[string]int {
	if path == "" {
		path = ".g8s/effort-classes.yml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]int)
	}
	return ParseTokenBudgets(data)
}

// PolicyContext provides task lineage and environmental context for policy evaluation.
type PolicyContext struct {
	RootTaskID       string
	Class            string
	OriginalRole     string
	OriginalPerm     string
	OriginalModel    string
	OriginalEffort   string
	LatestSucceeded  bool
	LatestVerdict    ClassifierVerdict
	History          []RungRecord
	Manifest         *config.File
	TokenBudget      int
	CumulativeTokens int
}

// EvaluateNextRung computes the next rung plan according to the ratified ladder policy.
//
// Rules:
//  1. Early termination: If the latest attempt succeeded -> ActionDone.
//  2. Token budget check: Cumulative tokens exceeding budget -> Refuses with TokenBudgetExceededError -> ActionHITL.
//  3. Ceiling enforcement: Rung count >= MaxLadderRungs (6) -> Refuses with CeilingExceededError -> ActionHITL.
//  4. Failure shape: Non-effort shape -> Refuses with NonEffortShapeError -> ActionHITL.
//  5. Rung 0: Diagnosis dispatch (read_only, low effort, classifies and routes).
//  6. Rungs 1..3: Effort +1 step along model's SupportedEfforts subset.
//  7. Subset exhaustion -> Model alternative rungs (Rungs 4..5).
//  8. Rungs 4..5: Next alternative model from manifest serving the class.
//  9. Rung 6 / No alternatives left -> ActionHITL (mandatory HITL).
func EvaluateNextRung(ctx PolicyContext) RungPlan {
	currentRung := len(ctx.History)

	// 1. Check success
	if ctx.LatestSucceeded {
		return RungPlan{
			RungIndex: currentRung,
			Action:    ActionDone,
			Reason:    "task succeeded: class checks passed",
		}
	}

	// 2. Token budget early HITL
	if ctx.TokenBudget > 0 && ctx.CumulativeTokens >= ctx.TokenBudget {
		err := &TokenBudgetExceededError{
			TaskID:      ctx.RootTaskID,
			Class:       ctx.Class,
			TokensUsed:  ctx.CumulativeTokens,
			TokenBudget: ctx.TokenBudget,
		}
		return RungPlan{
			RungIndex:    currentRung,
			Action:       ActionHITL,
			Reason:       err.Error(),
			RefusalError: err,
		}
	}

	// 3. Ceiling enforcement
	if currentRung >= MaxLadderRungs {
		err := &CeilingExceededError{
			TaskID:    ctx.RootTaskID,
			RungCount: currentRung,
		}
		return RungPlan{
			RungIndex:    currentRung,
			Action:       ActionHITL,
			Reason:       err.Error(),
			RefusalError: err,
		}
	}

	// 4. Failure-shape check
	if !ctx.LatestVerdict.Shape.IsEffortShaped() {
		err := &NonEffortShapeError{
			TaskID: ctx.RootTaskID,
			Shape:  ctx.LatestVerdict.Shape,
			Reason: ctx.LatestVerdict.Reason,
		}
		return RungPlan{
			RungIndex:    currentRung,
			Action:       ActionHITL,
			Reason:       err.Error(),
			StaleTag:     ctx.LatestVerdict.StaleTag,
			RefusalError: err,
		}
	}

	// 5. Rung 0: Diagnosis rung
	if currentRung == 0 {
		diagRole := ctx.OriginalRole
		if diagRole == "" {
			diagRole = "verifier"
		}
		return RungPlan{
			RungIndex:  0,
			Action:     ActionDiagnosis,
			Role:       diagRole,
			Permission: "read_only",
			Model:      ctx.OriginalModel,
			Effort:     config.EffortLow,
			Reason:     "rung 0: diagnosis dispatch (read_only, low effort) to verify failure shape and class checks",
			StaleTag:   ctx.LatestVerdict.StaleTag,
		}
	}

	// Gather all used models in lineage
	usedModels := []string{ctx.OriginalModel}
	for _, h := range ctx.History {
		if h.Model != "" {
			usedModels = append(usedModels, h.Model)
		}
	}

	currentModel := ctx.OriginalModel
	currentEffort := ctx.OriginalEffort

	// Find the latest executed effort and model from history
	for i := len(ctx.History) - 1; i >= 0; i-- {
		h := ctx.History[i]
		if currentModel == ctx.OriginalModel && h.Model != "" {
			currentModel = h.Model
		}
		if currentEffort == ctx.OriginalEffort && h.Effort != "" && h.RungIndex > 0 {
			currentEffort = h.Effort
		}
	}

	view := routing.NormalizeCatalogModel("", nil, &config.ModelCatalog{
		ID: currentModel,
	})
	// Look up model view from manifest if available
	if ctx.Manifest != nil {
		for _, p := range ctx.Manifest.Providers {
			for _, m := range p.Models {
				if strings.EqualFold(m.ID, currentModel) {
					view = routing.NormalizeModelEffort(p.Name, &m)
					break
				}
			}
		}
	} else if cat, err := config.LoadDefaultCatalog(); err == nil && cat != nil {
		for _, catProv := range cat.Providers {
			if catModel, ok := catProv.FindModel(currentModel); ok {
				view = routing.NormalizeCatalogModel(catProv.Name, &catProv, &catModel)
				break
			}
		}
	}

	// 6. Rungs 1..3: Effort +1 step along supported subset
	if currentRung >= 1 && currentRung <= 3 {
		nextEffort, ok := NextEffortLevel(view, currentEffort)
		if ok {
			return RungPlan{
				RungIndex:  currentRung,
				Action:     ActionEscalateEffort,
				Role:       ctx.OriginalRole,
				Permission: ctx.OriginalPerm,
				Model:      currentModel,
				Effort:     nextEffort,
				Reason:     fmt.Sprintf("rung %d: effort escalated to %s along model %s supported subset", currentRung, nextEffort, currentModel),
				StaleTag:   ctx.LatestVerdict.StaleTag,
			}
		}
		// If subset is exhausted, fall through immediately to model alternative!
	}

	// 7. Rungs 4..5 (or early subset exhaustion): Model alternative
	if currentRung < MaxLadderRungs {
		altProv, altModel, ok := FindAlternativeModel(ctx.Manifest, usedModels)
		if ok {
			// Resolve default effort for the alternative model
			altEffort := config.EffortMedium
			if ctx.OriginalEffort != "" {
				altEffort = ctx.OriginalEffort
			}
			return RungPlan{
				RungIndex:  currentRung,
				Action:     ActionModelAlternative,
				Role:       ctx.OriginalRole,
				Permission: ctx.OriginalPerm,
				Provider:   altProv,
				Model:      altModel,
				Effort:     altEffort,
				Reason:     fmt.Sprintf("rung %d: model alternative candidate %s (provider %s)", currentRung, altModel, altProv),
				StaleTag:   ctx.LatestVerdict.StaleTag,
			}
		}
	}

	// 8. Rung 6 / No alternatives left: HITL mandatory
	return RungPlan{
		RungIndex: currentRung,
		Action:    ActionHITL,
		Reason:    fmt.Sprintf("rung %d: all automated options exhausted, HITL mandatory", currentRung),
		StaleTag:  ctx.LatestVerdict.StaleTag,
	}
}
