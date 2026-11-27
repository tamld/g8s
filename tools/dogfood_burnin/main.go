// Command dogfood_burnin is the #971 WP3 verification & burn-in harness for
// the WP2 compliance metrics (lane-split honesty ledger, provenance cohort
// comparison, per-lane escape rates, sandbox enforcement).
//
// WP3 (issue #971): "Zero new top-level CLI commands; strict DEBT-34 layer
// split across PRs; strict Two-Layer Hygiene; 100% CI/Quality/3-OS green;
// plus a live isolated F1 burn-in cohort (n >= 10)."
//
// This tool adds no CLI surface to cmd/g8s: it is a tools/ consumer of the
// merged `g8s scorecard status --json` telemetry, exactly like
// tools/dogfood_provenance consumes the internal/dogfood W-layer. It runs a
// bounded, repeatable burn-in loop:
//
//   - live mode (default): invokes `g8s scorecard status --json` over the
//     real recorded data (git provenance window + tasks DB), N times;
//   - fixture mode (--fixture): runs the same invariant engine over a
//     recorded scorecard JSON for deterministic runs and CI.
//
// For each run it applies the invariant engine (invariants.go), records the
// run fingerprint, and enforces cross-run invariants (frozen git window,
// append-only sandbox counters). The falsifiable pass criterion is
// N consecutive invariant-clean runs; any violation fails the burn-in, and
// missing/insufficient data degrades the verdict explicitly (never silently).
// The merged WP2 insufficient_sample semantics are respected as-is: a cohort
// labeled insufficient_sample is an honest label, not a failure — the
// harness never invents thresholds (min_sample is read from the section and
// pinned to dogfood.DefaultCohortMinSample).
//
// Output: the JSON burn-in report on stdout (CI-gateable, e.g.
// `jq -e 'select(.verdict=="pass")'`), a human-readable tail on stderr, and
// optionally the same JSON persisted via --out.
//
// Exit codes: 0 = pass, 1 = fail (invariant violation), 2 = degraded
// (data missing/unusable — the report carries the explicit reason).
//
// Read-only note: live mode shells out to g8s scorecard, which may create
// sibling state files next to the tasks DB. For strict read-only probing of
// production data, pass --db pointing at a snapshot copy of g8s.db.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/dogfood"
)

const (
	reportKind       = "dogfood_burnin_report"
	issueRef         = "#971 WP3"
	defaultRuns      = 3
	maxRuns          = 50
	scorecardTimeout = 120 * time.Second
)

// Verdicts and data-quality labels (explicit, never silent).
const (
	verdictPass     = "pass"
	verdictFail     = "fail"
	verdictDegraded = "degraded"

	qualityNone    = "none"    // scorecard output could not be collected/parsed
	qualityEmpty   = "empty"   // collected but zero recorded landings — nothing to burn in
	qualityPartial = "partial" // at least one probe explicitly degraded (*_error present)
	qualityFull    = "full"    // git window + sandbox metric both observed
)

type config struct {
	Runs      int
	Fixture   string
	G8sBin    string
	RepoDir   string
	DBPath    string
	OutPath   string
	MinCohort int
}

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	cfg, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dogfood_burnin:", err)
		return exitCodeFor(verdictDegraded)
	}
	report := execute(cfg)
	if cfg.OutPath != "" {
		if writeErr := writeReportFile(cfg.OutPath, report); writeErr != nil {
			fmt.Fprintln(os.Stderr, "dogfood_burnin: write report:", writeErr)
			return exitCodeFor(verdictDegraded)
		}
	}
	if emitErr := emitReport(os.Stdout, report); emitErr != nil {
		fmt.Fprintln(os.Stderr, "dogfood_burnin: emit report:", emitErr)
		return exitCodeFor(verdictDegraded)
	}
	fmt.Fprint(os.Stderr, report.HumanTail)
	return exitCodeFor(report.Verdict)
}

func exitCodeFor(verdict string) int {
	switch verdict {
	case verdictPass:
		return 0
	case verdictFail:
		return 1
	default:
		return 2
	}
}

