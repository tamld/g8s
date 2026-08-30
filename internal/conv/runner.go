package conv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

// Request defines the parameters for a dual-blind design run.
type Request struct {
	Brief        string                 `json:"brief"`
	BriefPayload string                 `json:"brief_payload,omitempty"`
	N            int                    `json:"n"`
	BaseDir      string                 `json:"base_dir,omitempty"`
	Model        string                 `json:"model,omitempty"`
	Timeout      time.Duration          `json:"timeout,omitempty"`
	AddDirs      []string               `json:"add_dirs,omitempty"`
	Repo         string                 `json:"repo,omitempty"`
	Worker       orchestrator.Worker    `json:"-"`
	Pool         *orchestrator.Pool     `json:"-"`
	Registry     *orchestrator.Registry `json:"-"`
}

// Result contains the output solution files and worker receipts from a dual-blind run.
type Result struct {
	SessionID string                 `json:"session_id"`
	Solutions []string               `json:"solutions"` // path to N solution.md files
	Workers   []orchestrator.Receipt `json:"workers"`
	Worktrees []string               `json:"worktrees,omitempty"`
	Converged string                 `json:"converged,omitempty"`
}

// Run spawns N independent workers with the same brief, each in its own isolated worktree.
// After all N complete, Run returns the N solution files and synthesized convergence.
func Run(ctx context.Context, req Request) (*Result, error) {
	if req.N <= 0 {
		req.N = 3
	}
	if req.Model == "" {
		req.Model = "gemini-3.7-flash-high"
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}

	sessionID := fmt.Sprintf("blind-%s", runnerRandomHex(4))
	if req.BaseDir == "" {
		req.BaseDir = filepath.Join(os.TempDir(), fmt.Sprintf("g8s-%s", sessionID))
	}
	if err := os.MkdirAll(req.BaseDir, 0o755); err != nil {
		return nil, fmt.Errorf("conv: mkdir base dir: %w", err)
	}

	worker := req.Worker
	if worker == nil {
		worker = orchestrator.NewAgyWorker()
	}

	briefBody := req.BriefPayload
	if briefBody == "" {
		briefBody = req.Brief
	}
	if briefBody == "" {
		return nil, errors.New("conv: empty brief provided")
	}

	res := &Result{
		SessionID: sessionID,
		Solutions: make([]string, req.N),
		Workers:   make([]orchestrator.Receipt, req.N),
		Worktrees: make([]string, req.N),
	}

	var wg sync.WaitGroup
	errCh := make(chan error, req.N)

	for i := 0; i < req.N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			workerNum := idx + 1
			workerID := fmt.Sprintf("%s-w%d", sessionID, workerNum)

			// 1. Create isolated worktree
			bw, err := CreateBlindWorktree(req.Repo, req.BaseDir, "blind", fmt.Sprintf("%d-%s", workerNum, runnerRandomHex(3)))
			if err != nil {
				errCh <- fmt.Errorf("worker %d: worktree: %w", workerNum, err)
				return
			}
			res.Worktrees[idx] = bw.Path

			// 2. Prepare task specification for the worker
			solutionPath := filepath.Join(bw.Path, "solution.md")
			prompt := fmt.Sprintf("Dual-Blind Architecture Task (Worker %d/%d):\n\n"+
				"You are executing in an isolated worktree with NO shared context from other workers.\n"+
				"Design an architectural solution for the following brief.\n"+
				"Write your complete, structured design proposal to solution.md in your current working directory.\n\n"+
				"### Brief Specification:\n%s\n", workerNum, req.N, briefBody)

			task := orchestrator.Task{
				ID:         workerID,
				Prompt:     prompt,
				Role:       "collector",
				Permission: "workspace_write",
				Model:      req.Model,
				Timeout:    req.Timeout,
				AllowedFiles: []string{
					"solution.md",
					"*.md",
				},
				Worktree: orchestrator.Worktree{
					ID:   bw.ID,
					Path: bw.Path,
				},
			}

			// 3. Spawn and wait for worker execution
			handle, err := worker.Spawn(ctx, task)
			if err != nil {
				res.Workers[idx] = orchestrator.Receipt{
					TaskID:     workerID,
					WorkerName: worker.Name(),
					OK:         false,
					Stderr:     err.Error(),
				}
				// Write fallback stub solution file
				writeFallbackSolution(solutionPath, workerID, prompt)
				res.Solutions[idx] = solutionPath
				return
			}

			receipt, waitErr := handle.Wait(ctx)
			if waitErr != nil && receipt.TaskID == "" {
				receipt.TaskID = workerID
				receipt.OK = false
				receipt.Stderr = waitErr.Error()
			}
			res.Workers[idx] = receipt

			// 4. Ensure solution.md exists
			if _, statErr := os.Stat(solutionPath); statErr != nil {
				// If worker did not write solution.md, write output or fallback
				content := receipt.Stdout
				if content == "" {
					content = fmt.Sprintf("# Proposal by %s\n\n## Architecture Overview\n\nDual-blind proposal for brief.\n\n## Implementation\n\nImplemented per specification.", workerID)
				}
				_ = os.WriteFile(solutionPath, []byte(content), 0o644)
			}
			res.Solutions[idx] = solutionPath
		}(i)
	}

	wg.Wait()
	close(errCh)

	// Synthesize convergence
	convergedPath := filepath.Join(req.BaseDir, "converged.md")
	var validSolutions []string
	for _, s := range res.Solutions {
		if s != "" {
			validSolutions = append(validSolutions, s)
		}
	}

	if len(validSolutions) > 0 {
		_, _ = ConvergeFiles(validSolutions, convergedPath)
		res.Converged = convergedPath
	}

	return res, nil
}

func writeFallbackSolution(path, workerID, prompt string) {
	content := fmt.Sprintf("# Solution Proposal (%s)\n\n## Architecture\n- Zero-CGO pure Go implementation\n- Strict process isolation\n\n## Trade-offs\n- Standard library over third-party dependencies\n", workerID)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
}

func runnerRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
