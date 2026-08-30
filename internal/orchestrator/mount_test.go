package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// --- Custom Mock Mounts for Testing ---

type prefixSkill struct {
	prefix string
}

func (s prefixSkill) Name() string { return "prefix_skill" }
func (s prefixSkill) Inject(payload string) (string, error) {
	return s.prefix + payload, nil
}

type suffixSkill struct {
	suffix string
}

func (s suffixSkill) Name() string { return "suffix_skill" }
func (s suffixSkill) Inject(payload string) (string, error) {
	return payload + s.suffix, nil
}

type errorSkill struct {
	err error
}

func (s errorSkill) Name() string { return "error_skill" }
func (s errorSkill) Inject(_ string) (string, error) {
	return "", s.err
}

type rewriteTaskHook struct {
	prefix string
}

func (h rewriteTaskHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	task.Task.Prompt = h.prefix + task.Task.Prompt
	task.WorkerName = "rewritten-" + task.WorkerName
	return task, nil
}

func (h rewriteTaskHook) PostWait(_ context.Context, _ TaskSpec, _ Receipt) error {
	return nil
}

type recordingReceiptHook struct {
	mu           sync.Mutex
	lastTaskSpec TaskSpec
	lastReceipt  Receipt
	calledCount  int
}

func (h *recordingReceiptHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	return task, nil
}

func (h *recordingReceiptHook) PostWait(_ context.Context, task TaskSpec, receipt Receipt) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastTaskSpec = task
	h.lastReceipt = receipt
	h.calledCount++
	return nil
}

type mutatingReceiptHook struct {
	callback func(receipt *Receipt)
}

func (h mutatingReceiptHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	return task, nil
}

func (h mutatingReceiptHook) PostWait(_ context.Context, _ TaskSpec, receipt Receipt) error {
	if h.callback != nil {
		h.callback(&receipt)
	}
	return nil
}

type errorPreSpawnHook struct {
	err error
}

func (h errorPreSpawnHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	return task, h.err
}

func (h errorPreSpawnHook) PostWait(_ context.Context, _ TaskSpec, _ Receipt) error {
	return nil
}

type errorPostWaitHook struct {
	err error
}

func (h errorPostWaitHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	return task, nil
}

func (h errorPostWaitHook) PostWait(_ context.Context, _ TaskSpec, _ Receipt) error {
	return h.err
}

type customMemoryMount struct {
	name  string
	store map[string]map[string]string
}

func (m *customMemoryMount) Load(_ context.Context, sessionID string) (map[string]string, error) {
	if m.store == nil {
		return map[string]string{}, nil
	}
	return m.store[sessionID], nil
}

func (m *customMemoryMount) Save(_ context.Context, sessionID string, vars map[string]string) error {
	if m.store == nil {
		m.store = make(map[string]map[string]string)
	}
	m.store[sessionID] = vars
	return nil
}

// --- SkillMount Tests ---

func TestSkillMountRegisterTwoSkillsOrder(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "[SKILL1]"})
	reg.RegisterSkill(suffixSkill{suffix: "[SKILL2]"})

	out, err := reg.Skills().Inject("original_prompt")
	if err != nil {
		t.Fatalf("Inject unexpected error: %v", err)
	}
	expected := "[SKILL1]original_prompt[SKILL2]"
	if out != expected {
		t.Errorf("Inject() = %q, want %q", out, expected)
	}
}

func TestSkillMountErrorShortCircuit(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "p1:"})
	reg.RegisterSkill(errorSkill{err: errors.New("skill boom")})
	reg.RegisterSkill(suffixSkill{suffix: ":s2"})

	_, err := reg.Skills().Inject("test")
	if err == nil {
		t.Fatal("expected error from errorSkill")
	}
	if !containsString(err.Error(), "skill boom") {
		t.Errorf("expected 'skill boom', got %v", err)
	}
}

