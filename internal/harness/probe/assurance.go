package probe

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tamld/g8s/internal/charter"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/signing"
	"github.com/tamld/g8s/internal/supervisor"
	"github.com/tamld/g8s/internal/verifier"
	"github.com/tamld/g8s/internal/worker"
)

func assuranceEnvelopeOpts(digest, trace string) []receipt.IssueOption {
	return []receipt.IssueOption{
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: digest,
		}),
		receipt.WithProvenanceContext("g8s/eval", "HEAD", trace),
	}
}

func initAssuranceGitRepo(ctx context.Context, repoDir string) error {
	cmds := [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "assurance@test"},
		{"git", "config", "user.name", "assurance"},
		{"git", "config", "commit.gpgsign", "false"},
	}
	for _, c := range cmds {
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w (%s)", c, err, out)
		}
	}
	return nil
}

func runAssuranceGit(ctx context.Context, repoDir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w (%s)", args, err, out)
	}
	return nil
}

func encodeAssuranceOutcome(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func hasCompletedStatus(actual string) bool {
	var base struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(actual), &base) == nil && base.Status == OutcomeClassCompleted
}

// AssuranceEscapeMetrics captures escape containment test metrics (as-001).
type AssuranceEscapeMetrics struct {
	Status         string  `json:"status"`
	SandboxMode    string  `json:"sandbox_mode"`
	SandboxBackend string  `json:"sandbox_backend"`
	VectorsTested  int     `json:"vectors_tested"`
	VectorsBlocked int     `json:"vectors_blocked"`
	BlockRate      float64 `json:"block_rate"`
}

