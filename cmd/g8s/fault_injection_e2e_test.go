package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

var (
	faultworkerOnce sync.Once
	faultworkerPath string
	faultworkerErr  error
)

func buildFaultworkerBinary(t *testing.T, dir string) string {
	t.Helper()
	faultworkerOnce.Do(func() {
		bin := filepath.Join(dir, "faultworker")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "../../tools/faultworker")
		if out, err := cmd.CombinedOutput(); err != nil {
			faultworkerErr = errors.New("build faultworker: " + err.Error() + ": " + string(out))
			return
		}
		faultworkerPath = bin
	})
	if faultworkerErr != nil {
		t.Fatalf("%v", faultworkerErr)
	}
	return faultworkerPath
}

type faultE2EEnv struct {
	t             *testing.T
	g8sBin        string
	faultBin      string
	stateDir      string
	repoDir       string
	providersPath string
	baseSHA       string
}

func setupFaultE2EEnv(t *testing.T) *faultE2EEnv {
	t.Helper()

	g8sBin := buildG8sBinary(t)
	faultBin := buildFaultworkerBinary(t, filepath.Dir(g8sBin))
	stateDir := t.TempDir()
	repoDir := t.TempDir()

	writeRepoFile := func(rel, content string) {
		full := filepath.Join(repoDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	writeRepoFile("go.mod", "module example.com/faulte2e\n\ngo 1.22\n")
	writeRepoFile("pkg/base_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestBase(t *testing.T) {}\n")
	writeRepoFile(".g8s/verifier-classes.yml", `schema_version: "verifier-classes.v1"

classes:
  - name: test
    status: hard
    founding_catch: "#920 (fault injection E2E test class)"
    priority: 10
    paths:
      - "pkg/**"
    checks:
      - type: go-test
        packages:
          - "./pkg"
`)

	gitRun := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	gitRun("init")
	gitRun("config", "user.email", "ci@example.com")
	gitRun("config", "user.name", "CI Runner")
	gitRun("add", "go.mod", "pkg/base_test.go", ".g8s/verifier-classes.yml")
	gitRun("commit", "-m", "init fixture repo")
	baseSHA := gitRun("rev-parse", "HEAD")

	providersPath := filepath.Join(t.TempDir(), "providers.json")
	providersDoc := map[string]any{
		"providers": []map[string]any{
			{
				"class":         "platform_dispatch",
				"name":          "faultworker",
				"default_model": "mock-v1",
				"args":          []string{faultBin, "--prompt", "{prompt}"},
				"models":        []map[string]any{{"id": "mock-v1"}},
			},
		},
	}
	rawProv, err := json.Marshal(providersDoc)
	if err != nil {
		t.Fatalf("marshal providers.json: %v", err)
	}
	if err := os.WriteFile(providersPath, rawProv, 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}

	return &faultE2EEnv{
		t:             t,
		g8sBin:        g8sBin,
		faultBin:      faultBin,
		stateDir:      stateDir,
		repoDir:       repoDir,
		providersPath: providersPath,
		baseSHA:       baseSHA,
	}
}

func (e *faultE2EEnv) cmdEnv() []string {
	drop := map[string]bool{
		"G8S_STATE_DIR":  true,
		"G8S_DB":         true,
		"G8S_PROVIDERS":  true,
		"G8S_TIER":       true,
		"G8S_RECEIPT_ID": true,
		"G8S_SCOPE_ROOT": true,
	}
	out := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if ok && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"G8S_STATE_DIR="+e.stateDir,
		"G8S_PROVIDERS="+e.providersPath,
	)
	return out
}

func (e *faultE2EEnv) run(args ...string) (testEnvelope, int, string) {
	e.t.Helper()
	cmd := exec.Command(e.g8sBin, args...)
	cmd.Dir = e.repoDir
	cmd.Env = e.cmdEnv()
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			e.t.Fatalf("exec %v: %v\n%s", args, err, string(out))
		}
	}
	var env testEnvelope
	_ = json.Unmarshal([]byte(strings.TrimSpace(string(out))), &env)
	return env, exitCode, string(out)
}

