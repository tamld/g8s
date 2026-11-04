package routing

import (
	"fmt"
	"sort"
	"strings"
)

const choicePermutationAgreementThreshold = 0.85

// ParseRouteTargetChoice splits a joint route_target choice into provider and model.
// Model IDs may contain slashes; only the first slash separates provider from model.
func ParseRouteTargetChoice(choice string) (provider, model string, ok bool) {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", "", false
	}
	parts := strings.SplitN(choice, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ParseJevAssistRouteAnswers reads provider/model from route_target with legacy provider+model fallback.
func ParseJevAssistRouteAnswers(answers map[string]Answer) (provider, model string, routeConfidence float64) {
	if answers == nil {
		return "", "", 0
	}
	if rt := strings.TrimSpace(answers["route_target"].Choice); rt != "" {
		p, m, ok := ParseRouteTargetChoice(rt)
		if ok {
			return p, m, answers["route_target"].Confidence
		}
	}
	provider = strings.TrimSpace(answers["provider"].Choice)
	model = strings.TrimSpace(answers["model"].Choice)
	if provider != "" && model != "" {
		conf := answers["model"].Confidence
		if pConf := answers["provider"].Confidence; pConf > 0 && (conf <= 0 || pConf < conf) {
			conf = pConf
		}
		return provider, model, conf
	}
	return "", "", 0
}

// BuildRouteTargetCriteria builds joint provider/model choice criteria for Jev assist.
func BuildRouteTargetCriteria(candidates []candidateModel) map[string]string {
	seen := make(map[string]struct{})
	out := make(map[string]string)
	for _, c := range candidates {
		key := c.provider + "/" + c.model
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out[key] = fmt.Sprintf("Route to provider %s with model %s.", c.provider, c.model)
	}
	return out
}

// ChoiceTopLabel returns the criteria key with the highest probability (ties broken lexicographically).
func ChoiceTopLabel(probs map[string]float64) string {
	if len(probs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(probs))
	for k := range probs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	bestKey := keys[0]
	bestScore := probs[bestKey]
	for _, k := range keys[1:] {
		if score := probs[k]; score > bestScore {
			bestScore = score
			bestKey = k
		}
	}
	return bestKey
}

// ChoicePermutationAgreement returns 1 when forward and reversed probability maps share the same top choice, else 0.
func ChoicePermutationAgreement(forward, reversed map[string]float64) float64 {
	if ChoiceTopLabel(forward) == ChoiceTopLabel(reversed) {
		return 1.0
	}
	return 0.0
}

// EvaluateChoicePermutationStability scores agreement across forward vs reversed criteria permutations.
// Each pair contributes 1 when top choices match; the mean must meet choicePermutationAgreementThreshold in harness gates.
func EvaluateChoicePermutationStability(pairs []struct {
	Forward  map[string]float64
	Reversed map[string]float64
},
) float64 {
	if len(pairs) == 0 {
		return 0
	}
	var sum float64
	for _, p := range pairs {
		sum += ChoicePermutationAgreement(p.Forward, p.Reversed)
	}
	return sum / float64(len(pairs))
}

// ChoicePermutationAgreementThreshold is the minimum stability rate for choice jaggedness evaluation.
func ChoicePermutationAgreementThreshold() float64 {
	return choicePermutationAgreementThreshold
}