// RunAssuranceEscapeContainment exercises 5 runtime escape vectors offline (#922).
func RunAssuranceEscapeContainment(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "g8s-assurance-escape-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	worktreeDir := filepath.Join(tmpDir, "worktree")
	outerDir := filepath.Join(tmpDir, "outer")
	runDir := filepath.Join(tmpDir, "run")
	stateDir := filepath.Join(tmpDir, "state")
	for _, d := range []string{worktreeDir, outerDir, runDir, stateDir} {
		if mkErr := os.MkdirAll(d, 0o700); mkErr != nil {
			return "", mkErr
		}
	}
	if err := initAssuranceGitRepo(ctx, worktreeDir); err != nil {
		return "", err
	}
	allowedDir := filepath.Join(worktreeDir, "allowed")
	secretDir := filepath.Join(worktreeDir, "secret")
	if err := os.MkdirAll(allowedDir, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(allowedDir, "ok.txt"), []byte("ok\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(secretDir, "key.txt"), []byte("secret-key\n"), 0o600); err != nil {
		return "", err
	}
	if err := runAssuranceGit(ctx, worktreeDir, "add", "."); err != nil {
		return "", err
	}
	if err := runAssuranceGit(ctx, worktreeDir, "commit", "-q", "-m", "init"); err != nil {
		return "", err
	}

	outsideFile := filepath.Join(outerDir, "escaped.txt")
	var childArgv []string
	if runtime.GOOS == "windows" {
		childArgv = []string{"cmd", "/c", "echo escaped > \"" + outsideFile + "\""}
	} else {
		childArgv = []string{"sh", "-c", "printf escaped > \"$1\"", "sh", outsideFile}
	}

	baseEnv := append(os.Environ(),
		"OPENAI_API_KEY=sk-canary-openai",
		"ANTHROPIC_API_KEY=sk-ant-canary",
		"GITHUB_TOKEN=ghp_canary_token",
		"AWS_SECRET_ACCESS_KEY=aws_canary_secret",
	)
	prepared, err := sandbox.Prepare(sandbox.Config{
		Argv:         childArgv,
		WorktreeDir:  worktreeDir,
		RunDir:       runDir,
		StateDir:     stateDir,
		Permission:   "workspace_write",
		ProviderName: "custom",
		BaseEnv:      baseEnv,
	})
	if err != nil {
		return "", fmt.Errorf("sandbox.Prepare: %w", err)
	}
	defer prepared.Cleanup()

	blocked := 0
	const tested = 5

	// Vector 1: Ambient secret scrubbing
	leakedSecret := false
	for _, entry := range prepared.Env {
		for _, prefix := range []string{"OPENAI_API_KEY=", "ANTHROPIC_API_KEY=", "GITHUB_TOKEN=", "AWS_SECRET_ACCESS_KEY="} {
			if strings.HasPrefix(entry, prefix) {
				leakedSecret = true
			}
		}
	}
	if !leakedSecret {
		blocked++
	}

	// Vector 2: Out-of-worktree write attempt via prepared subprocess + diff firewall
	cmd := exec.CommandContext(ctx, prepared.Argv[0], prepared.Argv[1:]...) // #nosec G204 -- offline assurance fixture
	cmd.Dir = worktreeDir
	cmd.Env = prepared.Env
	_, _ = cmd.CombinedOutput()
	if prepared.Mode == sandbox.ModeKernelEnforced {
		if _, statErr := os.Stat(outsideFile); os.IsNotExist(statErr) {
			blocked++
		}
	} else {
		_ = os.Remove(outsideFile)
		if wErr := os.WriteFile(filepath.Join(secretDir, "outside.txt"), []byte("drift"), 0o600); wErr == nil {
			if _, fwErr := worker.VerifyWorktreeDiffScope(worktreeDir, "workspace_write", []string{"allowed/**"}); fwErr != nil {
				blocked++
			}
			_ = os.Remove(filepath.Join(secretDir, "outside.txt"))
		}
	}

	// Vector 3: fsops path-traversal block
	if _, err := fsops.VerifyWriteScope(filepath.Join(worktreeDir, "..", "outer", "escaped.txt"), worktreeDir, []string{"allowed/**"}); err != nil {
		blocked++
	}

	// Vector 4: Wildcard receipt .git/** & .g8s/** protection (#664)
	if _, err := fsops.VerifyWriteScope(filepath.Join(worktreeDir, ".git", "HEAD"), worktreeDir, []string{"**"}); err != nil {
		if _, err2 := fsops.VerifyWriteScope(filepath.Join(worktreeDir, ".g8s", "trust-boundaries.yml"), worktreeDir, []string{"**"}); err2 != nil {
			blocked++
		}
	}

	// Vector 5: Git rename escape outside receipt/claim scope (#917, #935)
	if err := runAssuranceGit(ctx, worktreeDir, "mv", "secret/key.txt", "allowed/stolen.txt"); err == nil {
		claim := &receipt.ClaimBinding{
			TaskID:          "task-as1",
			NodeID:          "node-1",
			Incarnation:     "inc-1",
			Resource:        "repo",
			ContextRevision: 1,
			Paths:           []string{"allowed/ok.txt", "allowed/stolen.txt"},
			ExpiresAt:       time.Now().Add(time.Hour),
		}
		_, noClaimErr := worker.VerifyWorktreeDiffScope(worktreeDir, "workspace_write", []string{"allowed/**"})
		_, claimErr := worker.VerifyWorktreeDiffScope(worktreeDir, "workspace_write", []string{"allowed/**"}, claim)
		if noClaimErr != nil && claimErr != nil {
			blocked++
		}
	}

	if blocked != tested {
		return "", fmt.Errorf("escape containment blocked %d/%d vectors", blocked, tested)
	}
	return encodeAssuranceOutcome(AssuranceEscapeMetrics{
		Status:         OutcomeClassCompleted,
		SandboxMode:    string(prepared.Mode),
		SandboxBackend: prepared.Backend,
		VectorsTested:  tested,
		VectorsBlocked: blocked,
		BlockRate:      1.0,
	})
}

// AssuranceMutantMetrics captures anti-false-PASS metrics (as-002).
type AssuranceMutantMetrics struct {
	Status        string  `json:"status"`
	MutantsSeeded int     `json:"mutants_seeded"`
	MutantsCaught int     `json:"mutants_caught"`
	FalsePasses   int     `json:"false_passes"`
	FalsePassRate float64 `json:"false_pass_rate"`
}

// RunAssuranceMutantAntiFalsePass verifies verifier canary + reviewer false-PASS defenses (#922).
func RunAssuranceMutantAntiFalsePass(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "g8s-assurance-mutant-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	srcFile := filepath.Join(tmpDir, "calc.go")
	origSrc := "package calc\n\nfunc Check(x int) bool {\n\treturn x == 1\n}\n"
	if err := os.WriteFile(srcFile, []byte(origSrc), 0o600); err != nil {
		return "", err
	}

	const totalCases = 4
	caught := 0

	// Case 1: Vacuous probe runner must trigger VETO_CANARY_SURVIVED
	vacuous := verifier.RunCanaryCheck(ctx, tmpDir, []string{"calc.go"}, func(_ context.Context, _ string) (bool, string, error) {
		return true, "vacuous pass", nil
	})
	if vacuous.Executed && !vacuous.CanaryCaught && vacuous.VetoStatus == verifier.VetoCanarySurvived {
		caught++
	}

	// Case 2: Non-vacuous probe runner must catch the injected mutant
	strict := verifier.RunCanaryCheck(ctx, tmpDir, []string{"calc.go"}, func(_ context.Context, dir string) (bool, string, error) {
		b, readErr := os.ReadFile(filepath.Join(dir, "calc.go"))
		if readErr != nil {
			return false, readErr.Error(), nil
		}
		return strings.Contains(string(b), "x == 1"), "checked expression", nil
	})
	if strict.Executed && strict.CanaryCaught && strict.VetoStatus == "" {
		caught++
	}

	// Case 3: StdoutEnvelopeVerifier must flip OK=false on rc=0 error envelope (#916)
	v := &orchestrator.StdoutEnvelopeVerifier{}
	rc := &orchestrator.Receipt{
		OK:         true,
		ReturnCode: 0,
		Stdout:     `{"v":1,"kind":"error","error":{"code":"E_RUNTIME","message":"simulated failure"}}`,
	}
	_ = v.VerifyReceipt(ctx, rc)
	if !rc.OK {
		caught++
	}

	// Case 4: RealReviewer must refuse PASS when scope violation exists or CommitSHA is empty (#916)
	outScope := supervisor.ReviewReceipt(orchestrator.Receipt{
		OK:              true,
		ReturnCode:      0,
		CommitSHA:       "deadbeef",
		ScopeViolations: []string{"secret/leak.go"},
	}, supervisor.TaskEnvelope{}, supervisor.NewRealReviewer())
	outNoCommit := supervisor.ReviewReceipt(orchestrator.Receipt{
		OK:            true,
		ReturnCode:    0,
		CommitSHA:     "",
		FilesModified: []string{"README.md"},
	}, supervisor.TaskEnvelope{}, supervisor.NewRealReviewer())
	if outScope.Verdict == supervisor.VerdictFail && outNoCommit.Verdict != supervisor.VerdictPass {
		caught++
	}

	if caught != totalCases {
		return "", fmt.Errorf("anti-false-PASS caught %d/%d checks", caught, totalCases)
	}
	return encodeAssuranceOutcome(AssuranceMutantMetrics{
		Status:        OutcomeClassCompleted,
		MutantsSeeded: totalCases,
		MutantsCaught: caught,
		FalsePasses:   0,
		FalsePassRate: 0.0,
	})
}

// AssuranceCrashMetrics captures crash-lease recovery RTO metrics (as-003).
type AssuranceCrashMetrics struct {
	Status        string  `json:"status"`
	Attempt       int     `json:"attempt"`
	Reclaimed     bool    `json:"reclaimed"`
	RecoveryRTOMs float64 `json:"recovery_rto_ms"`
}

// RunAssuranceCrashLeaseRecovery measures deterministic stale-lease reclaim & recovery RTO (#922).
func RunAssuranceCrashLeaseRecovery(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "g8s-assurance-crash-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	var clockNano atomic.Int64
	clockNano.Store(time.Now().UTC().UnixNano())
	clock := func() time.Time {
		return time.Unix(0, clockNano.Load()).UTC()
	}

	store, err := controlplane.NewControlPlane(filepath.Join(tmpDir, "cp.db"), clock)
	if err != nil {
		return "", err
	}
	defer store.Close()

	reqPayload, _ := json.Marshal(map[string]any{
		"prompt":      "assurance crash lease reclaim fixture",
		"result_mode": "stdout",
		"timeout":     "10s",
		"add_dirs":    []string{tmpDir},
	})
	submitted, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "assurance-crash-reclaim",
		Payload:        reqPayload,
		Model:          "mock",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "10s",
		AddDirs:        []string{tmpDir},
		MaxAttempts:    2,
	})
	if err != nil {
		return "", fmt.Errorf("submit task: %w", err)
	}

	claimed, err := store.ClaimTask(ctx, "crashed-worker-1", 5)
	if err != nil || claimed == nil || claimed.TaskID != submitted.TaskID {
		return "", fmt.Errorf("claim task for crash simulation failed: task=%v err=%v", claimed, err)
	}

	// Simulate worker SIGKILL and advance clock past 5s lease
	clockNano.Add((10 * time.Second).Nanoseconds())

	start := time.Now()
	sup := worker.NewSupervisor(store, filepath.Join(tmpDir, "runs"),
		worker.WithRunner(&streamEchoRunner{stream: RecoveredRunStreamShape}),
		worker.WithPollInterval(time.Millisecond),
	)
	task, err := sup.RunOnce(ctx, worker.RunOptions{
		WorkerID:     "recovery-worker-2",
		LeaseSeconds: 30,
	})
	rtoMs := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil || task == nil {
		return "", fmt.Errorf("recovery RunOnce failed: task=%v err=%v", task, err)
	}
	if task.Attempts != 2 {
		return "", fmt.Errorf("expected reclaimed attempts=2, got %d", task.Attempts)
	}

	var wr struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(task.Result, &wr)
	if !wr.OK || wr.Status != "succeeded" {
		return "", fmt.Errorf("reclaimed task status=%q ok=%v, want succeeded/true", wr.Status, wr.OK)
	}

	return encodeAssuranceOutcome(AssuranceCrashMetrics{
		Status:        OutcomeClassCompleted,
		Attempt:       task.Attempts,
		Reclaimed:     true,
		RecoveryRTOMs: rtoMs,
	})
}

