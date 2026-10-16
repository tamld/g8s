package telemetry

// Sovereign HITL Ground Truth Calibration & Hypothesis Falsification Engine
// (issue #596, Hypotheses H1, H2, H3).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// ThermalState represents a rule's oscillation state on the Sine-Wave quality curve (Hypothesis H2).
type ThermalState string

const (
	// ThermalHot indicates an active rule injected into JIT context (subject to K_max carrying capacity).
	ThermalHot ThermalState = "hot"
	// ThermalWarm indicates a decaying rule monitored at verification boundaries.
	ThermalWarm ThermalState = "warm"
	// ThermalCold indicates a dormant rule stored in SQLite (0 prompt tokens) and re-awakened on regression.
	ThermalCold ThermalState = "cold"
)

// HITLVerdict represents a Human Sovereign Ground Truth calibration signal.
type HITLVerdict string

const (
	// HITLVerdictTP confirms a rule or advisory caught a real regression (True Positive).
	HITLVerdictTP HITLVerdict = "tp"
	// HITLVerdictFP marks a rule or advisory as noisy/over-blocking (False Positive).
	HITLVerdictFP HITLVerdict = "fp"
	// HITLVerdictVeto vetoes an F0 override or flags an escaped regression.
	HITLVerdictVeto HITLVerdict = "veto"
)

// EpistemicStatus tracks whether a governance axiom is still an unproven hypothesis,
// empirically validated, or falsified by telemetry data.
type EpistemicStatus string

const (
	EpistemicHypothesis EpistemicStatus = "hypothesis"
	EpistemicValidated  EpistemicStatus = "validated"
	EpistemicFalsified  EpistemicStatus = "falsified"
)

// Calibration constants: Sovereign HITL weight (AlphaHITL) is strictly greater than
// automated proxy steps (AlphaAuto, BetaAuto) so human Ground Truth always dominates.
const (
	AlphaHITL          = 0.50
	AlphaAuto          = 0.10
	BetaAuto           = 0.15
	LambdaDecayPerDay  = 0.05
	HotThreshold       = 0.65
	WarmThreshold      = 0.30
	DefaultInitialWt   = 0.70
	MinSamplesForProof = 5
)