func (e *faultE2EEnv) issueReceipt(pathGlob string) string {
	e.t.Helper()
	env, rc, raw := e.run("receipt", "issue", "--actor", "f0-supervisor", "--path", pathGlob, "--ttl", "600", "--json")
	if rc != 0 {
		e.t.Fatalf("receipt issue rc=%d: %s", rc, raw)
	}
	var data struct {
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.ReceiptID == "" {
		e.t.Fatalf("decode receipt_id err=%v raw=%s", err, raw)
	}
	return data.ReceiptID
}

func (e *faultE2EEnv) submitWorkspaceWrite(key, receiptID, timeout, maxAttempts, prompt string) string {
	e.t.Helper()
	env, rc, raw := e.run(
		"submit",
		"--provider", "faultworker",
		"--model", "mock-v1",
		"--role", "implementer",
		"--permission", "workspace_write",
		"--allow-workspace-write",
		"--receipt-id", receiptID,
		"--max-attempts", maxAttempts,
		"--timeout", timeout,
		"--idempotency-key", key,
		"--prompt", prompt,
		"--json",
	)
	if rc != 0 {
		e.t.Fatalf("submit %s rc=%d: %s", key, rc, raw)
	}
	var task controlplane.Task
	if err := json.Unmarshal(env.Data, &task); err != nil || task.TaskID == "" {
		e.t.Fatalf("decode submitted task err=%v raw=%s", err, raw)
	}
	return task.TaskID
}

func (e *faultE2EEnv) getTask(taskID string) controlplane.Task {
	e.t.Helper()
	env, rc, raw := e.run("get", taskID, "--json")
	if rc != 0 {
		e.t.Fatalf("get %s rc=%d: %s", taskID, rc, raw)
	}
	var task controlplane.Task
	if err := json.Unmarshal(env.Data, &task); err != nil {
		e.t.Fatalf("decode task %s err=%v raw=%s", taskID, err, raw)
	}
	return task
}

func waitFaultProcessDead(pid int, limit time.Duration) bool {
	if pid <= 0 {
		return true
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if !isFaultTestProcessAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !isFaultTestProcessAlive(pid)
}

type faultProcNotification struct {
	PID           int  `json:"pid"`
	GrandchildPID int  `json:"grandchild_pid"`
	Ready         bool `json:"ready"`
}

func TestFaultInjectionE2E_CrashStaleLeaseReclaimVerifyAndDeliver(t *testing.T) {
	env := setupFaultE2EEnv(t)
	rcptID := env.issueReceipt("pkg/**")

	var attempt atomic.Int32
	readyCh := make(chan faultProcNotification, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var note faultProcNotification
		_ = json.NewDecoder(r.Body).Decode(&note)
		if r.URL.Query().Get("phase") == "ready" {
			readyCh <- note
			w.WriteHeader(http.StatusOK)
			return
		}
		n := attempt.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action":        "dirty_hang",
				"dirty_target":  "pkg/corrupt.go",
				"dirty_content": "package broken !!!\n",
				"sleep":         "30s",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action":  "clean_write",
			"target":  "pkg/recovered.go",
			"content": "package pkg\n\nfunc Recovered() int { return 7 }\n",
		})
	}))
	defer srv.Close()

	specJSON, _ := json.Marshal(map[string]string{
		"mode":       "notify-controlled",
		"notify_url": srv.URL,
	})
	taskID := env.submitWorkspaceWrite("crash-reclaim-1", rcptID, "30s", "2", "FAULTWORKER_SPEC:"+string(specJSON))

	// Attempt 1: short 2s lease so Worker 2 can reclaim after SIGKILL.
	worker1 := exec.Command(env.g8sBin, "worker", "--once", "--lease", "2", "--provider", "faultworker", "--model", "mock-v1", "--json")
	worker1.Dir = env.repoDir
	worker1.Env = env.cmdEnv()
	if err := worker1.Start(); err != nil {
		t.Fatalf("start worker1: %v", err)
	}
	t.Cleanup(func() {
		if worker1.Process != nil {
			_ = worker1.Process.Kill()
		}
	})

	var activeProcs faultProcNotification
	select {
	case activeProcs = <-readyCh:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for attempt 1 ready callback")
	}
	t.Cleanup(func() {
		forceKillFaultTestPID(activeProcs.GrandchildPID)
		forceKillFaultTestPID(activeProcs.PID)
	})

	running := env.getTask(taskID)
	if running.State != controlplane.StateRunning {
		t.Fatalf("state during active attempt = %q, want RUNNING", running.State)
	}

	// Hard-kill worker1 mid-lease. On Windows, closing worker1's process handle
	// terminates the entire JobObject tree automatically. On POSIX, external
	// SIGKILL on worker1 cannot run signal handlers or cross every bwrap/Setsid
	// domain, so reap Attempt 1 sleepers cleanly before Worker 2 reclaims.
	if err := worker1.Process.Kill(); err != nil {
		t.Fatalf("kill worker1: %v", err)
	}
	_ = worker1.Wait()

	if runtime.GOOS == "windows" {
		if !waitFaultProcessDead(activeProcs.PID, 5*time.Second) {
			t.Fatalf("faultworker pid %d survived worker1 JobObject close on Windows", activeProcs.PID)
		}
		if !waitFaultProcessDead(activeProcs.GrandchildPID, 5*time.Second) {
			t.Fatalf("detached grandchild pid %d survived worker1 JobObject close on Windows", activeProcs.GrandchildPID)
		}
	} else {
		forceKillFaultTestPID(activeProcs.GrandchildPID)
		forceKillFaultTestPID(activeProcs.PID)
	}

	if _, err := os.Stat(filepath.Join(env.repoDir, "pkg", "corrupt.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirty file must not leak into repoDir after worker1 crash, stat=%v", err)
	}

	// Wait past the 2s lease TTL so Worker 2 reconciles and reclaims Attempt 2.
	time.Sleep(2500 * time.Millisecond)

	_, w2rc, w2out := env.run("worker", "--once", "--lease", "15", "--provider", "faultworker", "--model", "mock-v1", "--json")
	if w2rc != 0 {
		t.Fatalf("worker2 recovery rc=%d out=%s", w2rc, w2out)
	}

	afterW2 := env.getTask(taskID)
	if afterW2.State != controlplane.StateWorkerCompleted || afterW2.Attempts != 2 {
		t.Fatalf("after worker2 state=%s attempts=%d, want WORKER_COMPLETED attempts=2", afterW2.State, afterW2.Attempts)
	}

	vEnv, vRC, vOut := env.run("verify", "--task", taskID, "--json")
	if vRC != 0 {
		t.Fatalf("verify rc=%d out=%s", vRC, vOut)
	}
	var verdict struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(vEnv.Data, &verdict); err != nil || verdict.Outcome != "pass" {
		t.Fatalf("verify outcome=%+v err=%v raw=%s", verdict, err, vOut)
	}
	if accepted := env.getTask(taskID); accepted.State != controlplane.StateSupervisorAccepted {
		t.Fatalf("state after verify = %s, want SUPERVISOR_ACCEPTED", accepted.State)
	}

	dEnv, dRC, dOut := env.run("deliver", taskID, "--expected-head-oid", env.baseSHA, "--prune-worktree", "--json")
	if dRC != 0 {
		t.Fatalf("deliver rc=%d out=%s", dRC, dOut)
	}
	var deliverRes struct {
		Applied        []string `json:"applied"`
		PrunedWorktree bool     `json:"pruned_worktree"`
	}
	if err := json.Unmarshal(dEnv.Data, &deliverRes); err != nil {
		t.Fatalf("decode deliver data: %v raw=%s", err, dOut)
	}
	if !deliverRes.PrunedWorktree || len(deliverRes.Applied) != 1 || deliverRes.Applied[0] != "pkg/recovered.go" {
		t.Fatalf("unexpected deliver payload: %+v", deliverRes)
	}
	if done := env.getTask(taskID); done.State != controlplane.StateSupervisorAccepted {
		t.Fatalf("state after deliver = %s, want SUPERVISOR_ACCEPTED", done.State)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, "pkg", "recovered.go")); err != nil {
		t.Fatalf("expected recovered.go delivered to repoDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, "pkg", "corrupt.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt.go from crashed attempt 1 must not exist in repoDir, stat=%v", err)
	}
}

