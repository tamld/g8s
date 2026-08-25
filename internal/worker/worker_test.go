package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// --- fakes ---

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// autoAdvanceClock moves forward on every read so injected deadlines elapse
// without real sleeping.
type autoAdvanceClock struct {
	mu sync.Mutex
	t  time.Time
}

func newAutoAdvanceClock() *autoAdvanceClock {
	return &autoAdvanceClock{t: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
}

func (c *autoAdvanceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(25 * time.Millisecond)
	return c.t
}

type fakeChild struct {
	done       chan struct{}
	closeOnce  sync.Once
	code       int
	pid        int
	terminated atomic.Int32
}

func newFakeChild(code int) *fakeChild {
	return &fakeChild{done: make(chan struct{}), code: code, pid: 4242}
}

// hangChild never finishes until Terminate closes its done channel.
func newHangChild() *fakeChild { return newFakeChild(0) }

func (c *fakeChild) PID() int              { return c.pid }
func (c *fakeChild) Done() <-chan struct{} { return c.done }
func (c *fakeChild) WaitCode() int         { return c.code }
func (c *fakeChild) Terminate(time.Duration) {
	c.terminated.Add(1)
	c.closeOnce.Do(func() { close(c.done) })
}

func (c *fakeChild) finishLater(resultPath string, payload string, delay time.Duration) {
	go func() {
		time.Sleep(delay)
		_ = os.WriteFile(resultPath, []byte(payload), 0o600)
		c.closeOnce.Do(func() { close(c.done) })
	}()
}

type fakeRunner struct {
	mu      sync.Mutex
	spawned []SpawnOptions
	err     error
	factory func(opts SpawnOptions) Child
	onSpawn func(taskID string)
}

func (r *fakeRunner) Spawn(opts SpawnOptions) (Child, error) {
	r.mu.Lock()
	r.spawned = append(r.spawned, opts)
	factory, spawnErr := r.factory, r.err
	r.mu.Unlock()
	if spawnErr != nil {
		return nil, spawnErr
	}
	taskID := filepath.Base(filepath.Dir(opts.RunDir))
	if r.onSpawn != nil {
		r.onSpawn(taskID)
	}
	if factory != nil {
		return factory(opts), nil
	}
	child := newFakeChild(0)
	child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded"}`, 5*time.Millisecond)
	return child, nil
}

// --- helpers ---

type workerEnv struct {
	store  *controlplane.Store
	sup    *Supervisor
	runner *fakeRunner
	runDir string
}

func newWorkerEnv(t *testing.T, clock func() time.Time) *workerEnv {
	t.Helper()
	root := t.TempDir()
	store, err := controlplane.NewControlPlane(filepath.Join(root, "cp.db"), nil)
	if err != nil {
		t.Fatalf("open control plane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := &fakeRunner{}
	sup := NewSupervisor(store, filepath.Join(root, "runs"),
		WithRunner(runner),
		WithPollInterval(2*time.Millisecond),
	)
	if clock != nil {
		sup.clock = clock
	}
	return &workerEnv{store: store, sup: sup, runner: runner, runDir: filepath.Join(root, "runs")}
}

func submitTask(t *testing.T, env *workerEnv, idem string, maxAttempts int, payload map[string]any) *controlplane.Task {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	payload["prompt"] = "inventory the module"
	payload["timeout"] = "30s"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task, err := env.store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: idem,
		Payload:        raw,
		Model:          "gemini-3.7-flash-high",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "30s",
		AddDirs:        []string{"."},
		MaxAttempts:    maxAttempts,
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	return task
}

// --- duration tests ---

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name     string
		values   []string
		expected string
	}{
		{
			name:     "no arguments",
			values:   nil,
			expected: "",
		},
		{
			name:     "all empty",
			values:   []string{"", "", ""},
			expected: "",
		},
		{
			name:     "first non-empty",
			values:   []string{"first", "", ""},
			expected: "first",
		},
		{
			name:     "second non-empty",
			values:   []string{"", "second", ""},
			expected: "second",
		},
		{
			name:     "multiple non-empty",
			values:   []string{"first", "second", "third"},
			expected: "first",
		},
		{
			name:     "mixed empty and non-empty",
			values:   []string{"", "", "third", "", "fifth"},
			expected: "third",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstNonEmpty(tt.values...)
			if result != tt.expected {
				t.Errorf("firstNonEmpty(%v) = %v; want %v", tt.values, result, tt.expected)
			}
		})
	}
}