// RuleWeight stores the Sine-Wave weight Wt, thermal state, and empirical TP/FP counters for a rule.
type RuleWeight struct {
	RuleID          string       `json:"rule_id"`
	Package         string       `json:"package"`
	Weight          float64      `json:"weight"`
	ThermalState    ThermalState `json:"thermal_state"`
	TPCount         int          `json:"tp_count"`
	FPCount         int          `json:"fp_count"`
	HITLTP          int          `json:"hitl_tp"`
	HITLFP          int          `json:"hitl_fp"`
	HITLVeto        int          `json:"hitl_veto"`
	DormantTriggers int          `json:"dormant_triggers"`
	DormantEscapes  int          `json:"dormant_escapes"`
	F1Score         float64      `json:"f1_score"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// OverrideReceipt records an F0 Supervisor decision to bypass an Advisory Signal (Level 1 Trias Politica).
type OverrideReceipt struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	RuleID      string    `json:"rule_id"`
	Actor       string    `json:"actor"`
	Reason      string    `json:"reason"`
	AutoOutcome string    `json:"auto_outcome"`
	HITLVerdict string    `json:"hitl_verdict,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// HITLFeedback represents a Level 3 Sovereign Human Ground Truth calibration event.
type HITLFeedback struct {
	ID        string      `json:"id"`
	RuleID    string      `json:"rule_id"`
	TaskID    string      `json:"task_id,omitempty"`
	Package   string      `json:"package,omitempty"`
	Verdict   HITLVerdict `json:"verdict"`
	Actor     string      `json:"actor"`
	Note      string      `json:"note,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

// HypothesisEvaluation summarizes empirical evidence for a single falsifiable governance hypothesis.
type HypothesisEvaluation struct {
	ID              string          `json:"id"`
	Statement       string          `json:"statement"`
	EpistemicStatus EpistemicStatus `json:"epistemic_status"`
	MetricName      string          `json:"metric_name"`
	MetricValue     float64         `json:"metric_value"`
	SampleCount     int             `json:"sample_count"`
	Summary         string          `json:"summary"`
}

// HypothesisReport bundles the live falsification status of H1, H2, and H3 with rule weights and overrides.
type HypothesisReport struct {
	GeneratedAt          time.Time              `json:"generated_at"`
	MaxActiveRulesPerPkg int                    `json:"max_active_rules_per_pkg"`
	Hypotheses           []HypothesisEvaluation `json:"hypotheses"`
	Rules                []RuleWeight           `json:"rules"`
	Overrides            []OverrideReceipt      `json:"overrides,omitempty"`
}

// ClassifyThermalState maps a rule weight Wt in [0, 1] to its ThermalState.
func ClassifyThermalState(weight float64) ThermalState {
	switch {
	case weight >= HotThreshold:
		return ThermalHot
	case weight >= WarmThreshold:
		return ThermalWarm
	default:
		return ThermalCold
	}
}

// ComputeRuleF1 calculates the harmonic mean F1 score of a rule's empirical catches,
// weighting HITL verdicts 3x relative to automated proxy counts.
func ComputeRuleF1(tp, fp, hitlTP, hitlFP, hitlVeto, dormantEscapes int) float64 {
	effectiveTP := float64(tp) + 3.0*float64(hitlTP+hitlVeto)
	effectiveFP := float64(fp) + 3.0*float64(hitlFP)
	effectiveFN := float64(dormantEscapes) + 2.0*float64(hitlVeto)
	denom := 2.0*effectiveTP + effectiveFP + effectiveFN
	if denom <= 0 {
		return 0.0
	}
	return (2.0 * effectiveTP) / denom
}

func clampWeight(w float64) float64 {
	if w < 0.0 {
		return 0.0
	}
	if w > 1.0 {
		return 1.0
	}
	return w
}

// GetOrInitRuleWeight retrieves the current RuleWeight for ruleID or initializes it at DefaultInitialWt.
func (e *TelemetryEngine) GetOrInitRuleWeight(ctx context.Context, ruleID, pkg string) (*RuleWeight, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return nil, errors.New("rule_id must not be empty")
	}
	pkg = strings.TrimSpace(pkg)

	row := e.db.QueryRowContext(ctx, `
		SELECT rule_id, package_name, weight, thermal_state,
		       tp_count, fp_count, hitl_tp, hitl_fp, hitl_veto,
		       dormant_triggers, dormant_escapes, updated_at
		FROM rule_weights WHERE rule_id = ?
	`, ruleID)

	var rw RuleWeight
	var stateStr string
	var updatedNano int64
	err := row.Scan(
		&rw.RuleID, &rw.Package, &rw.Weight, &stateStr,
		&rw.TPCount, &rw.FPCount, &rw.HITLTP, &rw.HITLFP, &rw.HITLVeto,
		&rw.DormantTriggers, &rw.DormantEscapes, &updatedNano,
	)
	if err == nil {
		rw.ThermalState = ThermalState(stateStr)
		rw.UpdatedAt = time.Unix(0, updatedNano)
		if rw.Package == "" && pkg != "" {
			rw.Package = pkg
		}
		rw.F1Score = ComputeRuleF1(rw.TPCount, rw.FPCount, rw.HITLTP, rw.HITLFP, rw.HITLVeto, rw.DormantEscapes)
		return &rw, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	now := e.getClock().UTC()
	rw = RuleWeight{
		RuleID:       ruleID,
		Package:      pkg,
		Weight:       DefaultInitialWt,
		ThermalState: ClassifyThermalState(DefaultInitialWt),
		UpdatedAt:    now,
	}
	if err := e.saveRuleWeight(ctx, rw); err != nil {
		return nil, err
	}
	return &rw, nil
}

func (e *TelemetryEngine) saveRuleWeight(ctx context.Context, rw RuleWeight) error {
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO rule_weights (
			rule_id, package_name, weight, thermal_state,
			tp_count, fp_count, hitl_tp, hitl_fp, hitl_veto,
			dormant_triggers, dormant_escapes, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rule_id) DO UPDATE SET
			package_name=excluded.package_name,
			weight=excluded.weight,
			thermal_state=excluded.thermal_state,
			tp_count=excluded.tp_count,
			fp_count=excluded.fp_count,
			hitl_tp=excluded.hitl_tp,
			hitl_fp=excluded.hitl_fp,
			hitl_veto=excluded.hitl_veto,
			dormant_triggers=excluded.dormant_triggers,
			dormant_escapes=excluded.dormant_escapes,
			updated_at=excluded.updated_at
	`,
		rw.RuleID, rw.Package, rw.Weight, string(rw.ThermalState),
		rw.TPCount, rw.FPCount, rw.HITLTP, rw.HITLFP, rw.HITLVeto,
		rw.DormantTriggers, rw.DormantEscapes, rw.UpdatedAt.UnixNano(),
	)
	return err
}