func TestFaultInjectionE2E_HardTimeoutKillsDetachedProcessTree(t *testing.T) {
	env := setupFaultE2EEnv(t)
	rcptID := env.issueReceipt("pkg/**")

	procCh := make(chan faultProcNotification, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var note faultProcNotification
		_ = json.NewDecoder(r.Body).Decode(&note)
		procCh <- note
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	specJSON, _ := json.Marshal(map[string]string{
		"mode":       "hang-with-grandchild",
		"notify_url": srv.URL,
		"sleep":      "60s",
	})
	taskID := env.submitWorkspaceWrite("timeout-tree-1", rcptID, "2s", "1", "FAULTWORKER_SPEC:"+string(specJSON))

	_, rc, out := env.run("worker", "--once", "--lease", "15", "--provider", "faultworker", "--model", "mock-v1", "--json")
	if rc == 0 {
		t.Fatalf("expected worker --once to exit non-zero on timeout, got 0: %s", out)
	}

	var spawned faultProcNotification
	select {
	case spawned = <-procCh:
	default:
		t.Fatalf("hang-with-grandchild did not notify PIDs before timeout; output: %s", out)
	}
	t.Cleanup(func() {
		forceKillFaultTestPID(spawned.GrandchildPID)
		forceKillFaultTestPID(spawned.PID)
	})

	if !waitFaultProcessDead(spawned.PID, 5*time.Second) {
		t.Fatalf("faultworker pid %d survived timeout kill on %s", spawned.PID, runtime.GOOS)
	}
	if !waitFaultProcessDead(spawned.GrandchildPID, 5*time.Second) {
		t.Fatalf("detached grandchild pid %d survived timeout sandbox cleanup on %s", spawned.GrandchildPID, runtime.GOOS)
	}

	task := env.getTask(taskID)
	if task.State == controlplane.StateSucceeded || task.State == controlplane.StateSupervisorAccepted {
		t.Fatalf("timed-out task must never be accepted/SUCCEEDED, got %s", task.State)
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(task.Result, &res); err != nil || res.OK {
		t.Fatalf("expected result.ok=false, got raw=%s err=%v", string(task.Result), err)
	}
	if task.LastError == nil {
		t.Fatal("expected non-empty LastError after timeout kill")
	}
	lowerErr := strings.ToLower(*task.LastError)
	validWindowsExit := runtime.GOOS == "windows" && strings.Contains(lowerErr, "exited with code 1")
	if !strings.Contains(lowerErr, "deadline exceeded") && !strings.Contains(lowerErr, "exited with code -1") && !validWindowsExit {
		t.Fatalf("expected deadline exceeded or forced signal exit in LastError, got %q", *task.LastError)
	}
}

func TestFaultInjectionE2E_StdoutErrorEnvelopeRC0FlipsFailed(t *testing.T) {
	env := setupFaultE2EEnv(t)

	specJSON, _ := json.Marshal(map[string]string{
		"mode":    "stdout-error-rc0",
		"message": "simulated rate limit",
	})
	_, subRC, subRaw := env.run(
		"submit",
		"--provider", "faultworker",
		"--model", "mock-v1",
		"--role", "scout",
		"--permission", "read_only",
		"--max-attempts", "1",
		"--timeout", "15s",
		"--idempotency-key", "err-envelope-rc0-1",
		"--prompt", "FAULTWORKER_SPEC:"+string(specJSON),
		"--json",
	)
	if subRC != 0 {
		t.Fatalf("submit rc=%d: %s", subRC, subRaw)
	}
	var subEnv testEnvelope
	_ = json.Unmarshal([]byte(strings.TrimSpace(subRaw)), &subEnv)
	var subTask controlplane.Task
	if err := json.Unmarshal(subEnv.Data, &subTask); err != nil || subTask.TaskID == "" {
		t.Fatalf("decode task: %v raw=%s", err, subRaw)
	}

	_, wRC, wOut := env.run("worker", "--once", "--provider", "faultworker", "--model", "mock-v1", "--json")
	if wRC == 0 {
		t.Fatalf("expected non-zero worker exit for stdout error envelope with rc=0, out=%s", wOut)
	}

	task := env.getTask(subTask.TaskID)
	if task.State == controlplane.StateSucceeded || task.State == controlplane.StateSupervisorAccepted {
		t.Fatalf("stdout error envelope task must never reach accepted/SUCCEEDED, got %s", task.State)
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(task.Result, &res); err != nil || res.OK {
		t.Fatalf("expected result.ok=false, got raw=%s err=%v", string(task.Result), err)
	}
	if task.LastError == nil || !strings.Contains(*task.LastError, "simulated rate limit") {
		t.Fatalf("expected LastError to preserve upstream error message, got %v", task.LastError)
	}

	if _, dRC, dOut := env.run("deliver", subTask.TaskID, "--json"); dRC == 0 {
		t.Fatalf("expected deliver to refuse unverified/errored task, got rc=0: %s", dOut)
	}
}

func TestFaultInjectionE2E_ConcurrentDeliverAndStaleHeadCAS(t *testing.T) {
	env := setupFaultE2EEnv(t)

	prepareVerified := func(key, target, content string) string {
		rcptID := env.issueReceipt("pkg/**")
		specJSON, _ := json.Marshal(map[string]string{
			"mode":    "clean-write",
			"target":  target,
			"content": content,
		})
		taskID := env.submitWorkspaceWrite(key, rcptID, "30s", "1", "FAULTWORKER_SPEC:"+string(specJSON))
		if _, rc, out := env.run("worker", "--once", "--provider", "faultworker", "--model", "mock-v1", "--json"); rc != 0 {
			t.Fatalf("worker %s rc=%d out=%s", key, rc, out)
		}
		if _, rc, out := env.run("verify", "--task", taskID, "--json"); rc != 0 {
			t.Fatalf("verify %s rc=%d out=%s", key, rc, out)
		}
		return taskID
	}

	task1 := prepareVerified("cas-race-1", "pkg/cas.go", "package pkg\n\nfunc CAS() int { return 1 }\n")
	task2 := prepareVerified("cas-stale-2", "pkg/cas2.go", "package pkg\n\nfunc CAS2() int { return 2 }\n")

	// 1. Explicit stale expected-head-oid must be rejected with non-zero exit & repo untouched.
	_, staleRC, staleOut := env.run("deliver", task1, "--expected-head-oid", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "--json")
	if staleRC == 0 {
		t.Fatalf("expected stale expected-head-oid deliver to fail, got rc=0: %s", staleOut)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, "pkg", "cas.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pkg/cas.go must not be written after STALE_HEAD refusal, stat=%v", err)
	}
	if st := env.getTask(task1).State; st != controlplane.StateSupervisorAccepted {
		t.Fatalf("task1 state = %s, want SUPERVISOR_ACCEPTED", st)
	}

	// 2. 4-way concurrent deliver race: exactly 1 winner and 3 losers.
	const racers = 4
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan int, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			cmd := exec.Command(env.g8sBin, "deliver", task1, "--expected-head-oid", env.baseSHA, "--prune-worktree", "--json")
			cmd.Dir = env.repoDir
			cmd.Env = env.cmdEnv()
			err := cmd.Run()
			if err == nil {
				results <- 0
				return
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				results <- exitErr.ExitCode()
				return
			}
			results <- -1
		}()
	}
	close(startGate)
	wg.Wait()
	close(results)

	winners := 0
	losers := 0
	for rc := range results {
		if rc == 0 {
			winners++
		} else {
			losers++
		}
	}
	if winners != 1 || losers != racers-1 {
		t.Fatalf("concurrent deliver race winners=%d losers=%d, want 1 winner and %d losers", winners, losers, racers-1)
	}
	if got, err := os.ReadFile(filepath.Join(env.repoDir, "pkg", "cas.go")); err != nil || !strings.Contains(string(got), "func CAS()") {
		t.Fatalf("pkg/cas.go corrupt or missing after concurrent deliver: err=%v content=%q", err, string(got))
	}
	if st := env.getTask(task1).State; st != controlplane.StateSupervisorAccepted {
		t.Fatalf("task1 final state = %s, want SUPERVISOR_ACCEPTED", st)
	}

	// 3. Advance repo HEAD and confirm default BaseSHA CAS rejects task2 without override flag.
	for _, gitArgs := range [][]string{
		{"add", "pkg/cas.go"},
		{"commit", "-m", "advance HEAD past task2 base_sha"},
	} {
		c := exec.Command("git", gitArgs...)
		c.Dir = env.repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", gitArgs, err, string(out))
		}
	}

	_, defRC, defOut := env.run("deliver", task2, "--json")
	if defRC == 0 {
		t.Fatalf("expected default BaseSHA CAS check to refuse delivery after repo HEAD advanced, got rc=0: %s", defOut)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, "pkg", "cas2.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pkg/cas2.go must remain absent after default BaseSHA STALE_HEAD refusal, stat=%v", err)
	}
}
