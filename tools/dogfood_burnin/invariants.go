// Burn-in invariant engine for the #971 WP2 compliance_vs_quality metrics
// (issue #971 WP3 — Verification & Burn-In slice).
//
// WP2 merged the lane-split honesty ledger, the provenance cohort comparison,
// the per-lane escape rates, and the sandbox enforcement metric into
// `g8s scorecard` axes.compliance_vs_quality. WP3 burns those metrics in:
// a bounded, repeatable harness asserts the metrics' structural invariants
// hold over real recorded data and emits a machine-readable report.
//
// Invariants (stable IDs, machine-gateable):
//
//   - I_FINITE             every metric number is finite (no NaN/±Inf escapes)
//   - I_STRUCTURE          section shape: metric arrays/objects present with typed fields
//   - I_MIN_SAMPLE_DECLARED min_sample declared and equal to the merged WP2
//                          semantic (dogfood.DefaultCohortMinSample = 20)
//   - I_LANE_SET           lane_ledger and escape_rate_by_lane carry the canonical
//                          5 ALDC lanes (0/D/R/F/S) in canonical order
//   - I_COHORT_SET         cohort_metrics carries the 3 canonical provenance cohorts
//   - I_RATE_LABEL         every rate_label ∈ {measured, insufficient_sample}
//   - I_MEASURED_DENOM     measured ⇔ n ≥ min_sample (n > 0); insufficient_sample ⇔
//                          n < min_sample with no rate emitted; escape-rate
//                          denominators non-zero
//   - I_RATE_BOUNDS        emitted rates within [0,1]
//   - I_CONSERVATION       counters conserve: lane buckets sum to the lane total,
//                          cohort commits and lane totals describe the same
//                          landing set, escape rows mirror the lane ledger
//   - I_SANDBOX_SUM        sandbox buckets sum to workspace_write_attempts
//   - I_DEGRADE_EXPLICIT   degradation is explicit: git_log_error ⇔ empty metric
//                          arrays; the sandbox metric is present or carries an
//                          explicit error — never silently absent
//   - I_WINDOW_FROZEN      (cross-run) same HEAD ⇒ git-window counters identical
//   - I_COUNTER_MONOTONE   (cross-run) sandbox counters non-decreasing (append-only DB)
//   - I_COHORT_MIN         (opt-in) workspace_write attempts ≥ --min-cohort
//                          (issue #971 WP3: live isolated F1 burn-in cohort n ≥ 10)
//
// The engine is pure: it consumes the decoded compliance_vs_quality section
// (map[string]any as produced by encoding/json) and returns machine-checkable
// violations. It never invents thresholds — min_sample is read from the
// section and cross-checked against dogfood.DefaultCohortMinSample, and the
// insufficient_sample semantics of the merged WP2 code are respected as-is
// (an insufficient_sample cohort is an honest label, not a failure).

package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/tamld/g8s/internal/dogfood"
	"github.com/tamld/g8s/internal/lane"
)

// Stable invariant IDs (machine-gateable in the burn-in report).
const (
	invFinite          = "I_FINITE"
	invStructure       = "I_STRUCTURE"
	invMinSample       = "I_MIN_SAMPLE_DECLARED"
	invLaneSet         = "I_LANE_SET"
	invCohortSet       = "I_COHORT_SET"
	invRateLabel       = "I_RATE_LABEL"
	invMeasuredDenom   = "I_MEASURED_DENOM"
	invRateBounds      = "I_RATE_BOUNDS"
	invConservation    = "I_CONSERVATION"
	invSandboxSum      = "I_SANDBOX_SUM"
	invDegradeExplicit = "I_DEGRADE_EXPLICIT"
	invWindowFrozen    = "I_WINDOW_FROZEN"
	invCounterMonotone = "I_COUNTER_MONOTONE"
	invCohortMin       = "I_COHORT_MIN"
)

// Violation is one machine-checkable invariant breach observed in a
// compliance_vs_quality section (or across two consecutive runs).
type Violation struct {
	Invariant string `json:"invariant"`
	Path      string `json:"path"`
	Detail    string `json:"detail"`
}

func viol(inv, path, format string, args ...any) Violation {
	return Violation{Invariant: inv, Path: path, Detail: fmt.Sprintf(format, args...)}
}

// canonicalLanes mirrors internal/dogfood.BuildLaneLedger's canonical lane
// order (hotfix, docs, refactor, feature, security = 0/D/R/F/S).
var canonicalLanes = []string{
	lane.LaneHotfix.String(),
	lane.LaneDocs.String(),
	lane.LaneRefactor.String(),
	lane.LaneFeature.String(),
	lane.LaneSecurity.String(),
}

