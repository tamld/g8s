package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	g8scontext "github.com/tamld/g8s/internal/context"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/telemetry"
	"github.com/tamld/g8s/internal/vault"
)

// agyUsage carries token usage metrics reported by the agy tool wrapper.

var (
	instanceIDOnce   sync.Once
	cachedInstanceID string
)

// GetInstanceID returns the cached instance ID for the worker package (#465).
func GetInstanceID() string {
	instanceIDOnce.Do(func() {
		cachedInstanceID = pathutil.InstanceID()
	})
	return cachedInstanceID
}

// sweepAttemptGroup is the post-run orphan sweep (#415 PR-1 & #439): after
// the attempt has ended, verify the attempt's process group is gone — a
// worker that exits while its same-group grandchildren keep running leaves
// silent orphans. Survivors get SIGKILL (best-effort) and an
// `orphan_killed` trace event; the stale pid file is removed either way.
// Windows containment lands in #415 PR-2 (Job Objects).
// Layer 2 (#439): If a marker is provided, candidate processes outside the
// process group (e.g. grandchildren that escaped via setsid) are swept.
func (s *Supervisor) sweepAttemptGroup(child Child, taskID string, marker ...string) {
	if child != nil {
		pid := child.PID()
		if groupAlive(pid) {
			_ = killProcessGroup(pid, syscallSIGKILL)
			if s != nil {
				s.ingestTrace(telemetry.TraceEventOrphanKilled, taskID, nil, fmt.Sprintf("same-group survivor killed after attempt end (instance=%s)", GetInstanceID()))
			}
		}
	}
	if len(marker) > 0 && marker[0] != "" && child != nil {
		s.sweepAttemptMarker(child, taskID, marker[0])
	}
}

const (
	runMarkerEnv            = "G8S_RUN_MARKER"
	defaultMaxCandidatePIDs = 512
	defaultPgrepDepth       = 8
	defaultSweepBudget      = 2 * time.Second
)

func attemptMarker(taskID string, attempts int) string {
	if attempts <= 0 {
		attempts = 1
	}
	return fmt.Sprintf("%s-%d", taskID, attempts)
}

// sweepAttemptMarker performs the Layer 2 containment escape sweep (#439).
// It searches candidate processes (descendants and orphaned processes) and
// kills any process whose environment matches G8S_RUN_MARKER=<marker> exactly.
func (s *Supervisor) sweepAttemptMarker(child Child, taskID, marker string) int {
	if child == nil {
		return 0
	}
	return s.sweepAttemptMarkerPID(child.PID(), taskID, marker)
}

// sweepAttemptMarkerPID executes the Layer 2 escape sweep starting from childPID.
func (s *Supervisor) sweepAttemptMarkerPID(childPID int, taskID, marker string) int {
	if runtime.GOOS == "windows" || marker == "" {
		return 0
	}
	candidates := collectMarkerCandidates(childPID, defaultMaxCandidatePIDs)
	killed, _ := s.sweepCandidatePIDs(candidates, taskID, marker, hasRunMarker, defaultMaxCandidatePIDs, defaultSweepBudget)
	return killed
}

