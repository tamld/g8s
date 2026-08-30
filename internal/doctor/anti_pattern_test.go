package doctor

import (
	"testing"
)

func TestGetAntiPatternCatalog(t *testing.T) {
	cat := GetAntiPatternCatalog()
	if cat == nil {
		t.Fatalf("expected non-nil anti-pattern catalog")
	}

	if cat.TotalRules != 10 {
		t.Errorf("expected 10 total rules, got %d", cat.TotalRules)
	}

	if len(cat.Rules) != 10 {
		t.Errorf("expected len(cat.Rules) == 10, got %d", len(cat.Rules))
	}

	expectedIDs := []string{
		"no_panic",
		"no_ignored_errors",
		"no_type_assertion_in_library",
		"todo_owner",
		"no_ai_artifacts",
		"test_pins_fabricated_symbol",
		"test_locks_impl_detail",
		"supervisor_thinks",
		"directive_brief",
		"missing_dual_blind",
	}

	foundIDs := make(map[string]bool)
	for _, r := range cat.Rules {
		if r.ID == "" || r.Name == "" || r.Severity == "" || r.Linter == "" || r.Description == "" {
			t.Errorf("rule has empty field: %+v", r)
		}
		foundIDs[r.ID] = true
	}

	for _, id := range expectedIDs {
		if !foundIDs[id] {
			t.Errorf("expected rule ID %q not found in catalog", id)
		}
	}
}