// RecordAutomatedSignal updates a rule's weight Wt using Level 2 Delayed Reality Proxy
// outcomes (prevented vs false positive) and time decay, and records dormant triggers/escapes
// when a Cold rule fires (Hypothesis H2).
func (e *TelemetryEngine) RecordAutomatedSignal(ctx context.Context, ruleID, pkg string, prevented bool, dormantEscape bool) (*RuleWeight, error) {
	rw, err := e.GetOrInitRuleWeight(ctx, ruleID, pkg)
	if err != nil {
		return nil, err
	}

	now := e.getClock().UTC()
	daysElapsed := now.Sub(rw.UpdatedAt).Hours() / 24.0
	if daysElapsed < 0 {
		daysElapsed = 0
	}
	decayed := rw.Weight * math.Exp(-LambdaDecayPerDay*daysElapsed)

	wasCold := rw.ThermalState == ThermalCold
	if wasCold && (prevented || dormantEscape) {
		rw.DormantTriggers++
		if dormantEscape {
			rw.DormantEscapes++
		}
	}

	if prevented {
		rw.TPCount++
		decayed += AlphaAuto
		if wasCold {
			// Re-awaken dormant rule at least to WarmThreshold upon regression catch
			decayed = math.Max(decayed, WarmThreshold+AlphaAuto)
		}
	} else {
		rw.FPCount++
		decayed -= BetaAuto
	}

	rw.Weight = clampWeight(decayed)
	rw.ThermalState = ClassifyThermalState(rw.Weight)
	rw.UpdatedAt = now
	rw.F1Score = ComputeRuleF1(rw.TPCount, rw.FPCount, rw.HITLTP, rw.HITLFP, rw.HITLVeto, rw.DormantEscapes)

	if err := e.saveRuleWeight(ctx, *rw); err != nil {
		return nil, err
	}
	return rw, nil
}

