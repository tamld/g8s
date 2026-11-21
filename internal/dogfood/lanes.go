// Lane-split honesty ledger and compliance-vs-quality metrics (issue #971 WP2).
//
// The ADR-0039 aggregate ledger counts receipt-backed vs F0-direct vs
// unclassified landings without lane attribution. WP2 splits the ledger by
// ALDC gate lane (0/D/R/F/S) so Lane D (prose) is auditable like every other
// lane — only its review depth differs per ALDC (lane-bundles: D runs
// G2/G3/doc-contract, F runs G1/G2/G4/G5/G6, S adds redaction-scan/sast-triad/
// review). It also adds the SANDBOX ENFORCEMENT (workspace_write) outcome
// metric and the provenance-cohort revert/fixup comparison labeled
// insufficient_sample when a cohort has fewer than MinSample (20) landings.

package dogfood

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/lane"
	"github.com/tamld/g8s/internal/sandbox"
)

// CommitRecord is one main landing classified for the lane-split ledger.
type CommitRecord struct {
	Subject     string     `json:"subject"`
	Paths       []string   `json:"paths,omitempty"`
	Provenance  Provenance `json:"provenance"`
	Lane        string     `json:"lane"`
	RevertFixup bool       `json:"revert_fixup"`
}

// NewCommitRecord classifies lane (via the deterministic ALDC router) and
// rework markers for one landing.
func NewCommitRecord(subject string, paths []string, prov Provenance) CommitRecord {
	return CommitRecord{
		Subject:     subject,
		Paths:       paths,
		Provenance:  prov,
		Lane:        ClassifyCommitLane(subject, paths),
		RevertFixup: IsRevertOrFixup(subject),
	}
}

// ClassifyCommitLane routes a landing to its ALDC gate lane via the
// deterministic router (trust boundaries -> S, docs-only -> D, refactor -> R,
// hotfix marker -> 0, default F).
func ClassifyCommitLane(summary string, paths []string) string {
	return lane.Route(lane.Input{Paths: paths, Summary: summary}).String()
}

// IsRevertOrFixup reports whether the commit subject marks rework: git revert
// ("Revert \"...\""), revert:/reverts, fixup!, squash!, or reapply markers.
func IsRevertOrFixup(subject string) bool {
	s := strings.ToLower(strings.TrimSpace(subject))
	for _, marker := range []string{"fixup!", "squash!", "reapply"} {
		if strings.HasPrefix(s, marker) {
			return true
		}
	}
	return strings.HasPrefix(s, "revert")
}

// LaneLedgerRow is one per-lane honesty-ledger row. Every lane is auditable
// (Auditable=true) — Lane D (prose) included; only ReviewDepth differs per ALDC.
type LaneLedgerRow struct {
	Lane         string `json:"lane"`
	LaneName     string `json:"lane_name"`
	ReviewDepth  string `json:"review_depth"`
	Auditable    bool   `json:"auditable"`
	Total        int    `json:"total"`
	Receipt      int    `json:"receipt_backed"`
	F0Direct     int    `json:"f0_direct"`
	Unclassified int    `json:"unclassified"`
	RevertFixup  int    `json:"revert_fixup_commits"`
}

// laneReviewDepth mirrors the ALDC review depth from .g8s/lane-bundles.yml:
// auditability is universal, gate depth is per-lane.
func laneReviewDepth(l lane.Lane) string {
	switch l {
	case lane.LaneHotfix:
		return "targeted-gates+retro-gate"
	case lane.LaneDocs:
		return "light (G2,G3,doc-contract)"
	case lane.LaneRefactor:
		return "mechanical (G2,G4,G5)"
	case lane.LaneFeature:
		return "full (G1,G2,G4,G5,G6)"
	case lane.LaneSecurity:
		return "p0 (G1..G6,doc-contract,redaction-scan,sast-triad,review)"
	}
	return "unknown"
}

