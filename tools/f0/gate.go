package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GateCheckResult holds the verdict of an individual gate check.
type GateCheckResult struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "PASS", "FAIL", "SKIP"
	Detail string `json:"detail"`
}

// GateOutput is the top-level machine-readable result of f0 gate check.
type GateOutput struct {
	Overall string            `json:"overall"` // "PASS" or "FAIL"
	HeadSHA string            `json:"head_sha"`
	Checks  []GateCheckResult `json:"checks"`
}

func gateCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 gate check [options]\n")
		return 1
	}

	switch args[0] {
	case "check":
		return runGateCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "f0 gate: unknown subcommand %q\n", args[0])
		return 1
	}
}

// GateOptions configures the inputs to EvaluateGate.
type GateOptions struct {
	RepoDir    string
	Action     string
	PR         int
	HeadSHA    string
	BaseRef    string
	ReportFile string
	PRBody     string
	Mode       string // "auto", "local", "remote"
}

var f0TasksRegex = regexp.MustCompile(`(?i)F0-Tasks:\s*([^\r\n]+)`)

func runGateCheck(args []string) int {
	var opts GateOptions
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 < len(args) {
				opts.RepoDir = args[i+1]
				i++
			}
		case "--action":
			if i+1 < len(args) {
				opts.Action = args[i+1]
				i++
			}
		case "--pr":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					opts.PR = n
				}
				i++
			}
		case "--head-sha":
			if i+1 < len(args) {
				opts.HeadSHA = args[i+1]
				i++
			}
		case "--base":
			if i+1 < len(args) {
				opts.BaseRef = args[i+1]
				i++
			}
		case "--report":
			if i+1 < len(args) {
				opts.ReportFile = args[i+1]
				i++
			}
		case "--pr-body":
			if i+1 < len(args) {
				opts.PRBody = args[i+1]
				i++
			}
		case "--mode":
			if i+1 < len(args) {
				opts.Mode = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}

	if opts.Action == "" {
		fmt.Fprintf(os.Stderr, "f0 gate check: --action is required\n")
		return 1
	}

	output, err := EvaluateGate(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "f0 gate check: %v\n", err)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "f0 gate check: encoding json: %v\n", err)
	}

	_ = jsonOutput // Output is always JSON as required

	if output.Overall == "PASS" {
		return 0
	}
	return 1
}