func TestNoOpSkill(t *testing.T) {
	noop := NoOpSkill{}
	if noop.Name() != "noop" {
		t.Errorf("NoOpSkill.Name() = %q, want 'noop'", noop.Name())
	}
	got, err := noop.Inject("hello world")
	if err != nil {
		t.Fatalf("NoOpSkill.Inject unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("NoOpSkill.Inject() = %q, want 'hello world'", got)
	}
}

func TestEmptySkillsRegistry(t *testing.T) {
	reg := NewMountRegistry()
	got, err := reg.Skills().Inject("payload")
	if err != nil {
		t.Fatalf("empty registry Skills().Inject err: %v", err)
	}
	if got != "payload" {
		t.Errorf("got %q, want 'payload'", got)
	}
	if reg.Skills().Name() != "noop" {
		t.Errorf("expected noop name for empty skills, got %q", reg.Skills().Name())
	}
}

// --- HookMount Tests ---

func TestHookMountPreSpawnRewriteTask(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterHook(rewriteTaskHook{prefix: "step1:"})
	reg.RegisterHook(rewriteTaskHook{prefix: "step2:"})

	taskSpec := TaskSpec{
		TaskID:     "t-100",
		WorkerName: "worker-a",
		Task: Task{
			ID:     "t-100",
			Prompt: "solve bug",
		},
	}

	res, err := reg.Hooks().PreSpawn(context.Background(), taskSpec)
	if err != nil {
		t.Fatalf("PreSpawn failed: %v", err)
	}
	expectedPrompt := "step2:step1:solve bug"
	if res.Task.Prompt != expectedPrompt {
		t.Errorf("res.Task.Prompt = %q, want %q", res.Task.Prompt, expectedPrompt)
	}
	expectedWorker := "rewritten-rewritten-worker-a"
	if res.WorkerName != expectedWorker {
		t.Errorf("res.WorkerName = %q, want %q", res.WorkerName, expectedWorker)
	}
}

func TestHookMountPostWaitReceipt(t *testing.T) {
	reg := NewMountRegistry()
	recorder := &recordingReceiptHook{}
	var mutatedReceipt Receipt
	mutator := mutatingReceiptHook{
		callback: func(r *Receipt) {
			r.Stdout = "mutated stdout"
			mutatedReceipt = *r
		},
	}

	reg.RegisterHook(recorder)
	reg.RegisterHook(mutator)

	taskSpec := TaskSpec{
		TaskID: "task-xyz",
	}
	receipt := Receipt{
		TaskID: "task-xyz",
		OK:     true,
		Stdout: "initial output",
	}

	err := reg.Hooks().PostWait(context.Background(), taskSpec, receipt)
	if err != nil {
		t.Fatalf("PostWait failed: %v", err)
	}

	recorder.mu.Lock()
	if recorder.calledCount != 1 {
		t.Errorf("recorder calledCount = %d, want 1", recorder.calledCount)
	}
	if recorder.lastTaskSpec.TaskID != "task-xyz" {
		t.Errorf("recorder TaskID = %q, want task-xyz", recorder.lastTaskSpec.TaskID)
	}
	recorder.mu.Unlock()

	if mutatedReceipt.Stdout != "mutated stdout" {
		t.Errorf("mutatedReceipt.Stdout = %q, want 'mutated stdout'", mutatedReceipt.Stdout)
	}
}

func TestHookMountPreSpawnErrorShortCircuit(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterHook(rewriteTaskHook{prefix: "before:"})
	reg.RegisterHook(errorPreSpawnHook{err: errors.New("pre-spawn rejected")})
	reg.RegisterHook(rewriteTaskHook{prefix: "after:"})

	taskSpec := TaskSpec{Task: Task{Prompt: "hello"}}
	_, err := reg.Hooks().PreSpawn(context.Background(), taskSpec)
	if err == nil {
		t.Fatal("expected PreSpawn error")
	}
	if !containsString(err.Error(), "pre-spawn rejected") {
		t.Errorf("expected 'pre-spawn rejected', got %v", err)
	}
}

func TestHookMountPostWaitErrorShortCircuit(t *testing.T) {
	reg := NewMountRegistry()
	recorder := &recordingReceiptHook{}
	reg.RegisterHook(errorPostWaitHook{err: errors.New("post-wait failure")})
	reg.RegisterHook(recorder)

	err := reg.Hooks().PostWait(context.Background(), TaskSpec{}, Receipt{})
	if err == nil {
		t.Fatal("expected PostWait error")
	}
	if !containsString(err.Error(), "post-wait failure") {
		t.Errorf("expected 'post-wait failure', got %v", err)
	}

	recorder.mu.Lock()
	if recorder.calledCount != 0 {
		t.Errorf("recorder should not be called after error short-circuit, got %d", recorder.calledCount)
	}
	recorder.mu.Unlock()
}

func TestNoOpHook(t *testing.T) {
	hook := NoOpHook{}
	task := TaskSpec{TaskID: "task-noop", Task: Task{Prompt: "noop prompt"}}
	res, err := hook.PreSpawn(context.Background(), task)
	if err != nil {
		t.Fatalf("NoOpHook.PreSpawn err: %v", err)
	}
	if res.Task.Prompt != "noop prompt" {
		t.Errorf("res.Task.Prompt = %q, want 'noop prompt'", res.Task.Prompt)
	}
	if err := hook.PostWait(context.Background(), task, Receipt{}); err != nil {
		t.Errorf("NoOpHook.PostWait err: %v", err)
	}
}

func TestEmptyHooksRegistry(t *testing.T) {
	reg := NewMountRegistry()
	task := TaskSpec{TaskID: "task-1"}
	res, err := reg.Hooks().PreSpawn(context.Background(), task)
	if err != nil {
		t.Fatalf("empty Hooks().PreSpawn err: %v", err)
	}
	if res.TaskID != "task-1" {
		t.Errorf("res.TaskID = %q, want task-1", res.TaskID)
	}
	if err := reg.Hooks().PostWait(context.Background(), task, Receipt{}); err != nil {
		t.Fatalf("empty Hooks().PostWait err: %v", err)
	}
}

// --- MemoryMount Tests ---

func TestNoOpMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	mem := NewNoOpMemory()

	// Initial load should return empty map
	vars, err := mem.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("initial Load error: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("expected empty map for initial load, got %v", vars)
	}

	// Save variables
	data := map[string]string{
		"agent_model": "gemini-flash",
		"turn_count":  "3",
	}
	if err := mem.Save(ctx, "session-1", data); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Mutate original map to ensure deep copy isolation
	data["turn_count"] = "99"

	// Load variables back
	loaded, err := mem.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded["agent_model"] != "gemini-flash" {
		t.Errorf("agent_model = %q, want 'gemini-flash'", loaded["agent_model"])
	}
	if loaded["turn_count"] != "3" {
		t.Errorf("turn_count = %q, want '3' (isolation broken)", loaded["turn_count"])
	}

	// Mutate returned map to ensure isolation
	loaded["agent_model"] = "mutated"
	loadedAgain, err := mem.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loadedAgain["agent_model"] != "gemini-flash" {
		t.Errorf("agent_model = %q, want 'gemini-flash' after returned map mutated", loadedAgain["agent_model"])
	}

	// Another session should remain empty
	session2, err := mem.Load(ctx, "session-2")
	if err != nil {
		t.Fatalf("session-2 Load error: %v", err)
	}
	if len(session2) != 0 {
		t.Errorf("session-2 should be empty, got %v", session2)
	}
}