// BuildLaneLedger aggregates commit records per canonical ALDC lane (0,D,R,F,S).
// Lane D is NOT exempt: it is auditable and counted; only its review depth
// differs (#971 WP2).
func BuildLaneLedger(records []CommitRecord) []LaneLedgerRow {
	order := []lane.Lane{lane.LaneHotfix, lane.LaneDocs, lane.LaneRefactor, lane.LaneFeature, lane.LaneSecurity}
	byLane := make(map[lane.Lane]*LaneLedgerRow, len(order))
	for _, l := range order {
		byLane[l] = &LaneLedgerRow{
			Lane:        l.String(),
			LaneName:    l.Name(),
			ReviewDepth: laneReviewDepth(l),
			Auditable:   true,
		}
	}
	for _, rec := range records {
		row, ok := byLane[lane.Lane(rec.Lane)]
		if !ok {
			continue
		}
		row.Total++
		switch rec.Provenance.Kind {
		case "receipt":
			row.Receipt++
		case "f0_direct":
			row.F0Direct++
		default:
			row.Unclassified++
		}
		if rec.RevertFixup {
			row.RevertFixup++
		}
	}
	rows := make([]LaneLedgerRow, 0, len(order))
	for _, l := range order {
		rows = append(rows, *byLane[l])
	}
	return rows
}

// Cohort labels for the provenance cohort quality comparison (#971 WP2).
const (
	CohortReceiptBacked = "receipt_backed"
	CohortF0Direct      = "f0_direct"
	CohortUnclassified  = "unclassified"
)

// RateLabel values for cohort and lane escape metrics.
const (
	RateLabelMeasured           = "measured"
	RateLabelInsufficientSample = "insufficient_sample"
)

// DefaultCohortMinSample mirrors the honesty-ledger min window: comparisons
// under 20 landings are labeled insufficient_sample (#971 WP2, current
// ledger state).
const DefaultCohortMinSample = 20

// CohortMetric compares revert/fixup rates across provenance cohorts.
type CohortMetric struct {
	Cohort             string  `json:"cohort"`
	Commits            int     `json:"commits"`
	RevertFixupCommits int     `json:"revert_fixup_commits"`
	RevertFixupRate    float64 `json:"revert_fixup_rate,omitempty"`
	RateLabel          string  `json:"rate_label"`
	MinSample          int     `json:"min_sample"`
}

// BuildCohortMetrics buckets landings into receipt-backed / F0-direct /
// unclassified cohorts and compares their revert/fixup rates. Cohorts with
// fewer than minSample landings carry RateLabel=insufficient_sample — the
// honest label for the current ledger state (#971 WP2).
func BuildCohortMetrics(records []CommitRecord, minSample int) []CohortMetric {
	if minSample <= 0 {
		minSample = DefaultCohortMinSample
	}
	counts := map[string]*CohortMetric{
		CohortReceiptBacked: {Cohort: CohortReceiptBacked, MinSample: minSample},
		CohortF0Direct:      {Cohort: CohortF0Direct, MinSample: minSample},
		CohortUnclassified:  {Cohort: CohortUnclassified, MinSample: minSample},
	}
	for _, rec := range records {
		key := cohortKeyFor(rec)
		m := counts[key]
		m.Commits++
		if rec.RevertFixup {
			m.RevertFixupCommits++
		}
	}
	out := make([]CohortMetric, 0, len(counts))
	for _, key := range []string{CohortReceiptBacked, CohortF0Direct, CohortUnclassified} {
		m := counts[key]
		if m.Commits >= minSample {
			m.RateLabel = RateLabelMeasured
			m.RevertFixupRate = float64(m.RevertFixupCommits) / float64(m.Commits)
		} else {
			m.RateLabel = RateLabelInsufficientSample
		}
		out = append(out, *m)
	}
	return out
}

func cohortKeyFor(rec CommitRecord) string {
	switch rec.Provenance.Kind {
	case "receipt":
		return CohortReceiptBacked
	case "f0_direct":
		return CohortF0Direct
	default:
		return CohortUnclassified
	}
}

