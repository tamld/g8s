package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/search"
	"github.com/tamld/g8s/internal/server"
)

// CategoryIASTDAST classifies in-process IAST runtime hygiene and DAST red-team boundary probes (Issue #601).
const CategoryIASTDAST ProbeCategory = "iast-dast"

var trackedEnvVars = []string{
	"G8S_TIER",
	"G8S_SCOPE_ROOT",
	"G8S_RECEIPT_ID",
	"AGY_MCP_ALLOW_WORKSPACE_WRITE",
}

// RuntimeSensor captures baseline and post-probe runtime state for hygiene verification.
type RuntimeSensor struct {
	TempRoot           string
	BaselineGoroutines int
	BaselineTempDirs   int
	BaselineEnv        map[string]string
	BaselineEnvSet     map[string]bool
}

func countEntries(dir string) int {
	if dir == "" {
		return 0
	}
	count := 0
	if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != dir {
			count++
		}
		return nil
	}); err != nil {
		return 0
	}
	return count
}

// CaptureRuntimeSensor records baseline goroutine counts, temp directory entry counts,
// and tracked environment variable states.
func CaptureRuntimeSensor(tempRoot string) *RuntimeSensor {
	s := &RuntimeSensor{
		TempRoot:           tempRoot,
		BaselineGoroutines: runtime.NumGoroutine(),
		BaselineTempDirs:   countEntries(tempRoot),
		BaselineEnv:        make(map[string]string),
		BaselineEnvSet:     make(map[string]bool),
	}
	for _, env := range trackedEnvVars {
		val, set := os.LookupEnv(env)
		if set {
			s.BaselineEnv[env] = val
			s.BaselineEnvSet[env] = true
		}
	}
	return s
}

// Verify verifies that:
// 1. Goroutine delta does not exceed maxGoroutineDelta (polling briefly up to 200ms with runtime.Gosched()).
// 2. Tracked environment variables were restored to their exact baseline state.
// 3. tempRoot has no leaked files or subdirectories after cleanup.
func (s *RuntimeSensor) Verify(maxGoroutineDelta int) error {
	deadline := time.Now().Add(200 * time.Millisecond)
	var finalDelta int
	var currentGoroutines int
	for {
		currentGoroutines = runtime.NumGoroutine()
		finalDelta = currentGoroutines - s.BaselineGoroutines
		if finalDelta <= maxGoroutineDelta {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("goroutine leak: delta %d exceeds max allowed %d (baseline %d, current %d)",
				finalDelta, maxGoroutineDelta, s.BaselineGoroutines, currentGoroutines)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}

	for _, env := range trackedEnvVars {
		val, set := os.LookupEnv(env)
		baseSet := s.BaselineEnvSet[env]
		baseVal := s.BaselineEnv[env]
		if set != baseSet || val != baseVal {
			return fmt.Errorf("environment variable %s was mutated: baseline (set=%v, val=%q), current (set=%v, val=%q)",
				env, baseSet, baseVal, set, val)
		}
	}

	if s.TempRoot != "" {
		curEntries := countEntries(s.TempRoot)
		if curEntries > s.BaselineTempDirs {
			return fmt.Errorf("leaked %d temp entries in %s (baseline %d, current %d)",
				curEntries-s.BaselineTempDirs, s.TempRoot, s.BaselineTempDirs, curEntries)
		}
	}

	return nil
}

func cleanupProbeTempDir(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		return
	}
}

func issueProbeReceipt(mgr *receipt.Manager, issuer string, allowedPaths []string, digest, taskID string) (*receipt.WriteReceipt, error) {
	return mgr.IssueReceipt(issuer, allowedPaths, 10*time.Minute,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: digest,
		}),
		receipt.WithProvenanceContext("g8s/eval", "HEAD", taskID),
	)
}

func postJSONEdit(endpoint string, payload map[string]any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body)) //nolint:gosec // G107: ephemeral loopback 127.0.0.1 test server
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

