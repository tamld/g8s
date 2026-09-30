package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/worker"
)

// CategorySelfAudit classifies harness self-audit probes (#451).
const CategorySelfAudit ProbeCategory = "self-audit"

// RecoveredRunStreamShape encodes the real incident #443 stream shape:
// mid-stream boilerplate echoing refusal phrasing, followed by a SUCCESS
// result event with a clean final deliverable response.
const RecoveredRunStreamShape = "{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"diff --git a/internal/worker/worker_test.go ... text_delta=\\\"This request was blocked by Gemini's filters.\\\"\"}}\n" +
	"{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"--- PASS: TestReadWorkerResultProviderRefusal\"}}\n" +
	"{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"Implemented the repair packet exactly as written. All tests pass.\"}}\n"

type auditChild struct {
	done chan struct{}
}

func (c *auditChild) PID() int                { return 42 }
func (c *auditChild) Done() <-chan struct{}   { return c.done }
func (c *auditChild) WaitCode() int           { return 0 }
func (c *auditChild) Terminate(time.Duration) {}

type streamEchoRunner struct {
	stream string
}

func (r *streamEchoRunner) Spawn(opts worker.SpawnOptions) (worker.Child, error) {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(r.stream))
	}
	done := make(chan struct{})
	close(done)
	return &auditChild{done: done}, nil
}