// EvaluateGate runs all evidence, forge, provenance, drift, and claim checks.
func EvaluateGate(opts GateOptions) (*GateOutput, error) {
	repoDir := opts.RepoDir
	action := opts.Action
	pr := opts.PR
	headSHA := opts.HeadSHA
	baseRef := opts.BaseRef
	reportFile := opts.ReportFile
	prBodyArg := opts.PRBody

	if repoDir == "" {
		repoDir = "."
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	if action == "" {
		action = "merge"
	}

	forge, _ := GetForgeAdapter(absRepo)
	cfg, err := LoadConfig(absRepo)
	if err != nil {
		cfg = &Config{CI: "none", VerifyCommands: defaultVerifyCommands(absRepo)}
	}

	// Resolve PR metadata if --pr is provided
	var forgeBody string
	if pr > 0 && forge != nil {
		fHead, fBase, fBody, fErr := forge.ResolvePR(absRepo, pr)
		if fErr == nil {
			if headSHA == "" {
				headSHA = fHead
			}
			if baseRef == "" {
				baseRef = fBase
			}
			forgeBody = fBody
		}
	}

	// Default headSHA to HEAD if still empty
	if headSHA == "" {
		out, err := exec.Command("git", "-C", absRepo, "rev-parse", "HEAD").Output()
		if err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	} else {
		// Resolve full SHA if possible
		out, err := exec.Command("git", "-C", absRepo, "rev-parse", headSHA).Output()
		if err == nil {
			headSHA = strings.TrimSpace(string(out))
		}
	}

	var headTreeSHA string
	targetTree := "HEAD^{tree}"
	if headSHA != "" {
		targetTree = headSHA + "^{tree}"
	}
	treeOut, err := exec.Command("git", "-C", absRepo, "rev-parse", targetTree).Output()
	if err == nil {
		headTreeSHA = strings.TrimSpace(string(treeOut))
	}

	// Default baseRef to origin/HEAD or default branch if still empty
	if baseRef == "" {
		baseRef = findDefaultBranch(absRepo)
	}

	// Resolve PR / Report text for claims check
	var fullText string
	if reportFile != "" {
		if content, err := os.ReadFile(reportFile); err == nil {
			fullText = string(content)
		}
	}
	if prBodyArg != "" {
		if content, err := os.ReadFile(prBodyArg); err == nil {
			if fullText != "" {
				fullText += "\n" + string(content)
			} else {
				fullText = string(content)
			}
		} else {
			if fullText != "" {
				fullText += "\n" + prBodyArg
			} else {
				fullText = prBodyArg
			}
		}
	}
	if fullText == "" && forgeBody != "" {
		fullText = forgeBody
	}

	// Diff paths between base and head
	var diffPaths []string
	diffCmd := exec.Command("git", "-C", absRepo, "diff", "--name-only", baseRef+"..."+headSHA)
	diffOut, err := diffCmd.Output()
	if err != nil {
		// Fallback to two-dot diff
		diffCmd = exec.Command("git", "-C", absRepo, "diff", "--name-only", baseRef, headSHA)
		diffOut, _ = diffCmd.Output()
	}
	for _, p := range strings.Split(string(diffOut), "\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			diffPaths = append(diffPaths, p)
		}
	}

	repoLedger, _ := OpenRepoLedger(absRepo)
	var ledgerEntries []Entry
	if repoLedger != nil {
		ledgerEntries, _ = repoLedger.ReadEntries()
	}

	mode := opts.Mode
	if mode == "" {
		mode = "auto"
	}
	hasEvidence := false
	for _, e := range ledgerEntries {
		if e.Kind != "gate" {
			hasEvidence = true
			break
		}
	}
	isBundleMode := (mode == "remote") || (mode != "local" && (!hasEvidence || len(ledgerEntries) == 0) && strings.Contains(fullText, "<!-- f0:bundle:"))
	var bundle *PortableBundle
	var bErr error
	if isBundleMode {
		bundle, bErr = ParsePortableBundle(fullText)
		if bErr == nil {
			if !shaMatches(bundle.HeadSHA, headSHA) && !(headTreeSHA != "" && bundle.TreeSHA != "" && bundle.TreeSHA == headTreeSHA) {
				bErr = fmt.Errorf("bundle head SHA %s (tree %s) does not match target head %s (tree %s)", bundle.HeadSHA, bundle.TreeSHA, headSHA, headTreeSHA)
			}
		}
	}

	var checks []GateCheckResult

	// -------------------------------------------------------------------------
	// G0: Mandate Check
	// -------------------------------------------------------------------------
	mResult, _ := CheckMandate(absRepo, action, diffPaths)
	if mResult.Allowed {
		checks = append(checks, GateCheckResult{
			ID:     "G0",
			Status: "PASS",
			Detail: mResult.Reason,
		})
	} else if isBundleMode && bundle != nil && bErr == nil && bundle.MandateID != "" {
		reqTier := ClassifyActionTier(absRepo, filepath.Base(absRepo), action, diffPaths, nil)
		effectiveTier := bundle.MandateTier
		if effectiveTier == "" {
			effectiveTier = "T2"
		}
		if reqTier != "T3" && reqTier != "T4" && tierRank(reqTier) <= tierRank(effectiveTier) {
			checks = append(checks, GateCheckResult{
				ID:     "G0",
				Status: "PASS",
				Detail: fmt.Sprintf("remote mandate %s verified (tier %s, no trust boundary violation)", bundle.MandateID, effectiveTier),
			})
		} else {
			checks = append(checks, GateCheckResult{
				ID:     "G0",
				Status: "FAIL",
				Detail: fmt.Sprintf("diff touches trust boundary (%s required, max %s)", reqTier, effectiveTier),
			})
		}
	} else {
		checks = append(checks, GateCheckResult{
			ID:     "G0",
			Status: "FAIL",
			Detail: mResult.Reason,
		})
	}

	// -------------------------------------------------------------------------
	// G1: Evidence at Head
	// -------------------------------------------------------------------------
	if isBundleMode {
		if bErr != nil {
			checks = append(checks, GateCheckResult{
				ID:     "G1",
				Status: "FAIL",
				Detail: fmt.Sprintf("bundle verification failed: %v", bErr),
			})
		} else {
			g1Found := false
			var g1Detail string
			for _, ev := range bundle.Evidence {
				if ev.Status == "PASS" && ev.ItemsChecked > 0 && matchVerifyCommand(ev.Cmd, cfg.VerifyCommands) {
					if shaMatches(ev.SHA, headSHA) || (headTreeSHA != "" && ev.TreeSHA != "" && ev.TreeSHA == headTreeSHA) {
						g1Found = true
						g1Detail = fmt.Sprintf("bundle clean PASS evidence %s matching verify_commands (%s)", ev.ID, strings.Join(ev.Cmd, " "))
						break
					}
				}
			}
			if g1Found {
				checks = append(checks, GateCheckResult{
					ID:     "G1",
					Status: "PASS",
					Detail: g1Detail,
				})
			} else {
				checks = append(checks, GateCheckResult{
					ID:     "G1",
					Status: "FAIL",
					Detail: "no clean PASS evidence found in bundle matching verify_commands with items_checked > 0",
				})
			}
		}
	} else {
		g1Found := false
		var g1Detail string
		for i := len(ledgerEntries) - 1; i >= 0; i-- {
			e := &ledgerEntries[i]
			if e.Status != "PASS" || e.Dirty {
				continue
			}
			if !shaMatches(e.SHA, headSHA) && !(headTreeSHA != "" && e.TreeSHA != "" && e.TreeSHA == headTreeSHA) {
				continue
			}
			if matchVerifyCommand(e.Cmd, cfg.VerifyCommands) {
				g1Found = true
				g1Detail = fmt.Sprintf("clean PASS evidence %s matching verify_commands (%s)", e.ID, strings.Join(e.Cmd, " "))
				break
			}
		}
		if g1Found {
			checks = append(checks, GateCheckResult{
				ID:     "G1",
				Status: "PASS",
				Detail: g1Detail,
			})
		} else {
			checks = append(checks, GateCheckResult{
				ID:     "G1",
				Status: "FAIL",
				Detail: "no clean PASS evidence found at head SHA matching verify_commands",
			})
		}
	}

	// -------------------------------------------------------------------------
	// G2: Forge CI
	// -------------------------------------------------------------------------
	if cfg.CI == "none" {
		checks = append(checks, GateCheckResult{
			ID:     "G2",
			Status: "SKIP",
			Detail: "ci declared none in policy",
		})
	} else if pr <= 0 {
		checks = append(checks, GateCheckResult{
			ID:     "G2",
			Status: "SKIP",
			Detail: "offline mode (no PR number provided)",
		})
	} else {
		forgeChecks, err := forge.ListChecks(absRepo, pr, headSHA)
		if err != nil {
			checks = append(checks, GateCheckResult{
				ID:     "G2",
				Status: "FAIL",
				Detail: fmt.Sprintf("failed to list CI checks: %v", err),
			})
		} else if len(forgeChecks) == 0 {
			checks = append(checks, GateCheckResult{
				ID:     "G2",
				Status: "FAIL",
				Detail: "no checks reported yet",
			})
		} else {
			var pendingChecks []string
			var failingChecks []string
			var waivedChecks []string
			var waiverErrors []string

			for _, c := range forgeChecks {
				b := strings.ToLower(c.Bucket)
				if b == "pass" || b == "skipping" {
					continue
				}
				if b == "pending" {
					pendingChecks = append(pendingChecks, c.Name)
					continue
				}

				// Check is failing or canceled. Check for valid waiver
				var matchingWaiver *Entry
				for i := len(ledgerEntries) - 1; i >= 0; i-- {
					e := &ledgerEntries[i]
					if e.Kind != "waiver" {
						continue
					}
					wPR, _ := e.Extra["pr"]
					wCheck, _ := e.Extra["check"].(string)
					wSHA, _ := e.Extra["head_sha"].(string)

					prMatch := false
					switch v := wPR.(type) {
					case int:
						prMatch = (v == pr)
					case float64:
						prMatch = (int(v) == pr)
					case string:
						prMatch = (v == strconv.Itoa(pr))
					}
					if !prMatch {
						continue
					}
					if !strings.EqualFold(wCheck, c.Name) {
						continue
					}
					if headSHA != "" && wSHA != "" && !strings.HasPrefix(wSHA, headSHA) && !strings.HasPrefix(headSHA, wSHA) {
						continue
					}
					matchingWaiver = e
					break
				}

				if matchingWaiver == nil {
					failingChecks = append(failingChecks, c.Name)
					continue
				}

				issueURL, _ := matchingWaiver.Extra["issue"].(string)
				sameRepo, open, titleAndBody, vErr := forge.VerifyIssue(absRepo, issueURL)
				if vErr != nil || !sameRepo || !open || !strings.Contains(strings.ToLower(titleAndBody), strings.ToLower(c.Name)) {
					reason := "issue check failed"
					if !sameRepo {
						reason = "waiver issue in another repository"
					} else if !open {
						reason = "waiver issue is not OPEN"
					} else if !strings.Contains(strings.ToLower(titleAndBody), strings.ToLower(c.Name)) {
						reason = "waiver issue does not mention check name"
					}
					waiverErrors = append(waiverErrors, fmt.Sprintf("%s: %s", c.Name, reason))
					failingChecks = append(failingChecks, c.Name)
					continue
				}

				m, _ := LoadActiveMandate()
				if m != nil {
					spent, _ := MandateBudgetSpent(m)
					if spent > m.ErrorBudget.Limit {
						waiverErrors = append(waiverErrors, fmt.Sprintf("%s: error budget exhausted (%d spent > %d limit)", c.Name, spent, m.ErrorBudget.Limit))
						failingChecks = append(failingChecks, c.Name)
						continue
					}
				}

				waivedChecks = append(waivedChecks, c.Name)
			}

			if len(pendingChecks) > 0 {
				checks = append(checks, GateCheckResult{
					ID:     "G2",
					Status: "FAIL",
					Detail: fmt.Sprintf("checks pending: %s", strings.Join(pendingChecks, ", ")),
				})
			} else if len(failingChecks) > 0 {
				detail := fmt.Sprintf("checks failing: %s", strings.Join(failingChecks, ", "))
				if len(waiverErrors) > 0 {
					detail += " (" + strings.Join(waiverErrors, "; ") + ")"
				}
				checks = append(checks, GateCheckResult{
					ID:     "G2",
					Status: "FAIL",
					Detail: detail,
				})
			} else {
				detail := "all checks passed"
				if len(waivedChecks) > 0 {
					detail += fmt.Sprintf(" (waived: %s)", strings.Join(waivedChecks, ", "))
				}
				checks = append(checks, GateCheckResult{
					ID:     "G2",
					Status: "PASS",
					Detail: detail,
				})
			}
		}
	}

	// -------------------------------------------------------------------------
	// G3: Provenance
	// -------------------------------------------------------------------------
	if isBundleMode {
		if bErr != nil {
			checks = append(checks, GateCheckResult{
				ID:     "G3",
				Status: "FAIL",
				Detail: fmt.Sprintf("bundle verification failed: %v", bErr),
			})
		} else {
			var g3Failures []string
			for _, f := range diffPaths {
				if isManualFree(f, cfg.ManualFree) {
					continue
				}
				expectedBlob, hasBlob := bundle.Blobs[f]
				if !hasBlob {
					g3Failures = append(g3Failures, "unattributed: "+f)
					continue
				}
				actualBlob, err := gitBlobAt(absRepo, headSHA, f)
				if err != nil || actualBlob != expectedBlob {
					g3Failures = append(g3Failures, "altered-after-delivery: "+f)
				}
			}
			if len(g3Failures) > 0 {
				checks = append(checks, GateCheckResult{
					ID:     "G3",
					Status: "FAIL",
					Detail: strings.Join(g3Failures, "; "),
				})
			} else {
				checks = append(checks, GateCheckResult{
					ID:     "G3",
					Status: "PASS",
					Detail: "provenance verified via portable bundle",
				})
			}
		}
	} else {
		// Extract task IDs from PR body / report
		var taskIDs []string
		if m := f0TasksRegex.FindStringSubmatch(fullText); len(m) >= 2 {
			for _, tid := range strings.Split(m[1], ",") {
				tid = strings.TrimSpace(tid)
				if tid != "" {
					taskIDs = append(taskIDs, tid)
				}
			}
		}

		// Find merge base commit time if no taskIDs given
		var mbCommitTime time.Time
		if len(taskIDs) == 0 {
			mbOut, mbErr := exec.Command("git", "-C", absRepo, "merge-base", baseRef, headSHA).Output()
			if mbErr == nil {
				mbSHA := strings.TrimSpace(string(mbOut))
				tsOut, tsErr := exec.Command("git", "-C", absRepo, "show", "-s", "--format=%cI", mbSHA).Output()
				if tsErr == nil {
					mbCommitTime, _ = time.Parse(time.RFC3339, strings.TrimSpace(string(tsOut)))
				}
			}
		}

		// Filter g8s-deliver entries
		appliedBlobs := make(map[string]string)
		deletedFiles := make(map[string]bool)

		for _, e := range ledgerEntries {
			if e.Kind != "g8s-deliver" {
				continue
			}
			if isDryRun(e) {
				continue
			}

			if len(taskIDs) > 0 {
				tid, _ := e.Extra["task_id"].(string)
				if !sliceContains(taskIDs, tid) {
					continue
				}
			} else if !mbCommitTime.IsZero() {
				eTime, err := time.Parse(time.RFC3339, e.Ts)
				if err == nil && eTime.Before(mbCommitTime) {
					continue
				}
			}

			applied := getExtraStringSlice(e.Extra["applied"])
			deleted := getExtraStringSlice(e.Extra["deleted"])
			blobs := getExtraStringMap(e.Extra["blobs"])

			for _, f := range applied {
				delete(deletedFiles, f)
				if bHash, ok := blobs[f]; ok {
					appliedBlobs[f] = bHash
				}
			}
			for _, f := range deleted {
				delete(appliedBlobs, f)
				deletedFiles[f] = true
			}
		}

		// Collect manual tags
		manualBlobs := make(map[string]string)
		for _, e := range ledgerEntries {
			if e.Kind == "manual" {
				file, _ := e.Extra["file"].(string)
				blob, _ := e.Extra["blob"].(string)
				if file != "" {
					manualBlobs[file] = blob
				}
			}
		}

		var g3Failures []string
		var manualUsed []string

		// For each file in diff D
		for _, f := range diffPaths {
			if isManualFree(f, cfg.ManualFree) {
				continue
			}
			expectedBlob, inApplied := appliedBlobs[f]
			inDeleted := deletedFiles[f]
			mBlob, inManual := manualBlobs[f]

			if !inApplied && !inDeleted && !inManual {
				g3Failures = append(g3Failures, "unattributed: "+f)
				continue
			}

			if inApplied {
				blobAtHead, err := gitBlobAt(absRepo, headSHA, f)
				if err == nil && blobAtHead != expectedBlob {
					if inManual && mBlob == blobAtHead {
						manualUsed = append(manualUsed, f)
					} else {
						g3Failures = append(g3Failures, "altered-after-delivery: "+f)
					}
				}
			} else if inManual {
				manualUsed = append(manualUsed, f)
			}
		}

		// For each applied file in Delivered: must be in D unless already matches head
		for f, expectedBlob := range appliedBlobs {
			if isManualFree(f, cfg.ManualFree) {
				continue
			}
			if !sliceContains(diffPaths, f) {
				blobAtHead, err := gitBlobAt(absRepo, headSHA, f)
				if err != nil || blobAtHead != expectedBlob {
					g3Failures = append(g3Failures, "dropped: "+f)
				}
			}
		}

		// For each deleted file in Delivered: must not exist at head
		for f := range deletedFiles {
			if isManualFree(f, cfg.ManualFree) {
				continue
			}
			if gitFileExistsAt(absRepo, headSHA, f) {
				g3Failures = append(g3Failures, "dropped: "+f)
			}
		}

		if len(g3Failures) > 0 {
			checks = append(checks, GateCheckResult{
				ID:     "G3",
				Status: "FAIL",
				Detail: strings.Join(g3Failures, "; "),
			})
		} else {
			detail := "provenance verified"
			if len(manualUsed) > 0 {
				detail += fmt.Sprintf(" (manual tags: %s)", strings.Join(manualUsed, ", "))
			}
			checks = append(checks, GateCheckResult{
				ID:     "G3",
				Status: "PASS",
				Detail: detail,
			})
		}
	}

	// -------------------------------------------------------------------------
	// G4: Gate Appeasement
	// -------------------------------------------------------------------------
	g4Res, _ := CheckGateAppeasement(absRepo, baseRef, headSHA, "", ledgerEntries)
	checks = append(checks, g4Res)

	// -------------------------------------------------------------------------
	// G6: Claim Citations
	// -------------------------------------------------------------------------
	violations, err := LintClaims(fullText, "gate", absRepo, headSHA, cfg.CI, repoLedger, forge)
	if err != nil {
		checks = append(checks, GateCheckResult{
			ID:     "G6",
			Status: "FAIL",
			Detail: fmt.Sprintf("claims lint error: %v", err),
		})
	} else if len(violations) > 0 {
		var msgs []string
		for _, v := range violations {
			msgs = append(msgs, fmt.Sprintf("line %d: %s (%s)", v.LineNum, v.Line, v.Reason))
		}
		checks = append(checks, GateCheckResult{
			ID:     "G6",
			Status: "FAIL",
			Detail: strings.Join(msgs, "; "),
		})
	} else {
		checks = append(checks, GateCheckResult{
			ID:     "G6",
			Status: "PASS",
			Detail: "all claims cited and verified",
		})
	}

	// -------------------------------------------------------------------------
	// G7: Issue Closure Criteria
	// -------------------------------------------------------------------------
	g7Res, _ := CheckIssueClosureGate(absRepo, fullText, forge, ledgerEntries)
	checks = append(checks, g7Res)

	// -------------------------------------------------------------------------
	// Compute Overall Result & Append Gate Entry
	// -------------------------------------------------------------------------
	overall := "PASS"
	for _, c := range checks {
		if c.Status == "FAIL" {
			overall = "FAIL"
			break
		}
	}

	if repoLedger != nil {
		entry := &Entry{
			ID:           newEvidenceID(),
			Ts:           time.Now().UTC().Format(time.RFC3339),
			Kind:         "gate",
			Repo:         absRepo,
			SHA:          headSHA,
			TreeSHA:      headTreeSHA,
			Cmd:          []string{"gate", "check", "--action", action},
			Status:       overall,
			ItemsChecked: len(checks),
			Extra: map[string]any{
				"action": action,
				"checks": checks,
			},
		}
		_ = repoLedger.Append(entry)
	}

	output := &GateOutput{
		Overall: overall,
		HeadSHA: headSHA,
		Checks:  checks,
	}
	return output, nil
}

