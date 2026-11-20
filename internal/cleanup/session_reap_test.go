package cleanup

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// Red Test Proof for S1 PR-3 (#393): the cleanup sweep must reap dead and
// zombie supervisor sessions from the v11 sessions registry — removing their
// worktrees and sess/* branches, deleting their rows — while live sessions
// are untouched. RED = TargetOrphanSession does not exist yet.

type sessionFixture struct {
	ID           string
	Status       string
	Mode         string
	WorktreePath string
	HeartbeatAge time.Duration // age of heartbeat relative to sweep time
}

func setupSessionWorld(t *testing.T) (repoDir string, store *controlplane.Store, rawDB *sql.DB) {
	t.Helper()
	repoDir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "seed")

	var err error
	store, err = controlplane.NewControlPlane(filepath.Join(repoDir, "g8s.db"), nil)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rawDB, err = sql.Open("sqlite", "file:"+filepath.Join(repoDir, "g8s.db"))
	if err != nil {
		t.Fatalf("raw db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	return repoDir, store, rawDB
}

func insertSessionRow(t *testing.T, db *sql.DB, f sessionFixture, now time.Time) {
	t.Helper()
	hb := float64(now.Add(-f.HeartbeatAge).Unix())
	status := f.Status
	if status == "" {
		status = "active"
	}
	mode := f.Mode
	if mode == "" {
		mode = "in_place"
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, started_at, heartbeat_at, status, mode, worktree_path) VALUES (?,?,?,?,?,?)`,
		f.ID, hb, hb, status, mode, f.WorktreePath); err != nil {
		t.Fatalf("insert session %s: %v", f.ID, err)
	}
}

func sessionRowExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count session %s: %v", id, err)
	}
	return n > 0
}

// A dead worktree session is fully reaped: worktree removed, sess/* branch
// deleted, registry row deleted.
func TestOrphanSessionReapsDeadWorktreeSession(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()

	// Simulate a crashed promoted session: branch + worktree dir left behind.
	if _, err := exec.Command("git", "-C", repoDir, "branch", "sess/crashed").CombinedOutput(); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	wtDir := filepath.Join(repoDir, "..", filepath.Base(repoDir)+"-wt-crashed")
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "--detach", wtDir).CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wtDir) })
	insertSessionRow(t, raw, sessionFixture{
		ID: "crashed", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: 2 * time.Minute,
	}, now)

	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if report.Summary[TargetOrphanSession] == 0 {
		t.Fatalf("expected orphan-session items, got %+v", report.Summary)
	}

	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "sess/crashed").CombinedOutput(); err == nil {
		t.Fatalf("branch sess/crashed still exists: %s", out)
	}
	if sessionRowExists(t, raw, "crashed") {
		t.Fatal("registry row still present after reap")
	}
}

// A stale-active session (heartbeat older than the grace period) is a
// zombie: marked dead, then reaped.
func TestOrphanSessionReapsStaleZombie(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	insertSessionRow(t, raw, sessionFixture{
		ID: "zombie", Status: "active", Mode: "in_place", HeartbeatAge: time.Hour,
	}, now)

	if _, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return now },
	}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if sessionRowExists(t, raw, "zombie") {
		t.Fatal("zombie row still present after reap")
	}
}

// A live session (fresh heartbeat) is never touched.
func TestOrphanSessionSparesLiveSession(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	insertSessionRow(t, raw, sessionFixture{
		ID: "live", Status: "active", Mode: "worktree", WorktreePath: "/nowhere/wt-live", HeartbeatAge: 2 * time.Second,
	}, now)

	if _, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return now },
	}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !sessionRowExists(t, raw, "live") {
		t.Fatal("live session row was reaped — must never happen")
	}
}

// Dry-run reports zombies too (H2): stale-active rows appear as
// would_delete with the zombie prefix and stay active after the sweep.
func TestOrphanSessionDryRunReportsZombies(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	insertSessionRow(t, raw, sessionFixture{
		ID: "z1", Status: "active", Mode: "in_place", HeartbeatAge: time.Hour,
	}, now)

	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		DryRun:             true,
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	found := false
	for _, it := range report.Items {
		if it.ID == "z1" {
			found = true
			if it.Action != "would_delete" {
				t.Fatalf("dry-run action = %q, want would_delete", it.Action)
			}
			if !strings.Contains(it.Detail, "zombie") {
				t.Fatalf("detail = %q, want zombie prefix", it.Detail)
			}
		}
	}
	if !found {
		t.Fatal("dry-run did not report the zombie session")
	}
	var status string
	if err := raw.QueryRow(`SELECT status FROM sessions WHERE id = 'z1'`).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "active" {
		t.Fatalf("status after dry-run = %q, want active (dry-run must not mark)", status)
	}
}

// Dry-run reports would_* actions and changes nothing.
func TestOrphanSessionDryRun(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	insertSessionRow(t, raw, sessionFixture{
		ID: "d1", Status: "dead", Mode: "in_place", HeartbeatAge: time.Hour,
	}, now)

	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		DryRun:             true,
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	found := false
	for _, it := range report.Items {
		if it.ID == "d1" {
			found = true
			if it.Action != "would_delete" {
				t.Fatalf("dry-run action = %q, want would_delete", it.Action)
			}
		}
	}
	if !found {
		t.Fatal("dry-run did not report the dead session")
	}
	// Same pooled handle as the insert: one consistent view for both checks.
	if !sessionRowExists(t, raw, "d1") {
		t.Fatal("dry-run deleted the row")
	}
}

// #982 red test: reaping a session whose worktree carries UNCOMMITTED work
// must never destroy that work. git worktree remove (no --force) refuses a
// dirty worktree, and the old fallback was a blind os.RemoveAll — the reap
// must instead capture the uncommitted diff + status + untracked file
// contents into the evidence dir before the force-removal, and the registry
// row must still be cleaned per the existing semantics.
func TestOrphanSessionReapDirtyWorktreePreservesUncommitted(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()

	if _, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", "sess/dirty", repoDir+"-wt-dirty").CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	wtDir := repoDir + "-wt-dirty"
	t.Cleanup(func() { _ = os.RemoveAll(wtDir) })
	// Uncommitted work in the session worktree: a tracked modification plus
	// untracked files (flat and nested) that no diff would ever contain.
	if err := os.WriteFile(filepath.Join(wtDir, "README.md"), []byte("seed\n\ndirty edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const note = "precious uncommitted notes\n"
	if err := os.WriteFile(filepath.Join(wtDir, "notes.txt"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wtDir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	const deep = "nested untracked content\n"
	if err := os.WriteFile(filepath.Join(wtDir, "src", "deep.txt"), []byte(deep), 0o600); err != nil {
		t.Fatal(err)
	}
	insertSessionRow(t, raw, sessionFixture{
		ID: "dirty", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: 2 * time.Minute,
	}, now)

	evidenceRoot := filepath.Join(t.TempDir(), "evidence")
	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             filepath.Join(repoDir, "g8s.db"),
		Targets:            []string{TargetOrphanSession},
		SessionGracePeriod: 30 * time.Second,
		EvidenceDir:        evidenceRoot,
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var item *CleanupItem
	for i := range report.Items {
		if report.Items[i].ID == "dirty" {
			item = &report.Items[i]
		}
	}
	if item == nil {
		t.Fatalf("session dirty not reported: %+v", report.Items)
	}
	if item.Action != "deleted" {
		t.Fatalf("action = %q (%s), want deleted", item.Action, item.Detail)
	}

	// Cleanup still happens: worktree gone, branch gone, row gone.
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "sess/dirty").CombinedOutput(); err == nil {
		t.Fatalf("branch sess/dirty still exists: %s", out)
	}
	if sessionRowExists(t, raw, "dirty") {
		t.Fatal("registry row still present after reap")
	}

	// Nothing is lost: the uncommitted state was captured to the evidence dir.
	evDir := filepath.Join(evidenceRoot, "session-reap-dirty")
	if _, err := os.Stat(evDir); err != nil {
		t.Fatalf("evidence dir missing: %v (item detail: %s)", err, item.Detail)
	}
	if !strings.Contains(item.Detail, "evidence") {
		t.Fatalf("detail = %q, want evidence capture note", item.Detail)
	}
	diff, err := os.ReadFile(filepath.Join(evDir, "worktree.diff"))
	if err != nil {
		t.Fatalf("read worktree.diff: %v", err)
	}
	if !strings.Contains(string(diff), "+dirty edit") {
		t.Fatalf("worktree.diff does not contain the tracked modification:\n%s", diff)
	}
	status, err := os.ReadFile(filepath.Join(evDir, "status.txt"))
	if err != nil {
		t.Fatalf("read status.txt: %v", err)
	}
	for _, want := range []string{"README.md", "notes.txt", "src/"} {
		if !strings.Contains(string(status), want) {
			t.Fatalf("status.txt missing %q:\n%s", want, status)
		}
	}
	gotNote, err := os.ReadFile(filepath.Join(evDir, "untracked", "notes.txt"))
	if err != nil {
		t.Fatalf("untracked notes.txt not preserved: %v", err)
	}
	if string(gotNote) != note {
		t.Fatalf("untracked notes.txt content = %q, want %q", gotNote, note)
	}
	gotDeep, err := os.ReadFile(filepath.Join(evDir, "untracked", "src", "deep.txt"))
	if err != nil {
		t.Fatalf("nested untracked deep.txt not preserved: %v", err)
	}
	if string(gotDeep) != deep {
		t.Fatalf("nested untracked deep.txt content = %q, want %q", gotDeep, deep)
	}
}

// seedSessionWorktreeWithUncommitted creates a registered worktree on branch
// sess/<id> carrying uncommitted work (tracked modification + untracked
// file) — the fixture every #982 guard test destroys NOTHING of.
func seedSessionWorktreeWithUncommitted(t *testing.T, repoDir, id string) string {
	t.Helper()
	wtDir := repoDir + "-wt-" + id
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", "sess/"+id, wtDir).CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wtDir) })
	if err := os.WriteFile(filepath.Join(wtDir, "README.md"), []byte("seed\n\ndirty edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "notes.txt"), []byte("precious uncommitted notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return wtDir
}

// reapOneSession needs a fully populated config when called directly.
func directReapConfig(repoDir string) CleanupConfig {
	return CleanupConfig{
		RepoDir:   repoDir,
		GitRunner: &DefaultCleanupGitRunner{},
		Writer:    io.Discard,
		DryRun:    false,
	}
}

func assertSessionUntouched(t *testing.T, db *sql.DB, repoDir, id, wtDir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(wtDir, "notes.txt")); err != nil {
		t.Fatalf("uncommitted notes.txt destroyed — worktree must be untouched: %v", err)
	}
	if out, err := os.ReadFile(filepath.Join(wtDir, "notes.txt")); err != nil || string(out) != "precious uncommitted notes\n" {
		t.Fatalf("notes.txt content changed: %q (%v)", out, err)
	}
	if !sessionRowExists(t, db, id) {
		t.Fatalf("registry row for %s was deleted — abort must keep the row", id)
	}
	if out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "sess/"+id).CombinedOutput(); err != nil {
		t.Fatalf("branch sess/%s was deleted — abort must keep the branch: %v (%s)", id, err, out)
	}
}

// #982 (b): a session revived between the tick listing and the reap (status
// flipped back to active) must abort the reap — no removal of any resource.
func TestReapOneSessionAbortsWhenSessionRevived(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	wtDir := seedSessionWorktreeWithUncommitted(t, repoDir, "revived")
	insertSessionRow(t, raw, sessionFixture{
		ID: "revived", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: time.Hour,
	}, now)
	// Simulate revival: flip the row back to active after the listing.
	if _, err := raw.Exec(`UPDATE sessions SET status = 'active' WHERE id = 'revived'`); err != nil {
		t.Fatal(err)
	}

	item, prune := reapOneSession(context.Background(), directReapConfig(repoDir), raw,
		reapableSession{id: "revived", worktreePath: wtDir},
		float64(now.Add(-30*time.Second).Unix()))
	if item.Action != "skipped" {
		t.Fatalf("action = %q (%s), want skipped", item.Action, item.Detail)
	}
	if !strings.Contains(item.Detail, "came back") {
		t.Fatalf("detail = %q, want revival reason", item.Detail)
	}
	if prune {
		t.Fatal("aborted reap must not schedule a prune")
	}
	assertSessionUntouched(t, raw, repoDir, "revived", wtDir)
}

// #982 (b) variant: the row still reads dead but its heartbeat was refreshed
// past the cutoff — the row changed, so the reap aborts.
func TestReapOneSessionAbortsOnHeartbeatRefresh(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	wtDir := seedSessionWorktreeWithUncommitted(t, repoDir, "refreshed")
	insertSessionRow(t, raw, sessionFixture{
		ID: "refreshed", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: time.Hour,
	}, now)
	if _, err := raw.Exec(`UPDATE sessions SET heartbeat_at = ? WHERE id = 'refreshed'`, float64(now.Unix())); err != nil {
		t.Fatal(err)
	}

	item, _ := reapOneSession(context.Background(), directReapConfig(repoDir), raw,
		reapableSession{id: "refreshed", worktreePath: wtDir},
		float64(now.Add(-30*time.Second).Unix()))
	if item.Action != "skipped" {
		t.Fatalf("action = %q (%s), want skipped", item.Action, item.Detail)
	}
	if !strings.Contains(item.Detail, "cutoff") {
		t.Fatalf("detail = %q, want heartbeat-cutoff reason", item.Detail)
	}
	assertSessionUntouched(t, raw, repoDir, "refreshed", wtDir)
}

// #982 (b) variant: the row vanished between listing and reap (a concurrent
// actor owns the reap) — abort, touch nothing.
func TestReapOneSessionAbortsWhenRowVanished(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	wtDir := seedSessionWorktreeWithUncommitted(t, repoDir, "vanished")
	insertSessionRow(t, raw, sessionFixture{
		ID: "vanished", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: time.Hour,
	}, now)
	if _, err := raw.Exec(`DELETE FROM sessions WHERE id = 'vanished'`); err != nil {
		t.Fatal(err)
	}

	item, _ := reapOneSession(context.Background(), directReapConfig(repoDir), raw,
		reapableSession{id: "vanished", worktreePath: wtDir},
		float64(now.Add(-30*time.Second).Unix()))
	if item.Action != "skipped" {
		t.Fatalf("action = %q (%s), want skipped", item.Action, item.Detail)
	}
	if !strings.Contains(item.Detail, "vanished") {
		t.Fatalf("detail = %q, want vanished-row reason", item.Detail)
	}
	if _, err := os.Stat(filepath.Join(wtDir, "notes.txt")); err != nil {
		t.Fatalf("worktree touched despite vanished row: %v", err)
	}
}

// #982 fail-safe: a GitRunner without the SessionWorktreeEvidence capability
// can never prove the uncommitted state was captured — the dirty-worktree
// force-removal must be refused and the worktree left in place.
func TestReapOneSessionRefusesDirtyForceRemoveWithoutEvidenceCapability(t *testing.T) {
	repoDir, _, raw := setupSessionWorld(t)
	now := time.Now()
	wtDir := seedSessionWorktreeWithUncommitted(t, repoDir, "nocap")
	insertSessionRow(t, raw, sessionFixture{
		ID: "nocap", Status: "dead", Mode: "worktree", WorktreePath: wtDir, HeartbeatAge: 2 * time.Minute,
	}, now)

	cfg := directReapConfig(repoDir)
	cfg.GitRunner = &dirtyRemoveRunner{}
	item, prune := reapOneSession(context.Background(), cfg, raw,
		reapableSession{id: "nocap", worktreePath: wtDir},
		float64(now.Add(-30*time.Second).Unix()))
	if item.Action != "skipped" {
		t.Fatalf("action = %q (%s), want skipped", item.Action, item.Detail)
	}
	if !strings.Contains(item.Detail, "left in place") {
		t.Fatalf("detail = %q, want refusal reason", item.Detail)
	}
	if prune {
		t.Fatal("refused reap must not schedule a prune")
	}
	assertSessionUntouched(t, raw, repoDir, "nocap", wtDir)
}

// dirtyRemoveRunner simulates a dirty worktree (git worktree remove refuses)
// while carrying no SessionWorktreeEvidence capability.
type dirtyRemoveRunner struct {
	MockCleanupGitRunner
}

func (f *dirtyRemoveRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string) error {
	return fmt.Errorf("git worktree remove: dirty worktree")
}

// A pre-v11 database without a sessions table sweeps as a no-op, not an error.
func TestOrphanSessionNoSessionsTable(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(repoDir, "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE other (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	report, err := RunCleanupSweep(context.Background(), CleanupConfig{
		RepoDir:            repoDir,
		DBPath:             dbPath,
		Targets:            []string{TargetOrphanSession},
		SessionGracePeriod: 30 * time.Second,
		Clock:              func() time.Time { return time.Now() },
	})
	if err != nil {
		t.Fatalf("sweep on legacy db: %v", err)
	}
	if len(report.Items) != 0 {
		t.Fatalf("legacy db produced items: %+v", report.Items)
	}
}
