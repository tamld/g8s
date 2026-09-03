package conv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

type mockWorker struct {
	name          string
	solutionTexts map[string]string
}

func (m *mockWorker) Name() string                        { return m.name }
func (m *mockWorker) Available(ctx context.Context) error { return nil }
func (m *mockWorker) Spawn(ctx context.Context, task orchestrator.Task) (orchestrator.Handle, error) {
	wtPath := task.Worktree.Path
	if wtPath != "" {
		solContent := m.solutionTexts[task.ID]
		if solContent == "" {
			solContent = fmt.Sprintf("# Proposal for %s\n\n## Architecture\n- Pure-Go implementation\n- Receipt delegation\n\n## Trade-offs\n- Simplicity over complexity\n", task.ID)
		}
		_ = os.WriteFile(filepath.Join(wtPath, "solution.md"), []byte(solContent), 0o644)
	}
	return &mockHandle{
		receipt: orchestrator.Receipt{
			TaskID:          task.ID,
			WorkerName:      m.name,
			OK:              true,
			FilesModified:   []string{"solution.md"},
			DurationSeconds: 0.1,
		},
	}, nil
}

type mockHandle struct {
	receipt orchestrator.Receipt
}

func (h *mockHandle) PID() int { return 1234 }
func (h *mockHandle) Wait(ctx context.Context) (orchestrator.Receipt, error) {
	return h.receipt, nil
}
func (h *mockHandle) Cancel(ctx context.Context) error { return nil }
func (h *mockHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func TestRunSpawnsNWorktrees(t *testing.T) {
	tempDir := t.TempDir()
	worker := &mockWorker{
		name:          "mock-agy",
		solutionTexts: map[string]string{},
	}

	req := Request{
		Brief:   "Implement dual-blind design workflow",
		N:       3,
		BaseDir: tempDir,
		Model:   "gemini-3.8-flash-high",
		Timeout: 30 * time.Second,
		Worker:  worker,
	}

	res, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(res.Solutions) != 3 {
		t.Errorf("expected 3 solutions, got %d", len(res.Solutions))
	}
	if len(res.Worktrees) != 3 {
		t.Errorf("expected 3 worktrees, got %d", len(res.Worktrees))
	}
	if len(res.Workers) != 3 {
		t.Errorf("expected 3 worker receipts, got %d", len(res.Workers))
	}

	for i, solPath := range res.Solutions {
		if solPath == "" {
			t.Errorf("solution %d path is empty", i)
		}
		if _, err := os.Stat(solPath); err != nil {
			t.Errorf("solution file %s does not exist: %v", solPath, err)
		}
	}

	if res.Converged == "" {
		t.Errorf("expected converged.md path, got empty")
	}
	if _, err := os.Stat(res.Converged); err != nil {
		t.Errorf("converged file does not exist: %v", err)
	}
}

func TestConverge(t *testing.T) {
	solA := `# Dual-Blind Architecture (Worker A)

## Core Architecture
- Zero-CGO pure Go state machine
- Process group isolation with Setpgid
- Cryptographic write receipt validation

## Storage Backend
- Modernc SQLite WAL driver for task queuing

## Trade-offs
- Pure Go modernc.org/sqlite has higher CPU overhead than CGO, but eliminates build dependencies.
`

	solB := `# Blind Architecture Proposal (Worker B)

## Core Architecture
- Zero-CGO pure Go state machine
- Capability delegation via write receipts
- Isolated OS process groups

## Storage Backend
- Modernc SQLite WAL driver with CAS leases

## Failure Recovery
- Adaptive silence detection with 5-minute escalation window
`

	solC := `# Multi-Worker Blind Design (Worker C)

## Core Architecture
- Pure-Go zero-trust process execution harness
- Write receipt single-use lifecycle
- Process group kill signal containment

## Storage Backend
- SQLite WAL mode via pure Go

## Metrics & Observability
- Live per-session heartbeat monitoring
`

	parsedA := ParseSolutionContent(solA, "worker-1")
	parsedB := ParseSolutionContent(solB, "worker-2")
	parsedC := ParseSolutionContent(solC, "worker-3")

	report := Converge([]*Solution{parsedA, parsedB, parsedC})

	if report.ParticipantCount != 3 {
		t.Errorf("expected 3 participants, got %d", report.ParticipantCount)
	}

	// Verify common ground identified
	if len(report.CommonGround) == 0 {
		t.Fatalf("expected common ground items, got 0")
	}

	var foundCoreArch, foundStorage bool
	for _, cg := range report.CommonGround {
		norm := NormalizeHeading(cg.Heading)
		if strings.Contains(norm, "core architecture") {
			foundCoreArch = true
			if len(cg.AgreedBy) != 3 {
				t.Errorf("expected all 3 workers in core architecture, got %v", cg.AgreedBy)
			}
		}
		if strings.Contains(norm, "storage backend") {
			foundStorage = true
		}
	}

	if !foundCoreArch {
		t.Errorf("expected Core Architecture in common ground")
	}
	if !foundStorage {
		t.Errorf("expected Storage Backend in common ground")
	}

	// Verify markdown output contains key sections
	md := report.MarkdownContent
	if !strings.Contains(md, "Common Ground") {
		t.Errorf("missing Common Ground in markdown: %s", md)
	}
	if !strings.Contains(md, "Executive Summary") {
		t.Errorf("missing Executive Summary in markdown: %s", md)
	}
}

func TestConvergeTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		solutionTexts []string
		expectCommon  int
		expectDiv     bool
	}{
		{
			name: "2 solutions with full agreement",
			solutionTexts: []string{
				"# Design 1\n\n## Invariants\n- Pure-Go zero-CGO\n\n## Security\n- POSIX 0600 permissions\n",
				"# Design 2\n\n## Invariants\n- Pure-Go zero-CGO\n\n## Security\n- POSIX 0600 permissions\n",
			},
			expectCommon: 2,
			expectDiv:    false,
		},
		{
			name: "3 solutions with 1 divergence",
			solutionTexts: []string{
				"# Design A\n\n## Engine\n- Pure-Go FSM\n\n## Storage\n- SQLite WAL pure-Go\n",
				"# Design B\n\n## Engine\n- Pure-Go FSM\n\n## Storage\n- SQLite WAL pure-Go\n",
				"# Design C\n\n## Engine\n- Pure-Go FSM\n\n## Network\n- Unix domain sockets\n",
			},
			expectCommon: 2,    // Engine (3/3), Storage (2/3 majority)
			expectDiv:    true, // Network (1/3)
		},
		{
			name: "4 solutions with mixed headings",
			solutionTexts: []string{
				"# Sol 1\n\n## Auth\n- HMAC tokens\n\n## Logging\n- Stdio JSON\n",
				"# Sol 2\n\n## Auth\n- HMAC tokens\n\n## Logging\n- Stdio JSON\n",
				"# Sol 3\n\n## Auth\n- HMAC tokens\n\n## Cache\n- In-memory LRU\n",
				"# Sol 4\n\n## Auth\n- HMAC tokens\n\n## Cache\n- In-memory LRU\n",
			},
			expectCommon: 3, // Auth (4/4), Logging (2/4), Cache (2/4)
			expectDiv:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sols := make([]*Solution, len(tc.solutionTexts))
			for i, txt := range tc.solutionTexts {
				sols[i] = ParseSolutionContent(txt, fmt.Sprintf("worker-%d", i+1))
			}

			report := Converge(sols)
			if len(report.CommonGround) < tc.expectCommon {
				t.Errorf("[%s] expected at least %d common ground items, got %d", tc.name, tc.expectCommon, len(report.CommonGround))
			}
			if tc.expectDiv && len(report.Divergences) == 0 {
				t.Errorf("[%s] expected divergence items, got 0", tc.name)
			}
		})
	}
}