// canonicalCohorts mirrors internal/dogfood.BuildCohortMetrics' canonical
// cohort order.
var canonicalCohorts = []string{
	dogfood.CohortReceiptBacked,
	dogfood.CohortF0Direct,
	dogfood.CohortUnclassified,
}

// rateLabels mirrors the merged WP2 rate labels; the harness never invents a
// third label nor re-interprets insufficient_sample.
var rateLabels = []string{dogfood.RateLabelMeasured, dogfood.RateLabelInsufficientSample}

// checkSection runs every per-run invariant over one decoded
// compliance_vs_quality section and returns all violations (empty = clean).
func checkSection(sec map[string]any) []Violation {
	var vs []Violation
	vs = append(vs, checkFinite(sec, "$")...)
	vs = append(vs, checkMinSampleDeclared(sec)...)

	laneRows, laneRowsOK := rowList(sec, "lane_ledger")
	cohortRows, cohortRowsOK := rowList(sec, "cohort_metrics")
	escapeRows, escapeRowsOK := rowList(sec, "escape_rate_by_lane")
	if !laneRowsOK || !cohortRowsOK || !escapeRowsOK {
		vs = append(vs, viol(invStructure, "$",
			"lane_ledger, cohort_metrics and escape_rate_by_lane must be arrays of objects"))
		vs = append(vs, checkSandbox(sec)...)
		return vs
	}

	gitErr := nonEmptyString(sec, "git_log_error")
	if gitErr {
		// Explicit degrade contract (#971 WP2): with a git-log failure the
		// three git-derived metric arrays must be explicitly empty.
		if len(laneRows)+len(cohortRows)+len(escapeRows) > 0 {
			vs = append(vs, viol(invDegradeExplicit, "$",
				"git_log_error present but metric arrays are non-empty (%d rows)", len(laneRows)+len(cohortRows)+len(escapeRows)))
		}
		vs = append(vs, checkSandbox(sec)...)
		return vs
	}
	if len(laneRows) == 0 && len(cohortRows) == 0 && len(escapeRows) == 0 {
		vs = append(vs, viol(invDegradeExplicit, "$",
			"no git_log_error but all git-derived metric arrays are empty"))
		vs = append(vs, checkSandbox(sec)...)
		return vs
	}

	vs = append(vs, checkLaneSet("lane_ledger", laneRows)...)
	vs = append(vs, checkLaneSet("escape_rate_by_lane", escapeRows)...)
	vs = append(vs, checkCohortSet(cohortRows)...)

	minSample := declaredMinSample(sec)
	laneState := checkLaneLedgerRows(laneRows, &vs)
	escapeState := checkEscapeRows(escapeRows, minSample, &vs)
	vs = append(vs, checkCohortRows(cohortRows, minSample)...)
	vs = append(vs, checkEscapeMirrorsLedger(escapeState, laneState)...)
	vs = append(vs, checkLandingSetConservation(laneState, cohortRows)...)
	vs = append(vs, checkSandbox(sec)...)
	return vs
}

