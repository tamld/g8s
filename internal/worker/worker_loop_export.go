package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"os"

	"path/filepath"
	"regexp"
	"runtime"

	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"

	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/telemetry"
)

// agyUsage carries token usage metrics reported by the agy tool wrapper.

func (s *Supervisor) reflexGate() OutputQualityGate {
	if s.qualityGate != nil {
		return s.qualityGate
	}
	s.reflexOnce.Do(func() {
		s.reflexGateIns = reflex.NewReflexGate()
	})
	return s.reflexGateIns
}

// telemetry lazily opens the closed-loop ingestion engine (#253). Opt-in via
// G8S_TELEMETRY=1 (default ledger) or G8S_TELEMETRY_DB=<path> (custom ledger).
func (s *Supervisor) telemetry() *telemetry.TelemetryEngine {
	s.telMu.Lock()
	defer s.telMu.Unlock()
	return s.telemetryLocked()
}

func (s *Supervisor) telemetryLocked() *telemetry.TelemetryEngine {
	if !s.telInited {
		s.telInited = true
		if os.Getenv("G8S_TELEMETRY") == "1" {
			cfg := telemetry.DefaultTelemetryConfig()
			if p := os.Getenv("G8S_TELEMETRY_DB"); p != "" {
				cfg.DBPath = p
			}
			// Short flush interval: worker processes are short-lived and the
			// engine flushes on Close, but a crash must not lose everything.
			cfg.FlushInterval = 2 * time.Second
			eng, err := telemetry.NewTelemetryEngine(cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[warn] telemetry engine disabled: %v\n", err)
			} else {
				s.telEngine = eng
			}
		}
	}
	return s.telEngine
}

// telEnter registers one in-flight run so the engine outlives every run that
// may still ingest (#394).
func (s *Supervisor) telEnter() {
	s.telMu.Lock()
	s.telRuns++
	s.telMu.Unlock()
}

// telExit drops one in-flight run; the last one out flushes and closes the
// engine and resets the lazy-init so a later run re-opens a fresh engine.
func (s *Supervisor) telExit() {
	s.telMu.Lock()
	defer s.telMu.Unlock()
	if s.telRuns > 0 {
		s.telRuns--
	}
	if s.telRuns == 0 {
		s.closeTelemetryLocked()
	}
}

// closeTelemetry flushes and releases the ingestion engine, resetting the
// lazy-init state so a subsequent run re-opens it instead of silently
// dropping events.
func (s *Supervisor) closeTelemetry() {
	s.telMu.Lock()
	defer s.telMu.Unlock()
	s.closeTelemetryLocked()
}

func (s *Supervisor) closeTelemetryLocked() {
	if s.telEngine != nil {
		if err := s.telEngine.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[warn] telemetry flush: %v\n", err)
		}
		s.telEngine = nil
	}
	s.telInited = false
}

// ingestTrace emits one best-effort trace event; telemetry failures must
// never break task flow.
func (s *Supervisor) ingestTrace(eventType telemetry.TraceEventType, taskID string, exitCode *int, errMsg string, extra ...map[string]any) {
	eng := s.telemetry()
	if eng == nil {
		return
	}
	payload := map[string]any{}
	for _, m := range extra {
		for k, v := range m {
			payload[k] = v
		}
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	ev := telemetry.TraceEvent{
		TaskID:    taskID,
		EventType: eventType,
		Timestamp: time.Now(),
		ExitCode:  exitCode,
		Payload:   payload,
	}
	if v, ok := payload["class"].(string); ok {
		ev.Class = v
	}
	if v, ok := payload["effort_class"].(string); ok {
		ev.EffortClass = v
	}
	if v, ok := payload["effort_requested"].(string); ok {
		ev.EffortRequested = v
	}
	if v, ok := payload["effort_applied"].(string); ok {
		ev.EffortApplied = v
	}
	if v, ok := payload["effort_mismatch"].(bool); ok {
		ev.EffortMismatch = v
	}
	if v, ok := payload["input_tokens"].(int); ok {
		ev.InputTokens = v
	}
	if v, ok := payload["output_tokens"].(int); ok {
		ev.OutputTokens = v
	}
	if v, ok := payload["duration_seconds"].(float64); ok {
		ev.DurationSeconds = v
	}
	if err := eng.IngestEvent(context.Background(), ev); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] telemetry ingest: %v\n", err)
	}
}