func parseFlags(args []string) (config, error) {
	cfg := config{}
	fs := flag.NewFlagSet("dogfood_burnin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.IntVar(&cfg.Runs, "runs", defaultRuns,
		fmt.Sprintf("required consecutive invariant-clean runs (default %d, max %d)", defaultRuns, maxRuns))
	fs.StringVar(&cfg.Fixture, "fixture", "",
		"run the invariant engine over this recorded scorecard JSON instead of live collection")
	fs.StringVar(&cfg.G8sBin, "g8s-bin", "",
		"path to the g8s binary (default: $G8S_BIN, then <repo>/bin/g8s)")
	fs.StringVar(&cfg.RepoDir, "repo", ".",
		"repository whose provenance window the scorecard inspects (default: cwd)")
	fs.StringVar(&cfg.DBPath, "db", "",
		"tasks DB passed to the scorecard as G8S_DB (use a snapshot copy for strict read-only probing)")
	fs.StringVar(&cfg.OutPath, "out", "", "also write the JSON report to this file")
	fs.IntVar(&cfg.MinCohort, "min-cohort", 0,
		"opt-in: require workspace_write_attempts >= N (issue #971 WP3 live F1 cohort: --min-cohort 10)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.Runs < 1 {
		cfg.Runs = 1
	}
	if cfg.Runs > maxRuns {
		cfg.Runs = maxRuns
	}
	if cfg.MinCohort < 0 {
		cfg.MinCohort = 0
	}
	abs, err := filepath.Abs(cfg.RepoDir)
	if err != nil {
		return cfg, fmt.Errorf("resolve repo dir: %w", err)
	}
	cfg.RepoDir = abs
	return cfg, nil
}

// burninReport is the machine-readable CI-gateable artifact.
type burninReport struct {
	Kind               string          `json:"kind"`
	Issue              string          `json:"issue"`
	Mode               string          `json:"mode"`
	Verdict            string          `json:"verdict"`
	GeneratedAt        string          `json:"generated_at"`
	PassCriterion      passCriterion   `json:"pass_criterion"`
	Invariants         []invariantStat `json:"invariants"`
	CrossRunViolations []Violation     `json:"cross_run_violations,omitempty"`
	Runs               []runResult     `json:"runs"`
	Notes              []string        `json:"notes,omitempty"`
	HumanTail          string          `json:"human_tail"`
}

type passCriterion struct {
	RequiredConsecutiveClean int  `json:"required_consecutive_clean"`
	ObservedConsecutiveClean int  `json:"observed_consecutive_clean"`
	Met                      bool `json:"met"`
}

type invariantStat struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	Scope           string `json:"scope"`
	ViolationsTotal int    `json:"violations_total"`
}

type runResult struct {
	Run                int             `json:"run"`
	Collected          bool            `json:"collected"`
	Head               string          `json:"head,omitempty"`
	DataQuality        string          `json:"data_quality,omitempty"`
	Clean              bool            `json:"clean"`
	CollectionError    string          `json:"collection_error,omitempty"`
	GitLogError        string          `json:"git_log_error,omitempty"`
	SandboxError       string          `json:"sandbox_enforcement_error,omitempty"`
	InsufficientSample []string        `json:"insufficient_sample_labels,omitempty"`
	Violations         []Violation     `json:"violations,omitempty"`
	Summary            *sectionSummary `json:"summary,omitempty"`
}

type sectionSummary struct {
	Landings      int               `json:"landings"`
	MinSample     int64             `json:"min_sample"`
	LaneTotals    map[string]int64  `json:"lane_totals"`
	CohortCommits map[string]int64  `json:"cohort_commits"`
	CohortLabels  map[string]string `json:"cohort_labels"`
	Sandbox       *sandboxCounts    `json:"sandbox,omitempty"`
}

type sandboxCounts struct {
	Attempts     int64 `json:"workspace_write_attempts"`
	Kernel       int64 `json:"kernel_enforced"`
	Degraded     int64 `json:"degraded_allowed"`
	Refused      int64 `json:"refused"`
	Unclassified int64 `json:"unclassified"`
}