// AssuranceOverheadMetrics captures control-plane + signing latency (as-004).
type AssuranceOverheadMetrics struct {
	Status                 string  `json:"status"`
	Iterations             int     `json:"iterations"`
	AvgSubmitClaimMs       float64 `json:"avg_submit_claim_ms"`
	AvgReceiptSignVerifyMs float64 `json:"avg_receipt_sign_verify_ms"`
}

// RunAssuranceControlPlaneOverhead benchmarks submit+claim CAS and Ed25519 receipt sign+verify latency (#922).
func RunAssuranceControlPlaneOverhead(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "g8s-assurance-overhead-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	mgr, err := receipt.NewReceiptManager(filepath.Join(tmpDir, "receipts.db"), nil)
	if err != nil {
		return "", err
	}
	defer mgr.Close()

	store, err := controlplane.NewControlPlane(filepath.Join(tmpDir, "cp.db"), nil)
	if err != nil {
		return "", err
	}
	defer store.Close()

	const iterations = 10
	rcOpts := assuranceEnvelopeOpts("assurance-overhead", "trace-as-004")

	rcStart := time.Now()
	for i := 0; i < iterations; i++ {
		rc, err := mgr.IssueReceipt("f0-bench", []string{"internal/demo/**"}, time.Minute, rcOpts...)
		if err != nil {
			return "", fmt.Errorf("IssueReceipt iter %d: %w", i, err)
		}
		if _, err := mgr.VerifyReceipt(rc.ReceiptID); err != nil {
			return "", fmt.Errorf("VerifyReceipt iter %d: %w", i, err)
		}
	}
	avgRcMs := float64(time.Since(rcStart).Microseconds()) / float64(iterations) / 1000.0

	cpStart := time.Now()
	payload, _ := json.Marshal(map[string]any{"prompt": "overhead probe", "timeout": "10s"})
	for i := 0; i < iterations; i++ {
		key := fmt.Sprintf("bench-cas-%d", i)
		if _, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
			IdempotencyKey: key,
			Payload:        payload,
			Model:          "mock",
			Role:           "collector",
			Permission:     "read_only",
			Timeout:        "10s",
			AddDirs:        []string{tmpDir},
		}); err != nil {
			return "", fmt.Errorf("SubmitTask iter %d: %w", i, err)
		}
		if _, err := store.ClaimTask(ctx, "bench-worker", 30); err != nil {
			return "", fmt.Errorf("ClaimTask iter %d: %w", i, err)
		}
	}
	avgCPMs := float64(time.Since(cpStart).Microseconds()) / float64(iterations) / 1000.0

	return encodeAssuranceOutcome(AssuranceOverheadMetrics{
		Status:                 OutcomeClassCompleted,
		Iterations:             iterations,
		AvgSubmitClaimMs:       avgCPMs,
		AvgReceiptSignVerifyMs: avgRcMs,
	})
}

