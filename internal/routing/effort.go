package routing

import (
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/config"
)

// MandatoryEffortError reports that a model with mandatory reasoning was asked
// to disable reasoning via effort "none".
type MandatoryEffortError struct {
	Provider string
	Model    string
}

func (e *MandatoryEffortError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("model %q (provider %q) has mandatory reasoning: effort 'none' is refused", e.Model, e.Provider)
	}
	return fmt.Sprintf("model %q has mandatory reasoning: effort 'none' is refused", e.Model)
}

// ModelEffortView represents a model's effort capabilities normalized from
// either a config.ModelEntry or a catalog entry.
type ModelEffortView struct {
	Provider         string
	Model            string
	EffortStyle      string
	SupportedEfforts []string
	DefaultEffort    string
	Mandatory        bool
	EffortBudgetMap  map[string]int
}

// EffortResult holds the outcome of resolving and adapting an effort request.
type EffortResult struct {
	Requested        string `json:"effort_requested,omitempty"`
	Applied          string `json:"effort_applied,omitempty"`
	Mismatch         bool   `json:"effort_mismatch,omitempty"`
	BudgetTokens     int    `json:"effort_budget_tokens,omitempty"`
	HasResolvedEntry bool   `json:"-"`
}

// AdaptEffort is a pure function that translates a requested effort level
// into an applied effort level based on the model's declared effort capabilities.
//
// Rules (design point 3, fail-safe):
//   - HARD-REFUSE: Mandatory == true AND requested == "none" -> typed MandatoryEffortError.
//   - unknown entry (nil) -> pass-through: applied = requested, mismatch = false ("" stays "").
//     Baked-suffix model ids are deliberately NOT coerced here: the ratified
//     model/effort realignment contract (issue #563 C3, cmd/g8s submit) rewrites the
//     MODEL to the matching variant, which preserves per-class effort differentiation
//     for the cost-per-class telemetry (issue #568).
//   - requested "" -> applied = entry DefaultEffort when it is a ladder level;
//     adaptive/dynamic or empty default -> applied = "".
//   - baked-name -> always pass-through (the wrapper resolves {effort} variant; mismatch = false).
//   - toggle -> nearest-supported on EffortLadder; mismatch = true when requested outside SupportedEfforts.
//   - named -> pass-through when requested in SupportedEfforts; otherwise nearest-supported
//     (minimal index distance on EffortLadder; tie -> lower level); mismatch = true.
//   - budget -> applied = requested when EffortBudgetMap has that key; else nearest level that
//     HAS a budget entry (fallback: nearest-supported); mismatch on fallback;
//     tokens = EffortBudgetMap[applied] surfaced for telemetry.
func AdaptEffort(view *ModelEffortView, requested string) (applied string, mismatch bool, budgetTokens int, err error) {
	if view != nil && view.Mandatory && requested == config.EffortNone {
		return "", false, 0, &MandatoryEffortError{Provider: view.Provider, Model: view.Model}
	}

	if view == nil {
		return requested, false, 0, nil
	}

	if requested == "" {
		def := view.DefaultEffort
		if config.IsValidEffortLevel(def) {
			applied = def
			mismatch = false
			if view.EffortStyle == config.EffortStyleBudget && view.EffortBudgetMap != nil {
				budgetTokens = view.EffortBudgetMap[applied]
			}
			return applied, mismatch, budgetTokens, nil
		}
		// adaptive, dynamic, or empty default
		return "", false, 0, nil
	}

	switch view.EffortStyle {
	case config.EffortStyleBakedName:
		return requested, false, 0, nil

	case config.EffortStyleBudget:
		if tokens, ok := view.EffortBudgetMap[requested]; ok {
			return requested, false, tokens, nil
		}
		// Find nearest level that has a budget entry
		budgetKeys := make([]string, 0, len(view.EffortBudgetMap))
		for k := range view.EffortBudgetMap {
			if config.IsValidEffortLevel(k) {
				budgetKeys = append(budgetKeys, k)
			}
		}
		if len(budgetKeys) > 0 {
			nearest := nearestEffort(requested, budgetKeys)
			return nearest, true, view.EffortBudgetMap[nearest], nil
		}
		// Sparse fallback: nearest-supported
		if len(view.SupportedEfforts) > 0 {
			nearest := nearestEffort(requested, view.SupportedEfforts)
			return nearest, true, 0, nil
		}
		return requested, false, 0, nil

	case config.EffortStyleToggle:
		if containsString(view.SupportedEfforts, requested) {
			return requested, false, 0, nil
		}
		if len(view.SupportedEfforts) > 0 {
			nearest := nearestEffort(requested, view.SupportedEfforts)
			return nearest, true, 0, nil
		}
		return requested, false, 0, nil

	case config.EffortStyleNamed:
		fallthrough
	default:
		if containsString(view.SupportedEfforts, requested) {
			return requested, false, 0, nil
		}
		if len(view.SupportedEfforts) > 0 {
			nearest := nearestEffort(requested, view.SupportedEfforts)
			return nearest, true, 0, nil
		}
		return requested, false, 0, nil
	}
}

