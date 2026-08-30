package orchestrator

import (
	"context"
	"fmt"
	"sync"
)

// FanOut spawns N workers concurrently, each in its own worktree, and
// returns all receipts. Concurrency caps in-flight workers at MaxParallel.
//
// Cancellation: ctx.Done cancels every spawned worker (process group kill)
// and short-circuits the wait.
//
// Error policy: a worker error does not abort the fan-out. Every worker
// gets a chance to run; the caller decides what to do with the receipts.
// FanOut only returns an error if no workers could be spawned at all
// (e.g. pool/registry both unavailable).
func FanOut(ctx context.Context, plan []TaskSpec, opts FanOutOptions) ([]Receipt, error) {
	if len(plan) == 0 {
		return nil, nil
	}
	if opts.Registry == nil || opts.Pool == nil {
		return nil, fmt.Errorf("orchestrator: Registry and Pool required")
	}
	worker, err := opts.Registry.Pick(ctx)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: pick worker: %w", err)
	}

	maxPar := opts.MaxParallel
	if maxPar <= 0 {
		maxPar = len(plan)
	}

	results := make([]Receipt, len(plan))
	errs := make([]error, len(plan))
	sem := make(chan struct{}, maxPar)
	var wg sync.WaitGroup

	for i, spec := range plan {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, spec TaskSpec) {
			defer wg.Done()
			defer func() { <-sem }()

			wt, err := opts.Pool.Acquire(ctx, spec.TaskID)
			if err != nil {
				errs[i] = fmt.Errorf("acquire worktree: %w", err)
				return
			}
			defer func() {
				_ = opts.Pool.Release(ctx, wt, results[i].OK)
			}()
			task := spec.Task
			task.Worktree = wt
			handle, err := worker.Spawn(ctx, task)
			if err != nil {
				errs[i] = fmt.Errorf("spawn worker: %w", err)
				return
			}
			receipt, werr := handle.Wait(ctx)
			verifier := &StdoutEnvelopeVerifier{}
			_ = verifier.VerifyReceipt(ctx, &receipt)
			receipt.FilesModified, receipt.CommitSHA = gitDiffNameOnly(opts.Pool.repo, wt.BaseSHA)
			receipt.ScopeViolations = diffScope(receipt.FilesModified, task.AllowedFiles)

			if spec.OrchestratorID != "" {
				receipt.OrchestratorID = spec.OrchestratorID
			}
			if spec.WorktreeID != "" {
				receipt.WorktreeID = spec.WorktreeID
			} else if receipt.WorktreeID == "" && wt.ID != "" {
				receipt.WorktreeID = wt.ID
			}
			if spec.WorkerName != "" {
				receipt.WorkerName = spec.WorkerName
			} else if receipt.WorkerName == "" && worker != nil {
				receipt.WorkerName = worker.Name()
			}
			if spec.Iter != 0 {
				receipt.Iter = spec.Iter
			} else if receipt.Iter == 0 && spec.Task.Iter != 0 {
				receipt.Iter = spec.Task.Iter
			}
			if receipt.TaskID == "" {
				receipt.TaskID = spec.TaskID
			}

			results[i] = receipt
			errs[i] = werr
		}(i, spec)
	}
	wg.Wait()

	if !anySpawned(errs) {
		return nil, fmt.Errorf("orchestrator: all %d fan-out workers failed to spawn: %v", len(plan), errs)
	}
	return results, nil
}

// anySpawned reports true if at least one worker produced a receipt (vs.
// failing in Acquire/Spawn before Wait).
func anySpawned(errs []error) bool {
	for _, e := range errs {
		if e == nil {
			return true
		}
	}
	return false
}

// FanOutOptions parameterizes FanOut.
type FanOutOptions struct {
	Registry    *Registry
	Pool        *Pool
	MaxParallel int
}