// AuditRefusalEcho feeds a stream shape through the worker drain pipeline's
// refusal classification path (RunOnce) and asserts the run outcome (#443).
func AuditRefusalEcho(ctx context.Context, stream string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "probe-refusal-echo-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	store, err := controlplane.NewControlPlane(filepath.Join(tmpDir, "cp.db"), nil)
	if err != nil {
		return "", fmt.Errorf("open control plane: %w", err)
	}
	defer store.Close()

	runner := &streamEchoRunner{stream: stream}
	sup := worker.NewSupervisor(store, filepath.Join(tmpDir, "runs"),
		worker.WithRunner(runner),
		worker.WithPollInterval(2*time.Millisecond),
	)

	reqPayload, err := json.Marshal(map[string]any{
		"prompt":      "harness self-audit refusal echo",
		"result_mode": "stdout",
		"timeout":     "10s",
		"add_dirs":    []string{tmpDir},
	})
	if err != nil {
		return "", fmt.Errorf("marshal task payload: %w", err)
	}

	_, err = store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "refusal-echo-audit-task",
		Payload:        reqPayload,
		Model:          "gemini-3.8-flash-high",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "10s",
		AddDirs:        []string{tmpDir},
		MaxAttempts:    1,
	})
	if err != nil {
		return "", fmt.Errorf("submit task: %w", err)
	}

	task, err := sup.RunOnce(ctx, worker.RunOptions{
		WorkerID:     "audit-worker",
		LeaseSeconds: 30,
	})
	if err != nil {
		return "", fmt.Errorf("RunOnce: %w", err)
	}
	if task == nil {
		return "", errors.New("nil task returned from RunOnce")
	}

	var wr struct {
		OK      bool   `json:"ok"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal(task.Result, &wr)

	if !wr.OK || wr.Status != "succeeded" {
		return OutcomeClassBlocked, fmt.Errorf("run classified as status=%q ok=%v reason=%q (want succeeded)", wr.Status, wr.OK, wr.Reason)
	}

	return OutcomeClassCompleted, nil
}

// AuditWorktreeDiscard asserts that an uncommitted dirty worktree survives
// Release(keep=true) (#443-f2, fix in #450).
func AuditWorktreeDiscard(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return OutcomeClassCompleted, nil
	}

	tmpDir, err := os.MkdirTemp("", "probe-worktree-discard-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return "", err
	}

	cmds := [][]string{
		{"git", "init", "-q", "-b", "master"},
		{"git", "config", "user.email", "audit@test"},
		{"git", "config", "user.name", "audit"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "init", "-q"},
	}
	for _, c := range cmds {
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %w (output: %s)", c, err, out)
		}
	}

	pool, err := orchestrator.NewPool(orchestrator.PoolOptions{
		Repo: repoDir,
		Root: filepath.Join(tmpDir, "worktrees"),
	})
	if err != nil {
		return "", fmt.Errorf("new pool: %w", err)
	}

	wt, err := pool.Acquire(ctx, "audit-task")
	if err != nil {
		return "", fmt.Errorf("acquire worktree: %w", err)
	}

	dirtyFile := filepath.Join(wt.Path, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted deliverable"), 0o644); err != nil {
		return "", fmt.Errorf("write uncommitted file: %w", err)
	}

	if err := pool.Release(ctx, wt, true); err != nil {
		return "", fmt.Errorf("release(keep=true): %w", err)
	}

	if _, statErr := os.Stat(wt.Path); os.IsNotExist(statErr) {
		return "", fmt.Errorf("dirty worktree directory %s was removed despite keep=true", wt.Path)
	}
	content, rerr := os.ReadFile(dirtyFile)
	if rerr != nil || string(content) != "uncommitted deliverable" {
		return "", fmt.Errorf("uncommitted file missing or corrupted after Release(keep=true): %v", rerr)
	}

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", wt.Branch)
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("branch %s was deleted on Release(keep=true): %w", wt.Branch, err)
	}

	return OutcomeClassCompleted, nil
}

// AuditSanitizerFidelity verifies dispatch.SanitizeOutput against JSON transport
// corruption cases (#434, fix in #445).
func AuditSanitizerFidelity() error {
	// Case 1: issue repro JSON with public literals and escapes
	c1In := `{"code":"def public_fixture(u):\n    token = False\n    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`
	if out := dispatch.SanitizeOutput(c1In); out != c1In {
		return fmt.Errorf("case 1: want byte-exact identical output, got %q", out)
	}

	// Case 2: escaped quote inside secret preserves JSONL validity
	c2In := `{"type":"item.completed","text":"set password: abc\"def\" here"}`
	c2Out := dispatch.SanitizeOutput(c2In)
	if !strings.Contains(c2Out, "password=<REDACTED>") {
		return fmt.Errorf("case 2: expected password=<REDACTED>, got %q", c2Out)
	}
	if !json.Valid([]byte(c2Out)) {
		return fmt.Errorf("case 2: expected valid JSON, got %q", c2Out)
	}
	if strings.Contains(c2Out, `abc"def"`) || strings.Contains(c2Out, `abc\"def\"`) {
		return fmt.Errorf("case 2: secret value leaked in %q", c2Out)
	}
	if strings.Contains(c2Out, "example-value-77") {
		return fmt.Errorf("case 2: unexpected example-value-77 in %q", c2Out)
	}

	// Case 3: source_lines array with public literal
	c3In := `{"source_lines":["def public_fixture(u):","    token = False","    return u.password is None and token is False"]}`
	if out := dispatch.SanitizeOutput(c3In); out != c3In {
		return fmt.Errorf("case 3: want byte-exact unchanged output, got %q", out)
	}

	// Case 4: public literal keeps following escape and content
	c4In := `{"code":"token = False\ngood = True"}`
	if out := dispatch.SanitizeOutput(c4In); out != c4In {
		return fmt.Errorf("case 4: want byte-exact unchanged output, got %q", out)
	}

	// Case 5: public literal function arguments unchanged
	c5In := `{"code":"x = f(token=False, retries=0, timeout=None)"}`
	if out := dispatch.SanitizeOutput(c5In); out != c5In {
		return fmt.Errorf("case 5: want byte-exact unchanged output, got %q", out)
	}

	// Case 6: non-public secret followed by escape is redacted and line stays valid JSON
	c6In := `{"cred":"credential=example-value-77\nnext chunk"}`
	c6Out := dispatch.SanitizeOutput(c6In)
	if !strings.Contains(c6Out, "credential=<REDACTED>") && !strings.Contains(c6Out, "cred=<REDACTED>") {
		return fmt.Errorf("case 6: expected credential redaction marker, got %q", c6Out)
	}
	if strings.Contains(c6Out, "example-value-77") {
		return fmt.Errorf("case 6: secret value example-value-77 leaked in %q", c6Out)
	}
	if !json.Valid([]byte(c6Out)) {
		return fmt.Errorf("case 6: expected valid JSON, got %q", c6Out)
	}

	// Case 7: URL fully redacted preserving trailing escape
	c7In := "{\"dsn\":\"postgresql://user:pass@db.example.com/sales\\n\"}"
	c7Out := dispatch.SanitizeOutput(c7In)
	if !strings.Contains(c7Out, "postgresql://<REDACTED>") {
		return fmt.Errorf("case 7: expected postgresql://<REDACTED>, got %q", c7Out)
	}
	if strings.Contains(c7Out, "user:pass") {
		return fmt.Errorf("case 7: leaked credentials user:pass in %q", c7Out)
	}
	if !strings.HasSuffix(c7Out, `\n"}`) {
		return fmt.Errorf("case 7: trailing \\n escape not preserved in %q", c7Out)
	}
	if !json.Valid([]byte(c7Out)) {
		return fmt.Errorf("case 7: expected valid JSON, got %q", c7Out)
	}

	// Case 8: escaped quotes around the value normalized
	c8In := `password:\"example-value-77\"`
	if out := dispatch.SanitizeOutput(c8In); out != "password=<REDACTED>" {
		return fmt.Errorf("case 8: want password=<REDACTED>, got %q", out)
	}

	return nil
}

// VerifyReceiptPathScope verifies whether candidatePath satisfies the declared
// allowed_paths glob patterns per zero-trust confinement (#359/#366).
func VerifyReceiptPathScope(allowed []string, candidatePath string) bool {
	if len(allowed) == 0 {
		return true
	}
	cleanFile := filepath.ToSlash(filepath.Clean(candidatePath))
	if cleanFile == ".." || strings.HasPrefix(cleanFile, "../") {
		return false
	}
	if strings.HasPrefix(cleanFile, "/") || isWindowsDrivePath(cleanFile) {
		return false
	}
	for _, pattern := range allowed {
		if pathMatches(cleanFile, pattern) {
			return true
		}
	}
	return false
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	d := path[0]
	return (d >= 'a' && d <= 'z') || (d >= 'A' && d <= 'Z')
}

func pathMatches(cleanFile, pattern string) bool {
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	if cleanFile == cleanPattern {
		return true
	}
	if strings.Contains(cleanPattern, "**") {
		return globstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}
	m, _ := filepath.Match(cleanPattern, cleanFile)
	return m
}

func globstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if globstarMatch(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := filepath.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return globstarMatch(fileSegs[1:], patSegs[1:])
}

// AuditReceiptBypass verifies that receipt path-scope enforcement rejects
// out-of-scope paths and accepts in-scope paths (#359/#366).
func AuditReceiptBypass(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "probe-receipt-bypass-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return "", fmt.Errorf("create receipt manager: %w", err)
	}
	defer mgr.Close()

	glob := "internal/harness/*"
	rc, err := mgr.IssueReceipt("brain-orchestrator", []string{glob}, 600*time.Second,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "probe-default",
		}),
		receipt.WithProvenanceContext("g8s/eval", "HEAD", "probe-receipt-bypass"),
	)
	if err != nil {
		return "", fmt.Errorf("issue receipt: %w", err)
	}

	verified, err := mgr.VerifyReceipt(rc.ReceiptID)
	if err != nil {
		return "", fmt.Errorf("verify receipt: %w", err)
	}
	if len(verified.AllowedPaths) != 1 || verified.AllowedPaths[0] != glob {
		return "", fmt.Errorf("unexpected allowed_paths: %v", verified.AllowedPaths)
	}

	// In-scope candidate: must PASS
	inScopePath := "internal/harness/probe.go"
	if !VerifyReceiptPathScope(verified.AllowedPaths, inScopePath) {
		return "", fmt.Errorf("in-scope path %q was incorrectly rejected", inScopePath)
	}

	// Out-of-scope candidates: must all REJECT
	outScopePaths := []string{
		"cmd/g8s/main.go",
		"internal/secret/key.pem",
		"/etc/passwd",
		"../../etc/shadow",
	}
	for _, p := range outScopePaths {
		if VerifyReceiptPathScope(verified.AllowedPaths, p) {
			return "", fmt.Errorf("out-of-scope path %q was incorrectly accepted", p)
		}
	}

	// Correlate with System 1 Reflex Policy Gate
	gate := reflex.NewReflexGate()
	signal := reflex.ReflexSignal{RiskScore: 1.0, BreachProb: 0.05, Confidence: 0.9}

	verdictIn := gate.EvaluatePolicy(signal, reflex.TriageRequest{
		AllowedPaths:  verified.AllowedPaths,
		FilesModified: []string{inScopePath},
	})
	if verdictIn.Action != reflex.ActionGrantReceipt {
		return "", fmt.Errorf("reflex gate rejected in-scope path %q: action=%s reason=%s", inScopePath, verdictIn.Action, verdictIn.Reason)
	}

	verdictOut := gate.EvaluatePolicy(signal, reflex.TriageRequest{
		AllowedPaths:  verified.AllowedPaths,
		FilesModified: []string{"cmd/g8s/main.go"},
	})
	if verdictOut.Action == reflex.ActionGrantReceipt {
		return "", fmt.Errorf("reflex gate accepted out-of-scope path cmd/g8s/main.go")
	}

	// Atomically consume receipt
	consumed, err := mgr.ValidateAndConsume(rc.ReceiptID, "worker-task-1")
	if err != nil {
		return "", fmt.Errorf("consume receipt: %w", err)
	}
	if !consumed.Consumed {
		return "", errors.New("receipt not marked consumed after ValidateAndConsume")
	}

	return OutcomeClassCompleted, nil
}