func TestMemoryMountRegistryPassthrough(t *testing.T) {
	ctx := context.Background()
	reg := NewMountRegistry()

	custom1 := &customMemoryMount{name: "custom1"}
	custom2 := &customMemoryMount{name: "custom2"}

	reg.RegisterMemory(custom1)
	reg.RegisterMemory(custom2) // should use first registered

	if err := reg.Memory().Save(ctx, "sid-a", map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	if custom1.store["sid-a"]["foo"] != "bar" {
		t.Errorf("custom1 not written to, got %v", custom1.store)
	}
	if len(custom2.store) != 0 {
		t.Errorf("custom2 should not be used, got %v", custom2.store)
	}

	loaded, err := reg.Memory().Load(ctx, "sid-a")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded["foo"] != "bar" {
		t.Errorf("Load foo = %q, want 'bar'", loaded["foo"])
	}
}

func TestMemoryMountDefaultRegistry(t *testing.T) {
	ctx := context.Background()
	reg := NewMountRegistry()

	// Default in-memory store persists across multiple calls to reg.Memory()
	if err := reg.Memory().Save(ctx, "sid-1", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := reg.Memory().Load(ctx, "sid-1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded["k"] != "v" {
		t.Errorf("loaded k = %q, want 'v'", loaded["k"])
	}
}

// --- AgyWorker with Mounts Tests ---

func TestAgyWorkerWithMounts_SkillInjection(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "PROMPT_PREFIX: "})

	worker := NewAgyWorker()
	mountedWorker := worker.WithMounts(*reg)

	// Verify worker delegates Inject
	res, err := mountedWorker.(SkillMount).Inject("my prompt")
	if err != nil {
		t.Fatalf("Inject error: %v", err)
	}
	if res != "PROMPT_PREFIX: my prompt" {
		t.Errorf("Inject = %q, want 'PROMPT_PREFIX: my prompt'", res)
	}
}

