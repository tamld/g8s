package doctor

// AntiPatternRule defines a single rule in the minimal anti-pattern catalog (DEBT-51).
type AntiPatternRule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Severity    string `json:"severity"` // HIGH | MED
	Linter      string `json:"linter"`
	Description string `json:"description"`
	LastFiring  string `json:"last_firing"`
}

// AntiPatternCatalogReport wraps the full 10-rule catalog with firing telemetry.
type AntiPatternCatalogReport struct {
	Rules        []AntiPatternRule `json:"rules"`
	TotalRules   int               `json:"total_rules"`
	Last24hFires int               `json:"last_24h_fires"`
}

// GetAntiPatternCatalog returns the canonical 10-rule minimal anti-pattern catalog (DEBT-51).
func GetAntiPatternCatalog() *AntiPatternCatalogReport {
	rules := []AntiPatternRule{
		{
			ID:          "no_panic",
			Name:        "No Panic in Non-Test Code",
			Severity:    "HIGH",
			Linter:      "tools/ai_lint.sh",
			Description: "No panic() in non-test code (Constitution Axiom 1/4 violation)",
			LastFiring:  "never",
		},
		{
			ID:          "no_ignored_errors",
			Name:        "No Silently Ignored Errors",
			Severity:    "HIGH",
			Linter:      "tools/ai_lint.sh",
			Description: "No silently ignored error swallowing on Close or defer calls",
			LastFiring:  "never",
		},
		{
			ID:          "no_type_assertion_in_library",
			Name:        "No Unchecked Type Assertion",
			Severity:    "HIGH",
			Linter:      "tools/ai_lint.sh",
			Description: "No unchecked type downcasts in internal library code",
			LastFiring:  "never",
		},
		{
			ID:          "todo_owner",
			Name:        "TODO Owner Annotation",
			Severity:    "MED",
			Linter:      "tools/ai_lint.sh",
			Description: "No TODO/FIXME/XXX debt without OWNER= annotation",
			LastFiring:  "never",
		},
		{
			ID:          "no_ai_artifacts",
			Name:        "No AI Conversational Boilerplate",
			Severity:    "MED",
			Linter:      "tools/ai_lint.sh",
			Description: "No conversational LLM boilerplate in source code",
			LastFiring:  "never",
		},
		{
			ID:          "test_pins_fabricated_symbol",
			Name:        "TDD Trap: Fabricated Symbol Pinning",
			Severity:    "HIGH",
			Linter:      "tools/ai_lint.sh",
			Description: "No test files pinning fabricated/undefined symbols (DEBT-49)",
			LastFiring:  "never",
		},
		{
			ID:          "test_locks_impl_detail",
			Name:        "TDD Trap: Implementation Detail Locking",
			Severity:    "MED",
			Linter:      "tools/ai_lint.sh",
			Description: "No test files asserting on private implementation details (DEBT-49)",
			LastFiring:  "never",
		},
		{
			ID:          "supervisor_thinks",
			Name:        "Supervisor Direct Polling Loop",
			Severity:    "HIGH",
			Linter:      "tools/brief_lint.sh",
			Description: "No supervisor/orchestrator direct polling loops or inline actions",
			LastFiring:  "never",
		},
		{
			ID:          "directive_brief",
			Name:        "Directive Brief Without Questions",
			Severity:    "MED",
			Linter:      "tools/brief_lint.sh",
			Description: "No directive briefs lacking open-question framing (DEBT-47 v2)",
			LastFiring:  "never",
		},
		{
			ID:          "missing_dual_blind",
			Name:        "Missing Dual-Blind on Complex Tasks",
			Severity:    "HIGH",
			Linter:      "tools/brief_lint.sh",
			Description: "No complex task briefs dispatched without --blind-converge (DEBT-48)",
			LastFiring:  "never",
		},
	}

	return &AntiPatternCatalogReport{
		Rules:        rules,
		TotalRules:   len(rules),
		Last24hFires: 0,
	}
}