// maybePause transitions NEEDS_INFO/BLOCKED outcomes (declared in the result
// file or fenced inside captured stdout) into paused states, releasing the lease.
func (s *Supervisor) maybePause(ctx context.Context, wr workerResult, stdoutText, taskID, workerID, token string) bool {
	candidates := []workerResult{wr}
	for _, raw := range fencedJSONPattern.FindAllStringSubmatch(stdoutText, -1) {
		var fenced workerResult
		if json.Unmarshal([]byte(raw[1]), &fenced) == nil && fenced.Status != "" {
			candidates = append(candidates, fenced)
		}
	}
	for _, candidate := range candidates {
		if candidate.Status != controlplane.StateNeedsInfo && candidate.Status != controlplane.StateBlocked {
			continue
		}
		reason := firstNonEmpty(candidate.Reason, candidate.Summary, strings.ToLower(candidate.Status))
		payload := mustResultJSON(candidate, "", "")
		if _, err := s.cp.PauseTask(taskID, workerID, token, candidate.Status, payload, reason); err != nil {
			return false
		}
		return true
	}
	return false
}

// providerRefusalPhrases are the canned refusal sentence fragments emitted
// by provider content filters (#344). Narrow by design — a task legitimately
// discussing content filtering must not false-positive, so we match the
// full refusal sentence stems, not individual words.

var pathRedactionRegex = regexp.MustCompile(`(/[a-zA-Z0-9_\-\.]+)+`)

// sanitizeExportError strips absolute paths and returns a discrete category and clean message.
func sanitizeExportError(op, target string, err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		cat := "io_error"
		switch pathErr.Op {
		case "mkdir":
			cat = "mkdir_failed"
		case "open", "write":
			cat = "write_failed"
		case "stat":
			cat = "stat_failed"
		case "read":
			cat = "read_failed"
		}
		return cat, fmt.Sprintf("%s %s: %v", pathErr.Op, target, pathErr.Err)
	}
	raw := err.Error()
	clean := pathRedactionRegex.ReplaceAllString(raw, "[path]")
	return "io_error", fmt.Sprintf("%s %s: %s", op, target, clean)
}

