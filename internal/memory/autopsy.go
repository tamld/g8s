package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/vault"
)

// AutopsyTrajectory captures a failure-to-fix learning trajectory (#689:
// "Học -> Sai -> Sửa -> Rút kinh nghiệm") across attempts or lineage tasks.
type AutopsyTrajectory struct {
	TaskID            string            `json:"task_id"`
	PreviousTaskID    string            `json:"previous_task_id,omitempty"`
	SessionID         string            `json:"session_id,omitempty"`
	FailedCheck       string            `json:"failed_check"`
	FailedExitCode    int               `json:"failed_exit_code"`
	DiagnosticExcerpt string            `json:"diagnostic_excerpt"`
	FixSummary        string            `json:"fix_summary"`
	Package           string            `json:"package,omitempty"`
	File              string            `json:"file"`
	Symbol            string            `json:"symbol,omitempty"`
	TestFile          string            `json:"test_file,omitempty"`
	TestCase          string            `json:"test_case,omitempty"`
	Preconditions     map[string]string `json:"preconditions,omitempty"`
}

// AutopsyReport is the distilled output of an Experiential Autopsy (#689).
type AutopsyReport struct {
	Trajectory      AutopsyTrajectory        `json:"trajectory"`
	SanitizedLesson string                   `json:"sanitized_lesson"`
	ProposedEntry   *MemoryEntry             `json:"proposed_entry,omitempty"`
	CandidateRecord vault.DistillationRecord `json:"candidate_record"`
	AutoProposed    bool                     `json:"auto_proposed"`
}