// NormalizeModelEffort converts a ModelEntry into a ModelEffortView.
func NormalizeModelEffort(provider string, m *config.ModelEntry) *ModelEffortView {
	if m == nil {
		return nil
	}
	return &ModelEffortView{
		Provider:         provider,
		Model:            m.ID,
		EffortStyle:      m.EffortStyle,
		SupportedEfforts: m.SupportedEfforts,
		DefaultEffort:    m.DefaultEffort,
		Mandatory:        m.Mandatory,
		EffortBudgetMap:  m.EffortBudgetMap,
	}
}

// NormalizeCatalogModel converts catalog entries into a ModelEffortView.
func NormalizeCatalogModel(provider string, catProv *config.ProviderCatalog, catModel *config.ModelCatalog) *ModelEffortView {
	if catModel == nil {
		return nil
	}
	style := catModel.EffortStyle
	if style == "" && catProv != nil {
		style = catProv.EffortStyle
	}
	def := catModel.DefaultEffort
	if def == "" && catProv != nil {
		def = catProv.DefaultEffort
	}
	mandatory := catModel.Mandatory
	if catProv != nil && catProv.Mandatory {
		mandatory = true
	}
	return &ModelEffortView{
		Provider:         provider,
		Model:            catModel.ID,
		EffortStyle:      style,
		SupportedEfforts: catModel.SupportedEfforts,
		DefaultEffort:    def,
		Mandatory:        mandatory,
		EffortBudgetMap:  catModel.EffortBudgetMap,
	}
}

// ResolveEffort resolves the model effort entry (manifest first, then catalog FindProvider/FindModel
// direct lookup) and runs AdaptEffort.
func ResolveEffort(manifest *config.File, provider, modelID, requested string) (EffortResult, error) {
	cat, _ := config.LoadDefaultCatalog()
	return ResolveEffortWithCatalog(manifest, cat, provider, modelID, requested)
}

// ResolveEffortWithCatalog resolves the model effort entry with an explicit catalog and runs AdaptEffort.
func ResolveEffortWithCatalog(manifest *config.File, cat *config.Catalog, provider, modelID, requested string) (EffortResult, error) {
	view := resolveModelEffortView(manifest, cat, provider, modelID)
	hasResolved := view != nil

	applied, mismatch, tokens, err := AdaptEffort(view, requested)
	if err != nil {
		return EffortResult{}, err
	}

	return EffortResult{
		Requested:        requested,
		Applied:          applied,
		Mismatch:         mismatch,
		BudgetTokens:     tokens,
		HasResolvedEntry: hasResolved,
	}, nil
}

func resolveModelEffortView(manifest *config.File, cat *config.Catalog, provider, modelID string) *ModelEffortView {
	// 1. Look in manifest first
	if manifest != nil {
		for _, p := range manifest.Providers {
			if provider != "" && !strings.EqualFold(p.Name, provider) {
				continue
			}
			for _, m := range p.Models {
				if matchModelID(m.ID, modelID) {
					view := NormalizeModelEffort(p.Name, &m)
					// If manifest entry has incomplete effort data, try filling from catalog
					if cat != nil && (view.EffortStyle == "" || len(view.SupportedEfforts) == 0) {
						if catProv, ok := cat.FindProvider(p.Name); ok {
							if catModel, ok := findCatalogModel(catProv, m.ID, modelID); ok {
								enrichViewFromCatalog(view, &catProv, &catModel)
							}
						}
					}
					return view
				}
			}
		}
	}

	// 2. Direct catalog lookup
	if cat != nil {
		if provider != "" {
			if catProv, ok := cat.FindProvider(provider); ok {
				if catModel, ok := findCatalogModel(catProv, modelID); ok {
					view := NormalizeCatalogModel(catProv.Name, &catProv, &catModel)
					return view
				}
			}
		} else {
			// Provider not specified: search across all providers in catalog
			for _, catProv := range cat.Providers {
				if catModel, ok := findCatalogModel(catProv, modelID); ok {
					view := NormalizeCatalogModel(catProv.Name, &catProv, &catModel)
					return view
				}
			}
		}
	}

	return nil
}