// LaneEscapeRate is the per-lane revert/fixup (escape) rate with honest
// sample labeling (#971 WP2 escape_rate_by_lane).
type LaneEscapeRate struct {
	Lane        string  `json:"lane"`
	LaneName    string  `json:"lane_name"`
	Total       int     `json:"total"`
	RevertFixup int     `json:"revert_fixup_commits"`
	EscapeRate  float64 `json:"escape_rate,omitempty"`
	RateLabel   string  `json:"rate_label"`
	MinSample   int     `json:"min_sample"`
}

// BuildEscapeRateByLane computes the revert/fixup (escape) rate per ALDC lane,
// labeled insufficient_sample when the lane has fewer than minSample landings.
func BuildEscapeRateByLane(records []CommitRecord, minSample int) []LaneEscapeRate {
	if minSample <= 0 {
		minSample = DefaultCohortMinSample
	}
	rows := BuildLaneLedger(records)
	out := make([]LaneEscapeRate, 0, len(rows))
	for _, row := range rows {
		r := LaneEscapeRate{
			Lane:        row.Lane,
			LaneName:    row.LaneName,
			Total:       row.Total,
			RevertFixup: row.RevertFixup,
			MinSample:   minSample,
		}
		if row.Total >= minSample {
			r.RateLabel = RateLabelMeasured
			r.EscapeRate = float64(row.RevertFixup) / float64(row.Total)
		} else {
			r.RateLabel = RateLabelInsufficientSample
		}
		out = append(out, r)
	}
	return out
}

// SandboxEnforcementOutcome buckets one workspace_write attempt envelope by
// its sandbox enforcement outcome (#971 WP2 SANDBOX ENFORCEMENT metric).
type SandboxEnforcementOutcome string

const (
	SandboxOutcomeKernelEnforced  SandboxEnforcementOutcome = "kernel_enforced"
	SandboxOutcomeDegradedAllowed SandboxEnforcementOutcome = "degraded_allowed"
	SandboxOutcomeRefused         SandboxEnforcementOutcome = "refused"
	SandboxOutcomeUnclassified    SandboxEnforcementOutcome = "unclassified"
)

// sandboxOutcomeAll is the stable emission order for summaries.
var sandboxOutcomeAll = []SandboxEnforcementOutcome{
	SandboxOutcomeKernelEnforced,
	SandboxOutcomeDegradedAllowed,
	SandboxOutcomeRefused,
	SandboxOutcomeUnclassified,
}