// checkFinite walks the whole section and flags any non-finite number.
// encoding/json cannot produce NaN/±Inf from strict input, but the engine
// also consumes in-memory sections and lenient parsers upstream — the check
// is defense in depth at the exact boundary the burn-in report gates on.
func checkFinite(node any, path string) []Violation {
	switch v := node.(type) {
	case map[string]any:
		var vs []Violation
		for k, child := range v {
			vs = append(vs, checkFinite(child, path+"."+k)...)
		}
		return vs
	case []any:
		var vs []Violation
		for i, child := range v {
			vs = append(vs, checkFinite(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return vs
	case float64:
		if math.IsNaN(v) {
			return []Violation{viol(invFinite, path, "NaN escaped into the metrics")}
		}
		if math.IsInf(v, 0) {
			return []Violation{viol(invFinite, path, "±Inf escaped into the metrics")}
		}
	}
	return nil
}

// checkMinSampleDeclared pins the threshold semantics: the section must
// declare min_sample and it must equal the merged WP2 semantic
// (dogfood.DefaultCohortMinSample). The harness never invents thresholds.
func checkMinSampleDeclared(sec map[string]any) []Violation {
	v, ok := sec["min_sample"]
	if !ok {
		return []Violation{viol(invMinSample, "$.min_sample", "min_sample not declared")}
	}
	n, ok := asCounter(v)
	if !ok {
		return []Violation{viol(invMinSample, "$.min_sample", "min_sample must be a non-negative integer, got %T", v)}
	}
	if n != int64(dogfood.DefaultCohortMinSample) {
		return []Violation{viol(invMinSample, "$.min_sample",
			"min_sample drifted from the merged WP2 semantic: got %d, want %d", n, dogfood.DefaultCohortMinSample)}
	}
	return nil
}

func declaredMinSample(sec map[string]any) int {
	if n, ok := asCounter(sec["min_sample"]); ok && n > 0 {
		return int(n)
	}
	return dogfood.DefaultCohortMinSample
}

// rowList extracts an array-of-objects field; ok=false means wrong shape.
func rowList(sec map[string]any, key string) ([]map[string]any, bool) {
	raw, ok := sec[key].([]any)
	if !ok {
		return nil, false
	}
	rows := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		rows = append(rows, row)
	}
	return rows, true
}

func nonEmptyString(sec map[string]any, key string) bool {
	s, ok := sec[key].(string)
	return ok && strings.TrimSpace(s) != ""
}

// asCounter coerces a decoded number into a non-negative integral counter.
func asCounter(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n != math.Trunc(n) {
			return 0, false
		}
		return int64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return int64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// rowCounter reads one integral counter field, appending I_STRUCTURE (missing
// field) and I_CONSERVATION (negative / non-integral) violations.
func rowCounter(row map[string]any, field, path string, vs *[]Violation) (int64, bool) {
	v, ok := row[field]
	if !ok {
		*vs = append(*vs, viol(invStructure, path+"."+field, "required counter field missing"))
		return 0, false
	}
	n, ok := asCounter(v)
	if !ok {
		*vs = append(*vs, viol(invConservation, path+"."+field,
			"counter must be a non-negative integer, got %v", v))
		return 0, false
	}
	return n, true
}

func rowLane(row map[string]any) string {
	s, _ := row["lane"].(string)
	return s
}

func rowCohort(row map[string]any) string {
	s, _ := row["cohort"].(string)
	return s
}

func rowRateLabel(row map[string]any) string {
	s, _ := row["rate_label"].(string)
	return s
}

// checkLaneSet asserts the rows carry exactly the canonical lane set in
// canonical order (mirrors BuildLaneLedger/BuildEscapeRateByLane output).
func checkLaneSet(field string, rows []map[string]any) []Violation {
	if len(rows) != len(canonicalLanes) {
		return []Violation{viol(invLaneSet, field,
			"expected %d canonical lanes 0/D/R/F/S, got %d", len(canonicalLanes), len(rows))}
	}
	var out []Violation
	for i, want := range canonicalLanes {
		if got := rowLane(rows[i]); got != want {
			out = append(out, viol(invLaneSet, fmt.Sprintf("%s[%d]", field, i),
				"expected canonical lane %q at position %d, got %q", want, i, got))
		}
	}
	return out
}

// checkCohortSet asserts the rows carry exactly the canonical cohort set.
func checkCohortSet(rows []map[string]any) []Violation {
	if len(rows) != len(canonicalCohorts) {
		return []Violation{viol(invCohortSet, "cohort_metrics",
			"expected %d canonical cohorts, got %d", len(canonicalCohorts), len(rows))}
	}
	var out []Violation
	for i, want := range canonicalCohorts {
		if got := rowCohort(rows[i]); got != want {
			out = append(out, viol(invCohortSet, fmt.Sprintf("cohort_metrics[%d]", i),
				"expected cohort %q at position %d, got %q", want, i, got))
		}
	}
	return out
}

// ledgerRowState is the extracted counter state of one ledger/escape row.
type ledgerRowState struct {
	Key         string
	Total       int64
	RevertFixup int64
}

// checkLaneLedgerRows validates the lane-split honesty ledger rows (#971 WP2
// LaneLedgerRow shape: no rate label — provenance buckets and counters only).
// Violations are appended through vs; the extracted counter state is returned
// for cross-table conservation checks.
func checkLaneLedgerRows(rows []map[string]any, vs *[]Violation) []ledgerRowState {
	state := make([]ledgerRowState, 0, len(rows))
	for i, row := range rows {
		path := fmt.Sprintf("lane_ledger[%d]", i)
		key := rowLane(row)
		if key == "" {
			*vs = append(*vs, viol(invStructure, path+".lane", "lane field missing"))
			key = fmt.Sprintf("row-%d", i)
		}
		total, okT := rowCounter(row, "total", path, vs)
		revert, okR := rowCounter(row, "revert_fixup_commits", path, vs)
		receipt, okRec := rowCounter(row, "receipt_backed", path, vs)
		f0, okF := rowCounter(row, "f0_direct", path, vs)
		unclassified, okU := rowCounter(row, "unclassified", path, vs)
		if !okT || !okR || !okRec || !okF || !okU {
			state = append(state, ledgerRowState{Key: key})
			continue
		}
		if receipt+f0+unclassified != total {
			*vs = append(*vs, viol(invConservation, path,
				"provenance buckets sum to %d but total=%d", receipt+f0+unclassified, total))
		}
		if revert > total {
			*vs = append(*vs, viol(invConservation, path+".revert_fixup_commits",
				"revert/fixup count %d exceeds lane total %d", revert, total))
		}
		state = append(state, ledgerRowState{Key: key, Total: total, RevertFixup: revert})
	}
	return state
}

// checkEscapeRows validates the escape_rate_by_lane rows: counters, the
// merged WP2 rate label set, and the measured/insufficient_sample denominator
// semantics with rate bounds.
func checkEscapeRows(rows []map[string]any, minSample int, vs *[]Violation) []ledgerRowState {
	state := make([]ledgerRowState, 0, len(rows))
	for i, row := range rows {
		path := fmt.Sprintf("escape_rate_by_lane[%d]", i)
		key := rowLane(row)
		if key == "" {
			*vs = append(*vs, viol(invStructure, path+".lane", "lane field missing"))
			key = fmt.Sprintf("row-%d", i)
		}
		total, okT := rowCounter(row, "total", path, vs)
		revert, okR := rowCounter(row, "revert_fixup_commits", path, vs)
		if !okT || !okR {
			state = append(state, ledgerRowState{Key: key})
			continue
		}
		label := rowRateLabel(row)
		if !knownRateLabel(label) {
			*vs = append(*vs, viol(invRateLabel, path+".rate_label",
				"rate_label %q not in the merged WP2 label set %v", label, rateLabels))
		}
		checkRateSemantics(row, path, label, total, minSample, "escape_rate", vs)
		state = append(state, ledgerRowState{Key: key, Total: total, RevertFixup: revert})
	}
	return state
}

// checkCohortRows validates cohort rows: canonical set already checked; here
// the counters, labels, denominator semantics, and rate bounds.
func checkCohortRows(rows []map[string]any, minSample int) []Violation {
	var vs []Violation
	for i, row := range rows {
		path := fmt.Sprintf("cohort_metrics[%d]", i)
		commits, okC := rowCounter(row, "commits", path, &vs)
		revert, _ := rowCounter(row, "revert_fixup_commits", path, &vs)
		if !okC {
			continue
		}
		if revert > commits {
			vs = append(vs, viol(invConservation, path+".revert_fixup_commits",
				"revert/fixup count %d exceeds cohort commits %d", revert, commits))
		}
		label := rowRateLabel(row)
		if !knownRateLabel(label) {
			vs = append(vs, viol(invRateLabel, path+".rate_label",
				"rate_label %q not in the merged WP2 label set %v", label, rateLabels))
		}
		checkRateSemantics(row, path, label, commits, minSample, "revert_fixup_rate", &vs)
		if ms, ok := rowCounter(row, "min_sample", path, &vs); ok && ms != int64(minSample) {
			vs = append(vs, viol(invMinSample, path+".min_sample",
				"row min_sample %d drifted from the declared section min_sample %d", ms, minSample))
		}
	}
	return vs
}

// checkRateSemantics enforces the merged WP2 sample semantics without
// inventing thresholds:
//
//   - measured ⇔ n ≥ min_sample and n > 0 (escape-rate denominator non-zero);
//   - insufficient_sample ⇔ n < min_sample and no rate emitted;
//   - any emitted rate lies within [0,1].
func checkRateSemantics(row map[string]any, path, label string, n int64, minSample int, rateField string, vs *[]Violation) {
	rate, hasRate := row[rateField]
	switch label {
	case dogfood.RateLabelMeasured:
		if n < int64(minSample) {
			*vs = append(*vs, viol(invMeasuredDenom, path,
				"labeled measured but n=%d < min_sample=%d", n, minSample))
		}
		if n <= 0 {
			*vs = append(*vs, viol(invMeasuredDenom, path,
				"labeled measured but denominator is zero (n=%d)", n))
		}
		if hasRate {
			r, ok := rate.(float64)
			if !ok {
				*vs = append(*vs, viol(invStructure, path+"."+rateField, "rate must be a number"))
			} else if r < 0 || r > 1 {
				*vs = append(*vs, viol(invRateBounds, path+"."+rateField,
					"rate %v outside [0,1]", r))
			}
		}
	case dogfood.RateLabelInsufficientSample:
		if n >= int64(minSample) {
			*vs = append(*vs, viol(invMeasuredDenom, path,
				"labeled insufficient_sample but n=%d ≥ min_sample=%d", n, minSample))
		}
		if hasRate {
			*vs = append(*vs, viol(invMeasuredDenom, path+"."+rateField,
				"insufficient_sample cohort must not emit a rate (got %v)", rate))
		}
	}
}

func knownRateLabel(label string) bool {
	for _, want := range rateLabels {
		if label == want {
			return true
		}
	}
	return false
}

// checkEscapeMirrorsLedger asserts every escape row mirrors its lane-ledger
// counterpart (same landing set).
func checkEscapeMirrorsLedger(escape, ledger []ledgerRowState) []Violation {
	var vs []Violation
	byLane := make(map[string]ledgerRowState, len(ledger))
	for _, row := range ledger {
		byLane[row.Key] = row
	}
	for _, e := range escape {
		l, ok := byLane[e.Key]
		if !ok {
			vs = append(vs, viol(invConservation, "escape_rate_by_lane",
				"escape row %q has no lane_ledger counterpart", e.Key))
			continue
		}
		if e.Total != l.Total || e.RevertFixup != l.RevertFixup {
			vs = append(vs, viol(invConservation, "escape_rate_by_lane."+e.Key,
				"escape counters (total=%d revert=%d) diverge from lane_ledger (total=%d revert=%d)",
				e.Total, e.RevertFixup, l.Total, l.RevertFixup))
		}
	}
	return vs
}

// checkLandingSetConservation asserts the lane ledger and the cohort metrics
// describe the same landing set: Σ lane totals == Σ cohort commits and
// Σ lane revert/fixup == Σ cohort revert/fixup.
func checkLandingSetConservation(ledger []ledgerRowState, cohortRows []map[string]any) []Violation {
	var vs []Violation
	var laneTotal, laneRevert int64
	for _, row := range ledger {
		laneTotal += row.Total
		laneRevert += row.RevertFixup
	}
	var cohortTotal, cohortRevert int64
	for i, row := range cohortRows {
		commits, okC := rowCounter(row, "commits", fmt.Sprintf("cohort_metrics[%d]", i), &vs)
		revert, okR := rowCounter(row, "revert_fixup_commits", fmt.Sprintf("cohort_metrics[%d]", i), &vs)
		if !okC || !okR {
			continue
		}
		cohortTotal += commits
		cohortRevert += revert
	}
	if laneTotal != cohortTotal {
		vs = append(vs, viol(invConservation, "$",
			"landing set mismatch: Σ lane totals=%d but Σ cohort commits=%d", laneTotal, cohortTotal))
	}
	if laneRevert != cohortRevert {
		vs = append(vs, viol(invConservation, "$",
			"rework set mismatch: Σ lane revert/fixup=%d but Σ cohort revert/fixup=%d", laneRevert, cohortRevert))
	}
	return vs
}

// checkSandbox validates the sandbox enforcement metric: present with
// conserving buckets, or explicitly errored — never silently absent.
func checkSandbox(sec map[string]any) []Violation {
	summary, hasSummary := sec["sandbox_enforcement"].(map[string]any)
	_, hasError := sec["sandbox_enforcement_error"]
	if hasSummary && hasError {
		return []Violation{viol(invStructure, "$.sandbox_enforcement",
			"sandbox_enforcement and sandbox_enforcement_error are mutually exclusive")}
	}
	if !hasSummary {
		if !hasError {
			return []Violation{viol(invStructure, "$.sandbox_enforcement",
				"sandbox metric silently absent: neither sandbox_enforcement nor sandbox_enforcement_error present")}
		}
		return nil
	}
	var vs []Violation
	path := "$.sandbox_enforcement"
	attempts, okA := rowCounter(summary, "workspace_write_attempts", path, &vs)
	var sum int64
	for _, field := range []string{"kernel_enforced", "degraded_allowed", "refused", "unclassified"} {
		n, ok := rowCounter(summary, field, path, &vs)
		if !ok {
			return vs
		}
		sum += n
	}
	if !okA {
		return vs
	}
	if sum != attempts {
		vs = append(vs, viol(invSandboxSum, path,
			"outcome buckets sum to %d but workspace_write_attempts=%d", sum, attempts))
	}
	return vs
}
