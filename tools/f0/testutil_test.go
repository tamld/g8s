package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// robustTempDir creates a temporary directory whose cleanup tolerates
// transient external writers. The TestExecuteT2Merge family flakes on
// 2-core CI runners with `TempDir RemoveAll cleanup: unlinkat ...:
// directory not empty` (testing.go:1464) — something (filesystem
// contention on shared runners, or a detached git subprocess) touches the
// directory while RemoveAll is iterating it. Local hammering (-count=60)
// does not reproduce, so instead of guessing the writer, cleanup becomes
// self-diagnosing: on failure it logs the leftover entries immediately,
// retries with backoff, and only fails the test — with the full leftover
// listing attached — if the directory genuinely cannot be cleaned.
//
// #986 follow-up: the leading external-writer hypothesis is `git commit`'s
// post-commit `gc --auto`, which detaches into a background process
// (gc.autoDetach defaults true) and keeps writing pack files into .git/
// after the git invocation returns. setupTestGitRepo now disables gc.auto /
// gc.autoDetach / core.fsmonitor for every test repo; if the flake still
// recurs, the leftover listing below names the racing directory (e.g.
// `.git/objects/pack/` → auto-gc, `.f0/` → ledger writer) and identifies
// the writer definitively.
func robustTempDir(t *testing.T) string {
	t.Helper()
	prefix := "f0-" + strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(t.Name()) + "-"
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() {
		// ~1s of total retry budget: absorbs a one-shot writer that takes
		// up to a second to finish (background git gc, slow CI fsync).
		const attempts = 10
		const backoff = 100 * time.Millisecond
		var lastErr error
		for i := 0; i < attempts; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			} else if lastErr == nil {
				// capture the scene on the first failure, before a retry
				// (or the departing writer) erases the evidence
				t.Logf("temp dir cleanup hit a transient error; leftovers at first sight: %s", listDir(t, dir))
			}
			lastErr = err
			time.Sleep(backoff)
		}
		t.Errorf("temp dir %s not cleanable after %d attempts (leftovers: %s): %v", dir, attempts, listDir(t, dir), lastErr)
	})
	return dir
}

func listDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "<unreadable: " + err.Error() + ">"
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return "<empty>"
	}
	return strings.Join(names, ", ")
}