func TestSpotChecker(t *testing.T) {
	// Contradictory solution
	badSol1 := `# Contradictory Solution
## Invariants
Zero-CGO binary but requires gcc and CGO_ENABLED=1 dynamic link.
## Storage
Global singleton database pointer.
`
	// Silent assumptions solution
	badSol2 := `# Privilege Escalation Solution
## Setup
Runs sudo chmod 777 /etc/g8s and accesses root directory.
`
	parsed1 := ParseSolutionContent(badSol1, "w1")
	parsed2 := ParseSolutionContent(badSol2, "w2")

	issues := RunSpotChecker([]*Solution{parsed1, parsed2})
	if len(issues) < 2 {
		t.Fatalf("expected at least 2 spot checker issues, got %d", len(issues))
	}

	var foundContradiction, foundAssumption bool
	for _, issue := range issues {
		if issue.Category == "self_contradiction" {
			foundContradiction = true
		}
		if issue.Category == "silent_assumption" {
			foundAssumption = true
		}
	}

	if !foundContradiction {
		t.Errorf("expected self_contradiction to be flagged")
	}
	if !foundAssumption {
		t.Errorf("expected silent_assumption to be flagged")
	}
}

func TestConvergeFiles(t *testing.T) {
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "sol1.md")
	f2 := filepath.Join(tempDir, "sol2.md")
	out := filepath.Join(tempDir, "converged.md")

	_ = os.WriteFile(f1, []byte("# Design 1\n\n## Component\n- Pure-Go parser\n"), 0o644)
	_ = os.WriteFile(f2, []byte("# Design 2\n\n## Component\n- Pure-Go parser\n"), 0o644)

	report, err := ConvergeFiles([]string{f1, f2}, out)
	if err != nil {
		t.Fatalf("ConvergeFiles failed: %v", err)
	}

	if report == nil {
		t.Fatal("report is nil")
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read converged.md: %v", err)
	}

	if !strings.Contains(string(data), "Pure-Go parser") {
		t.Errorf("expected converged.md to contain Pure-Go parser: %s", string(data))
	}
}