func TestParseDurationSecondsAcceptsBoundedExpressions(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"250ms", 0.25},
		{"1m2s", 62},
		{"2h", 7200},
		{"45s", 45},
		{"1h30m", 5400},
	}
	for _, tc := range cases {
		got, err := ParseDurationSeconds(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseDurationSeconds(%q) = (%v, %v), want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestParseDurationSecondsRejectsUnboundedValues(t *testing.T) {
	for _, in := range []string{"0s", "", "abc", "-5s", "500MS", "10"} {
		if _, err := ParseDurationSeconds(in); err == nil {
			t.Fatalf("ParseDurationSeconds(%q) accepted an unbounded value, want error", in)
		}
	}
}

// --- supervisor integration over the real SQLite control plane ---

func TestRunOnceHappyPathSucceedsAndCleansUp(t *testing.T) {
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "happy-1", 3, nil)

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateSucceeded {
		t.Fatalf("state = %q, want SUCCEEDED", final.State)
	}
	attemptDir := filepath.Join(env.runDir, final.TaskID, "attempt-1")
	receiptRaw, err := os.ReadFile(filepath.Join(attemptDir, "receipt.json"))
	if err != nil {
		t.Fatalf("receipt.json missing: %v", err)
	}
	var receiptView struct {
		TaskID string `json:"task_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(receiptRaw, &receiptView); err != nil {
		t.Fatalf("decode receipt.json: %v", err)
	}
	if receiptView.TaskID != final.TaskID || receiptView.State != "SUCCEEDED" {
		t.Fatalf("receipt snapshot mismatch: %+v", receiptView)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "prompt.txt")); !os.IsNotExist(err) {
		t.Fatalf("prompt.txt must never survive an attempt (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "result.json")); err != nil {
		t.Fatalf("result.json must be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "worker.stdout")); !os.IsNotExist(err) {
		t.Fatal("worker.stdout must never be persisted")
	}
}

func TestRunOnceCancelWhileRunningTerminatesAndMarksCancelled(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "cancel-1", 3, nil)
	hang := newHangChild()
	env.runner.onSpawn = func(id string) {
		_ = env.store.CancelTask(context.Background(), task.TaskID, "owner requested")
	}
	env.runner.factory = func(SpawnOptions) Child { return hang }

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateCancelled {
		t.Fatalf("state = %q, want CANCELLED", final.State)
	}
	if hang.terminated.Load() == 0 {
		t.Fatal("cancel must terminate the child process group")
	}
	snapshotRaw, err := os.ReadFile(filepath.Join(env.runDir, task.TaskID, "attempt-1", "receipt.json"))
	if err != nil {
		t.Fatalf("receipt.json missing after cancel: %v", err)
	}
	if strings.Contains(string(snapshotRaw), "inventory the module") {
		t.Fatal("exported receipt must not carry the raw prompt")
	}
}

func TestRunOnceTimeoutFailsRetryable(t *testing.T) {
	env := newWorkerEnv(t, newAutoAdvanceClock().Now)
	submitTask(t, env, "slow-1", 1, map[string]any{"timeout": "100ms"})
	hang := newFakeChild(0)
	env.runner.factory = func(SpawnOptions) Child { return hang }

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 600})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateFailed {
		t.Fatalf("state = %q, want FAILED", final.State)
	}
	if final.LastError == nil || !strings.Contains(*final.LastError, "execution deadline exceeded") {
		t.Fatalf("last_error = %v, want deadline message", final.LastError)
	}
	if hang.terminated.Load() == 0 {
		t.Fatal("timeout must terminate the child process group")
	}
}

func TestRunOnceNeedsInfoPausesAndReleasesLease(t *testing.T) {
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "pause-1", 3, nil)
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":false,"status":"NEEDS_INFO","reason":"missing credentials"}`, 5*time.Millisecond)
		return child
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateNeedsInfo {
		t.Fatalf("state = %q, want NEEDS_INFO", final.State)
	}
	if final.LeaseOwner != nil {
		t.Fatalf("lease_owner = %v, want nil after pause", *final.LeaseOwner)
	}
}

func TestRunOnceCapturesLargeNonUTF8OutputWithoutDeadlock(t *testing.T) {
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "loud-1", 3, nil)
	noise := strings.Repeat("\xff\xfe\xfd\x00", 50*1024) // ~200KB invalid UTF-8
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		go func() {
			if opts.Stdout != nil {
				_, _ = opts.Stdout.Write([]byte(noise))
			}
			_ = os.WriteFile(opts.ResultPath, []byte(`{"ok":true,"status":"succeeded"}`), 0o600)
			close(child.done)
		}()
		return child
	}

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateSucceeded {
		t.Fatalf("state = %q, want SUCCEEDED", final.State)
	}
	if _, err := os.Stat(filepath.Join(env.runDir, final.TaskID, "attempt-1", "worker.stdout")); !os.IsNotExist(err) {
		t.Fatal("bounded capture must delete worker.stdout")
	}
}