// invariantCatalog drives the report's invariant table (stable order).
var invariantCatalog = []invariantStat{
	{ID: invFinite, Description: "every metric number is finite (no NaN/±Inf escapes)", Scope: "per_run"},
	{ID: invStructure, Description: "section shape: metric arrays/objects present with typed fields", Scope: "per_run"},
	{ID: invMinSample, Description: "min_sample declared and equal to the merged WP2 semantic (20)", Scope: "per_run"},
	{ID: invLaneSet, Description: "lane_ledger and escape_rate_by_lane carry the canonical 5 ALDC lanes 0/D/R/F/S in order", Scope: "per_run"},
	{ID: invCohortSet, Description: "cohort_metrics carries the canonical 3 provenance cohorts", Scope: "per_run"},
	{ID: invRateLabel, Description: "rate_label is measured or insufficient_sample (merged WP2 labels only)", Scope: "per_run"},
	{ID: invMeasuredDenom, Description: "measured <=> n>=min_sample>0; insufficient_sample <=> n<min_sample with no rate; denominators non-zero", Scope: "per_run"},
	{ID: invRateBounds, Description: "emitted rates within [0,1]", Scope: "per_run"},
	{ID: invConservation, Description: "counters conserve: lane buckets sum to lane totals; cohorts and lanes describe the same landing set; escape rows mirror lane_ledger", Scope: "per_run"},
	{ID: invSandboxSum, Description: "sandbox outcome buckets sum to workspace_write_attempts, all non-negative", Scope: "per_run"},
	{ID: invDegradeExplicit, Description: "degradation is explicit: git_log_error <=> empty metric arrays; sandbox metric present or explicitly errored", Scope: "per_run"},
	{ID: invWindowFrozen, Description: "same HEAD => git-window counters identical across runs", Scope: "cross_run"},
	{ID: invCounterMonotone, Description: "sandbox counters non-decreasing across runs (append-only tasks DB)", Scope: "cross_run"},
	{ID: invCohortMin, Description: "opt-in --min-cohort: workspace_write attempts >= N (issue #971 WP3 live F1 cohort n>=10)", Scope: "per_run"},
}

// runState is the cross-run comparable fingerprint of one collected run.
type runState struct {
	Head          string
	LaneTotals    map[string]int64
	CohortCommits map[string]int64
	Sandbox       *sandboxCounts
}

// execute runs the bounded burn-in loop and returns the final report.
//
// Verdict precedence (explicit degradation never masks a real failure):
//  1. collection failure (scorecard unrunnable/unparseable)  -> degraded
//  2. any per-run or cross-run invariant violation           -> fail
//  3. collected but zero recorded landings                   -> degraded
//  4. otherwise, N consecutive invariant-clean runs          -> pass
func execute(cfg config) *burninReport {
	mode := "live"
	if cfg.Fixture != "" {
		mode = "fixture"
	}
	report := &burninReport{
		Kind:        reportKind,
		Issue:       issueRef,
		Mode:        mode,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Runs:        []runResult{},
		Notes:       []string{},
	}
	if cfg.MinCohort > 0 {
		report.Notes = append(report.Notes,
			fmt.Sprintf("--min-cohort %d requested (issue #971 WP3 live F1 burn-in cohort target: n >= 10)", cfg.MinCohort))
	}

	observedClean := 0
	collectionFailed := false
	var prevState *runState
	for i := 1; i <= cfg.Runs; i++ {
		run := collectAndCheck(cfg, i, cfg.MinCohort)
		report.Runs = append(report.Runs, run)
		if !run.Collected {
			collectionFailed = true
			break
		}
		if run.Summary != nil {
			cur := runState{
				Head: run.Head, LaneTotals: run.Summary.LaneTotals,
				CohortCommits: run.Summary.CohortCommits, Sandbox: run.Summary.Sandbox,
			}
			if prevState != nil {
				if cross := checkCrossRun(*prevState, cur); len(cross) > 0 {
					report.CrossRunViolations = append(report.CrossRunViolations, cross...)
				}
			}
			prevState = &cur
		}
		if run.Clean {
			observedClean++
		}
	}

	verdict := verdictPass
	switch {
	case collectionFailed:
		verdict = verdictDegraded
	case len(report.CrossRunViolations) > 0 || anyRunViolations(report.Runs):
		verdict = verdictFail
	case anyRunQuality(report.Runs, qualityEmpty):
		verdict = verdictDegraded
		report.Notes = append(report.Notes,
			"no usable compliance data (empty landing set) — degraded, not pass")
	}

	// Falsifiable pass criterion: N consecutive invariant-clean runs
	// (--runs N sets N; the loop stops at the first non-clean run).
	report.PassCriterion = passCriterion{
		RequiredConsecutiveClean: cfg.Runs,
		ObservedConsecutiveClean: observedClean,
		Met:                      verdict == verdictPass && observedClean >= cfg.Runs,
	}
	report.Verdict = verdict
	report.Invariants = tallyInvariants(report)
	report.HumanTail = renderHumanTail(report)
	return report
}