// collectMarkerCandidates gathers candidate PIDs for the Layer 2 sweep:
// 1. Descendants of childPID via `pgrep -P` iterated to bounded depth.
// 2. Orphaned processes with PPID 1 owned by the current user (newest PIDs first).
func collectMarkerCandidates(childPID, maxPIDs int) []int {
	if maxPIDs <= 0 {
		maxPIDs = defaultMaxCandidatePIDs
	}
	var (
		candidates []int
		visited    = make(map[int]bool)
	)

	// 1. Descendant walk from childPID
	if childPID > 0 {
		visited[childPID] = true
		current := []int{childPID}
		for depth := 0; depth < defaultPgrepDepth && len(current) > 0 && len(candidates) < maxPIDs; depth++ {
			var next []int
			for _, parent := range current {
				out, err := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
				if err != nil {
					continue
				}
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					pid, perr := strconv.Atoi(line)
					if perr == nil && pid > 0 && !visited[pid] {
						visited[pid] = true
						candidates = append(candidates, pid)
						next = append(next, pid)
						if len(candidates) >= maxPIDs {
							break
						}
					}
				}
				if len(candidates) >= maxPIDs {
					break
				}
			}
			current = next
		}
	}

	// 2. Orphaned processes (PPID 1) owned by current user (for double-fork setsid escapes)
	if len(candidates) < maxPIDs {
		uid := os.Getuid()
		out, err := exec.Command("pgrep", "-u", strconv.Itoa(uid), "-P", "1").Output()
		if err == nil {
			var orphanPIDs []int
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if pid, perr := strconv.Atoi(line); perr == nil && pid > 1 && !visited[pid] {
					orphanPIDs = append(orphanPIDs, pid)
				}
			}
			// Sort newest PIDs first so recent escapes are checked first
			sort.Slice(orphanPIDs, func(i, j int) bool {
				return orphanPIDs[i] > orphanPIDs[j]
			})
			for _, pid := range orphanPIDs {
				visited[pid] = true
				candidates = append(candidates, pid)
				if len(candidates) >= maxPIDs {
					break
				}
			}
		}
	}

	return candidates
}

// hasRunMarker checks whether the process with the given PID has an exact match
// for G8S_RUN_MARKER=<marker> in its environment.
//
// INVARIANT (POSIX Layer 2 Containment):
// A candidate process is killed if and only if its environment contains an exact
// match for G8S_RUN_MARKER=<attempt-unique-value>. We NEVER kill by process age,
// binary name, or parent heuristics alone. This eliminates accidental
// termination of unrelated system or user processes.
// hasRunMarkerLinuxOpt provides an allocation-free environment check for Linux.
// ⚡ Bolt Optimization: Replace `strings.Split(string(data), "\x00")` with a direct byte scan
// to avoid massive memory allocations scaling with environment size.
func hasRunMarkerLinuxOpt(data []byte, targetBytes []byte) bool {
	idx := 0
	for {
		j := bytes.Index(data[idx:], targetBytes)
		if j < 0 {
			break
		}
		matchIdx := idx + j
		startOk := matchIdx == 0 || data[matchIdx-1] == 0
		endIdx := matchIdx + len(targetBytes)
		endOk := endIdx == len(data) || data[endIdx] == 0
		if startOk && endOk {
			return true
		}
		idx = matchIdx + 1
	}
	return false
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// hasRunMarkerDarwinOpt provides a fast regexp-free environment check for Darwin.
// ⚡ Bolt Optimization: Replace dynamic `regexp.MustCompile` inside the loop with zero-allocation `bytes.Index`.
func hasRunMarkerDarwinOpt(out []byte, targetBytes []byte) bool {
	idx := 0
	for {
		j := bytes.Index(out[idx:], targetBytes)
		if j < 0 {
			break
		}
		matchIdx := idx + j
		startOk := matchIdx == 0 || isSpace(out[matchIdx-1])
		endIdx := matchIdx + len(targetBytes)
		endOk := endIdx == len(out) || isSpace(out[endIdx])
		if startOk && endOk {
			return true
		}
		idx = matchIdx + 1
	}
	return false
}

func hasRunMarker(pid int, marker string) bool {
	if pid <= 1 || marker == "" {
		return false
	}
	target := runMarkerEnv + "=" + marker
	targetBytes := []byte(target)

	if runtime.GOOS == "linux" {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			return false
		}
		return hasRunMarkerLinuxOpt(data, targetBytes)
	}

	if runtime.GOOS == "darwin" {
		out, err := exec.Command("ps", "-E", "-ww", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return false
		}
		return hasRunMarkerDarwinOpt(out, targetBytes)
	}

	return false
}

// sweepCandidatePIDs examines candidate PIDs up to maxPIDs within the budget,
// killing any process where check(pid, marker) returns true.
func (s *Supervisor) sweepCandidatePIDs(
	candidates []int,
	taskID, marker string,
	check func(int, string) bool,
	maxPIDs int,
	budget time.Duration,
) (killed, examined int) {
	if check == nil {
		check = hasRunMarker
	}
	if maxPIDs <= 0 {
		maxPIDs = defaultMaxCandidatePIDs
	}
	if budget <= 0 {
		budget = defaultSweepBudget
	}

	start := time.Now()
	for _, pid := range candidates {
		if examined >= maxPIDs {
			break
		}
		if time.Since(start) >= budget {
			break
		}
		examined++
		if check(pid, marker) {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			if s != nil {
				s.ingestTrace(telemetry.TraceEventOrphanKilled, taskID, nil, fmt.Sprintf("layer=2: setsid survivor killed after attempt end (instance=%s)", GetInstanceID()))
			}
			killed++
		}
	}
	return killed, examined
}