// ExportReceiptDetailed writes the redacted task snapshot as sealed evidence into the
// attempt directory and centralized Evidence Lake, and persists the structured export outcome.
func (s *Supervisor) ExportReceiptDetailed(ctx context.Context, taskID, runDir string) controlplane.ReceiptExportStatus {
	now := float64(s.clock().UnixNano()) / 1e9
	exportStatus := controlplane.ReceiptExportStatus{
		UpdatedAt: now,
	}

	snap, err := s.cp.GetTask(ctx, taskID)
	if err != nil || snap == nil {
		exportStatus.Local = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusFailed,
			Time:     now,
			Category: "task_not_found",
			Error:    "task lookup failed",
		}
		exportStatus.Central = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
		return exportStatus
	}

	snapData, err := controlplane.CanonicalReceiptSnapshot(snap)
	if err != nil {
		exportStatus.Local = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusFailed,
			Time:     now,
			Category: "marshal_failed",
			Error:    "canonical snapshot generation failed",
		}
		exportStatus.Central = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
		return exportStatus
	}

	hash := sha256.Sum256(snapData)
	digest := hex.EncodeToString(hash[:])

	// 1. Attempt local runDir export
	if runDir == "" {
		exportStatus.Local = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
	} else {
		receiptPath := filepath.Join(runDir, "receipt.json")
		if werr := os.WriteFile(receiptPath, snapData, 0o600); werr != nil {
			fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", receiptPath, werr)
			cat, sErr := sanitizeExportError("write", "receipt.json", werr)
			exportStatus.Local = controlplane.ExportDestinationStatus{
				Status:   controlplane.ExportStatusFailed,
				Time:     now,
				Category: cat,
				Error:    sErr,
				Path:     "receipt.json",
			}
		} else {
			exportStatus.Local = controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
				Time:   now,
				Digest: digest,
				Path:   "receipt.json",
			}
		}
	}

	// 2. Attempt centralized Evidence Lake export
	if s.evidenceDir == "" {
		exportStatus.Central = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
	} else {
		taskEvidenceDir := filepath.Join(s.evidenceDir, taskID)
		if merr := os.MkdirAll(taskEvidenceDir, 0o700); merr != nil {
			fmt.Fprintf(os.Stderr, "[warn] worker: mkdir %s: %v\n", taskEvidenceDir, merr)
			cat, sErr := sanitizeExportError("mkdir", "evidence_dir", merr)
			exportStatus.Central = controlplane.ExportDestinationStatus{
				Status:   controlplane.ExportStatusFailed,
				Time:     now,
				Category: cat,
				Error:    sErr,
				Path:     filepath.Join(taskID, "receipt.json"),
			}
		} else {
			evReceiptPath := filepath.Join(taskEvidenceDir, "receipt.json")
			if werr := os.WriteFile(evReceiptPath, snapData, 0o600); werr != nil {
				fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", evReceiptPath, werr)
				cat, sErr := sanitizeExportError("write", "receipt.json", werr)
				exportStatus.Central = controlplane.ExportDestinationStatus{
					Status:   controlplane.ExportStatusFailed,
					Time:     now,
					Category: cat,
					Error:    sErr,
					Path:     filepath.Join(taskID, "receipt.json"),
				}
			} else {
				if snap.Attempts > 0 {
					attemptPath := filepath.Join(taskEvidenceDir, fmt.Sprintf("attempt_%d.json", snap.Attempts))
					if aerr := os.WriteFile(attemptPath, snapData, 0o600); aerr != nil {
						fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", attemptPath, aerr)
					}
				}
				exportStatus.Central = controlplane.ExportDestinationStatus{
					Status: controlplane.ExportStatusSucceeded,
					Time:   now,
					Digest: digest,
					Path:   filepath.Join(taskID, "receipt.json"),
				}
			}
		}
	}

	// Success is true if local succeeded (or disabled) and central did not fail
	localOk := exportStatus.Local.Status == controlplane.ExportStatusSucceeded
	centralOk := exportStatus.Central.Status == controlplane.ExportStatusSucceeded || exportStatus.Central.Status == controlplane.ExportStatusDisabled
	exportStatus.Success = localOk && centralOk
	exportStatus.FullyEvidenced = localOk && (s.evidenceDir == "" || exportStatus.Central.Status == controlplane.ExportStatusSucceeded)

	// Persist outcome into control plane
	_ = s.cp.RecordReceiptExport(ctx, taskID, exportStatus)

	return exportStatus
}

// ExportReceipt writes the redacted task snapshot as sealed evidence into the
// attempt directory and centralized Evidence Lake; failures are recorded in the
// task's structured receipt export status without masking execution outcomes.
func (s *Supervisor) ExportReceipt(ctx context.Context, taskID, runDir string) {
	_ = s.ExportReceiptDetailed(ctx, taskID, runDir)
}

// LoopIsolation hands each concurrent loop worker a private working directory
// (e.g. a pool worktree, #394). Acquire is called once per worker before its
// first attempt; release runs when that worker exits. An acquire error fails
// the whole loop with exit 1 — concurrency must never silently degrade into a
// shared checkout.
type LoopIsolation interface {
	Acquire(ctx context.Context, workerID string) (dir string, release func(), err error)
}

// LoopOptions parameterize RunLoop.
type LoopOptions struct {
	WorkerID     string
	LeaseSeconds int
	Provider     string
	Model        string
	Once         bool
	// Concurrency bounds the drain pool: <=1 (and any Once request) keeps
	// the serial drain byte-identical; N>1 claims and runs up to N attempts
	// simultaneously.
	Concurrency int
	// Isolation, when set, engages once per concurrent worker.
	Isolation LoopIsolation
	// OnTask observes each completed attempt. It is invoked from worker
	// goroutines — callbacks must synchronize themselves.
	OnTask func(*controlplane.Task)
	// OnError observes every RunOnce error (claim/execute/control-plane
	// failures — empty claims do not count). The drain still parks on the
	// error and its exit code is unchanged; the callback lets the caller
	// distinguish "queue drained" from "control-plane failure" (#394 PR-A2).
	// It is invoked from worker goroutines — callbacks must synchronize.
	OnError func(error)
}

// maxLoopConcurrency caps the drain pool so an internal caller passing an
// absurd Concurrency cannot fork itself before the isolation gate applies.
const maxLoopConcurrency = 64