// ClassifySandboxOutcome reads one attempt result envelope (raw JSON) and the
// task's requested permission, bucketing the sandbox enforcement outcome.
// Non-workspace_write attempts return SandboxOutcomeUnclassified; callers feed
// the SANDBOX ENFORCEMENT (workspace_write) metric with workspace_write tasks
// only.
func ClassifySandboxOutcome(permission, resultJSON string) SandboxEnforcementOutcome {
	if !strings.EqualFold(strings.TrimSpace(permission), "workspace_write") {
		return SandboxOutcomeUnclassified
	}
	if strings.TrimSpace(resultJSON) == "" {
		return SandboxOutcomeUnclassified
	}
	var env struct {
		Status              string `json:"status"`
		RejectedBeforeSpawn bool   `json:"rejected_before_spawn"`
		ContainmentMode     string `json:"containment_mode"`
		SandboxDegraded     bool   `json:"sandbox_degraded"`
		PolicyEnforcement   *struct {
			Effective struct {
				ContainmentMode string `json:"containment_mode"`
			} `json:"effective_enforcement"`
			Evaluated struct {
				RejectedBeforeSpawn bool `json:"rejected_before_spawn"`
			} `json:"evaluated_policy"`
		} `json:"policy_enforcement"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &env); err != nil {
		return SandboxOutcomeUnclassified
	}

	rejected := env.RejectedBeforeSpawn || strings.EqualFold(strings.TrimSpace(env.Status), "sandbox_policy_rejected")
	if env.PolicyEnforcement != nil {
		rejected = rejected || env.PolicyEnforcement.Evaluated.RejectedBeforeSpawn
	}
	if rejected {
		return SandboxOutcomeRefused
	}

	mode := strings.TrimSpace(env.ContainmentMode)
	if mode == "" && env.PolicyEnforcement != nil {
		mode = strings.TrimSpace(env.PolicyEnforcement.Effective.ContainmentMode)
	}
	if mode == string(sandbox.ModeKernelEnforced) {
		return SandboxOutcomeKernelEnforced
	}
	if env.SandboxDegraded {
		return SandboxOutcomeDegradedAllowed
	}
	return SandboxOutcomeUnclassified
}

// SandboxEnforcementSummary counts workspace_write attempts by enforcement
// outcome (kernel_enforced / degraded_allowed / refused), #971 WP2.
type SandboxEnforcementSummary struct {
	WorkspaceWriteAttempts int `json:"workspace_write_attempts"`
	KernelEnforced         int `json:"kernel_enforced"`
	DegradedAllowed        int `json:"degraded_allowed"`
	Refused                int `json:"refused"`
	Unclassified           int `json:"unclassified"`
}

// SummarizeSandboxEnforcement folds per-attempt outcomes into the summary.
func SummarizeSandboxEnforcement(outcomes []SandboxEnforcementOutcome) SandboxEnforcementSummary {
	var s SandboxEnforcementSummary
	counts := map[SandboxEnforcementOutcome]int{}
	for _, o := range outcomes {
		counts[o]++
	}
	s.WorkspaceWriteAttempts = counts[SandboxOutcomeKernelEnforced] + counts[SandboxOutcomeDegradedAllowed] + counts[SandboxOutcomeRefused] + counts[SandboxOutcomeUnclassified]
	s.KernelEnforced = counts[SandboxOutcomeKernelEnforced]
	s.DegradedAllowed = counts[SandboxOutcomeDegradedAllowed]
	s.Refused = counts[SandboxOutcomeRefused]
	s.Unclassified = counts[SandboxOutcomeUnclassified]
	return s
}

// ParseCommitRecords parses the NUL-framed git log stream shared by the
// provenance tooling and the scorecard lane analysis:
//
//	records separated by \x00; per record: subject \x01 body \x04 \n file list
//
// produced by `git log --name-only --format=%x00%s%x01%B%x04 <range>`.
func ParseCommitRecords(out string) []CommitRecord {
	var records []CommitRecord
	for _, chunk := range strings.Split(out, "\x00") {
		subject, rest, ok := strings.Cut(chunk, "\x01")
		if !ok {
			continue
		}
		body, fileBlock, found := strings.Cut(rest, "\x04")
		if !found {
			// No file-list terminator: treat everything as body (no paths).
			body = rest
			fileBlock = ""
		}
		subject = strings.TrimSpace(subject)
		if subject == "" && strings.TrimSpace(body) == "" {
			continue
		}
		var paths []string
		for _, line := range strings.Split(fileBlock, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				paths = append(paths, line)
			}
		}
		records = append(records, NewCommitRecord(subject, paths, ClassifyCommit(body)))
	}
	return records
}

// FormatLaneLedger renders the lane-split honesty ledger as a markdown table
// with the explicit Lane D auditability note (#971 WP2).
func FormatLaneLedger(rows []LaneLedgerRow) string {
	var b strings.Builder
	b.WriteString("### Lane-split provenance (non-exempt)\n\n")
	b.WriteString("Lane D (prose) is auditable and counted like every lane — only its ALDC review depth differs.\n\n")
	b.WriteString("| Lane | Name | Review depth | Auditable | Receipt | F0-direct | Unclassified | Revert/fixup | Total |\n")
	b.WriteString("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range rows {
		auditable := "yes"
		if !row.Auditable {
			auditable = "no"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %d | %d | %d |\n",
			row.Lane, row.LaneName, row.ReviewDepth, auditable,
			row.Receipt, row.F0Direct, row.Unclassified, row.RevertFixup, row.Total)
	}
	b.WriteString("\n")
	return b.String()
}