// RecordHITLFeedback records a Level 3 Sovereign Human Ground Truth verdict (tp, fp, veto)
// and immediately anchors the target rule's weight Wt with AlphaHITL >> AlphaAuto (#596).
func (e *TelemetryEngine) RecordHITLFeedback(ctx context.Context, fb HITLFeedback) (*RuleWeight, error) {
	fb.RuleID = strings.TrimSpace(fb.RuleID)
	if fb.RuleID == "" {
		return nil, errors.New("rule_id is required for HITL feedback")
	}
	switch fb.Verdict {
	case HITLVerdictTP, HITLVerdictFP, HITLVerdictVeto:
	default:
		return nil, fmt.Errorf("invalid HITL verdict %q (must be tp, fp, or veto)", fb.Verdict)
	}
	if strings.TrimSpace(fb.Actor) == "" {
		fb.Actor = "human-operator"
	}
	now := e.getClock().UTC()
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = now
	}
	if fb.ID == "" {
		seq := e.seq.Add(1)
		fb.ID = fmt.Sprintf("hitl-%d-%06d-%s", now.UnixNano(), seq%1000000, randomString(6))
	}

	if _, err := e.db.ExecContext(ctx, `
		INSERT INTO hitl_feedback (id, rule_id, task_id, package_name, verdict, actor, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, fb.ID, fb.RuleID, fb.TaskID, fb.Package, string(fb.Verdict), fb.Actor, fb.Note, fb.CreatedAt.UnixNano()); err != nil {
		return nil, fmt.Errorf("insert hitl feedback: %w", err)
	}

	// Update any correlated OverrideReceipt with the human sovereign verdict
	if fb.TaskID != "" {
		_, _ = e.db.ExecContext(ctx, `
			UPDATE override_receipts SET hitl_verdict = ? WHERE rule_id = ? AND task_id = ?
		`, string(fb.Verdict), fb.RuleID, fb.TaskID)
	} else {
		_, _ = e.db.ExecContext(ctx, `
			UPDATE override_receipts SET hitl_verdict = ? WHERE rule_id = ? AND hitl_verdict = ''
		`, string(fb.Verdict), fb.RuleID)
	}

	rw, err := e.GetOrInitRuleWeight(ctx, fb.RuleID, fb.Package)
	if err != nil {
		return nil, err
	}

	wasCold := rw.ThermalState == ThermalCold
	switch fb.Verdict {
	case HITLVerdictTP:
		rw.HITLTP++
		if wasCold {
			rw.DormantTriggers++
		}
		rw.Weight = clampWeight(math.Max(rw.Weight+AlphaHITL, HotThreshold+0.15))
	case HITLVerdictVeto:
		rw.HITLVeto++
		if wasCold {
			rw.DormantTriggers++
			rw.DormantEscapes++
		}
		rw.Weight = clampWeight(math.Max(rw.Weight+AlphaHITL, 0.90))
	case HITLVerdictFP:
		rw.HITLFP++
		rw.Weight = clampWeight(rw.Weight - AlphaHITL)
	}

	rw.ThermalState = ClassifyThermalState(rw.Weight)
	rw.UpdatedAt = now
	rw.F1Score = ComputeRuleF1(rw.TPCount, rw.FPCount, rw.HITLTP, rw.HITLFP, rw.HITLVeto, rw.DormantEscapes)

	if err := e.saveRuleWeight(ctx, *rw); err != nil {
		return nil, err
	}
	return rw, nil
}

// RecordOverrideReceipt records a Level 1 Trias Politica OverrideReceipt when F0
// bypasses an Advisory Signal (#595, #596).
func (e *TelemetryEngine) RecordOverrideReceipt(ctx context.Context, rec OverrideReceipt) (*OverrideReceipt, error) {
	rec.TaskID = strings.TrimSpace(rec.TaskID)
	rec.RuleID = strings.TrimSpace(rec.RuleID)
	rec.Reason = strings.TrimSpace(rec.Reason)
	if rec.TaskID == "" || rec.RuleID == "" || rec.Reason == "" {
		return nil, errors.New("task_id, rule_id, and reason are required for override receipt")
	}
	if strings.TrimSpace(rec.Actor) == "" {
		rec.Actor = "f0-supervisor"
	}
	if strings.TrimSpace(rec.AutoOutcome) == "" {
		rec.AutoOutcome = "pending"
	}
	now := e.getClock().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.ID == "" {
		seq := e.seq.Add(1)
		rec.ID = fmt.Sprintf("ovr-%d-%06d-%s", now.UnixNano(), seq%1000000, randomString(6))
	}

	if _, err := e.db.ExecContext(ctx, `
		INSERT INTO override_receipts (id, task_id, rule_id, actor, reason, auto_outcome, hitl_verdict, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, rec.ID, rec.TaskID, rec.RuleID, rec.Actor, rec.Reason, rec.AutoOutcome, rec.HITLVerdict, rec.CreatedAt.UnixNano()); err != nil {
		return nil, fmt.Errorf("insert override receipt: %w", err)
	}
	return &rec, nil
}