var autopsySecretRegexes = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{12,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*["']?[A-Za-z0-9_./+-]{12,}["']?`),
}

// SanitizeAutopsyText scrubs credentials via dispatch.SanitizeOutput and explicit
// token regexes so raw secrets never enter memory.db or vault.db (#689).
func SanitizeAutopsyText(s string) string {
	clean := dispatch.SanitizeOutput(strings.TrimSpace(s))
	for _, re := range autopsySecretRegexes {
		clean = re.ReplaceAllString(clean, "[REDACTED_SECRET]")
	}
	return clean
}

// RunExperientialAutopsy distills a failure-to-fix trajectory into a sanitized
// CandidateRecord + MemoryEntry and optionally proposes it through the GateSensor (#689).
func (a *LocalSQLiteMemoryAdapter) RunExperientialAutopsy(ctx context.Context, traj AutopsyTrajectory, autoPropose bool) (*AutopsyReport, error) {
	if strings.TrimSpace(traj.TaskID) == "" {
		return nil, fmt.Errorf("%w: task_id is required for experiential autopsy", ErrInvalidInput)
	}
	if strings.TrimSpace(traj.FailedCheck) == "" {
		traj.FailedCheck = "verify_check"
	}
	if traj.FailedExitCode == 0 {
		traj.FailedExitCode = 1
	}

	cleanDiag := SanitizeAutopsyText(traj.DiagnosticExcerpt)
	cleanFix := SanitizeAutopsyText(traj.FixSummary)
	if cleanFix == "" {
		cleanFix = "Resolved failure in " + strings.TrimSpace(traj.File)
	}
	pkg := strings.TrimSpace(traj.Package)
	if pkg == "" && strings.TrimSpace(traj.File) != "" {
		pkg = filepath.ToSlash(filepath.Dir(strings.TrimSpace(traj.File)))
	}

	problem := fmt.Sprintf("Check %s failed (exit=%d): %s", traj.FailedCheck, traj.FailedExitCode, cleanDiag)
	lesson := fmt.Sprintf("[Autopsy %s] Problem: %s | Fix: %s", traj.TaskID, problem, cleanFix)

	sum := sha256.Sum256([]byte(traj.TaskID + ":" + lesson))
	entryID := "autopsy-" + hex.EncodeToString(sum[:6])

	preconds := make(map[string]string)
	for k, v := range traj.Preconditions {
		preconds[k] = v
	}
	if strings.TrimSpace(traj.File) != "" && preconds["file"] == "" {
		preconds["file"] = strings.TrimSpace(traj.File)
	}
	if pkg != "" && preconds["package"] == "" {
		preconds["package"] = pkg
	}
	if strings.TrimSpace(traj.TestFile) != "" && preconds["test_file"] == "" {
		preconds["test_file"] = strings.TrimSpace(traj.TestFile)
	}
	if strings.TrimSpace(traj.TestCase) != "" && preconds["test_case"] == "" {
		preconds["test_case"] = strings.TrimSpace(traj.TestCase)
	}
	if strings.TrimSpace(traj.Symbol) != "" && preconds["symbol"] == "" {
		preconds["symbol"] = strings.TrimSpace(traj.Symbol)
	}

	sessionID := strings.TrimSpace(traj.SessionID)
	if sessionID == "" {
		sessionID = "autopsy-" + traj.TaskID
	}

	entry := &MemoryEntry{
		ID:            entryID,
		Kind:          KindProcedure,
		Scope:         ScopeGlobal,
		SessionID:     sessionID,
		TaskID:        traj.TaskID,
		PromotedBy:    "experiential-autopsy",
		Payload:       lesson,
		Preconditions: preconds,
	}

	cand := vault.DistillationRecord{
		ID:     "VAULT-" + strings.ToUpper(entryID),
		Title:  fmt.Sprintf("Autopsy Lesson: %s (%s)", traj.FailedCheck, filepath.Base(traj.File)),
		Status: "APPLIED",
		Causality: vault.CausalityAnchor{
			Problem:   problem,
			TradeOff:  cleanFix,
			RootCause: cleanDiag,
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package: pkg,
			File:    strings.TrimSpace(traj.File),
			Symbol:  strings.TrimSpace(traj.Symbol),
		},
		ForensicVerification: vault.ForensicAnchor{
			TestFile: strings.TrimSpace(traj.TestFile),
			TestCase: strings.TrimSpace(traj.TestCase),
		},
	}

	report := &AutopsyReport{
		Trajectory:      traj,
		SanitizedLesson: lesson,
		ProposedEntry:   entry,
		CandidateRecord: cand,
		AutoProposed:    autoPropose,
	}

	if autoPropose {
		if err := a.ProposeEntry(ctx, entry); err != nil {
			return nil, fmt.Errorf("memory: propose experiential autopsy entry: %w", err)
		}
	}

	return report, nil
}

// ExtractTrajectoryFromTask inspects a controlplane.Task (and its parent or
// multiple attempts) to synthesize an AutopsyTrajectory when a task succeeds
// after a prior failure (#689).
func ExtractTrajectoryFromTask(ctx context.Context, store *controlplane.Store, task *controlplane.Task) (*AutopsyTrajectory, bool) {
	if task == nil {
		return nil, false
	}

	var reqPayload struct {
		Prompt       string   `json:"prompt"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	if len(task.Request) > 0 {
		_ = json.Unmarshal(task.Request, &reqPayload)
	}

	primaryFile := ""
	for _, p := range reqPayload.AllowedPaths {
		p = strings.TrimSpace(p)
		if p != "" && !strings.Contains(p, "*") {
			primaryFile = p
			break
		}
	}
	if primaryFile == "" && len(reqPayload.AllowedPaths) > 0 {
		primaryFile = strings.TrimSuffix(strings.TrimPrefix(reqPayload.AllowedPaths[0], "./"), "/*")
	}

	// Case 1: Task itself succeeded on attempt > 1
	if task.Attempts > 1 {
		return &AutopsyTrajectory{
			TaskID:            task.TaskID,
			SessionID:         "task-" + task.TaskID,
			FailedCheck:       "worker_retry",
			FailedExitCode:    1,
			DiagnosticExcerpt: fmt.Sprintf("Task %s required %d attempts before passing verification", task.TaskID, task.Attempts),
			FixSummary:        SanitizeAutopsyText(reqPayload.Prompt),
			File:              primaryFile,
		}, true
	}

	// Case 2: Task has a ParentTaskID that failed
	if store != nil && task.ParentTaskID != nil && strings.TrimSpace(*task.ParentTaskID) != "" {
		parent, err := store.GetTask(ctx, strings.TrimSpace(*task.ParentTaskID))
		if err == nil && parent != nil && parent.State == controlplane.StateFailed {
			diag := string(parent.Result)
			if len(diag) > 240 {
				diag = diag[:240]
			}
			return &AutopsyTrajectory{
				TaskID:            task.TaskID,
				PreviousTaskID:    parent.TaskID,
				SessionID:         "lineage-" + parent.TaskID,
				FailedCheck:       "parent_task_failed",
				FailedExitCode:    1,
				DiagnosticExcerpt: SanitizeAutopsyText(diag),
				FixSummary:        SanitizeAutopsyText(reqPayload.Prompt),
				File:              primaryFile,
			}, true
		}
	}

	return nil, false
}