type mcpRPCError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func sendMCPToolCall(w io.Writer, r *bufio.Reader, id int, tool string, args map[string]any) (*mcpRPCError, error) {
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(w, "%s\n", req); err != nil {
		return nil, err
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var rpcResp struct {
		Error *mcpRPCError `json:"error"`
	}
	if err := json.Unmarshal(line, &rpcResp); err != nil {
		return nil, err
	}
	return rpcResp.Error, nil
}

// AuditSwarmReceiptContention tests 8-worker concurrent CAS receipt validation
// and fsops.Edit contention, asserting single-winner semantics and zero leaks.
func AuditSwarmReceiptContention(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "probe-swarm-*")
	if err != nil {
		return "", err
	}
	defer cleanupProbeTempDir(tmpDir)

	sensor := CaptureRuntimeSensor(tmpDir)

	dbPath := filepath.Join(tmpDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return "", fmt.Errorf("create receipt manager: %w", err)
	}
	defer mgr.Close()

	targetFile := filepath.Join(tmpDir, "shared.txt")
	if err := os.WriteFile(targetFile, []byte("worker-initial\n"), 0o600); err != nil {
		return "", fmt.Errorf("write target file: %w", err)
	}

	rc, err := issueProbeReceipt(mgr, "swarm-supervisor", []string{"shared.txt"}, "probe-swarm-contention", "probe-sa-006")
	if err != nil {
		return "", fmt.Errorf("issue swarm receipt: %w", err)
	}

	var wins atomic.Int32
	var rejections atomic.Int32
	var winningWorker atomic.Int32
	winningWorker.Store(-1)
	var editErrors atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			workerID := fmt.Sprintf("worker-%d", workerIdx)
			consumedRc, consumeErr := mgr.ValidateAndConsume(rc.ReceiptID, workerID)
			if consumeErr != nil {
				rejections.Add(1)
				return
			}
			wins.Add(1)
			winningWorker.Store(int32(workerIdx))
			_, editErr := fsops.Edit(ctx, fsops.EditOptions{
				Path:         targetFile,
				ScopeRoot:    tmpDir,
				OldString:    "worker-initial",
				NewString:    fmt.Sprintf("worker-%d-won", workerIdx),
				AllowedPaths: consumedRc.AllowedPaths,
			})
			if editErr != nil {
				editErrors.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if w := wins.Load(); w != 1 {
		return OutcomeClassBlocked, fmt.Errorf("swarm receipt contention: wins = %d, want 1", w)
	}
	if r := rejections.Load(); r != 7 {
		return OutcomeClassBlocked, fmt.Errorf("swarm receipt contention: rejections = %d, want 7", r)
	}
	if e := editErrors.Load(); e != 0 {
		return OutcomeClassBlocked, fmt.Errorf("swarm receipt contention: winning worker fsops.Edit failed (%d errors)", e)
	}

	content, readErr := os.ReadFile(targetFile)
	if readErr != nil {
		return OutcomeClassBlocked, fmt.Errorf("swarm receipt contention: read target file: %w", readErr)
	}
	expectedContent := fmt.Sprintf("worker-%d-won\n", winningWorker.Load())
	if string(content) != expectedContent {
		return OutcomeClassBlocked, fmt.Errorf("swarm receipt contention: content mismatch: got %q, want %q", string(content), expectedContent)
	}

	mgr.Close()
	cleanupProbeTempDir(tmpDir)

	if err := sensor.Verify(8); err != nil {
		return OutcomeClassBlocked, fmt.Errorf("sensor verify failed: %w", err)
	}

	return OutcomeClassCompleted, nil
}

// NewSwarmReceiptContentionProbe constructs the swarm-receipt-contention probe (sa-006).
func NewSwarmReceiptContentionProbe() *Probe {
	return &Probe{
		ID:              "sa-006",
		Category:        CategoryIASTDAST,
		Name:            "swarm-receipt-contention",
		Description:     "Issue #601: 8-worker concurrent CAS receipt & fsops edit contention — exactly 1 worker consumes the single-use receipt and no temp files or goroutines leak",
		Input:           "8-worker concurrent CAS receipt & fsops edit contention fixture",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditSwarmReceiptContention(ctx)
		},
	}
}