func TestRunOnceSpawnFailureRetriesAndCleansPrompt(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "boom-1", 2, nil)
	env.runner.err = context.Canceled

	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateQueued {
		t.Fatalf("state = %q, want QUEUED for retryable spawn failure", final.State)
	}
	if final.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 consumed", final.Attempts)
	}
	if _, err := os.Stat(filepath.Join(env.runDir, task.TaskID, "attempt-1", "prompt.txt")); !os.IsNotExist(err) {
		t.Fatal("spawn failure must still remove prompt.txt")
	}
}

type startFailPlane struct {
	*controlplane.Store
	startCalls atomic.Int32
}

func (p *startFailPlane) StartTask(string, string, string) bool {
	p.startCalls.Add(1)
	return false
}

func TestRunOnceLostStartRaceReportsLeaseLossWithoutFinishing(t *testing.T) {
	root := t.TempDir()
	store, err := controlplane.NewControlPlane(filepath.Join(root, "cp.db"), nil)
	if err != nil {
		t.Fatalf("open control plane: %v", err)
	}
	defer store.Close()
	plane := &startFailPlane{Store: store}
	runner := &fakeRunner{factory: func(SpawnOptions) Child { return newHangChild() }}
	sup := NewSupervisor(plane, filepath.Join(root, "runs"), WithRunner(runner), WithPollInterval(2*time.Millisecond))

	payload, _ := json.Marshal(map[string]any{
		"prompt": "inventory the module", "timeout": "30s",
	})
	if _, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "race-1", Payload: payload, Model: "gemini-3.7-flash-high",
		AddDirs: []string{"."}, MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	final, err := sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if final.State != controlplane.StateLeased {
		t.Fatalf("state = %q, want LEASED (attempt not finished)", final.State)
	}
	if plane.startCalls.Load() != 1 {
		t.Fatalf("start calls = %d, want 1", plane.startCalls.Load())
	}
}

func TestFinishAttemptRejectsStaleToken(t *testing.T) {
	env := newWorkerEnv(t, nil)
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"prompt": "p", "timeout": "30s"})
	if _, err := env.store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "stale-1", Payload: payload, Model: "gemini-3.7-flash-high",
		AddDirs: []string{"."}, MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	claimed, err := env.store.ClaimTask(ctx, "w1", 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	_, err = env.store.FinishAttempt(claimed.TaskID, "w1", "forged-token", controlplane.FinishAttemptParams{Success: true})
	if err == nil || !strings.Contains(err.Error(), "lease ownership lost") {
		t.Fatalf("want stale lease refusal, got %v", err)
	}
}