// EnforceCarryingCapacity enforces the "One-In, One-Out" carrying capacity K_max
// on Hot rules for a given package (Hypothesis H1). Any Hot rules beyond maxK
// (ordered by weight descending) are demoted to ThermalWarm.
func (e *TelemetryEngine) EnforceCarryingCapacity(ctx context.Context, pkg string, maxK int) ([]RuleWeight, error) {
	if maxK <= 0 {
		maxK = 3
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT rule_id, package_name, weight, thermal_state,
		       tp_count, fp_count, hitl_tp, hitl_fp, hitl_veto,
		       dormant_triggers, dormant_escapes, updated_at
		FROM rule_weights
		WHERE package_name = ? AND thermal_state = 'hot'
		ORDER BY weight DESC, updated_at DESC
	`, strings.TrimSpace(pkg))
	if err != nil {
		return nil, err
	}

	var hotRules []RuleWeight
	for rows.Next() {
		var rw RuleWeight
		var stateStr string
		var updatedNano int64
		if err := rows.Scan(
			&rw.RuleID, &rw.Package, &rw.Weight, &stateStr,
			&rw.TPCount, &rw.FPCount, &rw.HITLTP, &rw.HITLFP, &rw.HITLVeto,
			&rw.DormantTriggers, &rw.DormantEscapes, &updatedNano,
		); err != nil {
			rows.Close()
			return nil, err
		}
		rw.ThermalState = ThermalState(stateStr)
		rw.UpdatedAt = time.Unix(0, updatedNano)
		hotRules = append(hotRules, rw)
	}
	rows.Close()

	for i := range hotRules {
		if i >= maxK {
			hotRules[i].ThermalState = ThermalWarm
			if hotRules[i].Weight >= HotThreshold {
				hotRules[i].Weight = HotThreshold - 0.01
			}
			_ = e.saveRuleWeight(ctx, hotRules[i])
		}
		hotRules[i].F1Score = ComputeRuleF1(
			hotRules[i].TPCount, hotRules[i].FPCount,
			hotRules[i].HITLTP, hotRules[i].HITLFP, hotRules[i].HITLVeto,
			hotRules[i].DormantEscapes,
		)
	}
	return hotRules, nil
}

// RecordKYieldSample logs an empirical observation of (active_k, first_pass_ok)
// for a task and package to test Hypothesis H1 (Carrying Capacity K_max).
func (e *TelemetryEngine) RecordKYieldSample(ctx context.Context, taskID, pkg string, activeK int, firstPassOK bool) error {
	now := e.getClock().UTC()
	seq := e.seq.Add(1)
	id := fmt.Sprintf("ky-%d-%06d-%s", now.UnixNano(), seq%1000000, randomString(6))
	okInt := 0
	if firstPassOK {
		okInt = 1
	}
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO k_yield_samples (id, task_id, package_name, active_k, first_pass_ok, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, strings.TrimSpace(taskID), strings.TrimSpace(pkg), activeK, okInt, now.UnixNano())
	return err
}

// ListRuleWeights returns all tracked RuleWeight records ordered by weight descending.
func (e *TelemetryEngine) ListRuleWeights(ctx context.Context) ([]RuleWeight, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT rule_id, package_name, weight, thermal_state,
		       tp_count, fp_count, hitl_tp, hitl_fp, hitl_veto,
		       dormant_triggers, dormant_escapes, updated_at
		FROM rule_weights
		ORDER BY weight DESC, rule_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RuleWeight
	for rows.Next() {
		var rw RuleWeight
		var stateStr string
		var updatedNano int64
		if err := rows.Scan(
			&rw.RuleID, &rw.Package, &rw.Weight, &stateStr,
			&rw.TPCount, &rw.FPCount, &rw.HITLTP, &rw.HITLFP, &rw.HITLVeto,
			&rw.DormantTriggers, &rw.DormantEscapes, &updatedNano,
		); err != nil {
			return nil, err
		}
		rw.ThermalState = ThermalState(stateStr)
		rw.UpdatedAt = time.Unix(0, updatedNano)
		rw.F1Score = ComputeRuleF1(rw.TPCount, rw.FPCount, rw.HITLTP, rw.HITLFP, rw.HITLVeto, rw.DormantEscapes)
		out = append(out, rw)
	}
	return out, rows.Err()
}

// EvaluateHypotheses computes the empirical falsification report for Hypotheses H1, H2, and H3 (#596).
// Each hypothesis remains in EpistemicHypothesis status until at least MinSamplesForProof
// observations have been recorded, preventing unproven axioms from being treated as dogma.
func (e *TelemetryEngine) EvaluateHypotheses(ctx context.Context, maxK int) (*HypothesisReport, error) {
	if maxK <= 0 {
		maxK = 3
	}
	rules, err := e.ListRuleWeights(ctx)
	if err != nil {
		return nil, err
	}

	// 1. Evaluate H1: Carrying Capacity K_max <= maxK vs K > maxK First-Pass Yield
	var boundedTotal, boundedPass int
	var unboundedTotal, unboundedPass int
	kRows, err := e.db.QueryContext(ctx, `SELECT active_k, first_pass_ok FROM k_yield_samples`)
	if err == nil {
		for kRows.Next() {
			var k, okInt int
			if scanErr := kRows.Scan(&k, &okInt); scanErr == nil {
				if k <= maxK {
					boundedTotal++
					boundedPass += okInt
				} else {
					unboundedTotal++
					unboundedPass += okInt
				}
			}
		}
		kRows.Close()
	}

	totalKSamples := boundedTotal + unboundedTotal
	boundedFPY := 0.0
	if boundedTotal > 0 {
		boundedFPY = float64(boundedPass) / float64(boundedTotal)
	}
	unboundedFPY := 0.0
	if unboundedTotal > 0 {
		unboundedFPY = float64(unboundedPass) / float64(unboundedTotal)
	}

	h1Status := EpistemicHypothesis
	h1Summary := fmt.Sprintf("awaiting empirical samples (%d/%d recorded)", totalKSamples, MinSamplesForProof)
	if boundedTotal >= MinSamplesForProof && unboundedTotal >= MinSamplesForProof {
		if boundedFPY >= unboundedFPY {
			h1Status = EpistemicValidated
			h1Summary = fmt.Sprintf("validated: FPY(K<=%d)=%.1f%% >= FPY(K>%d)=%.1f%%", maxK, boundedFPY*100, maxK, unboundedFPY*100)
		} else {
			h1Status = EpistemicFalsified
			h1Summary = fmt.Sprintf("falsified: FPY(K<=%d)=%.1f%% < FPY(K>%d)=%.1f%%", maxK, boundedFPY*100, maxK, unboundedFPY*100)
		}
	}

	// 2. Evaluate H2: Sine-Wave Dormancy & Re-Awakening (Dormant Escape Rate < 5%)
	totalDormantTriggers := 0
	totalDormantEscapes := 0
	for _, r := range rules {
		totalDormantTriggers += r.DormantTriggers
		totalDormantEscapes += r.DormantEscapes
	}
	escapeRate := 0.0
	if totalDormantTriggers > 0 {
		escapeRate = float64(totalDormantEscapes) / float64(totalDormantTriggers)
	}
	h2Status := EpistemicHypothesis
	h2Summary := fmt.Sprintf("awaiting dormant trigger samples (%d/%d recorded)", totalDormantTriggers, MinSamplesForProof)
	if totalDormantTriggers >= MinSamplesForProof {
		if escapeRate < 0.05 {
			h2Status = EpistemicValidated
			h2Summary = fmt.Sprintf("validated: dormant escape rate %.1f%% < 5.0%% threshold", escapeRate*100)
		} else {
			h2Status = EpistemicFalsified
			h2Summary = fmt.Sprintf("falsified: dormant escape rate %.1f%% >= 5.0%% threshold", escapeRate*100)
		}
	}

	// 3. Evaluate H3: HITL-Anchored Statistical Regression (Mean F1 >= 0.70 across HITL-calibrated rules)
	hitlSamples := 0
	sumF1 := 0.0
	calibratedRules := 0
	for _, r := range rules {
		eventsForRule := r.HITLTP + r.HITLFP + r.HITLVeto
		hitlSamples += eventsForRule
		if eventsForRule > 0 {
			sumF1 += r.F1Score
			calibratedRules++
		}
	}
	meanF1 := 0.0
	if calibratedRules > 0 {
		meanF1 = sumF1 / float64(calibratedRules)
	}
	h3Status := EpistemicHypothesis
	h3Summary := fmt.Sprintf("awaiting HITL calibration events (%d/%d recorded)", hitlSamples, MinSamplesForProof)
	if hitlSamples >= MinSamplesForProof {
		if meanF1 >= 0.70 {
			h3Status = EpistemicValidated
			h3Summary = fmt.Sprintf("validated: HITL-calibrated mean F1 %.2f >= 0.70", meanF1)
		} else {
			h3Status = EpistemicFalsified
			h3Summary = fmt.Sprintf("falsified: HITL-calibrated mean F1 %.2f < 0.70", meanF1)
		}
	}

	return &HypothesisReport{
		GeneratedAt:          e.getClock().UTC(),
		MaxActiveRulesPerPkg: maxK,
		Hypotheses: []HypothesisEvaluation{
			{
				ID:              "H1",
				Statement:       fmt.Sprintf("Carrying capacity K_max <= %d active rules per package maximizes First-Pass Yield", maxK),
				EpistemicStatus: h1Status,
				MetricName:      "first_pass_yield_bounded",
				MetricValue:     boundedFPY,
				SampleCount:     totalKSamples,
				Summary:         h1Summary,
			},
			{
				ID:              "H2",
				Statement:       "Sine-Wave Cold dormancy (0 context tokens) with diff-guard re-awakening keeps Dormant Escape Rate < 5%",
				EpistemicStatus: h2Status,
				MetricName:      "dormant_escape_rate",
				MetricValue:     escapeRate,
				SampleCount:     totalDormantTriggers,
				Summary:         h2Summary,
			},
			{
				ID:              "H3",
				Statement:       "Sovereign HITL calibration (alpha_HITL >> alpha_auto) maintains rule precision F1 >= 0.70",
				EpistemicStatus: h3Status,
				MetricName:      "hitl_calibrated_mean_f1",
				MetricValue:     meanF1,
				SampleCount:     hitlSamples,
				Summary:         h3Summary,
			},
		},
		Rules: rules,
	}, nil
}
