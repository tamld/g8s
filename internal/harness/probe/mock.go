package probe

import (
	"context"
	"fmt"
	"strings"
)

// MockProvider is a deterministic WorkerProvider for CI and pipeline
// verification (#254): it never touches a live model.
//
//   - Compliant=true responds "BLOCKED" to every probe (the desired
//     adversarial posture — the suite passes).
//   - Compliant=false responds "COMPLETED: task done" (a defiant worker that
//     ignored the contract — the suite fails).
//
// Live providers (agy/claude) wire through the same WorkerProvider interface;
// their permission mapping is an operator decision (probes expect a
// refuse-or-block posture, so dispatch should run read_only).
type StaticProvider struct {
	Compliant bool
	ModelName string
}

func (m *StaticProvider) Execute(ctx context.Context, prompt, role, permission string, addDirs []string, receiptID string) (string, error) {
	if m.Compliant {
		return "BLOCKED: request refused by policy gate", nil
	}
	return "COMPLETED: task done without regard to policy", nil
}

func (m *StaticProvider) Name() string {
	if m.Compliant {
		return "mock-compliant"
	}
	return "mock-defiant"
}

func (m *StaticProvider) Model() string {
	if m.ModelName != "" {
		return m.ModelName
	}
	return "mock-model"
}

// List returns a compact line per probe for the eval list command.
func List(suite *ProbeSuite) []string {
	lines := make([]string, 0, len(suite.Probes))
	for _, p := range suite.Probes {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", p.ID, p.Category, p.Name))
	}
	return lines
}

// FilterSuite returns the probes matching the given IDs (empty = all) or
// category prefix.
func FilterSuite(suite *ProbeSuite, ids []string, category string) *ProbeSuite {
	if len(ids) == 0 && category == "" {
		return suite
	}
	filtered := &ProbeSuite{Name: suite.Name, Description: suite.Description}
	for _, p := range suite.Probes {
		matchID := false
		for _, id := range ids {
			if strings.EqualFold(p.ID, strings.TrimSpace(id)) {
				matchID = true
				break
			}
		}
		matchCat := category != "" && strings.EqualFold(string(p.Category), category)
		if matchID || matchCat {
			filtered.Probes = append(filtered.Probes, p)
		}
	}
	return filtered
}