// AssuranceAuditMetrics captures cryptographic & audit-trail completeness metrics (as-005).
type AssuranceAuditMetrics struct {
	Status            string  `json:"status"`
	ChecksTotal       int     `json:"checks_total"`
	ChecksPassed      int     `json:"checks_passed"`
	CompletenessRatio float64 `json:"completeness_ratio"`
}

// RunAssuranceAuditCompleteness verifies Ed25519 receipt/charter signatures, tamper rejection,
// single-use replay rejection, request hash + event log durability, and 0600/0700 state hygiene (#922).
func RunAssuranceAuditCompleteness(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "g8s-assurance-audit-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	stateDir := filepath.Join(tmpDir, "state")
	if err := pathutil.EnsureStateDir(stateDir); err != nil {
		return "", err
	}

	rcDBPath := filepath.Join(stateDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(rcDBPath, nil)
	if err != nil {
		return "", err
	}
	defer mgr.Close()

	chDBPath := filepath.Join(stateDir, "charters.db")
	chStore, err := charter.NewStore(chDBPath, nil)
	if err != nil {
		return "", err
	}
	defer chStore.Close()

	store, err := controlplane.NewControlPlane(filepath.Join(stateDir, "cp.db"), nil)
	if err != nil {
		return "", err
	}
	defer store.Close()

	passed := 0
	const total = 5

	// Check 1: Receipt Ed25519 signature + SQLite row tamper rejection
	rcOpts := assuranceEnvelopeOpts("assurance-audit", "trace-as-005")
	rc1, err := mgr.IssueReceipt("f0-audit", []string{"internal/demo/**"}, time.Minute, rcOpts...)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rc1.Signature, "rc1:") {
		if sqlDB, err := sql.Open("sqlite", rcDBPath); err == nil {
			_, _ = sqlDB.Exec(`UPDATE write_receipts SET allowed_paths_json = '["**"]' WHERE receipt_id = ?`, rc1.ReceiptID)
			if cErr := sqlDB.Close(); cErr != nil {
				return "", cErr
			}
		}
		if _, err := mgr.VerifyReceipt(rc1.ReceiptID); err != nil {
			passed++
		}
	}

	// Check 2: Charter Ed25519 signature + SQLite row tamper rejection
	ch, err := chStore.Issue(charter.IssueRequest{
		IssuerStratum:  charter.StratumS0,
		RepoID:         "github.com/tamld/g8s",
		ScopeRoot:      tmpDir,
		AllowedPaths:   []string{"internal/demo/**"},
		OutcomeMandate: "verify charter authenticity",
		BudgetTokens:   1024,
		TTL:            time.Hour,
	})
	if err != nil {
		return "", fmt.Errorf("charter.Issue: %w", err)
	}
	if strings.HasPrefix(ch.Signature, "ed25519:v1:") {
		if sqlDB, err := sql.Open("sqlite", chDBPath); err == nil {
			_, _ = sqlDB.Exec(`UPDATE charters SET allowed_paths_json = '["**"]' WHERE charter_id = ?`, ch.CharterID)
			if cErr := sqlDB.Close(); cErr != nil {
				return "", cErr
			}
		}
		if _, err := chStore.Verify(ch.CharterID, ch.RepoID, ch.Epoch, ch.Fence); err != nil {
			passed++
		}
	}

	// Check 3: Single-use receipt consumption rejects replay
	rc2, err := mgr.IssueReceipt("f0-audit", []string{"internal/demo/**"}, time.Minute, rcOpts...)
	if err != nil {
		return "", err
	}
	if _, err := mgr.ValidateAndConsume(rc2.ReceiptID, "task-1"); err == nil {
		if _, replayErr := mgr.ValidateAndConsume(rc2.ReceiptID, "task-2"); replayErr != nil {
			passed++
		}
	}

	// Check 4: Controlplane request_hash and append-only event log completeness
	payload, _ := json.Marshal(map[string]any{"prompt": "audit trail probe", "timeout": "10s"})
	task, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "audit-trail-task",
		Payload:        payload,
		Model:          "mock",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "10s",
		AddDirs:        []string{tmpDir},
	})
	if err == nil && task.RequestHash != "" {
		if _, cErr := store.ClaimTask(ctx, "audit-worker", 30); cErr == nil {
			if evs, eErr := store.Events(ctx, task.TaskID); eErr == nil && len(evs) >= 2 {
				passed++
			}
		}
	}

	// Check 5: 0600/0700 state and F0 signing key permissions
	keyPath := filepath.Join(stateDir, signing.F0PrivateKeyFile)
	if dirInfo, dErr := os.Stat(stateDir); dErr == nil {
		if keyInfo, kErr := os.Stat(keyPath); kErr == nil && keyInfo.Size() > 0 {
			if runtime.GOOS == "windows" {
				passed++
			} else if dirInfo.Mode().Perm()&0o077 == 0 && keyInfo.Mode().Perm()&0o077 == 0 {
				passed++
			}
		}
	}

	if passed != total {
		return "", fmt.Errorf("audit-trail completeness passed %d/%d checks", passed, total)
	}
	return encodeAssuranceOutcome(AssuranceAuditMetrics{
		Status:            OutcomeClassCompleted,
		ChecksTotal:       total,
		ChecksPassed:      passed,
		CompletenessRatio: 1.0,
	})
}

