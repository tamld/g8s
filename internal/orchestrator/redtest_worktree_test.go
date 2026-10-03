package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/process"
)

// -----------------------------------------------------------------------------
// Guarantee 4: Worktree collision-refusal holds for the state-dir root
// -----------------------------------------------------------------------------

// TestRedtest_Worktree_CollisionRefusal_DefaultStateDirRoot verifies that Pool.Acquire
// uses the current default root (<state_dir>/worktrees) and enforces #477's .git marker
// collision refusal rule: when a collision with a live worktree is detected, Acquire
// refuses to RemoveAll and retries with a fresh shortID.
func TestRedtest_Worktree_CollisionRefusal_DefaultStateDirRoot(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)

	repo := setupGitRepo(t)

	// NewPool with Root omitted must default to <state_dir>/worktrees (#465/#468)
	pool, err := NewPool(PoolOptions{Repo: repo})
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	expectedRoot := filepath.Join(stateDir, "worktrees")
	if pool.root != expectedRoot {
		t.Fatalf("expected pool root %q, got %q", expectedRoot, pool.root)
	}

	// Pre-place a live worktree at the would-be shortID collision path
	collisionID := "deadbeef"
	plantedDir := filepath.Join(expectedRoot, "wt-"+collisionID)
	if err := os.MkdirAll(plantedDir, 0o755); err != nil {
		t.Fatalf("mkdir planted dir: %v", err)
	}
	gitMarker := filepath.Join(plantedDir, ".git")
	if err := os.WriteFile(gitMarker, []byte("gitdir: /foreign/repo/.git/worktrees/wt-deadbeef\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	deliverableFile := filepath.Join(plantedDir, "deliverable.txt")
	preciousContent := []byte("irreplaceable deliverable data from another session")
	if err := os.WriteFile(deliverableFile, preciousContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// Override shortIDFn: first call collides with collisionID, second generates freshID "cafebabe"
	origShortIDFn := shortIDFn
	t.Cleanup(func() { shortIDFn = origShortIDFn })

	callCount := 0
	shortIDFn = func() string {
		callCount++
		if callCount == 1 {
			return collisionID
		}
		return "cafebabe"
	}

	ctx := context.Background()
	wt, err := pool.Acquire(ctx, "task-retry-success")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer func() { _ = pool.Release(ctx, wt, false) }()

	if wt.ID != "wt-cafebabe" {
		t.Fatalf("expected wt.ID wt-cafebabe, got %s", wt.ID)
	}
	if wt.Path == plantedDir {
		t.Fatalf("acquired worktree path equals planted live worktree path")
	}

	// Verify planted live worktree and deliverable were NOT removed by RemoveAll
	if _, err := os.Stat(plantedDir); os.IsNotExist(err) {
		t.Fatalf("planted worktree directory was destroyed by RemoveAll")
	}
	if _, err := os.Stat(gitMarker); os.IsNotExist(err) {
		t.Fatalf(".git marker was destroyed")
	}
	content, err := os.ReadFile(deliverableFile)
	if err != nil || string(content) != string(preciousContent) {
		t.Fatalf("deliverable content corrupted or removed: %v", err)
	}
}

// TestRedtest_Worktree_CollisionRefusal_5Attempts_NoRemoveAll verifies that after 5
// consecutive collisions, Acquire returns an error and NEVER calls RemoveAll on the live worktree.
func TestRedtest_Worktree_CollisionRefusal_5Attempts_NoRemoveAll(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)

	repo := setupGitRepo(t)
	pool, err := NewPool(PoolOptions{Repo: repo})
	if err != nil {
		t.Fatalf("NewPool failed: %v", err)
	}

	expectedRoot := filepath.Join(stateDir, "worktrees")
	collisionID := "deadcafe"
	plantedDir := filepath.Join(expectedRoot, "wt-"+collisionID)
	if err := os.MkdirAll(plantedDir, 0o755); err != nil {
		t.Fatalf("mkdir planted dir: %v", err)
	}
	gitMarker := filepath.Join(plantedDir, ".git")
	if err := os.WriteFile(gitMarker, []byte("gitdir: /foreign/repo/.git/worktrees/wt-deadcafe\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	deliverableFile := filepath.Join(plantedDir, "precious.txt")
	preciousContent := []byte("uncommitted preserved work")
	if err := os.WriteFile(deliverableFile, preciousContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	origShortIDFn := shortIDFn
	t.Cleanup(func() { shortIDFn = origShortIDFn })
	attempts := 0
	shortIDFn = func() string {
		attempts++
		return collisionID
	}

	ctx := context.Background()
	_, err = pool.Acquire(ctx, "task-refusal-5attempts")
	if err == nil {
		t.Fatal("expected error on 5 collisions, got nil")
	}

	expectedErr := fmt.Sprintf("worktree path collision with a live worktree after 5 attempts: %s", plantedDir)
	if err.Error() != expectedErr {
		t.Fatalf("expected error %q, got %q", expectedErr, err.Error())
	}
	if attempts != 5 {
		t.Errorf("expected 5 attempts, got %d", attempts)
	}

	// Verify planted directory and contents were NEVER removed
	if _, statErr := os.Stat(plantedDir); os.IsNotExist(statErr) {
		t.Fatalf("planted worktree was destroyed by RemoveAll")
	}
	if _, statErr := os.Stat(gitMarker); os.IsNotExist(statErr) {
		t.Fatalf("planted .git marker was destroyed")
	}
	content, rerr := os.ReadFile(deliverableFile)
	if rerr != nil || string(content) != string(preciousContent) {
		t.Fatalf("deliverable content corrupted or destroyed: %v", rerr)
	}
}

// TestRedtest_Worktree_OrphanSweep_NeverRemovesPreservedWorktree_WindowsChain verifies
// the sa-002 Windows-chain scenario (POSIX-simulated): when an orphan sweep executes
// against a preserved live worktree carrying a .git marker, even if the worktree registration
// check misses it (simulated by passing a disjoint repo or unregistered candidate),
// the .git marker rule prevents its removal.
func TestRedtest_Worktree_OrphanSweep_NeverRemovesPreservedWorktree_WindowsChain(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", stateDir)

	worktreeRoot := filepath.Join(stateDir, "worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}

	// 1. Live preserved worktree with .git marker and deliverable
	liveWT := filepath.Join(worktreeRoot, "wt-preserved-sa002")
	if err := os.MkdirAll(filepath.Join(liveWT, "deep"), 0o755); err != nil {
		t.Fatalf("mkdir liveWT: %v", err)
	}
	gitMarker := filepath.Join(liveWT, ".git")
	if err := os.WriteFile(gitMarker, []byte("gitdir: /another/repo/.git/worktrees/wt-preserved-sa002\n"), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}
	deliverablePath := filepath.Join(liveWT, "deep", "deliverable.txt")
	deliverableContent := []byte("preserved deliverables from task sa-002")
	if err := os.WriteFile(deliverablePath, deliverableContent, 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}

	// 2. Genuine orphan debris without .git marker
	debrisDir := filepath.Join(worktreeRoot, "wt-debris-abandoned")
	if err := os.MkdirAll(debrisDir, 0o755); err != nil {
		t.Fatalf("mkdir debrisDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(debrisDir, "junk.tmp"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write debris file: %v", err)
	}

	// POSIX simulation: run cleanup hook targeting orphan-dir from a DIFFERENT repo
	// (so git worktree list has no record of wt-preserved-sa002).
	disjointRepo := setupGitRepo(t)
	err := RunCleanupHook(context.Background(), CleanupHookOptions{
		Targets:         []string{cleanup.TargetOrphanDir},
		RepoDir:         disjointRepo,
		WorktreeBaseDir: worktreeRoot,
	})
	if err != nil {
		t.Fatalf("RunCleanupHook failed: %v", err)
	}

	// Live worktree must be preserved
	if _, err := os.Stat(deliverablePath); err != nil {
		t.Fatalf("live worktree deliverable was removed by orphan sweep: %v", err)
	}
	if _, err := os.Stat(gitMarker); err != nil {
		t.Fatalf("live worktree .git marker was removed by orphan sweep: %v", err)
	}

	// Genuine debris must be removed
	if _, err := os.Stat(debrisDir); err == nil {
		t.Fatalf("genuine debris directory was not removed by orphan sweep")
	}
}

// -----------------------------------------------------------------------------
// Guarantee 5: InstanceID partitions the blast radius
// -----------------------------------------------------------------------------

// mockProcessLister provides hermetic process listing for testing process qualification.
type mockProcessLister struct {
	processes []process.ProcessInfo
}

func (m *mockProcessLister) List() ([]process.ProcessInfo, error) {
	return m.processes, nil
}
func (m *mockProcessLister) Kill(pid int) error      { return nil }
func (m *mockProcessLister) KillForce(pid int) error { return nil }
func (m *mockProcessLister) IsAlive(pid int) bool    { return true }
func (m *mockProcessLister) ResolveCWD(pid int) string {
	for _, p := range m.processes {
		if p.PID == pid {
			return p.CWD
		}
	}
	return ""
}

// recordingProcessManager wraps cleanup.DefaultProcessManager to execute genuine
// ghost process qualification (#468 identity-scoped contract) while recording
// kill invocations hermetically without signaling external host processes.
type recordingProcessManager struct {
	cleanup.DefaultProcessManager
	mu         sync.Mutex
	killedWith map[int][]syscall.Signal
}

func (r *recordingProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.killedWith == nil {
		r.killedWith = make(map[int][]syscall.Signal)
	}
	r.killedWith[pid] = append(r.killedWith[pid], sig)
	return nil
}

func (r *recordingProcessManager) IsProcessAlive(pid int) bool {
	return true
}

func (r *recordingProcessManager) WasKilled(pid int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.killedWith[pid]) > 0
}

// TestRedtest_InstanceID_PartitionsBlastRadius verifies:
//  1. Two state dirs yield two distinct, persistent InstanceIDs.
//  2. Aegis kill vector POSIX simulation: when Instance A runs a ghost-process sweep
//     with a stale heartbeat mimicking the PID of Instance B's live worker, the
//     #468 identity-scoped kill contract checks CWD/cmdline corroboration and does
//     NOT kill Instance B's live worker.
func TestRedtest_InstanceID_PartitionsBlastRadius(t *testing.T) {
	stateDirA := t.TempDir()
	stateDirB := t.TempDir()
	repoA := t.TempDir()
	repoB := t.TempDir()

	// 1. Verify InstanceID partition across distinct state dirs
	t.Setenv("G8S_STATE_DIR", stateDirA)
	idA := pathutil.InstanceID()
	if len(idA) != 32 {
		t.Fatalf("expected 32-char hex instance ID for A, got %q", idA)
	}

	t.Setenv("G8S_STATE_DIR", stateDirB)
	idB := pathutil.InstanceID()
	if len(idB) != 32 {
		t.Fatalf("expected 32-char hex instance ID for B, got %q", idB)
	}

	if idA == idB {
		t.Fatalf("InstanceID across distinct state dirs must be partitioned, got identical: %s", idA)
	}

	// 2. Setup heartbeats in Instance A's repo:
	// - Stale heartbeat for PID 9001 (recycled / colliding PID, mimicking Instance B's worker)
	// - Stale heartbeat for PID 9002 (Instance A's own genuine stale worker)
	hbDirA := filepath.Join(repoA, ".heartbeat", "agy")
	if err := os.MkdirAll(hbDirA, 0o755); err != nil {
		t.Fatalf("mkdir hbDirA: %v", err)
	}

	now := time.Now()
	writeHB := func(dir string, pid int, session string) {
		hb := cleanup.HeartbeatData{
			SessionID:   session,
			PID:         pid,
			Binary:      "agy",
			CommandLine: "agy worker --task=" + session,
			StartedAt:   now.Add(-30 * time.Minute).Format(time.RFC3339),
			LastUpdate:  now.Add(-15 * time.Minute).Format(time.RFC3339), // > 5m maxAge (stale)
			Status:      "running",
		}
		data, err := json.Marshal(hb)
		if err != nil {
			t.Fatalf("marshal hb: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, session+".json"), data, 0o644); err != nil {
			t.Fatalf("write hb: %v", err)
		}
	}

	writeHB(hbDirA, 9001, "sess-aegis-recycled")
	writeHB(hbDirA, 9002, "sess-own-stale")

	// 3. Process layout on the host:
	// - PID 9001: Instance B's LIVE worker! CWD is repoB, CommandLine references repoB.
	// - PID 9002: Instance A's STALE worker! CWD is repoA, CommandLine references repoA.
	lister := &mockProcessLister{
		processes: []process.ProcessInfo{
			{
				PID:         9001,
				PPID:        100,
				Binary:      "agy",
				CommandLine: "agy worker --model test --repo " + repoB,
				CWD:         repoB, // Outside repoA! Corroboration path is repoB!
			},
			{
				PID:         9002,
				PPID:        100,
				Binary:      "agy",
				CommandLine: "agy worker --model test --repo " + repoA,
				CWD:         repoA, // Inside repoA! Corroboration path is repoA!
			},
		},
	}

	pm := &recordingProcessManager{
		DefaultProcessManager: cleanup.DefaultProcessManager{
			RepoDir: repoA,
			Lister:  lister,
		},
	}

	// 4. Run cleanup hook for Instance A with target ghost-process
	t.Setenv("G8S_STATE_DIR", stateDirA)
	err := RunCleanupHook(context.Background(), CleanupHookOptions{
		Targets:        []string{cleanup.TargetGhostProcess},
		RepoDir:        repoA,
		HeartbeatDir:   filepath.Join(repoA, ".heartbeat"),
		ProcessManager: pm,
		Clock:          func() time.Time { return now },
		GracePeriod:    10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunCleanupHook failed: %v", err)
	}

	// 5. Verify the identity-scoped kill contract (#468):
	// - PID 9001 belongs to Instance B (corroboration path points to repoB).
	//   Despite having a stale heartbeat in repoA (PID recycling / collision),
	//   it must NOT be killed!
	if pm.WasKilled(9001) {
		t.Fatalf("CRITICAL CONTRACT VIOLATION (#468 aegis kill vector): PID 9001 (Instance B's live worker) was killed by Instance A's ghost sweep!")
	}

	// - PID 9002 belongs to Instance A (corroboration path points to repoA).
	//   It has a stale heartbeat and verified repoA corroboration, so it MUST be killed.
	if !pm.WasKilled(9002) {
		t.Fatalf("expected PID 9002 (Instance A's own stale worker) to be killed, but it survived")
	}

	t.Logf("Blast radius partitioned successfully: Instance A killed own ghost (PID %d) and spared Instance B worker (PID %d)", 9002, 9001)
}

// TestRedtest_POSIX_SkipExample illustrates standard POSIX skip discipline
// for any non-portable platform probes per constraints.
func TestRedtest_POSIX_SkipDiscipline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only: skipping Unix domain / signal test on Windows")
	}
	// On POSIX, syscall.SIGTERM is 15
	if syscall.SIGTERM != 15 {
		t.Errorf("expected SIGTERM=15 on POSIX, got %d", syscall.SIGTERM)
	}
}