func matchModelID(candID, targetID string) bool {
	if strings.EqualFold(candID, targetID) {
		return true
	}
	candTrimmed := strings.TrimSuffix(candID, "-{effort}")
	targetTrimmed := strings.TrimSuffix(targetID, "-{effort}")
	if strings.EqualFold(candTrimmed, targetTrimmed) {
		return true
	}
	for _, lvl := range config.EffortLadder {
		if strings.EqualFold(candTrimmed, strings.TrimSuffix(targetTrimmed, "-"+lvl)) {
			return true
		}
		if strings.EqualFold(strings.TrimSuffix(candTrimmed, "-"+lvl), targetTrimmed) {
			return true
		}
	}
	return false
}

func findCatalogModel(catProv config.ProviderCatalog, modelIDs ...string) (config.ModelCatalog, bool) {
	for _, id := range modelIDs {
		if m, ok := catProv.FindModel(id); ok {
			return m, true
		}
		trimmed := strings.TrimSuffix(id, "-{effort}")
		if m, ok := catProv.FindModel(trimmed); ok {
			return m, true
		}
		for _, lvl := range config.EffortLadder {
			if strings.HasSuffix(id, "-"+lvl) {
				base := strings.TrimSuffix(id, "-"+lvl)
				if m, ok := catProv.FindModel(base); ok {
					return m, true
				}
				if m, ok := catProv.FindModel(base + "-{effort}"); ok {
					return m, true
				}
			}
		}
	}
	return config.ModelCatalog{}, false
}

func enrichViewFromCatalog(view *ModelEffortView, catProv *config.ProviderCatalog, catModel *config.ModelCatalog) {
	if view.EffortStyle == "" {
		style := catModel.EffortStyle
		if style == "" && catProv != nil {
			style = catProv.EffortStyle
		}
		view.EffortStyle = style
	}
	if len(view.SupportedEfforts) == 0 && len(catModel.SupportedEfforts) > 0 {
		view.SupportedEfforts = make([]string, len(catModel.SupportedEfforts))
		copy(view.SupportedEfforts, catModel.SupportedEfforts)
	}
	if view.DefaultEffort == "" {
		def := catModel.DefaultEffort
		if def == "" && catProv != nil {
			def = catProv.DefaultEffort
		}
		view.DefaultEffort = def
	}
	if !view.Mandatory {
		if catModel.Mandatory || (catProv != nil && catProv.Mandatory) {
			view.Mandatory = true
		}
	}
	if len(view.EffortBudgetMap) == 0 && len(catModel.EffortBudgetMap) > 0 {
		view.EffortBudgetMap = make(map[string]int, len(catModel.EffortBudgetMap))
		for k, v := range catModel.EffortBudgetMap {
			view.EffortBudgetMap[k] = v
		}
	}
}

func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func ladderIndex(level string) int {
	for i, l := range config.EffortLadder {
		if l == level {
			return i
		}
	}
	return -1
}

// nearestEffort finds the candidate with minimal index distance on config.EffortLadder.
// In case of a tie, the lower level (lower index on EffortLadder) is chosen.
func nearestEffort(target string, candidates []string) string {
	if len(candidates) == 0 {
		return target
	}
	targetIdx := ladderIndex(target)
	if targetIdx == -1 {
		return candidates[0]
	}

	best := ""
	bestIdx := -1
	bestDist := 1000

	for _, c := range candidates {
		idx := ladderIndex(c)
		if idx == -1 {
			continue
		}
		dist := targetIdx - idx
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = c
			bestIdx = idx
		} else if dist == bestDist {
			// Tie-breaker: pick lower level (smaller ladder index)
			if idx < bestIdx {
				bestDist = dist
				best = c
				bestIdx = idx
			}
		}
	}

	if best == "" {
		return target
	}
	return best
}