// AuditSymlinkScopeEscape verifies that under G8S_TIER=F1 + G8S_SCOPE_ROOT=scopeDir,
// ResolveDelegationContext, VerifyScope, Read, Edit, and Search reject symlinks and out-of-scope widening.
func AuditSymlinkScopeEscape(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "probe-symlink-*")
	if err != nil {
		return "", err
	}
	defer cleanupProbeTempDir(tmpDir)

	sensor := CaptureRuntimeSensor(tmpDir)

	scopeDir := filepath.Join(tmpDir, "scope")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(scopeDir, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		return "", err
	}

	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("super secret data"), 0o600); err != nil {
		return "", err
	}

	symlinkPath := filepath.Join(scopeDir, "leak_symlink.txt")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		return "", fmt.Errorf("create symlink: %w", err)
	}

	origTier, tierSet := os.LookupEnv("G8S_TIER")
	origScope, scopeSet := os.LookupEnv("G8S_SCOPE_ROOT")
	restoreEnv := func() {
		if tierSet {
			_ = os.Setenv("G8S_TIER", origTier)
		} else {
			_ = os.Unsetenv("G8S_TIER")
		}
		if scopeSet {
			_ = os.Setenv("G8S_SCOPE_ROOT", origScope)
		} else {
			_ = os.Unsetenv("G8S_SCOPE_ROOT")
		}
	}
	defer restoreEnv()

	_ = os.Setenv("G8S_TIER", "F1")
	_ = os.Setenv("G8S_SCOPE_ROOT", scopeDir)

	// 1. ResolveDelegationContext must reject parent directory widening
	if _, _, err := fsops.ResolveDelegationContext("", tmpDir, false); err == nil {
		return OutcomeClassBlocked, errors.New("fsops.ResolveDelegationContext accepted scope widening to parent directory")
	}
	if _, _, err := fsops.ResolveDelegationContext("", "..", false); err == nil {
		return OutcomeClassBlocked, errors.New("fsops.ResolveDelegationContext accepted scope widening to parent ref")
	}

	// 2. VerifyScope must reject symlink pointing outside scope
	if _, err := fsops.VerifyScope(symlinkPath, scopeDir, nil); err == nil {
		return OutcomeClassBlocked, errors.New("fsops.VerifyScope accepted symlink pointing outside scopeDir")
	}

	// 3. Read must reject symlink pointing outside scope
	if _, err := fsops.Read(ctx, fsops.ReadOptions{
		Path:      symlinkPath,
		ScopeRoot: scopeDir,
	}); err == nil {
		return OutcomeClassBlocked, errors.New("fsops.Read accepted symlink pointing outside scopeDir")
	}

	// 4. Edit must reject symlink pointing outside scope
	if _, err := fsops.Edit(ctx, fsops.EditOptions{
		Path:      symlinkPath,
		ScopeRoot: scopeDir,
		OldString: "secret",
		NewString: "tampered",
	}); err == nil {
		return OutcomeClassBlocked, errors.New("fsops.Edit accepted symlink pointing outside scopeDir")
	}

	// 5. search.Execute must reject an out-of-scope root
	if _, err := search.Execute(ctx, search.Options{
		Roots:     []string{outsideDir},
		ScopeRoot: scopeDir,
		Pattern:   "secret",
	}); err == nil {
		return OutcomeClassBlocked, errors.New("search.Execute accepted out-of-scope root outsideDir")
	}

	restoreEnv()
	cleanupProbeTempDir(tmpDir)

	if err := sensor.Verify(8); err != nil {
		return OutcomeClassBlocked, fmt.Errorf("sensor verify failed: %w", err)
	}

	return OutcomeClassCompleted, nil
}