// RunLoop drains claimable tasks until none remain, the context ends, or
// once-mode completes a single attempt. It returns a process-style exit code.
func (s *Supervisor) RunLoop(ctx context.Context, opts LoopOptions) int {
	s.reapOrphans()
	n := opts.Concurrency
	if n < 1 {
		n = 1
	}
	if n > maxLoopConcurrency {
		fmt.Fprintf(os.Stderr, "[warn] concurrent loop: clamping concurrency %d to %d\n", n, maxLoopConcurrency)
		n = maxLoopConcurrency
	}
	if n == 1 || opts.Once {
		return s.runSerialLoop(ctx, opts)
	}
	return s.runConcurrentLoop(ctx, opts, n)
}

// runSerialLoop is the historical drain: claim, run, repeat until empty.
func (s *Supervisor) runSerialLoop(ctx context.Context, opts LoopOptions) int {
	for {
		if ctx.Err() != nil {
			return ExitCodeForSignal(15)
		}
		task, err := s.RunOnce(ctx, RunOptions{
			WorkerID:     opts.WorkerID,
			LeaseSeconds: opts.LeaseSeconds,
			Provider:     opts.Provider,
			Model:        opts.Model,
		})
		if err != nil || task == nil {
			if err != nil && opts.OnError != nil {
				opts.OnError(err)
			}
			return 0
		}
		if opts.OnTask != nil {
			opts.OnTask(task)
		}
		if opts.Once {
			switch task.State {
			case controlplane.StateSucceeded, controlplane.StateCancelled, controlplane.StateQueued:
				return 0
			default:
				return 1
			}
		}
	}
}

// runConcurrentLoop drains through up to n simultaneous RunOnce cycles. A
// worker whose claim returns empty parks; the loop is drained only when every
// worker parks at the same moment, so retryable re-queues surfaced by a
// finishing worker are re-claimed by that worker itself. Cancellation unwinds
// in-flight attempts and reports the SIGTERM exit convention (143).
func (s *Supervisor) runConcurrentLoop(ctx context.Context, opts LoopOptions, n int) int {
	var (
		mu     sync.Mutex
		cond   = sync.NewCond(&mu)
		active int
		parked int
		stop   bool
		isoErr error
	)
	setStop := func() {
		mu.Lock()
		if !stop {
			stop = true
			cond.Broadcast()
		}
		mu.Unlock()
	}
	loopDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			setStop()
		case <-loopDone:
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dir string
			var release func()
			if opts.Isolation != nil {
				d, rel, aerr := opts.Isolation.Acquire(ctx, opts.WorkerID)
				if aerr != nil {
					mu.Lock()
					if isoErr == nil {
						isoErr = aerr
					}
					mu.Unlock()
					setStop()
					return
				}
				dir, release = d, rel
				defer release()
			}
			for {
				mu.Lock()
				if stop {
					mu.Unlock()
					return
				}
				active++
				mu.Unlock()

				task, err := s.RunOnce(ctx, RunOptions{
					WorkerID:     opts.WorkerID,
					LeaseSeconds: opts.LeaseSeconds,
					Provider:     opts.Provider,
					Model:        opts.Model,
					Dir:          dir,
				})

				mu.Lock()
				active--
				if err != nil || task == nil {
					if err != nil && opts.OnError != nil {
						opts.OnError(err)
					}
					parked++
					if parked == n && active == 0 {
						stop = true
						cond.Broadcast()
					}
					for !stop {
						cond.Wait()
					}
					mu.Unlock()
					return
				}
				mu.Unlock()
				if opts.OnTask != nil {
					opts.OnTask(task)
				}
			}
		}()
	}
	wg.Wait()
	close(loopDone)
	if ctx.Err() != nil {
		return ExitCodeForSignal(15)
	}
	if isoErr != nil {
		fmt.Fprintf(os.Stderr, "[warn] concurrent loop: isolation acquire: %v\n", isoErr)
		return 1
	}
	return 0
}