// collect terminates any surviving child, applies the terminal branch, and
// guarantees output capture files are removed and evidence exported.
func (s *Supervisor) collect(
	ctx context.Context,
	child Child,
	reason, taskID, workerID, token, runDir, promptPath, resultPath, stdoutPath, stderrPath string,
	resultMode string,
	permission, receiptID string,
	startTime time.Time,
	deliverableDir string,
	req taskRequest,
) (*controlplane.Task, error) {
	select {
	case <-child.Done():
	default:
		child.Terminate(terminateGrace)
	}
	code := child.WaitCode()
	// #415 PR-2 (Windows): the attempt has reached its terminal branch —
	// close the Job Object handle so the kernel reaps the whole tree
	// (KILL_ON_JOB_CLOSE), including grandchildren that survived taskkill.
	if jc, ok := child.(interface{ closeJob() }); ok {
		jc.closeJob()
	}

	stdoutRaw, _ := os.ReadFile(stdoutPath)
	stderrRaw, _ := os.ReadFile(stderrPath)
	stdoutText := dispatch.CaptureBounded(stdbuf(stdoutRaw), s.captureMaxBytes)
	stderrText := dispatch.CaptureBounded(stdbuf(stderrRaw), s.captureMaxBytes)
	os.Remove(stdoutPath) // worker output is never persisted.
	os.Remove(stderrPath)

	durationSec := s.clock().Sub(startTime).Seconds()
	if durationSec < 0 {
		durationSec = 0
	}

	var toolPolicyEvidence *dispatch.ToolPolicyEvidence
	if req.ToolPolicy == "none" || req.Permission == "supplied_text" {
		ev := dispatch.ExtractToolPolicyEvidence(stdoutText, stderrText, req.ToolPolicyEvidenceLog)
		ev.Provider = firstNonEmpty(req.Provider, "agy")
		ev.Model = req.Model
		toolPolicyEvidence = &ev
	}

	switch {
	case reason == "lease_lost" || strings.HasPrefix(reason, "lease_lost"):
		return s.snapshot(ctx, taskID, runDir, promptPath)
	case reason == "cancelled":
		env := outcomeEnvelope(false, "cancelled", stdoutText, stderrText)
		env["duration"] = durationSec
		env["duration_seconds"] = durationSec
		if u := extractAGYUsage(stdoutText); u != nil {
			env["usage"] = u
		}
		if toolPolicyEvidence != nil {
			env["tool_policy_evidence"] = toolPolicyEvidence
			env["offered_count"] = toolPolicyEvidence.OfferedCount
			env["denied_count"] = toolPolicyEvidence.DeniedCount
			env["invoked_count"] = toolPolicyEvidence.InvokedCount
		}
		s.ingestTrace(telemetry.TraceEventTaskCancelled, taskID, nil, "cancelled by orchestrator")
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(env),
			Success:   false,
			Retryable: false,
			Err:       "cancelled by orchestrator",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish cancelled attempt: %w", ferr)
		}
	case reason == "timeout":
		env := outcomeEnvelope(false, "timeout", stdoutText, stderrText)
		env["duration"] = durationSec
		env["duration_seconds"] = durationSec
		if u := extractAGYUsage(stdoutText); u != nil {
			env["usage"] = u
		}
		if toolPolicyEvidence != nil {
			env["tool_policy_evidence"] = toolPolicyEvidence
			env["offered_count"] = toolPolicyEvidence.OfferedCount
			env["denied_count"] = toolPolicyEvidence.DeniedCount
			env["invoked_count"] = toolPolicyEvidence.InvokedCount
		}
		s.ingestTrace(telemetry.TraceEventTaskTimeout, taskID, nil, "execution deadline exceeded")
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(env),
			Success:   false,
			Retryable: true,
			Err:       "execution deadline exceeded",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish timed-out attempt: %w", ferr)
		}
	case reason == "invalid_timeout":
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(outcomeEnvelope(false, "invalid_result", stdoutText, stderrText)),
			Success:   false,
			Retryable: false,
			Err:       "task requested an invalid execution timeout",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish invalid-timeout attempt: %w", ferr)
		}
	default:
		rawResult, oversize := readResultFile(resultPath)
		wr := readWorkerResult(rawResult, stdoutText, code)
		success := code == 0 && wr.OK
		if success && strings.TrimSpace(stdoutText) == "" {
			if apiErr := extractWorkerAPIErrorLine(stderrText); apiErr != "" {
				wr.OK = false
				wr.Status = "failed"
				wr.Reason = apiErr
				wr.Summary = "CLI agent reported API error on stderr with empty output"
				success = false
			}
		}
		if toolPolicyEvidence != nil && toolPolicyEvidence.InvokedCount > 0 {
			wr.OK = false
			wr.Status = "contract_violation"
			wr.Reason = "model tool call invoked under tool_policy=none"
			wr.ContractViolation = mustJSON(map[string]any{
				"policy":        "tool_policy=none",
				"invoked_count": toolPolicyEvidence.InvokedCount,
			})
			success = false
		}

		// L3 post-run Jev quality gate (#253/#371/#418): assess output quality
		// before structural validation — catches hallucination that passes
		// schema checks but fails semantic review. DurationSec computed via
		// the injectable clock (worker review finding: fix 1). ContextPacket
		// enriched from the closed-loop telemetry ledger (#418).
		if success && os.Getenv("G8S_TELEMETRY") == "1" {
			gate := s.reflexGate()
			excerpt := stdoutText
			if len(excerpt) > 2000 {
				// Rune-aware truncation (worker review finding: fix 3).
				for len(excerpt) > 2000 && !utf8.RuneStart(excerpt[2000]) {
					excerpt = excerpt[:2000-1]
				}
				excerpt = excerpt[:2000]
			}
			elapsed := int(s.clock().Sub(startTime).Seconds())
			if elapsed < 0 {
				elapsed = 0
			}
			broker := g8scontext.NewBroker(g8scontext.Sources{
				RecentOutcomes: func(ctx context.Context) ([]string, error) {
					eng := s.telemetry()
					if eng == nil {
						return nil, errors.New("telemetry engine not available")
					}
					events, err := eng.QueryEvents(ctx, telemetry.TraceFilter{Limit: 5})
					if err != nil {
						return nil, err
					}
					var out []string
					for _, ev := range events {
						out = append(out, fmt.Sprintf("%s %s %s", ev.EventType, ev.TaskID, ev.Error))
					}
					return out, nil
				},
				VaultNotes: func(ctx context.Context) ([]string, error) {
					maxK := vault.ResolveMaxActiveRules()
					if maxK <= 0 {
						return nil, nil
					}
					vdb := strings.TrimSpace(os.Getenv("G8S_VAULT_DB"))
					if vdb == "" {
						vdb = filepath.Join(pathutil.DefaultStateDir(), "vault.db")
					}
					if _, statErr := os.Stat(vdb); statErr != nil {
						return nil, nil
					}
					v, verr := vault.NewVault(vdb, nil)
					if verr != nil {
						return nil, verr
					}
					defer v.Close()
					applied := "APPLIED"
					recs, lerr := v.List(ctx, vault.VaultFilter{Status: &applied, Limit: maxK})
					if lerr != nil {
						return nil, lerr
					}
					return vault.FormatJITWhisper(recs), nil
				},
				SomPhase: nil,
			}, func(event, detail string) {
				fmt.Fprintf(os.Stderr, "[warn] L3 context broker: %s: %s\n", event, detail)
			})
			packet := broker.Assemble(ctx)
			oqReq := reflex.OutputQualityRequest{
				TaskID:        taskID,
				OutputExcerpt: excerpt,
				ExpectedShape: "analysis",
				ExitCode:      code,
				DurationSec:   elapsed,
				ContextPacket: packet,
			}
			signal, qerr := gate.EmitOutputQualitySignal(ctx, oqReq)
			if qerr == nil {
				verdict := reflex.EvaluateOutputQuality(signal)
				// #411: Jev-judged quality is ADVISORY by default — a
				// context-blind reject must not fail the attempt (ADR-0021
				// §8 anti-pattern; same split as the #398 L2 floor). Blocking
				// requires the explicit operator flag.
				if verdict.Action == "reject" {
					fmt.Fprintf(os.Stderr, "[warn] L3 quality gate: Jev rejected output for %s: %s\n", taskID, verdict.Reason)
					if l3RejectBlocks() {
						wr = workerResult{
							OK:      false,
							Status:  "blocked",
							Reason:  verdict.Reason,
							Summary: "L3 post-run quality gate: output failed Jev assessment",
						}
						success = false
					}
				}
			}
		}

		if paused := s.maybePause(ctx, wr, stdoutText, taskID, workerID, token); paused {
			break
		}
		// #253 closed-loop ingestion: the terminal outcome of every attempt
		// becomes a trace event (exit code + failure signature).
		exitCode := code
		durationSec := s.clock().Sub(startTime).Seconds()
		if durationSec < 0 {
			durationSec = 0
		}
		var inTokens, outTokens int
		if wr.Usage != nil {
			inTokens = wr.Usage.InputTokens
			outTokens = wr.Usage.OutputTokens
		}
		telemetryPayload := map[string]any{
			"class":            firstNonEmpty(req.EffortClass, "unregistered"),
			"effort_class":     firstNonEmpty(req.EffortClass, "unregistered"),
			"effort_requested": req.EffortRequested,
			"effort_applied":   req.EffortApplied,
			"input_tokens":     inTokens,
			"output_tokens":    outTokens,
			"duration_seconds": durationSec,
		}
		if req.EffortMismatch {
			telemetryPayload["effort_mismatch"] = true
		}
		if success {
			s.ingestTrace(telemetry.TraceEventTaskCompleted, taskID, &exitCode, "", telemetryPayload)
		} else {
			s.ingestTrace(telemetry.TraceEventTaskFailed, taskID, &exitCode, firstNonEmpty(wr.Reason, wr.Status, "failed"), telemetryPayload)
		}
		finishErr := ""
		if !success {
			finishErr = firstNonEmpty(wr.Reason, wr.Status, "failed")
		}

		// #346: consume the delegated-write receipt exactly once when the
		// scoped write attempt succeeded — before the attempt is finished.
		// A replay by a different task is a hard, non-retryable failure.
		if success && deliverableDir != "" {
			allowedPaths := resolveReceiptAllowedPaths(receiptID)
			if violations, dfErr := VerifyWorktreeDiffScope(deliverableDir, permission, allowedPaths); dfErr != nil {
				wr = workerResult{
					OK:      false,
					Status:  "contract_violation",
					Reason:  dfErr.Error(),
					Summary: "post-run worktree diff firewall rejected out-of-scope file mutations",
					ContractViolation: mustJSON(map[string]any{
						"policy":          "worktree_diff_firewall",
						"path_violations": violations,
					}),
				}
				success = false
				finishErr = firstNonEmpty(finishErr, wr.Reason)
			}
		}
		if success && permission == "workspace_write" && receiptID != "" {
			if cerr := s.cp.ConsumeWriteReceipt(ctx, receiptID, taskID); cerr != nil {
				if errors.Is(cerr, controlplane.ErrReceiptAlreadyConsumed) {
					wr = workerResult{
						OK:      false,
						Status:  "failed",
						Reason:  "write receipt already consumed by another task (replay suspected)",
						Summary: "delegated-write receipt replay detected",
					}
				} else {
					wr = workerResult{
						OK:      false,
						Status:  "failed",
						Reason:  "receipt consume failed: " + cerr.Error(),
						Summary: "delegated-write receipt could not be consumed",
					}
				}
				success = false
				finishErr = firstNonEmpty(finishErr, wr.Reason)
			}
		}

		// Perform result validation before finishing attempt
		resultJSON := mustResultJSON(wr, stdoutText, stderrText)
		task, _ := s.cp.GetTask(ctx, taskID)
		attempt := 1
		if task != nil {
			attempt = task.Attempts
		}
		validation, errorCalls := s.validateResult(ctx, taskID, workerID, attempt, wr, resultJSON, rawResult, oversize, stdoutText, stderrText)

		// Store error calls if any
		for _, ec := range errorCalls {
			_ = s.cp.AddErrorCall(ctx, taskID, ec)
		}

		// Store validation result
		if verr := s.cp.ValidateResult(ctx, taskID, validation); verr != nil {
			return nil, fmt.Errorf("store validation result: %w", verr)
		}

		// If validation fails, mark as non-retryable failure
		if !validation.Valid {
			success = false
			finishErr = firstNonEmpty(finishErr, "validation failed")
			// #383: the stored envelope must carry the rejection verdict —
			// never the worker's self-reported success. Legitimate failures
			// already carry ok=false; only a self-reported success that
			// failed schema validation needs the rewrite.
			if wr.OK {
				rejected := wr
				rejected.OK = false
				rejected.Response = ""
				rejected.Status = "failed"
				rejected.Reason = firstNonEmpty(wr.Reason, firstOf(validation.SchemaErrors), "result rejected by schema validation")
				rejected.Summary = "worker result failed schema validation"
				resultJSON = mustResultJSON(rejected, stdoutText, stderrText)
			}
		}

		// Perform contract validation
		contractValidation, contractErrorCalls := s.validateContract(ctx, taskID, workerID, attempt, resultJSON)

		// Store contract error calls if any
		for _, ec := range contractErrorCalls {
			_ = s.cp.AddErrorCall(ctx, taskID, ec)
		}

		// Store contract validation result
		if cverr := s.cp.ValidateContract(ctx, taskID, contractValidation); cverr != nil {
			return nil, fmt.Errorf("store contract validation result: %w", cverr)
		}

		// If contract validation fails, mark as non-retryable failure
		if !contractValidation.Valid {
			success = false
			finishErr = firstNonEmpty(finishErr, "contract validation failed")
		}

		// Determine retryable: not retryable if contract violation or validation failed
		hasContractViolation := len(wr.ContractViolation) > 0 || !contractValidation.Valid
		var m map[string]any
		if err := json.Unmarshal(resultJSON, &m); err == nil {
			if req.EffortClass != "" {
				m["effort_class"] = req.EffortClass
			}
			if req.Effort != "" {
				m["effort"] = req.Effort
			}
			if req.EffortRequested != "" {
				m["effort_requested"] = req.EffortRequested
			}
			if req.EffortApplied != "" {
				m["effort_applied"] = req.EffortApplied
			}
			if req.EffortMismatch {
				m["effort_mismatch"] = true
			}
			if req.EffortBudgetTokens > 0 {
				m["effort_budget_tokens"] = req.EffortBudgetTokens
			}
			m["duration"] = durationSec
			m["duration_seconds"] = durationSec
			if wr.ProviderDurationSeconds > 0 {
				m["provider_duration_seconds"] = wr.ProviderDurationSeconds
			}
			if wr.Usage != nil && m["usage"] == nil {
				m["usage"] = wr.Usage
			}
			if success && deliverableDir != "" {
				m["deliverable"] = map[string]any{
					"mode": "worktree",
					"dir":  deliverableDir,
				}
			}
			if toolPolicyEvidence != nil {
				m["tool_policy_evidence"] = toolPolicyEvidence
				m["offered_count"] = toolPolicyEvidence.OfferedCount
				m["denied_count"] = toolPolicyEvidence.DeniedCount
				m["invoked_count"] = toolPolicyEvidence.InvokedCount
			}
			resultJSON = mustJSON(m)
		}
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    resultJSON,
			Success:   success,
			Retryable: !hasContractViolation && validation.Valid,
			Err:       finishErr,
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish attempt: %w", ferr)
		}
	}
	return s.snapshot(ctx, taskID, runDir, promptPath)
}

// reflexGate lazily creates the Jev gate for the L3 post-run quality check
// (#253/#371/#418). Cached per Supervisor to reuse the HTTP client and key pool.
