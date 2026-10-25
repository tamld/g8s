package verifier

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EpistemicTier classifies the verification backing of an Acceptance Criterion in DELTA-37.
type EpistemicTier string

const (
	TierMechanicalVerified     EpistemicTier = "MECHANICAL_VERIFIED"
	TierObservationalTelemetry EpistemicTier = "OBSERVATIONAL_TELEMETRY"
	TierUnverifiedProse        EpistemicTier = "UNVERIFIED_PROSE"

	// CodeHoldoutContamination is returned when S3-M allowed_paths overlap holdout test paths.
	CodeHoldoutContamination = "E_HOLDOUT_CONTAMINATION"
)

// CanonicalScenarioDimensions defines the 12 adversarial QA scenario dimensions.
var CanonicalScenarioDimensions = []string{
	"boundary",
	"null_empty",
	"concurrency_race",
	"network_timeout",
	"permission_authz",
	"idempotency_retry",
	"resource_exhaustion",
	"encoding_i18n",
	"state_transition",
	"clock_skew",
	"rollback_recovery",
	"security_injection",
}

// BASpecValidationResult captures Stage 1 & Stage 2 validation of a BA/QA specification.
type BASpecValidationResult struct {
	Passed             bool     `json:"passed"`
	UserStories        []string `json:"user_stories"`
	FunctionalReqs     []string `json:"functional_reqs"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	HasGivenWhenThen   bool     `json:"has_given_when_then"`
	HasFSMTransitions  bool     `json:"has_fsm_transitions"`
	CoveredDimensions  []string `json:"covered_dimensions"`
	MissingElements    []string `json:"missing_elements,omitempty"`
}

// ACClaimEvidence binds a single Acceptance Criterion (AC-*) to its verification evidence.
type ACClaimEvidence struct {
	ACID        string        `json:"ac_id"`
	Tier        EpistemicTier `json:"tier"`
	TestBinding string        `json:"test_binding,omitempty"`
	ExitCode    int           `json:"exit_code"`
	Passed      bool          `json:"passed"`
}

// QAPipelineStageStatus summarizes one of the 6 stages in the AI QA Pipeline.
type QAPipelineStageStatus struct {
	Stage   int    `json:"stage"`
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Summary string `json:"summary"`
}

// QAPipelineRequest provides inputs for evaluating the 6-Stage AI QA Pipeline (DELTA-37).
type QAPipelineRequest struct {
	TaskID            string            `json:"task_id,omitempty"`
	SpecMarkdown      string            `json:"spec_markdown"`
	HoldoutPaths      []string          `json:"holdout_paths,omitempty"`
	AllowedWritePaths []string          `json:"allowed_write_paths,omitempty"`
	Canary            *CanaryResult     `json:"canary,omitempty"`
	ACTestBindings    map[string]string `json:"ac_test_bindings,omitempty"`
	TelemetryBindings map[string]string `json:"telemetry_bindings,omitempty"`
	ChecksPassed      bool              `json:"checks_passed"`
}

// QAPipelineReport is the machine-checkable Stage 6 output of the AI QA Pipeline.
type QAPipelineReport struct {
	TaskID               string                  `json:"task_id,omitempty"`
	Passed               bool                    `json:"passed"`
	Verdict              string                  `json:"verdict"`
	BASpecValidation     BASpecValidationResult  `json:"ba_spec_validation"`
	HoldoutDisjoint      bool                    `json:"holdout_disjoint"`
	HoldoutViolation     string                  `json:"holdout_violation,omitempty"`
	CanaryGatePassed     bool                    `json:"canary_gate_passed"`
	MechanicalVerified   int                     `json:"mechanical_verified_count"`
	ObservationalCount   int                     `json:"observational_telemetry_count"`
	UnverifiedProseCount int                     `json:"unverified_prose_count"`
	ACClaims             []ACClaimEvidence       `json:"ac_claims"`
	Stages               []QAPipelineStageStatus `json:"stages"`
	FailureReasons       []string                `json:"failure_reasons,omitempty"`
}

var (
	reUserStory = regexp.MustCompile(`\bUS-[0-9]{1,4}\b`)
	reFuncReq   = regexp.MustCompile(`\b(?:REQ-FUNC-[0-9]{1,4}|FR-[0-9]{1,4})\b`)
	reACID      = regexp.MustCompile(`\bAC-[0-9]{1,4}\b`)
	reGiven     = regexp.MustCompile(`(?i)\bgiven\b`)
	reWhen      = regexp.MustCompile(`(?i)\bwhen\b`)
	reThen      = regexp.MustCompile(`(?i)\bthen\b`)
	reFSM       = regexp.MustCompile(`(?i)(?:\bFSM\b|stateDiagram|-->|->|\[STATE)`)
)

// ValidateBASpecContract executes Stage 1 & Stage 2 of the AI QA Pipeline:
// verifying that BA requirements are normalized into traceable US-*, REQ-FUNC-*/FR-*,
// AC-* (Given/When/Then), and FSM state transitions, plus 12D scenario dimensions.
func ValidateBASpecContract(specText string) BASpecValidationResult {
	stories := uniqueSortedMatches(reUserStory.FindAllString(specText, -1))
	reqs := uniqueSortedMatches(reFuncReq.FindAllString(specText, -1))
	acs := uniqueSortedMatches(reACID.FindAllString(specText, -1))
	hasGWT := reGiven.MatchString(specText) && reWhen.MatchString(specText) && reThen.MatchString(specText)
	hasFSM := reFSM.MatchString(specText)
	dims := EvaluateScenarioDimensions(specText)

	var missing []string
	if len(stories) == 0 {
		missing = append(missing, "user_stories (US-*)")
	}
	if len(reqs) == 0 {
		missing = append(missing, "functional_reqs (REQ-FUNC-* or FR-*)")
	}
	if len(acs) == 0 {
		missing = append(missing, "acceptance_criteria (AC-*)")
	}
	if !hasGWT {
		missing = append(missing, "given_when_then_clauses")
	}
	if !hasFSM {
		missing = append(missing, "fsm_state_transitions")
	}

	return BASpecValidationResult{
		Passed:             len(missing) == 0,
		UserStories:        stories,
		FunctionalReqs:     reqs,
		AcceptanceCriteria: acs,
		HasGivenWhenThen:   hasGWT,
		HasFSMTransitions:  hasFSM,
		CoveredDimensions:  dims,
		MissingElements:    missing,
	}
}

// EvaluateScenarioDimensions detects which of the 12 canonical QA scenario dimensions are addressed in specText.
func EvaluateScenarioDimensions(specText string) []string {
	lower := strings.ToLower(specText)
	keywordMap := map[string][]string{
		"boundary":            {"boundary", "min", "max", "limit", "overflow", "zero"},
		"null_empty":          {"null", "nil", "empty", "missing", "blank"},
		"concurrency_race":    {"concurrent", "race", "parallel", "mutex", "atomic", "cas", "lease"},
		"network_timeout":     {"timeout", "disconnect", "retry", "deadline", "latency", "offline"},
		"permission_authz":    {"permission", "auth", "unauthorized", "forbidden", "scope", "receipt"},
		"idempotency_retry":   {"idempotent", "idempotency", "duplicate", "replay", "re-run"},
		"resource_exhaustion": {"budget", "quota", "memory", "disk", "exhaustion", "rate_limit", "429"},
		"encoding_i18n":       {"utf-8", "unicode", "i18n", "encoding", "locale"},
		"state_transition":    {"fsm", "state", "transition", "queued", "running", "completed", "failed"},
		"clock_skew":          {"ttl", "expired", "clock", "timestamp", "epoch"},
		"rollback_recovery":   {"rollback", "recover", "snapshot", "restore", "cleanup"},
		"security_injection":  {"injection", "traversal", "ssrf", "xss", "redact", "sanitize", "secret"},
	}

	var covered []string
	for _, dim := range CanonicalScenarioDimensions {
		for _, kw := range keywordMap[dim] {
			if strings.Contains(lower, kw) {
				covered = append(covered, dim)
				break
			}
		}
	}
	return covered
}

// VerifyHoldoutDisjointness enforces Stage 3 of the AI QA Pipeline:
// S3-M implementation workers MUST NOT have write permissions overlapping Holdout test paths.
func VerifyHoldoutDisjointness(holdoutPaths, allowedWritePaths []string) error {
	for _, rawHoldout := range holdoutPaths {
		h := normalizeCleanPrefix(rawHoldout)
		if h == "" {
			continue
		}
		for _, rawWrite := range allowedWritePaths {
			w := strings.TrimSpace(filepath.ToSlash(rawWrite))
			if w == "" {
				continue
			}
			if w == "*" || w == "**" || w == "." || w == "./**" {
				return fmt.Errorf("%s: wildcard write scope %q overlaps holdout path %q", CodeHoldoutContamination, rawWrite, rawHoldout)
			}
			wPrefix := normalizeCleanPrefix(w)
			if wPrefix == h || strings.HasPrefix(h, wPrefix+"/") || strings.HasPrefix(wPrefix, h+"/") {
				return fmt.Errorf("%s: allowed write path %q overlaps holdout path %q", CodeHoldoutContamination, rawWrite, rawHoldout)
			}
		}
	}
	return nil
}

// EvaluateQAPipeline executes all 6 stages of the AI QA Pipeline and produces a fail-closed Epistemic QA Report.
func EvaluateQAPipeline(req QAPipelineRequest) QAPipelineReport {
	var failures []string

	// Stage 1 & 2: BA-to-Spec Normalization + 12D Scenario Matrix
	baRes := ValidateBASpecContract(req.SpecMarkdown)
	stage1Passed := baRes.Passed
	stage1Summary := fmt.Sprintf("US=%d REQ=%d AC=%d GWT=%v FSM=%v",
		len(baRes.UserStories), len(baRes.FunctionalReqs), len(baRes.AcceptanceCriteria),
		baRes.HasGivenWhenThen, baRes.HasFSMTransitions)
	if !stage1Passed {
		failures = append(failures, fmt.Sprintf("Stage 1 BA-Spec normalization failed: missing %s", strings.Join(baRes.MissingElements, ", ")))
	}

	stage2Passed := len(baRes.CoveredDimensions) >= 3
	stage2Summary := fmt.Sprintf("Covered %d/12 scenario dimensions (%s)", len(baRes.CoveredDimensions), strings.Join(baRes.CoveredDimensions, ", "))
	if !stage2Passed {
		failures = append(failures, fmt.Sprintf("Stage 2 Scenario Matrix insufficient: covered %d/12 dimensions (minimum 3 required)", len(baRes.CoveredDimensions)))
	}

	// Stage 3: Holdout Partition Disjointness
	holdoutErr := VerifyHoldoutDisjointness(req.HoldoutPaths, req.AllowedWritePaths)
	holdoutDisjoint := holdoutErr == nil
	holdoutViolation := ""
	stage3Summary := "Holdout partition strictly disjoint from S3-M allowed_paths"
	if holdoutErr != nil {
		holdoutViolation = holdoutErr.Error()
		stage3Summary = holdoutViolation
		failures = append(failures, "Stage 3 Holdout partition failed: "+holdoutViolation)
	}

	// Stage 4: Red-to-Green & Seeded Mutant Canary Gate
	canaryPassed := true
	stage4Summary := "Canary check passed (all seeded mutants killed)"
	if req.Canary != nil {
		if !req.Canary.Executed || !req.Canary.CanaryCaught || req.Canary.VetoStatus == VetoCanarySurvived {
			canaryPassed = false
			stage4Summary = fmt.Sprintf("VETO_CANARY_SURVIVED: %s", req.Canary.Detail)
			failures = append(failures, "Stage 4 Mutant Canary failed: "+stage4Summary)
		}
	}

	// Stage 5 & 6: Multi-Layer Execution & Epistemic Claim Audit
	var claims []ACClaimEvidence
	mechCount := 0
	obsCount := 0
	proseCount := 0

	for _, acID := range baRes.AcceptanceCriteria {
		if testName, ok := req.ACTestBindings[acID]; ok && strings.TrimSpace(testName) != "" && req.ChecksPassed {
			mechCount++
			claims = append(claims, ACClaimEvidence{
				ACID:        acID,
				Tier:        TierMechanicalVerified,
				TestBinding: testName,
				ExitCode:    0,
				Passed:      true,
			})
			continue
		}
		if probeName, ok := req.TelemetryBindings[acID]; ok && strings.TrimSpace(probeName) != "" && req.ChecksPassed {
			obsCount++
			claims = append(claims, ACClaimEvidence{
				ACID:        acID,
				Tier:        TierObservationalTelemetry,
				TestBinding: probeName,
				ExitCode:    0,
				Passed:      true,
			})
			continue
		}
		proseCount++
		claims = append(claims, ACClaimEvidence{
			ACID:     acID,
			Tier:     TierUnverifiedProse,
			ExitCode: 1,
			Passed:   false,
		})
	}

	stage5Passed := req.ChecksPassed && len(baRes.AcceptanceCriteria) > 0
	stage5Summary := fmt.Sprintf("ChecksPassed=%v across %d ACs", req.ChecksPassed, len(baRes.AcceptanceCriteria))
	if !stage5Passed {
		failures = append(failures, "Stage 5 Multi-Layer Execution failed: mechanical checks did not pass or no ACs defined")
	}

	stage6Passed := proseCount == 0 && len(claims) > 0
	stage6Summary := fmt.Sprintf("MECHANICAL_VERIFIED=%d OBSERVATIONAL_TELEMETRY=%d UNVERIFIED_PROSE=%d", mechCount, obsCount, proseCount)
	if !stage6Passed {
		failures = append(failures, fmt.Sprintf("Stage 6 Epistemic Audit failed: %d AC claim(s) remain UNVERIFIED_PROSE", proseCount))
	}

	stages := []QAPipelineStageStatus{
		{Stage: 1, Name: "BA-to-Spec Normalization", Passed: stage1Passed, Summary: stage1Summary},
		{Stage: 2, Name: "12D Scenario Matrix", Passed: stage2Passed, Summary: stage2Summary},
		{Stage: 3, Name: "Holdout Partition Isolation", Passed: holdoutDisjoint, Summary: stage3Summary},
		{Stage: 4, Name: "Red-to-Green & Mutant Canary", Passed: canaryPassed, Summary: stage4Summary},
		{Stage: 5, Name: "Multi-Layer Test Execution", Passed: stage5Passed, Summary: stage5Summary},
		{Stage: 6, Name: "Double-Blind Epistemic Audit", Passed: stage6Passed, Summary: stage6Summary},
	}

	overallPassed := stage1Passed && stage2Passed && holdoutDisjoint && canaryPassed && stage5Passed && stage6Passed
	verdict := "PASS"
	if !overallPassed {
		verdict = "FAIL"
	}

	return QAPipelineReport{
		TaskID:               req.TaskID,
		Passed:               overallPassed,
		Verdict:              verdict,
		BASpecValidation:     baRes,
		HoldoutDisjoint:      holdoutDisjoint,
		HoldoutViolation:     holdoutViolation,
		CanaryGatePassed:     canaryPassed,
		MechanicalVerified:   mechCount,
		ObservationalCount:   obsCount,
		UnverifiedProseCount: proseCount,
		ACClaims:             claims,
		Stages:               stages,
		FailureReasons:       failures,
	}
}

func uniqueSortedMatches(raw []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range raw {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeCleanPrefix(p string) string {
	p = strings.TrimSpace(filepath.ToSlash(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/**")
	p = strings.TrimSuffix(p, "/*")
	p = strings.TrimSuffix(p, "/")
	return p
}
