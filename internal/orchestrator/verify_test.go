package orchestrator

import (
	"context"
	"errors"
	"testing"
)

func TestStdoutEnvelopeVerifier(t *testing.T) {
	tests := []struct {
		name        string
		receipt     Receipt
		wantOK      bool
		wantReturn  int
		wantErrorIn string
	}{
		{
			name: "success plain text",
			receipt: Receipt{
				OK:        true,
				Stdout:    "task completed successfully",
				RawStdout: []byte("task completed successfully"),
			},
			wantOK:     true,
			wantReturn: 0,
		},
		{
			name: "success json envelope",
			receipt: Receipt{
				OK:        true,
				Stdout:    `{"v":1,"kind":"task","cmd":"submit","data":{"state":"SUCCEEDED"}}`,
				RawStdout: []byte(`{"v":1,"kind":"task","cmd":"submit","data":{"state":"SUCCEEDED"}}`),
			},
			wantOK:     true,
			wantReturn: 0,
		},
		{
			name: "error json envelope with code and message",
			receipt: Receipt{
				OK:        true,
				Stdout:    `{"v":1,"kind":"error","cmd":"g8s","error":{"code":"E_USAGE","message":"unknown flag --invalid"}}`,
				RawStdout: []byte(`{"v":1,"kind":"error","cmd":"g8s","error":{"code":"E_USAGE","message":"unknown flag --invalid"}}`),
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "unknown flag",
		},
		{
			name: "error json envelope in RawStdout only",
			receipt: Receipt{
				OK:        true,
				RawStdout: []byte(`{"kind":"error","error":{"code":"E_RUNTIME","message":"subprocess crashed"}}`),
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "subprocess crashed",
		},
		{
			name: "fenced json error envelope",
			receipt: Receipt{
				OK:     true,
				Stdout: "Some output\n```json\n{\"kind\":\"error\",\"error\":{\"code\":\"E_HARNESS\",\"message\":\"permission violation\"}}\n```\nDone",
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "permission violation",
		},
	}

	verifier := &StdoutEnvelopeVerifier{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.receipt
			err := verifier.VerifyReceipt(ctx, &r)
			if err != nil {
				t.Fatalf("VerifyReceipt() returned unexpected error: %v", err)
			}
			if r.OK != tt.wantOK {
				t.Errorf("r.OK = %v, want %v", r.OK, tt.wantOK)
			}
			if tt.wantReturn != 0 && r.ReturnCode != tt.wantReturn {
				t.Errorf("r.ReturnCode = %d, want %d", r.ReturnCode, tt.wantReturn)
			}
			if tt.wantErrorIn != "" && (r.LastError == "" || !contains(r.LastError, tt.wantErrorIn)) {
				t.Errorf("r.LastError = %q, want containing %q", r.LastError, tt.wantErrorIn)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type failingVerifier struct{}

func (failingVerifier) VerifyReceipt(ctx context.Context, receipt *Receipt) error {
	return errors.New("verification failed intentionally")
}

func TestApplyReceiptVerifierFailsClosed(t *testing.T) {
	verifier := failingVerifier{}
	receipt := &Receipt{
		OK:         true,
		ReturnCode: 0,
		LastError:  "",
		RawStdout:  []byte("some output"),
	}
	ctx := context.Background()

	err := applyReceiptVerifier(ctx, verifier, receipt)

	if err == nil {
		t.Fatal("expected error from applyReceiptVerifier")
	}
	if receipt.OK {
		t.Fatal("expected receipt.OK = false after verification failure")
	}
	if receipt.ReturnCode != 1 {
		t.Fatalf("expected receipt.ReturnCode = 1, got %d", receipt.ReturnCode)
	}
	if receipt.LastError == "" {
		t.Fatal("expected receipt.LastError to be populated")
	}
}

type fakeWorkerForFanOut struct {
	name      string
	available bool
	spawnErr  error
}

func (w fakeWorkerForFanOut) Name() string { return w.name }
func (w fakeWorkerForFanOut) Available(ctx context.Context) error {
	if !w.available {
		return errors.New("unavailable")
	}
	return nil
}

func (w fakeWorkerForFanOut) Spawn(ctx context.Context, t Task) (Handle, error) {
	if w.spawnErr != nil {
		return nil, w.spawnErr
	}
	return &fakeHandleForFanOut{receipt: Receipt{
		OK:         true,
		TaskID:     t.ID,
		WorkerName: w.name,
		CommitSHA:  "deadbeef",
	}}, nil
}

type fakeHandleForFanOut struct {
	receipt Receipt
}

func (h *fakeHandleForFanOut) PID() int { return 123 }
func (h *fakeHandleForFanOut) Wait(ctx context.Context) (Receipt, error) {
	return h.receipt, nil
}
func (h *fakeHandleForFanOut) Cancel(ctx context.Context) error { return nil }
func (h *fakeHandleForFanOut) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func TestFanOutRoutesWorkerName(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q", "-b", "master")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	r := NewRegistry()
	_ = r.Register("worker-a", func() Worker { return fakeWorkerForFanOut{name: "worker-a", available: true} })
	_ = r.Register("worker-b", func() Worker { return fakeWorkerForFanOut{name: "worker-b", available: true} })

	pool, err := NewPool(PoolOptions{Repo: dir, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	plan := []TaskSpec{
		{TaskID: "task-1", WorkerName: "worker-a", Task: Task{ID: "task-1"}},
		{TaskID: "task-2", WorkerName: "worker-b", Task: Task{ID: "task-2"}},
		{TaskID: "task-3", Task: Task{ID: "task-3"}}, // no WorkerName, should use Pick (first available = worker-a)
	}

	ctx := context.Background()
	results, err := FanOut(ctx, plan, FanOutOptions{
		Registry: r,
		Pool:     pool,
	})
	if err != nil {
		t.Fatalf("FanOut returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].WorkerName != "worker-a" {
		t.Errorf("task-1 expected worker-a, got %q", results[0].WorkerName)
	}
	if results[1].WorkerName != "worker-b" {
		t.Errorf("task-2 expected worker-b, got %q", results[1].WorkerName)
	}
	if results[2].WorkerName != "worker-a" {
		t.Errorf("task-3 (no WorkerName) expected worker-a (first available), got %q", results[2].WorkerName)
	}
	if results[0].WorktreePath == "" {
		t.Errorf("task-1 expected WorktreePath populated on receipt")
	}
}
