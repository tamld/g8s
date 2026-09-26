package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/lockfile"
	"github.com/tamld/g8s/internal/orchestrator"
)

const sessionGateBranchPrefix = "sess"

// sessionGate is the flock-gated conditional isolation decision (#393): the
// first supervisor session over a checkout runs in-place holding the kernel
// lock; a contended session is auto-promoted into an isolated pool worktree
// and never touches the holder's refs. The lock is kernel-released on
// process death — crash-safe by construction, no stale locks, no TOCTOU.
type sessionGate struct {
	ID           string
	Mode         string
	WorktreePath string

	store      *controlplane.Store
	lock       *lockfile.Lock
	pool       *orchestrator.Pool
	acquiredWt orchestrator.Worktree
}

// acquireSessionGate arbitrates a supervisor session over repoDir against
// other live sessions and registers it in the control-plane sessions
// registry. Callers must Finish the gate when the session ends.
func acquireSessionGate(ctx context.Context, store *controlplane.Store, repoDir string) (*sessionGate, error) {
	lockPath := filepath.Join(repoDir, ".g8s", "session.lock")
	lock, err := lockfile.TryLock(lockPath)
	if err == nil {
		id := newSessionID()
		if regErr := store.RegisterSession(ctx, controlplane.Session{
			ID:   id,
			Mode: controlplane.SessionModeInPlace,
		}); regErr != nil {
			_ = lock.Release()
			return nil, fmt.Errorf("session gate: register in-place session: %w", regErr)
		}
		return &sessionGate{ID: id, Mode: controlplane.SessionModeInPlace, store: store, lock: lock}, nil
	}
	if !errors.Is(err, lockfile.ErrContended) {
		return nil, fmt.Errorf("session gate: %w", err)
	}
	// Contended: auto-promote into an isolated pool worktree.
	pool, err := orchestrator.NewPool(orchestrator.PoolOptions{Repo: repoDir, Prefix: sessionGateBranchPrefix})
	if err != nil {
		return nil, fmt.Errorf("session gate: worktree pool: %w", err)
	}
	id := newSessionID()
	wt, err := pool.Acquire(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("session gate: promote to worktree: %w", err)
	}
	if regErr := store.RegisterSession(ctx, controlplane.Session{
		ID:           id,
		Mode:         controlplane.SessionModeWorktree,
		WorktreePath: wt.Path,
	}); regErr != nil {
		_ = pool.Release(ctx, wt, false)
		return nil, fmt.Errorf("session gate: register promoted session: %w", regErr)
	}
	return &sessionGate{
		ID:           id,
		Mode:         controlplane.SessionModeWorktree,
		WorktreePath: wt.Path,
		store:        store,
		pool:         pool,
		acquiredWt:   wt,
	}, nil
}

// Heartbeat refreshes the session's liveness in the registry. It fails once
// another actor marked the session dead — the caller must re-arbitrate.
func (g *sessionGate) Heartbeat(ctx context.Context) error {
	return g.store.HeartbeatSession(ctx, g.ID)
}

// Finish marks the session dead and frees its resources. keepWorktree keeps
// a promoted session's worktree for inspection instead of cleaning it up.
func (g *sessionGate) Finish(ctx context.Context, keepWorktree bool) error {
	err := g.store.MarkSessionDead(ctx, g.ID)
	if g.lock != nil {
		if lerr := g.lock.Release(); err == nil {
			err = lerr
		}
	}
	if g.pool != nil {
		if rerr := g.pool.Release(ctx, g.acquiredWt, keepWorktree); err == nil {
			err = rerr
		}
	}
	return err
}

// sessionHeartbeatLoop keeps the registry row alive while the session runs.
// It stops on ErrSessionNotActive: heartbeating a dead row is a silent
// no-op forever — re-arbitration is the cleanup slice's (PR-3) concern.
func sessionHeartbeatLoop(ctx context.Context, g *sessionGate, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := g.Heartbeat(ctx); errors.Is(err, controlplane.ErrSessionNotActive) {
				return
			}
		}
	}
}

func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b[:])
}