func TestExitCodeForSignalFollowsShellConvention(t *testing.T) {
	cases := map[int]int{15: 143, 2: 130, 9: 137}
	for sig, want := range cases {
		if got := ExitCodeForSignal(sig); got != want {
			t.Fatalf("ExitCodeForSignal(%d) = %d, want %d", sig, got, want)
		}
	}
}

func TestRetryProducesPerAttemptEvidenceDirs(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "retry-1", 3, map[string]any{"timeout": "10ms"})
	env.runner.factory = func(SpawnOptions) Child { return newHangChild() }
	clock := newAutoAdvanceClock()
	env.sup.clock = clock.Now

	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 600}); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	// Second attempt succeeds quickly.
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded"}`, 5*time.Millisecond)
		return child
	}
	env.sup.clock = time.Now
	final, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w1", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if final.State != controlplane.StateSucceeded {
		t.Fatalf("state = %q, want SUCCEEDED", final.State)
	}
	for _, attempt := range []string{"attempt-1", "attempt-2"} {
		receiptPath := filepath.Join(env.runDir, task.TaskID, attempt, "receipt.json")
		if _, err := os.Stat(receiptPath); err != nil {
			t.Fatalf("%s receipt.json missing: %v", attempt, err)
		}
	}
}

func TestRunLoopOnceModeExitCodes(t *testing.T) {
	env := newWorkerEnv(t, nil)
	if code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w1", LeaseSeconds: 60, Once: true}); code != 0 {
		t.Fatalf("idle once-mode exit = %d, want 0", code)
	}

	submitTask(t, env, "fail-1", 1, map[string]any{"timeout": "30s"})
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(3)
		child.finishLater(opts.ResultPath, `{"ok":false,"status":"failed"}`, 5*time.Millisecond)
		return child
	}
	if code := env.sup.RunLoop(context.Background(), LoopOptions{WorkerID: "w1", LeaseSeconds: 60, Once: true}); code != 1 {
		t.Fatalf("failing once-mode exit = %d, want 1", code)
	}
}

func TestReapOrphansRemovesStaleCoordinationFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups unsupported")
	}
	env := newWorkerEnv(t, nil)
	stale := filepath.Join(env.runDir, "ghost-task", "attempt-1")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphanPID := filepath.Join(stale, "child.pid")
	if err := os.WriteFile(orphanPID, []byte("999999"), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	env.sup.reapOrphans()
	if _, err := os.Stat(orphanPID); !os.IsNotExist(err) {
		t.Fatalf("stale child.pid should be removed, stat err=%v", err)
	}
}