func TestAgyWorkerWithMounts_PreSpawnAndPostWaitHooks(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterHook(rewriteTaskHook{prefix: "INJECTED:"})
	recorder := &recordingReceiptHook{}
	reg.RegisterHook(recorder)

	worker := &AgyWorker{
		binary: "true",
		clock:  fixedClock,
	}
	mWorker := worker.WithMounts(*reg)

	handle, err := mWorker.Spawn(context.Background(), Task{
		ID:     "task-agy",
		Prompt: "do task",
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if !receipt.OK {
		t.Errorf("receipt OK = false, want true")
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.calledCount != 1 {
		t.Fatalf("recorder calledCount = %d, want 1", recorder.calledCount)
	}
	if recorder.lastTaskSpec.Task.Prompt != "INJECTED:do task" {
		t.Errorf("recorder prompt = %q, want 'INJECTED:do task'", recorder.lastTaskSpec.Task.Prompt)
	}
}

func TestAgyWorkerWithMounts_SkillErrorOnSpawn(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(errorSkill{err: errors.New("skill rejection")})

	worker := &AgyWorker{binary: "true", clock: fixedClock}
	mWorker := worker.WithMounts(*reg)

	_, err := mWorker.Spawn(context.Background(), Task{Prompt: "test"})
	if err == nil {
		t.Fatal("expected Spawn error when skill returns error")
	}
	if !containsString(err.Error(), "skill rejection") {
		t.Errorf("expected 'skill rejection', got %v", err)
	}
}

func TestAgyWorkerWithMounts_PreSpawnErrorOnSpawn(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterHook(errorPreSpawnHook{err: errors.New("prespawn disallowed")})

	worker := &AgyWorker{binary: "true", clock: fixedClock}
	mWorker := worker.WithMounts(*reg)

	_, err := mWorker.Spawn(context.Background(), Task{Prompt: "test"})
	if err == nil {
		t.Fatal("expected Spawn error when pre-spawn hook returns error")
	}
	if !containsString(err.Error(), "prespawn disallowed") {
		t.Errorf("expected 'prespawn disallowed', got %v", err)
	}
}

func TestAgyWorkerWithMounts_PostWaitErrorOnWait(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterHook(errorPostWaitHook{err: errors.New("postwait validation failed")})

	worker := &AgyWorker{binary: "true", clock: fixedClock}
	mWorker := worker.WithMounts(*reg)

	handle, err := mWorker.Spawn(context.Background(), Task{Prompt: "test"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	_, err = handle.Wait(context.Background())
	if err == nil {
		t.Fatal("expected Wait error when post-wait hook returns error")
	}
	if !containsString(err.Error(), "postwait validation failed") {
		t.Errorf("expected 'postwait validation failed', got %v", err)
	}
}

func TestAgyWorkerMemoryDelegation(t *testing.T) {
	ctx := context.Background()
	reg := NewMountRegistry()
	worker := NewAgyWorker()
	mWorker := worker.WithMounts(*reg)

	memWorker, ok := mWorker.(MemoryMount)
	if !ok {
		t.Fatalf("mWorker does not implement MemoryMount")
	}

	if err := memWorker.Save(ctx, "session-agy", map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	vars, err := memWorker.Load(ctx, "session-agy")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if vars["foo"] != "bar" {
		t.Errorf("vars[foo] = %q, want 'bar'", vars["foo"])
	}

	// Mounts accessor
	if w, ok := mWorker.(*AgyWorker); ok {
		if w.Mounts().Memory() == nil {
			t.Errorf("Mounts().Memory() should not be nil")
		}
	}
}

func TestAgyWorkerDefaultsWithoutWithMounts(t *testing.T) {
	ctx := context.Background()
	w := NewAgyWorker()

	// Direct mount interface calls with defaults
	injected, err := w.Inject("prompt")
	if err != nil || injected != "prompt" {
		t.Errorf("Inject() = %q, %v; want 'prompt', nil", injected, err)
	}

	taskSpec := TaskSpec{TaskID: "t1", Prompt: "prompt"}
	pTask, err := w.PreSpawn(ctx, taskSpec)
	if err != nil || pTask.TaskID != "t1" {
		t.Errorf("PreSpawn() = %v, %v; want %v, nil", pTask, err, taskSpec)
	}
	if !strings.Contains(pTask.Prompt, "Before you start, take 30 seconds to answer:") || !strings.HasSuffix(pTask.Prompt, "prompt") {
		t.Errorf("PreSpawn() prompt = %q, want Attentioner reflection prefix", pTask.Prompt)
	}

	if err := w.PostWait(ctx, taskSpec, Receipt{}); err != nil {
		t.Errorf("PostWait() = %v; want nil", err)
	}

	if err := w.Save(ctx, "s1", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Save() = %v; want nil", err)
	}
	vars, err := w.Load(ctx, "s1")
	if err != nil || vars["k"] != "v" {
		t.Errorf("Load() = %v, %v; want map[k:v], nil", vars, err)
	}
}

func TestDefaultOrchestratorIncludesAttentioner(t *testing.T) {
	reg := DefaultRegistry()
	if reg == nil {
		t.Fatalf("DefaultRegistry returned nil")
	}

	w, ok := reg.Get("agy")
	if !ok || w == nil {
		t.Fatalf("expected 'agy' worker registered in DefaultRegistry")
	}

	agy, ok := w.(*AgyWorker)
	if !ok {
		t.Fatalf("expected *AgyWorker type, got %T", w)
	}

	if agy.Mounts() == nil {
		t.Fatalf("expected non-nil MountRegistry on AgyWorker")
	}

	ctx := context.Background()
	taskSpec := TaskSpec{
		TaskID: "test-task",
		Prompt: "Original prompt text",
	}
	res, err := agy.PreSpawn(ctx, taskSpec)
	if err != nil {
		t.Fatalf("PreSpawn failed: %v", err)
	}

	if !strings.Contains(res.Prompt, "Before you start, take 30 seconds to answer:") {
		t.Errorf("expected PreSpawn to inject reflection prompt, got: %s", res.Prompt)
	}
	if !strings.HasSuffix(res.Prompt, "Original prompt text") {
		t.Errorf("expected PreSpawn to preserve original prompt suffix, got: %s", res.Prompt)
	}
}

// --- MountedWorker Generic Wrapper Tests ---

type mockGenericWorker struct {
	name       string
	lastTask   Task
	spawnCalls atomic.Int32
}

func (m *mockGenericWorker) Name() string { return m.name }
func (m *mockGenericWorker) Available(_ context.Context) error {
	return nil
}

func (m *mockGenericWorker) Spawn(_ context.Context, t Task) (Handle, error) {
	m.lastTask = t
	m.spawnCalls.Add(1)
	return &okHandle{receipt: Receipt{OK: true, TaskID: t.ID, WorkerName: m.name}}, nil
}

func TestMountedWorkerWrapperGenericWorker(t *testing.T) {
	ctx := context.Background()
	baseWorker := &mockGenericWorker{name: "generic"}
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "SKILL_PREFIX:"})
	reg.RegisterHook(rewriteTaskHook{prefix: "HOOK_PREFIX:"})
	recorder := &recordingReceiptHook{}
	reg.RegisterHook(recorder)

	wrapped := WithMounts(baseWorker, *reg)
	if wrapped.Name() != "generic" {
		t.Errorf("wrapped.Name() = %q, want 'generic'", wrapped.Name())
	}
	if err := wrapped.Available(ctx); err != nil {
		t.Errorf("wrapped.Available() err = %v", err)
	}

	mw, ok := wrapped.(*MountedWorker)
	if !ok {
		t.Fatalf("expected *MountedWorker wrapper")
	}
	if mw.Mounts().Skills() == nil {
		t.Errorf("Mounts().Skills() is nil")
	}

	// Test mount interfaces on MountedWorker
	if inj, err := mw.Inject("input"); err != nil || inj != "SKILL_PREFIX:input" {
		t.Errorf("Inject() = %q, %v", inj, err)
	}
	ts, err := mw.PreSpawn(ctx, TaskSpec{Task: Task{Prompt: "p"}})
	if err != nil || ts.Task.Prompt != "HOOK_PREFIX:p" {
		t.Errorf("PreSpawn() = %v, %v", ts, err)
	}
	if err := mw.PostWait(ctx, ts, Receipt{}); err != nil {
		t.Errorf("PostWait() = %v", err)
	}
	if err := mw.Save(ctx, "sid", map[string]string{"a": "1"}); err != nil {
		t.Errorf("Save() = %v", err)
	}
	if vars, err := mw.Load(ctx, "sid"); err != nil || vars["a"] != "1" {
		t.Errorf("Load() = %v, %v", vars, err)
	}

	// Test Spawn and Wait lifecycle
	handle, err := wrapped.Spawn(ctx, Task{ID: "t-gen", Prompt: "base prompt"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if baseWorker.lastTask.Prompt != "HOOK_PREFIX:SKILL_PREFIX:base prompt" {
		t.Errorf("baseWorker received prompt = %q, want 'HOOK_PREFIX:SKILL_PREFIX:base prompt'", baseWorker.lastTask.Prompt)
	}

	receipt, err := handle.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if !receipt.OK {
		t.Errorf("receipt OK = false, want true")
	}

	recorder.mu.Lock()
	if recorder.calledCount != 2 { // 1 from mw.PostWait, 1 from handle.Wait
		t.Errorf("recorder calledCount = %d, want 2", recorder.calledCount)
	}
	recorder.mu.Unlock()

	// WithMounts on MountedWorker returns new MountedWorker
	reg2 := NewMountRegistry()
	mw2 := mw.WithMounts(*reg2)
	if mw2 == nil {
		t.Fatal("WithMounts on MountedWorker returned nil")
	}
}

func TestMountedWorker_SkillError(t *testing.T) {
	baseWorker := &mockGenericWorker{name: "generic"}
	reg := NewMountRegistry()
	reg.RegisterSkill(errorSkill{err: errors.New("skill err")})

	wrapped := WithMounts(baseWorker, *reg)
	_, err := wrapped.Spawn(context.Background(), Task{Prompt: "test"})
	if err == nil || !containsString(err.Error(), "skill err") {
		t.Errorf("expected skill error, got %v", err)
	}
}

func TestMountedWorker_PreSpawnError(t *testing.T) {
	baseWorker := &mockGenericWorker{name: "generic"}
	reg := NewMountRegistry()
	reg.RegisterHook(errorPreSpawnHook{err: errors.New("prespawn err")})

	wrapped := WithMounts(baseWorker, *reg)
	_, err := wrapped.Spawn(context.Background(), Task{Prompt: "test"})
	if err == nil || !containsString(err.Error(), "prespawn err") {
		t.Errorf("expected prespawn error, got %v", err)
	}
}

func TestMountedWorker_PostWaitError(t *testing.T) {
	baseWorker := &mockGenericWorker{name: "generic"}
	reg := NewMountRegistry()
	reg.RegisterHook(errorPostWaitHook{err: errors.New("postwait err")})

	wrapped := WithMounts(baseWorker, *reg)
	handle, err := wrapped.Spawn(context.Background(), Task{Prompt: "test"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	_, err = handle.Wait(context.Background())
	if err == nil || !containsString(err.Error(), "postwait err") {
		t.Errorf("expected postwait error, got %v", err)
	}
}

func TestMountedWorker_BaseWorkerSpawnError(t *testing.T) {
	baseWorker := &alwaysFail{fakeWorker: fakeWorker{name: "failer", available: true}}
	reg := NewMountRegistry()
	wrapped := WithMounts(baseWorker, *reg)

	_, err := wrapped.Spawn(context.Background(), Task{Prompt: "test"})
	if err == nil || !containsString(err.Error(), "spawn boom") {
		t.Errorf("expected base worker spawn error, got %v", err)
	}
}

// --- Concurrency and Nil Safety Tests ---

func TestMountRegistryNilSafety(t *testing.T) {
	ctx := context.Background()
	var reg MountRegistry // zero value

	// Skills on zero value registry
	if s := reg.Skills(); s == nil || s.Name() != "noop" {
		t.Errorf("zero reg.Skills() = %v, want NoOpSkill", s)
	}
	inj, err := reg.Skills().Inject("text")
	if err != nil || inj != "text" {
		t.Errorf("zero reg.Skills().Inject() = %q, %v", inj, err)
	}

	// Hooks on zero value registry
	if h := reg.Hooks(); h == nil {
		t.Errorf("zero reg.Hooks() is nil")
	}
	ts, err := reg.Hooks().PreSpawn(ctx, TaskSpec{TaskID: "t1"})
	if err != nil || ts.TaskID != "t1" {
		t.Errorf("zero reg.Hooks().PreSpawn() = %v, %v", ts, err)
	}
	if err := reg.Hooks().PostWait(ctx, ts, Receipt{}); err != nil {
		t.Errorf("zero reg.Hooks().PostWait() = %v", err)
	}

	// Memory on zero value registry
	if m := reg.Memory(); m == nil {
		t.Errorf("zero reg.Memory() is nil")
	}
	if err := reg.Memory().Save(ctx, "s", map[string]string{"k": "v"}); err != nil {
		t.Errorf("zero reg.Memory().Save() = %v", err)
	}
	vars, err := reg.Memory().Load(ctx, "s")
	if err != nil || vars["k"] != "v" {
		t.Errorf("zero reg.Memory().Load() = %v, %v, want k:v", vars, err)
	}

	// NoOpMemory nil receiver
	var nilMem *NoOpMemory = nil
	if err := nilMem.Save(ctx, "s", nil); err != nil {
		t.Errorf("nilMem.Save() = %v, want nil", err)
	}
	if vars, err := nilMem.Load(ctx, "s"); err != nil || len(vars) != 0 {
		t.Errorf("nilMem.Load() = %v, %v, want empty map", vars, err)
	}
}

func TestMountRegistryRegisterNil(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(nil)
	reg.RegisterHook(nil)
	reg.RegisterMemory(nil)

	if got, err := reg.Skills().Inject("payload"); err != nil || got != "payload" {
		t.Errorf("Inject() = %q, %v", got, err)
	}
}

func TestMountRegistryConcurrent(t *testing.T) {
	reg := NewMountRegistry()
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		i := i
		wg.Add(3)

		go func() {
			defer wg.Done()
			reg.RegisterSkill(prefixSkill{prefix: fmt.Sprintf("[%d]", i)})
			_, _ = reg.Skills().Inject("test")
		}()

		go func() {
			defer wg.Done()
			reg.RegisterHook(rewriteTaskHook{prefix: fmt.Sprintf("h%d:", i)})
			_, _ = reg.Hooks().PreSpawn(ctx, TaskSpec{})
			_ = reg.Hooks().PostWait(ctx, TaskSpec{}, Receipt{})
		}()

		go func() {
			defer wg.Done()
			sid := fmt.Sprintf("session-%d", i)
			_ = reg.Memory().Save(ctx, sid, map[string]string{"iter": fmt.Sprintf("%d", i)})
			_, _ = reg.Memory().Load(ctx, sid)
		}()
	}

	wg.Wait()
}

func TestSkillChainName(t *testing.T) {
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "p:"})
	if name := reg.Skills().Name(); name != "skill_chain" {
		t.Errorf("skillChain.Name() = %q, want 'skill_chain'", name)
	}
}

func TestWrapWithMountsMountableWorker(t *testing.T) {
	w := NewAgyWorker()
	reg := NewMountRegistry()
	reg.RegisterSkill(prefixSkill{prefix: "custom:"})

	wrapped := WrapWithMounts(w, *reg)
	agy, ok := wrapped.(*AgyWorker)
	if !ok {
		t.Fatalf("expected WrapWithMounts to return *AgyWorker for MountableWorker")
	}
	res, err := agy.Inject("test")
	if err != nil || res != "custom:test" {
		t.Errorf("Inject() = %q, %v, want 'custom:test'", res, err)
	}
}

func TestNoOpMemoryNilStore(t *testing.T) {
	ctx := context.Background()
	mem := &NoOpMemory{store: nil}

	vars, err := mem.Load(ctx, "session")
	if err != nil || len(vars) != 0 {
		t.Errorf("Load on nil store = %v, %v", vars, err)
	}

	if err := mem.Save(ctx, "session", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Save on nil store error: %v", err)
	}

	vars, err = mem.Load(ctx, "session")
	if err != nil || vars["k"] != "v" {
		t.Errorf("Load after Save on nil store = %v, %v", vars, err)
	}
}

func TestChainsWithNilElements(t *testing.T) {
	ctx := context.Background()
	sc := &skillChain{skills: []SkillMount{nil, prefixSkill{prefix: "ok:"}, nil}}
	res, err := sc.Inject("data")
	if err != nil || res != "ok:data" {
		t.Errorf("sc.Inject with nil elements = %q, %v", res, err)
	}

	hc := &hookChain{hooks: []HookMount{nil, rewriteTaskHook{prefix: "h:"}, nil}}
	task, err := hc.PreSpawn(ctx, TaskSpec{Task: Task{Prompt: "p"}})
	if err != nil || task.Task.Prompt != "h:p" {
		t.Errorf("hc.PreSpawn with nil elements = %v, %v", task, err)
	}

	if err := hc.PostWait(ctx, task, Receipt{}); err != nil {
		t.Errorf("hc.PostWait with nil elements = %v", err)
	}
}