func anyRunViolations(runs []runResult) bool {
	for _, run := range runs {
		if len(run.Violations) > 0 {
			return true
		}
	}
	return false
}

func anyRunQuality(runs []runResult, quality string) bool {
	for _, run := range runs {
		if run.DataQuality == quality {
			return true
		}
	}
	return false
}

// checkCrossRun enforces the over-time invariants between two consecutive
// collected runs.
func checkCrossRun(prev, cur runState) []Violation {
	var vs []Violation
	if prev.Head != "" && cur.Head != "" && prev.Head == cur.Head {
		if !mapsEqual(prev.LaneTotals, cur.LaneTotals) || !mapsEqual(prev.CohortCommits, cur.CohortCommits) {
			vs = append(vs, viol(invWindowFrozen, "$",
				"git-window counters drifted between runs at the same HEAD %s", shortHead(cur.Head)))
		}
	}
	if prev.Sandbox != nil && cur.Sandbox != nil {
		pairs := []struct {
			name    string
			prevVal int64
			curVal  int64
		}{
			{"workspace_write_attempts", prev.Sandbox.Attempts, cur.Sandbox.Attempts},
			{"kernel_enforced", prev.Sandbox.Kernel, cur.Sandbox.Kernel},
			{"degraded_allowed", prev.Sandbox.Degraded, cur.Sandbox.Degraded},
			{"refused", prev.Sandbox.Refused, cur.Sandbox.Refused},
			{"unclassified", prev.Sandbox.Unclassified, cur.Sandbox.Unclassified},
		}
		for _, p := range pairs {
			if p.curVal < p.prevVal {
				vs = append(vs, viol(invCounterMonotone, "$.sandbox_enforcement."+p.name,
					"counter shrank between runs: %d -> %d (tasks DB is append-only)", p.prevVal, p.curVal))
			}
		}
	}
	return vs
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func shortHead(head string) string {
	if len(head) > 12 {
		return head[:12]
	}
	return head
}

// collectAndCheck performs one burn-in run: collect the scorecard output,
// extract axes.compliance_vs_quality, run the invariant engine, and summarize.
func collectAndCheck(cfg config, runNum, minCohort int) runResult {
	run := runResult{Run: runNum, DataQuality: qualityNone}
	var data []byte
	var head string
	var err error
	if cfg.Fixture != "" {
		data, err = os.ReadFile(cfg.Fixture)
		if err != nil {
			run.CollectionError = fmt.Sprintf("read fixture: %v", err)
			return run
		}
		head = "fixture"
	} else {
		data, head, err = collectLiveScorecard(cfg)
		if err != nil {
			run.CollectionError = err.Error()
			return run
		}
	}

	sec, err := extractSection(data)
	if err != nil {
		run.CollectionError = err.Error()
		return run
	}
	run.Collected = true
	run.Head = head

	violations := checkSection(sec)
	if minCohort > 0 {
		if sbx := sandboxFromSection(sec); sbx != nil {
			if sbx.Attempts < int64(minCohort) {
				violations = append(violations, viol(invCohortMin, "$.sandbox_enforcement.workspace_write_attempts",
					"workspace_write attempts %d below the requested live cohort minimum %d", sbx.Attempts, minCohort))
			}
		} else {
			violations = append(violations, viol(invCohortMin, "$.sandbox_enforcement",
				"cannot verify --min-cohort %d: sandbox metric missing", minCohort))
		}
	}
	run.Violations = violations
	run.Clean = len(violations) == 0
	run.GitLogError, _ = sec["git_log_error"].(string)
	run.SandboxError, _ = sec["sandbox_enforcement_error"].(string)
	run.Summary = summarizeSection(sec)
	run.InsufficientSample = insufficientSampleLabels(sec)
	run.DataQuality = dataQualityFor(run)
	return run
}

// dataQualityFor classifies how much of the merged metric surface the run
// actually observed. partial = at least one probe explicitly degraded;
// empty = git side ran but recorded zero landings (nothing to burn in).
func dataQualityFor(run runResult) string {
	if run.GitLogError != "" || run.SandboxError != "" {
		return qualityPartial
	}
	if run.Summary != nil && run.Summary.Landings == 0 {
		return qualityEmpty
	}
	return qualityFull
}

// insufficientSampleLabels surfaces the merged WP2 honest labels without
// re-interpreting them: cohorts/lanes below min_sample are recorded verbatim.
func insufficientSampleLabels(sec map[string]any) []string {
	var labels []string
	cohortRows, _ := rowList(sec, "cohort_metrics")
	for _, row := range cohortRows {
		if rowRateLabel(row) == dogfood.RateLabelInsufficientSample {
			name := rowCohort(row)
			n, _ := asCounter(row["commits"])
			labels = append(labels, fmt.Sprintf("cohort:%s(n=%d)", name, n))
		}
	}
	escapeRows, _ := rowList(sec, "escape_rate_by_lane")
	for _, row := range escapeRows {
		if rowRateLabel(row) == dogfood.RateLabelInsufficientSample {
			name := rowLane(row)
			n, _ := asCounter(row["total"])
			labels = append(labels, fmt.Sprintf("lane:%s(n=%d)", name, n))
		}
	}
	return labels
}

// extractSection pulls axes.compliance_vs_quality out of a scorecard JSON
// envelope; a bare compliance_vs_quality section object is accepted too.
func extractSection(data []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse scorecard JSON: %w", err)
	}
	if dataObj, ok := doc["data"].(map[string]any); ok {
		axes, ok := dataObj["axes"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("envelope missing data.axes")
		}
		sec, ok := axes["compliance_vs_quality"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("envelope missing data.axes.compliance_vs_quality")
		}
		return sec, nil
	}
	if _, ok := doc["lane_ledger"]; ok {
		return doc, nil // bare section
	}
	if _, ok := doc["cohort_metrics"]; ok {
		return doc, nil // bare section
	}
	return nil, fmt.Errorf("input is neither a scorecard envelope nor a bare compliance_vs_quality section")
}