// NewRefusalEchoProbe constructs the refusal-echo self-audit probe (#443).
func NewRefusalEchoProbe() *Probe {
	return &Probe{
		ID:              "sa-001",
		Category:        CategorySelfAudit,
		Name:            "refusal-echo",
		Description:     "Incident #443: refusal-classifier echo poisoning — mid-stream boilerplate echo must not block completed runs with clean final responses",
		Input:           RecoveredRunStreamShape,
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditRefusalEcho(ctx, RecoveredRunStreamShape)
		},
	}
}

// NewWorktreeDiscardProbe constructs the worktree-discard self-audit probe (#443-f2, #450).
func NewWorktreeDiscardProbe() *Probe {
	return &Probe{
		ID:              "sa-002",
		Category:        CategorySelfAudit,
		Name:            "worktree-discard",
		Description:     "Incident #443-f2 (#450): dirty worktree destroyed by Pool.Release — uncommitted dirty worktree must survive Release(keep=true)",
		Input:           "dirty worktree fixture Release(keep=true)",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditWorktreeDiscard(ctx)
		},
	}
}

// NewSanitizerFidelityProbe constructs the sanitizer-fidelity self-audit probe (#434, #445).
func NewSanitizerFidelityProbe() *Probe {
	return &Probe{
		ID:              "sa-003",
		Category:        CategorySelfAudit,
		Name:            "sanitizer-fidelity",
		Description:     "Incident #434 (#445): JSON transport corruption — SanitizeOutput must preserve public literals and transport escape pairs",
		Input:           "sanitizer JSON transport fidelity matrix",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			if err := AuditSanitizerFidelity(); err != nil {
				return "", err
			}
			return OutcomeClassCompleted, nil
		},
	}
}

// NewReceiptBypassProbe constructs the receipt-bypass self-audit probe (#359, #366).
func NewReceiptBypassProbe() *Probe {
	return &Probe{
		ID:              "sa-004",
		Category:        CategorySelfAudit,
		Name:            "receipt-bypass",
		Description:     "Incident class: write outside receipt scope (#359/#366) — verify receipt path-scope enforcement rejects out-of-scope paths and accepts in-scope paths",
		Input:           "receipt path-scope verification fixture",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditReceiptBypass(ctx)
		},
	}
}

// SelfAuditProbes returns the 4 harness self-audit probes (#451).
func SelfAuditProbes() []*Probe {
	return []*Probe{
		NewRefusalEchoProbe(),
		NewWorktreeDiscardProbe(),
		NewSanitizerFidelityProbe(),
		NewReceiptBypassProbe(),
	}
}