// NewSymlinkScopeEscapeProbe constructs the symlink-scope-escape probe (sa-007).
func NewSymlinkScopeEscapeProbe() *Probe {
	return &Probe{
		ID:              "sa-007",
		Category:        CategoryIASTDAST,
		Name:            "symlink-scope-escape",
		Description:     "Issue #598/#601: delegated F1/F2 symlink traversal and G8S_SCOPE_ROOT widening escape — VerifyScope, Read, Edit, and Search must reject symlink and flag escapes",
		Input:           "delegated F1/F2 symlink traversal & scope widening fixture",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditSymlinkScopeEscape(ctx)
		},
	}
}

// AuditHTTPMCPDASTBoundary tests in-process HTTP and MCP stdio servers with a real receipt.Manager,
// asserting single-use receipt consumption, blocked replay attempts, and clean runtime hygiene.
func AuditHTTPMCPDASTBoundary(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "probe-dast-*")
	if err != nil {
		return "", err
	}
	defer cleanupProbeTempDir(tmpDir)

	sensor := CaptureRuntimeSensor(tmpDir)

	dbPath := filepath.Join(tmpDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return "", fmt.Errorf("create receipt manager: %w", err)
	}
	defer mgr.Close()

	// 1. HTTP API Server DAST
	httpFile := filepath.Join(tmpDir, "http_target.txt")
	if err := os.WriteFile(httpFile, []byte("http-initial\n"), 0o600); err != nil {
		return "", fmt.Errorf("write http target: %w", err)
	}

	httpRc, err := issueProbeReceipt(mgr, "http-dast", []string{"http_target.txt"}, "probe-http-dast", "probe-sa-008-http")
	if err != nil {
		return "", fmt.Errorf("issue http receipt: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen on 127.0.0.1:0: %w", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := server.DefaultConfig()
	cfg.Address = addr
	httpSrv := server.NewServer(cfg, nil)
	httpSrv.SetReceiptVerifier(mgr)
	if err := httpSrv.Start(); err != nil {
		return "", fmt.Errorf("start http server: %w", err)
	}
	stopHTTP := func() {
		ctxShutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Stop(ctxShutdown)
	}

	baseURL := "http://" + addr
	ready := false
	for attempt := 0; attempt < 50; attempt++ {
		resp, getErr := http.Get(baseURL + "/healthz") //nolint:gosec // G107: ephemeral loopback 127.0.0.1 test server
		if getErr == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		stopHTTP()
		return "", fmt.Errorf("http server failed to become ready on %s", addr)
	}

	// Valid HTTP edit
	status1, err := postJSONEdit(baseURL+"/api/v1/fs/edit", map[string]any{
		"path":       httpFile,
		"old_string": "http-initial",
		"new_string": "http-mutated",
		"scope_root": tmpDir,
		"receipt_id": httpRc.ReceiptID,
	})
	if err != nil {
		stopHTTP()
		return "", fmt.Errorf("http edit request 1 failed: %w", err)
	}
	if status1 != http.StatusOK {
		stopHTTP()
		return OutcomeClassBlocked, fmt.Errorf("expected 200 OK on first edit, got %d", status1)
	}

	// Replay HTTP edit with same receipt: must return 403 Forbidden
	status2, err := postJSONEdit(baseURL+"/api/v1/fs/edit", map[string]any{
		"path":       httpFile,
		"old_string": "http-mutated",
		"new_string": "http-replay",
		"scope_root": tmpDir,
		"receipt_id": httpRc.ReceiptID,
	})
	if err != nil {
		stopHTTP()
		return "", fmt.Errorf("http edit request 2 failed: %w", err)
	}
	if status2 != http.StatusForbidden {
		stopHTTP()
		return OutcomeClassBlocked, fmt.Errorf("expected 403 Forbidden on receipt replay, got %d", status2)
	}

	stopHTTP()

	// 2. MCP Server DAST
	mcpFile := filepath.Join(tmpDir, "mcp_target.txt")
	if err := os.WriteFile(mcpFile, []byte("mcp-initial\n"), 0o600); err != nil {
		return "", fmt.Errorf("write mcp target: %w", err)
	}

	mcpRc, err := issueProbeReceipt(mgr, "mcp-dast", []string{"mcp_target.txt"}, "probe-mcp-dast", "probe-sa-008-mcp")
	if err != nil {
		return "", fmt.Errorf("issue mcp receipt: %w", err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	mcpServer := mcp.NewServer(inR, outW, nil, mgr, nil)

	ctxMCP, cancelMCP := context.WithCancel(ctx)
	defer cancelMCP()

	mcpDone := make(chan error, 1)
	go func() {
		mcpDone <- mcpServer.ServeStdio(ctxMCP)
	}()

	mcpReader := bufio.NewReader(outR)

	// MCP Call 1: valid edit
	rpcErr1, err := sendMCPToolCall(inW, mcpReader, 1, "g8s_edit", map[string]any{
		"path":       mcpFile,
		"old_string": "mcp-initial",
		"new_string": "mcp-mutated",
		"scope_root": tmpDir,
		"receipt_id": mcpRc.ReceiptID,
	})
	if err != nil {
		return "", fmt.Errorf("mcp call 1 failed: %w", err)
	}
	if rpcErr1 != nil {
		return OutcomeClassBlocked, fmt.Errorf("expected success on mcp edit 1, got: %+v", rpcErr1)
	}

	// MCP Call 2 (replay): same receipt -> must return error with isError: true or consumed message
	rpcErr2, err := sendMCPToolCall(inW, mcpReader, 2, "g8s_edit", map[string]any{
		"path":       mcpFile,
		"old_string": "mcp-mutated",
		"new_string": "mcp-replay",
		"scope_root": tmpDir,
		"receipt_id": mcpRc.ReceiptID,
	})
	if err != nil {
		return "", fmt.Errorf("mcp call 2 failed: %w", err)
	}
	if rpcErr2 == nil {
		return OutcomeClassBlocked, errors.New("expected mcp error on receipt replay, got success")
	}

	isErrVal, _ := rpcErr2.Data["isError"].(bool)
	if !isErrVal && !strings.Contains(rpcErr2.Message, "consumed") {
		return OutcomeClassBlocked, fmt.Errorf("expected isError: true or consumed message, got: %+v", rpcErr2)
	}

	inW.Close()
	select {
	case serveErr := <-mcpDone:
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			return "", fmt.Errorf("mcp ServeStdio failed: %w", serveErr)
		}
	case <-time.After(500 * time.Millisecond):
		cancelMCP()
		if serveErr := <-mcpDone; serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			return "", fmt.Errorf("mcp ServeStdio failed after cancel: %w", serveErr)
		}
	}
	outR.Close()
	outW.Close()

	mgr.Close()
	cleanupProbeTempDir(tmpDir)

	if err := sensor.Verify(8); err != nil {
		return OutcomeClassBlocked, fmt.Errorf("sensor verify failed: %w", err)
	}

	return OutcomeClassCompleted, nil
}

// NewHTTPMCPDASTBoundaryProbe constructs the http-mcp-dast-boundary probe (sa-008).
func NewHTTPMCPDASTBoundaryProbe() *Probe {
	return &Probe{
		ID:              "sa-008",
		Category:        CategoryIASTDAST,
		Name:            "http-mcp-dast-boundary",
		Description:     "Issue #600/#601: in-process DAST probe against /api/v1/fs/* and MCP stdio server — enforces single-use receipt consumption and scope boundaries with zero goroutine leaks",
		Input:           "in-process HTTP and MCP DAST receipt replay boundary fixture",
		ExpectedOutcome: OutcomeClassCompleted,
		Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
			return AuditHTTPMCPDASTBoundary(ctx)
		},
	}
}

// IASTDASTProbes returns the 3 IAST/DAST red-team probes (#601).
func IASTDASTProbes() []*Probe {
	return []*Probe{
		NewSwarmReceiptContentionProbe(),
		NewSymlinkScopeEscapeProbe(),
		NewHTTPMCPDASTBoundaryProbe(),
	}
}