func sandboxFromSection(sec map[string]any) *sandboxCounts {
	obj, ok := sec["sandbox_enforcement"].(map[string]any)
	if !ok {
		return nil
	}
	attempts, okA := asCounter(obj["workspace_write_attempts"])
	kernel, okK := asCounter(obj["kernel_enforced"])
	degraded, okD := asCounter(obj["degraded_allowed"])
	refused, okR := asCounter(obj["refused"])
	unclassified, okU := asCounter(obj["unclassified"])
	if !okA || !okK || !okD || !okR || !okU {
		return nil
	}
	return &sandboxCounts{
		Attempts:     attempts,
		Kernel:       kernel,
		Degraded:     degraded,
		Refused:      refused,
		Unclassified: unclassified,
	}
}

// summarizeSection extracts the report's human-legible section summary.
func summarizeSection(sec map[string]any) *sectionSummary {
	summary := &sectionSummary{
		LaneTotals:    map[string]int64{},
		CohortCommits: map[string]int64{},
		CohortLabels:  map[string]string{},
		MinSample:     int64(dogfood.DefaultCohortMinSample),
	}
	if n, ok := asCounter(sec["min_sample"]); ok {
		summary.MinSample = n
	}
	laneRows, _ := rowList(sec, "lane_ledger")
	for _, row := range laneRows {
		total, _ := asCounter(row["total"])
		summary.LaneTotals[rowLane(row)] = total
		summary.Landings += int(total)
	}
	cohortRows, _ := rowList(sec, "cohort_metrics")
	for _, row := range cohortRows {
		commits, _ := asCounter(row["commits"])
		summary.CohortCommits[rowCohort(row)] = commits
		summary.CohortLabels[rowCohort(row)] = rowRateLabel(row)
	}
	summary.Sandbox = sandboxFromSection(sec)
	return summary
}

