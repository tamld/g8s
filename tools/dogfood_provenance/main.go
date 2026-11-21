// Command dogfood_provenance publishes the main-landing provenance ledger.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tamld/g8s/internal/dogfood"
)

const (
	windowSize = 200
	minWindow  = 20
	// maxUnclassifiedPercent is the single enforcement threshold (ADR-0039).
	maxUnclassifiedPercent = 30
	conventionPath         = "docs/decisions/0039-dogfooding-honesty-ledger.md"
)

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func collect(ref string, baseline bool) ([]dogfood.CommitRecord, string, error) {
	return collectFrom(ref, baseline, git)
}

// collectFrom gathers the window's landings with subject, body, and touched
// paths so the aggregate ledger, the lane-split ledger (#971 WP2), and the
// cohort metrics all see one commit set.
func collectFrom(ref string, baseline bool, git func(...string) (string, error)) ([]dogfood.CommitRecord, string, error) {
	if _, err := git("rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return nil, "", err
	}
	shallow, err := git("rev-parse", "--is-shallow-repository")
	if err != nil || shallow != "false" {
		return nil, "", fmt.Errorf("full Git history required: shallow=%q, error=%v", shallow, err)
	}
	rangeRef := ref
	label := "Historical baseline (before convention; diagnostic only)"
	if !baseline {
		// First-parent history locates the main landing, not a PR-side commit.
		anchor, err := git("log", ref, "--first-parent", "--diff-merges=first-parent", "--no-patch", "--diff-filter=A", "--format=%H", "--", conventionPath)
		if err != nil {
			return nil, "", err
		}
		if anchor == "" {
			// Valid only while the convention has not landed. A deleted ADR or
			// shallow clone must not silently reset an established ledger.
			if _, err := git("cat-file", "-e", ref+":"+conventionPath); err == nil {
				return nil, "", fmt.Errorf("convention exists but its main-landing anchor is missing")
			}
			return nil, "Before convention: ADR-0039 has not landed on main", nil
		}
		anchors := strings.Fields(anchor)
		anchor = anchors[len(anchors)-1] // Never reset the epoch on re-addition.
		rangeRef = anchor + ".." + ref
		label = "Main landings after convention anchor `" + anchor + "`"
	}
	// NUL framing preserves multiline messages; the \x01 subject and \x04
	// body/file-list terminators feed dogfood.ParseCommitRecords so the
	// lane-split ledger can route each landing by its touched paths (#971 WP2).
	out, err := git("log", fmt.Sprintf("-%d", windowSize), "--name-only", "--format=%x00%s%x01%B%x04", rangeRef)
	if err != nil {
		return nil, "", err
	}
	return dogfood.ParseCommitRecords(out), label, nil
}

func render(records []dogfood.CommitRecord, label string, baseline bool) (string, bool) {
	var report []dogfood.Provenance
	for _, rec := range records {
		report = append(report, rec.Provenance)
	}
	r, f, u, n, ratio := dogfood.Ratio(report)
	var b strings.Builder
	fmt.Fprintf(&b, "## Dogfooding provenance ledger\n\n%s\n\n| Kind | Count |\n| --- | ---: |\n| Receipt | %d |\n| F0-direct | %d |\n| Unclassified | %d |\n| Total | %d |\n\n", label, r, f, u, n)
	if r+f == 0 {
		b.WriteString("Receipt ratio: undefined (no classified commits).\n")
	} else {
		fmt.Fprintf(&b, "Receipt ratio: %.2f%% = receipts / (receipts + F0-direct).\n", ratio*100)
	}
	if n > 0 {
		fmt.Fprintf(&b, "Honesty gap: %.2f%% unclassified; budget ≤%d%%.\n", float64(u)*100/float64(n), maxUnclassifiedPercent)
	}
	if n < minWindow {
		fmt.Fprintf(&b, "Window too small (n<%d), ratio not yet meaningful; no enforcement.\n", minWindow)
	}
	failed := !baseline && n >= minWindow && u*100 > maxUnclassifiedPercent*n
	if baseline {
		b.WriteString("Historical baseline only; no enforcement and no retro-tagging.\n")
	} else if failed {
		b.WriteString("FAIL: unclassified honesty gap exceeds budget.\n")
	}
	b.WriteString("\nTarget: ≥70% receipt-backed for substantive Go/CI changes (aggregate ledger does not infer substantive scope or verify receipt authenticity).\n")
	return b.String(), failed
}

// renderLaneSplitSections emits the #971 WP2 lane-split ledger, provenance
// cohort comparison, and per-lane escape rates. Lane D (prose) is auditable —
// only its ALDC review depth differs.
func renderLaneSplitSections(records []dogfood.CommitRecord) string {
	var b strings.Builder
	b.WriteString("\n## Lane-split honesty ledger (issue #971 WP2)\n\n")
	b.WriteString(dogfood.FormatLaneLedger(dogfood.BuildLaneLedger(records)))

	b.WriteString("### Provenance cohort quality\n\n")
	b.WriteString("Revert/fixup rate by provenance cohort; cohorts under n=")
	fmt.Fprintf(&b, "%d carry the insufficient_sample label (current ledger state).\n\n", dogfood.DefaultCohortMinSample)
	b.WriteString("| Cohort | Commits | Revert/fixup | Rate | Label |\n| --- | ---: | ---: | ---: | --- |\n")
	for _, m := range dogfood.BuildCohortMetrics(records, minWindow) {
		rate := "—"
		if m.RateLabel == dogfood.RateLabelMeasured {
			rate = fmt.Sprintf("%.2f%%", m.RevertFixupRate*100)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n", m.Cohort, m.Commits, m.RevertFixupCommits, rate, m.RateLabel)
	}

	b.WriteString("\n### Escape rate by lane\n\n")
	b.WriteString("| Lane | Name | Landings | Revert/fixup | Escape rate | Label |\n| --- | --- | ---: | ---: | ---: | --- |\n")
	for _, e := range dogfood.BuildEscapeRateByLane(records, minWindow) {
		rate := "—"
		if e.RateLabel == dogfood.RateLabelMeasured {
			rate = fmt.Sprintf("%.2f%%", e.EscapeRate*100)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s |\n", e.Lane, e.LaneName, e.Total, e.RevertFixup, rate, e.RateLabel)
	}
	b.WriteString("\n")
	return b.String()
}

func run() error {
	ref := flag.String("ref", "origin/main", "main ref to inspect")
	baseline := flag.Bool("baseline", false, "diagnostic last-200 baseline including pre-convention history; never enforce")
	flag.Parse()
	records, label, err := collect(*ref, *baseline)
	if err != nil {
		return err
	}
	text, failed := render(records, label, *baseline)
	text += renderLaneSplitSections(records)
	metrics, metricsErr := renderMetricsSections(*ref, *baseline, git)
	if metricsErr != nil {
		return metricsErr
	}
	text += metrics
	if _, err := fmt.Print(text); err != nil {
		return err
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(text)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if failed {
		return fmt.Errorf("provenance honesty gap exceeds budget")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