func matchVerifyCommand(entryCmd []string, verifyCmds [][]string) bool {
	clean := stripEnv(entryCmd)
	var stripped []string
	for _, a := range clean {
		if a != "-json" && a != "--json" {
			stripped = append(stripped, a)
		}
	}
	for _, vCmd := range verifyCmds {
		if len(stripped) >= len(vCmd) {
			match := true
			for i := range vCmd {
				if stripped[i] != vCmd[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

func gitBlobAt(repoDir, sha, path string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", sha+":"+path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitFileExistsAt(repoDir, sha, path string) bool {
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "-e", sha+":"+path)
	return cmd.Run() == nil
}

func sliceContains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func isDryRun(e Entry) bool {
	if len(e.Extra) == 0 {
		return false
	}
	val, ok := e.Extra["dry_run"]
	if !ok {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

func getExtraStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	if s, ok := v.([]string); ok {
		return s
	}
	if list, ok := v.([]any); ok {
		var res []string
		for _, item := range list {
			if str, ok := item.(string); ok {
				res = append(res, str)
			}
		}
		return res
	}
	return nil
}

func getExtraStringMap(v any) map[string]string {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]string); ok {
		return m
	}
	if m, ok := v.(map[string]any); ok {
		res := make(map[string]string)
		for k, val := range m {
			if str, ok := val.(string); ok {
				res[k] = str
			}
		}
		return res
	}
	return nil
}

func isManualFree(file string, patterns []string) bool {
	for _, p := range patterns {
		if pathMatches(file, p) {
			return true
		}
	}
	return false
}