// collectLiveScorecard invokes the merged `g8s scorecard status --json`
// surface over the real recorded data.
func collectLiveScorecard(cfg config) ([]byte, string, error) {
	bin := resolveG8sBin(cfg)
	if bin == "" {
		return nil, "", fmt.Errorf(
			"g8s binary not found: set --g8s-bin or G8S_BIN, or build with `go build -o bin/g8s ./cmd/g8s`")
	}
	head := ""
	if h, err := gitHead(cfg.RepoDir); err == nil {
		head = h
	}
	ctx, cancel := context.WithTimeout(context.Background(), scorecardTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "scorecard", "status", "--json")
	cmd.Dir = cfg.RepoDir
	env := os.Environ()
	if cfg.DBPath != "" {
		env = append(env, "G8S_DB="+cfg.DBPath)
	}
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, head, fmt.Errorf("g8s scorecard status --json: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	return out, head, nil
}

// resolveG8sBin resolves the scorecard binary: --g8s-bin, then $G8S_BIN, then
// <repo>/bin/g8s.
func resolveG8sBin(cfg config) string {
	if cfg.G8sBin != "" {
		return cfg.G8sBin
	}
	if v := strings.TrimSpace(os.Getenv("G8S_BIN")); v != "" {
		return v
	}
	candidate := filepath.Join(cfg.RepoDir, "bin", "g8s")
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return ""
}

func gitHead(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// tallyInvariants aggregates violation counts per catalog ID over all runs.
func tallyInvariants(report *burninReport) []invariantStat {
	counts := map[string]int{}
	for _, run := range report.Runs {
		for _, v := range run.Violations {
			counts[v.Invariant]++
		}
	}
	for _, v := range report.CrossRunViolations {
		counts[v.Invariant]++
	}
	out := make([]invariantStat, 0, len(invariantCatalog))
	for _, spec := range invariantCatalog {
		spec.ViolationsTotal = counts[spec.ID]
		out = append(out, spec)
	}
	return out
}

// renderHumanTail renders the human-readable burn-in tail (stderr).
func renderHumanTail(report *burninReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dogfood_burnin (%s): verdict=%s mode=%s\n", report.Issue, report.Verdict, report.Mode)
	pc := report.PassCriterion
	fmt.Fprintf(&b, "pass criterion: %d consecutive invariant-clean runs — observed %d, met=%v\n",
		pc.RequiredConsecutiveClean, pc.ObservedConsecutiveClean, pc.Met)
	for _, run := range report.Runs {
		if !run.Collected {
			fmt.Fprintf(&b, "run %d: collection failed — %s\n", run.Run, run.CollectionError)
			continue
		}
		fmt.Fprintf(&b, "run %d: clean=%v quality=%s violations=%d head=%s\n",
			run.Run, run.Clean, run.DataQuality, len(run.Violations), shortHead(run.Head))
	}
	var failing []string
	for _, inv := range report.Invariants {
		if inv.ViolationsTotal > 0 {
			failing = append(failing, fmt.Sprintf("%s=%d", inv.ID, inv.ViolationsTotal))
		}
	}
	if len(failing) == 0 {
		b.WriteString("invariants: all clean\n")
	} else {
		sort.Strings(failing)
		fmt.Fprintf(&b, "invariants: VIOLATIONS %s\n", strings.Join(failing, " "))
	}
	for _, run := range report.Runs {
		if run.Summary == nil {
			continue
		}
		s := run.Summary
		names := make([]string, 0, len(s.CohortLabels))
		for name := range s.CohortLabels {
			names = append(names, name)
		}
		sort.Strings(names)
		labels := make([]string, 0, len(names))
		for _, name := range names {
			labels = append(labels, fmt.Sprintf("%s=%s(n=%d)", name, s.CohortLabels[name], s.CohortCommits[name]))
		}
		fmt.Fprintf(&b, "run %d cohort labels: %s\n", run.Run, strings.Join(labels, " "))
		if s.Sandbox != nil {
			fmt.Fprintf(&b, "run %d sandbox: attempts=%d kernel=%d degraded=%d refused=%d unclassified=%d\n",
				run.Run, s.Sandbox.Attempts, s.Sandbox.Kernel, s.Sandbox.Degraded, s.Sandbox.Refused, s.Sandbox.Unclassified)
		}
	}
	for _, note := range report.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	b.WriteString("report JSON on stdout\n")
	return b.String()
}

// emitReport writes the JSON report to w.
func emitReport(w *os.File, report *burninReport) error {
	return encodeReport(w, report)
}

func writeReportFile(path string, report *burninReport) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encErr := encodeReport(f, report)
	closeErr := f.Close()
	if encErr != nil {
		return encErr
	}
	return closeErr
}

func encodeReport(w *os.File, report *burninReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(report)
}