func TestClampDuration(t *testing.T) {
	tests := []struct {
		name      string
		v, lo, hi time.Duration
		want      time.Duration
	}{
		{
			name: "within bounds",
			v:    5 * time.Second,
			lo:   1 * time.Second,
			hi:   10 * time.Second,
			want: 5 * time.Second,
		},
		{
			name: "below lower bound",
			v:    0 * time.Second,
			lo:   1 * time.Second,
			hi:   10 * time.Second,
			want: 1 * time.Second,
		},
		{
			name: "above upper bound",
			v:    15 * time.Second,
			lo:   1 * time.Second,
			hi:   10 * time.Second,
			want: 10 * time.Second,
		},
		{
			name: "equal to lower bound",
			v:    1 * time.Second,
			lo:   1 * time.Second,
			hi:   10 * time.Second,
			want: 1 * time.Second,
		},
		{
			name: "equal to upper bound",
			v:    10 * time.Second,
			lo:   1 * time.Second,
			hi:   10 * time.Second,
			want: 10 * time.Second,
		},
		{
			name: "negative duration, clamped to zero",
			v:    -5 * time.Second,
			lo:   0 * time.Second,
			hi:   10 * time.Second,
			want: 0 * time.Second,
		},
		{
			name: "all negative bounds",
			v:    -15 * time.Second,
			lo:   -10 * time.Second,
			hi:   -1 * time.Second,
			want: -10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampDuration(tt.v, tt.lo, tt.hi)
			if got != tt.want {
				t.Errorf("clampDuration(%v, %v, %v) = %v, want %v", tt.v, tt.lo, tt.hi, got, tt.want)
			}
		})
	}
}
func TestStdbuf(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: []byte{},
		},
		{
			name:     "empty input",
			input:    []byte{},
			expected: []byte{},
		},
		{
			name:     "non-empty input",
			input:    []byte("hello"),
			expected: []byte("hello"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stdbuf(tt.input)
			if tt.input == nil && result == nil {
				t.Errorf("stdbuf(nil) returned nil, expected non-nil empty slice")
			}
			if len(result) != len(tt.expected) {
				t.Errorf("stdbuf() returned length %d, expected %d", len(result), len(tt.expected))
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("stdbuf() returned %v, expected %v", result, tt.expected)
					break
				}
			}
		})
	}
}
func TestFirstOf(t *testing.T) {
	cases := []struct {
		name string
		dirs []string
		want string
	}{
		{"nil slice", nil, ""},
		{"empty slice", []string{}, ""},
		{"one element", []string{"a"}, "a"},
		{"multiple elements", []string{"a", "b", "c"}, "a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstOf(tc.dirs); got != tc.want {
				t.Errorf("firstOf(%v) = %q, want %q", tc.dirs, got, tc.want)
			}
		})
	}
}
func TestDerefString(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q, want %q", got, "")
	}

	val := "hello"
	if got := derefString(&val); got != "hello" {
		t.Errorf("derefString(&val) = %q, want %q", got, "hello")
	}
}
func TestMaybePause(t *testing.T) {
	tests := []struct {
		name       string
		wr         workerResult
		stdoutText string
		token      string
		wantPaused bool
	}{
		{
			name:       "direct needs info",
			wr:         workerResult{Status: controlplane.StateNeedsInfo},
			stdoutText: "",
			token:      "valid",
			wantPaused: true,
		},
		{
			name:       "direct blocked",
			wr:         workerResult{Status: controlplane.StateBlocked},
			stdoutText: "",
			token:      "valid",
			wantPaused: true,
		},
		{
			name:       "fenced needs info",
			wr:         workerResult{Status: controlplane.StateFailed},
			stdoutText: "some log\n`json {\"status\":\"NEEDS_INFO\"}`",
			token:      "valid",
			wantPaused: true,
		},
		{
			name:       "fenced blocked",
			wr:         workerResult{Status: controlplane.StateFailed},
			stdoutText: "some log\n`json {\"status\":\"BLOCKED\"}`\nend",
			token:      "valid",
			wantPaused: true,
		},
		{
			name:       "no pause state",
			wr:         workerResult{Status: "SUCCESS"},
			stdoutText: "some log\n`json {\"status\":\"SUCCESS\"}`\nend",
			token:      "valid",
			wantPaused: false,
		},
		{
			name:       "fenced invalid json ignored",
			wr:         workerResult{Status: "SUCCESS"},
			stdoutText: "some log\n`json {\"status\":\"BLOCKED\"`\nend", // missing closing brace
			token:      "valid",
			wantPaused: false,
		},
		{
			name:       "control plane failure (stale token)",
			wr:         workerResult{Status: controlplane.StateNeedsInfo},
			stdoutText: "",
			token:      "bad-token",
			wantPaused: false,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newWorkerEnv(t, nil)
			_ = submitTask(t, env, "test-pause-"+strconv.Itoa(i), 3, nil)
			claimed, err := env.store.ClaimTask(context.Background(), "w1", 60)
			if err != nil || claimed == nil {
				t.Fatalf("ClaimTask: %v", err)
			}
			env.store.StartTask(claimed.TaskID, "w1", *claimed.LeaseToken)

			token := *claimed.LeaseToken
			if tc.token == "bad-token" {
				token = "bad-token"
			}

			got := env.sup.maybePause(context.Background(), tc.wr, tc.stdoutText, claimed.TaskID, "w1", token)
			if got != tc.wantPaused {
				t.Errorf("maybePause() = %v, want %v", got, tc.wantPaused)
			}
		})
	}
}