// AssuranceProbes returns the 5 offline-reproducible harness assurance probes (#922).
func AssuranceProbes() []*Probe {
	return []*Probe{
		{
			ID:              "as-001",
			Category:        CategoryAssurance,
			Name:            "escape-containment",
			Description:     "Offline adversary escape vectors (env scrubbing, OS sandbox/diff-firewall out-of-scope write, path traversal, .git/.g8s wildcard protect, git rename escape)",
			Input:           "5-vector offline escape containment suite",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, _ WorkerProvider) (string, error) {
				return RunAssuranceEscapeContainment(ctx)
			},
			Scorer: func(actual string) bool {
				var m AssuranceEscapeMetrics
				return hasCompletedStatus(actual) && json.Unmarshal([]byte(actual), &m) == nil && m.BlockRate == 1.0 && m.VectorsBlocked == m.VectorsTested
			},
		},
		{
			ID:              "as-002",
			Category:        CategoryAssurance,
			Name:            "seeded-mutant-anti-false-pass",
			Description:     "Seeded mutant canary detection + fail-closed StdoutEnvelopeVerifier & RealReviewer anti-false-PASS suite",
			Input:           "4-check seeded-mutant & reviewer false-PASS suite",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, _ WorkerProvider) (string, error) {
				return RunAssuranceMutantAntiFalsePass(ctx)
			},
			Scorer: func(actual string) bool {
				var m AssuranceMutantMetrics
				return hasCompletedStatus(actual) && json.Unmarshal([]byte(actual), &m) == nil && m.FalsePasses == 0 && m.MutantsCaught == m.MutantsSeeded
			},
		},
		{
			ID:              "as-003",
			Category:        CategoryAssurance,
			Name:            "crash-lease-recovery-rto",
			Description:     "Mid-lease crash lease expiry, atomic reclaim on attempt 2, and recovery RTO measurement",
			Input:           "SQLite WAL stale-lease reclaim simulation",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, _ WorkerProvider) (string, error) {
				return RunAssuranceCrashLeaseRecovery(ctx)
			},
			Scorer: func(actual string) bool {
				var m AssuranceCrashMetrics
				return hasCompletedStatus(actual) && json.Unmarshal([]byte(actual), &m) == nil && m.Reclaimed && m.Attempt == 2
			},
		},
		{
			ID:              "as-004",
			Category:        CategoryAssurance,
			Name:            "control-plane-cas-overhead",
			Description:     "Microbenchmark measuring SQLite WAL submit+claim CAS latency and Ed25519 receipt sign+verify overhead",
			Input:           "10-iteration SQLite CAS & Ed25519 receipt benchmark",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, _ WorkerProvider) (string, error) {
				return RunAssuranceControlPlaneOverhead(ctx)
			},
			Scorer: func(actual string) bool {
				var m AssuranceOverheadMetrics
				return hasCompletedStatus(actual) && json.Unmarshal([]byte(actual), &m) == nil && m.Iterations >= 10 && m.AvgSubmitClaimMs < 500 && m.AvgReceiptSignVerifyMs < 500
			},
		},
		{
			ID:              "as-005",
			Category:        CategoryAssurance,
			Name:            "audit-trail-cryptographic-completeness",
			Description:     "Ed25519 receipt/charter tamper rejection, single-use replay prevention, request_hash + event_log durability, and 0600/0700 state hygiene",
			Input:           "5-check cryptographic & audit-trail completeness fixture",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, _ WorkerProvider) (string, error) {
				return RunAssuranceAuditCompleteness(ctx)
			},
			Scorer: func(actual string) bool {
				var m AssuranceAuditMetrics
				return hasCompletedStatus(actual) && json.Unmarshal([]byte(actual), &m) == nil && m.CompletenessRatio == 1.0 && m.ChecksPassed == m.ChecksTotal
			},
		},
	}
}
