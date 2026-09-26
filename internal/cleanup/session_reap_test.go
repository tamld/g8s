package cleanup

import (
	"context"
	"database/sql"
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