// reapOrphans kills process groups recorded by stale child.pid files left by
// crashed predecessors; best-effort only, never fails the run.
func (s *Supervisor) reapOrphans() {
	if runtime.GOOS == "windows" {
		return
	}
	taskDirs, err := os.ReadDir(s.runRoot)
	if err != nil {
		return
	}
	for _, taskDir := range taskDirs {
		if !taskDir.IsDir() {
			continue
		}
		attempts, err := os.ReadDir(filepath.Join(s.runRoot, taskDir.Name()))
		if err != nil {
			continue
		}
		for _, attempt := range attempts {
			pidPath := filepath.Join(s.runRoot, taskDir.Name(), attempt.Name(), "child.pid")
			raw, rerr := os.ReadFile(pidPath)
			if rerr != nil {
				continue
			}
			pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if perr == nil && pid > 0 {
				_ = killProcessGroup(pid, syscallSIGKILL)
			}
			_ = os.Remove(pidPath)
		}
	}
}

// ExitCodeForSignal maps a terminating signal number onto the shell exit-code
// convention (128+n), so SIGTERM reports 143 exactly like the baseline.
func ExitCodeForSignal(sig int) int { return 128 + sig }

func sameLease(t *controlplane.Task, workerID, token string) bool {
	return t.LeaseOwner != nil && *t.LeaseOwner == workerID &&
		t.LeaseToken != nil && *t.LeaseToken == token
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstOf(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func stdbuf(raw []byte) []byte {
	if raw == nil {
		return []byte{}
	}
	return raw
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"ok":false,"status":"marshal_error"}`)
	}
	return data
}

func sanitizeTrack(raw string, name string, altered *[]string) string {
	sanitized := dispatch.SanitizeOutput(raw)
	if sanitized != raw {
		*altered = append(*altered, name)
	}
	return sanitized
}

func outcomeEnvelope(ok bool, status, stdout, stderr string) map[string]any {
	var altered []string
	sanStdout := sanitizeTrack(stdout, "stdout", &altered)
	sanStderr := sanitizeTrack(stderr, "stderr", &altered)
	env := map[string]any{
		"ok":     ok,
		"status": status,
		"stdout": sanStdout,
		"stderr": sanStderr,
	}
	if len(altered) > 0 {
		env["artifact_altered"] = true
		env["altered_fields"] = altered
	}
	return env
}

// verifyExecutableIdentity checks if the executable is what it claims to be.
// verifyExecutableIdentity checks if the executable is what it claims to be.
// It detects fake scripts (e.g., python3 that's actually a Node script) by delegating
// to dispatch.VerifyExecutableIdentity.
func verifyExecutableIdentity(path string) error {
	return dispatch.VerifyExecutableIdentity(path)
}

func mustResultJSON(wr workerResult, stdout, stderr string) json.RawMessage {
	envelope := map[string]any{
		"ok":     wr.OK,
		"status": wr.Status,
	}
	if wr.Reason != "" {
		envelope["reason"] = wr.Reason
	}
	if wr.Summary != "" {
		envelope["summary"] = wr.Summary
	}
	var altered []string
	if wr.Response != "" {
		// #383: the response is worker/model-controlled content that now
		// reaches result_json and the Evidence Lake — it goes through the
		// same central sanitization as stdout, no exceptions.
		envelope["response"] = sanitizeTrack(wr.Response, "response", &altered)
	}
	if len(wr.ContractViolation) > 0 {
		envelope["contract_violation"] = wr.ContractViolation
	}
	if wr.Usage != nil {
		envelope["usage"] = wr.Usage
	}
	if wr.ProviderDurationSeconds > 0 {
		envelope["provider_duration_seconds"] = wr.ProviderDurationSeconds
	}
	if stdout != "" {
		envelope["stdout"] = sanitizeTrack(stdout, "stdout", &altered)
	}
	if stderr != "" {
		envelope["stderr"] = sanitizeTrack(stderr, "stderr", &altered)
	}
	if len(altered) > 0 {
		envelope["artifact_altered"] = true
		envelope["altered_fields"] = altered
	}
	return mustJSON(envelope)
}

// l3RejectBlocks reports whether a L3 Jev reject must fail the attempt
// (#411, #418): advisory by default — blocking is the operator's explicit opt-in
// via G8S_L3_JEV_BLOCK=1. With #418 L3 context enrichment wired via ContextPacket,
// enabling G8S_L3_JEV_BLOCK=1 becomes rational for campaigns requiring strict semantic review.
func l3RejectBlocks() bool {
	return os.Getenv("G8S_L3_JEV_BLOCK") == "1"
}
