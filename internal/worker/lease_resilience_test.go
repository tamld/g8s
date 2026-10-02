package worker

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
)

type stubWorkerControlPlane struct {
	WorkerControlPlane
	getTaskFunc        func(ctx context.Context, taskID string) (*controlplane.Task, error)
	renewHeartbeatFunc func(ctx context.Context, taskID, workerID string, extensionSeconds int) error
}

func (s *stubWorkerControlPlane) GetTask(ctx context.Context, taskID string) (*controlplane.Task, error) {
	if s.getTaskFunc != nil {
		return s.getTaskFunc(ctx, taskID)
	}
	if s.WorkerControlPlane != nil {
		return s.WorkerControlPlane.GetTask(ctx, taskID)
	}
	return nil, nil
}

func (s *stubWorkerControlPlane) RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error {
	if s.renewHeartbeatFunc != nil {
		return s.renewHeartbeatFunc(ctx, taskID, workerID, extensionSeconds)
	}
	if s.WorkerControlPlane != nil {
		return s.WorkerControlPlane.RenewHeartbeat(ctx, taskID, workerID, extensionSeconds)
	}
	return nil
}

// TestAwaitOutcome_GetTaskTransientFailure_AttemptSurvives tests that when GetTask
// fails transiently (1-2 times) then recovers, the attempt survives without being killed (#465).
func TestAwaitOutcome_GetTaskTransientFailure_AttemptSurvives(t *testing.T) {
	var calls int32
	token := "tok-123"
	owner := "worker-1"
	now := time.Now()
	expiry := float64(now.Add(30*time.Second).UnixNano()) / 1e9

	child := newFakeChild(0)

	cp := &stubWorkerControlPlane{
		getTaskFunc: func(ctx context.Context, taskID string) (*controlplane.Task, error) {
			c := atomic.AddInt32(&calls, 1)
			if c <= 2 {
				return nil, errors.New("sqlite_busy: database is locked")
			}
			// On 3rd call, close child.done so awaitOutcome terminates cleanly
			defer func() {
				select {
				case <-child.Done():
				default:
					close(child.done)
				}
			}()
			return &controlplane.Task{
				TaskID:         taskID,
				State:          controlplane.StateRunning,
				LeaseOwner:     &owner,
				LeaseToken:     &token,
				LeaseExpiresAt: &expiry,
			}, nil
		},
	}

	sup := &Supervisor{
		cp:           cp,
		clock:        time.Now,
		pollInterval: 10 * time.Millisecond,
	}

	reason := sup.awaitOutcome(context.Background(), child, "t1", "worker-1", token, taskRequest{Timeout: "10s"}, 10)
	if reason != "" {
		t.Fatalf("expected attempt to survive (empty reason), got: %q", reason)
	}
	if gotCalls := atomic.LoadInt32(&calls); gotCalls < 3 {
		t.Fatalf("expected at least 3 calls to GetTask, got %d", gotCalls)
	}
}

// TestAwaitOutcome_GenuineExpiry_KillFiresWithAttribution verifies that when GetTask
// reports the lease genuinely expired, a kill fires with an attribution reason containing "expired" (#465).
func TestAwaitOutcome_GenuineExpiry_KillFiresWithAttribution(t *testing.T) {
	token := "tok-exp"
	owner := "worker-exp"
	pastExpiry := float64(time.Now().Add(-10*time.Second).UnixNano()) / 1e9

	child := newFakeChild(0)

	cp := &stubWorkerControlPlane{
		getTaskFunc: func(ctx context.Context, taskID string) (*controlplane.Task, error) {
			return &controlplane.Task{
				TaskID:         taskID,
				State:          controlplane.StateRunning,
				LeaseOwner:     &owner,
				LeaseToken:     &token,
				LeaseExpiresAt: &pastExpiry,
			}, nil
		},
	}

	sup := &Supervisor{
		cp:           cp,
		clock:        time.Now,
		pollInterval: 10 * time.Millisecond,
	}

	reason := sup.awaitOutcome(context.Background(), child, "t-exp", "worker-exp", token, taskRequest{Timeout: "10s"}, 10)
	if reason == "" {
		t.Fatal("expected kill reason for expired lease, got empty string")
	}
	if !strings.Contains(reason, "expired") {
		t.Fatalf("expected kill reason to contain 'expired', got %q", reason)
	}
	if !strings.Contains(reason, "lease expired at") {
		t.Fatalf("expected kill reason to contain 'lease expired at', got %q", reason)
	}
}

// TestAwaitOutcome_RenewHeartbeatTransientFailure_AttemptSurvives verifies that a transient
// failure during RenewHeartbeat triggers an authoritative re-check and survives if the lease is valid (#465).
func TestAwaitOutcome_RenewHeartbeatTransientFailure_AttemptSurvives(t *testing.T) {
	var renewCalls int32
	token := "tok-renew"
	owner := "worker-renew"
	now := time.Now()
	expiry := float64(now.Add(30*time.Second).UnixNano()) / 1e9

	child := newFakeChild(0)

	cp := &stubWorkerControlPlane{
		getTaskFunc: func(ctx context.Context, taskID string) (*controlplane.Task, error) {
			return &controlplane.Task{
				TaskID:         taskID,
				State:          controlplane.StateRunning,
				LeaseOwner:     &owner,
				LeaseToken:     &token,
				LeaseExpiresAt: &expiry,
			}, nil
		},
		renewHeartbeatFunc: func(ctx context.Context, taskID, workerID string, extensionSeconds int) error {
			c := atomic.AddInt32(&renewCalls, 1)
			if c == 1 {
				// Close child after first renew failure so awaitOutcome finishes after grace re-check
				go func() {
					time.Sleep(150 * time.Millisecond)
					select {
					case <-child.Done():
					default:
						close(child.done)
					}
				}()
				return errors.New("sqlite_busy: database is locked")
			}
			return nil
		},
	}

	// Use leaseSeconds = 1 so heartbeat interval is minHeartbeatInterval (100ms)
	sup := &Supervisor{
		cp:           cp,
		clock:        time.Now,
		pollInterval: 10 * time.Millisecond,
	}

	reason := sup.awaitOutcome(context.Background(), child, "t-renew", "worker-renew", token, taskRequest{Timeout: "10s"}, 1)
	if reason != "" {
		t.Fatalf("expected attempt to survive transient renewal failure, got %q", reason)
	}
	if got := atomic.LoadInt32(&renewCalls); got < 1 {
		t.Fatalf("expected at least 1 renew call, got %d", got)
	}
}

// TestAwaitOutcome_PersistentFailure_BoundedGiveUp verifies that 3 consecutive
// transient failures bound the loop and give up with attribution (#465).
func TestAwaitOutcome_PersistentFailure_BoundedGiveUp(t *testing.T) {
	child := newFakeChild(0)

	cp := &stubWorkerControlPlane{
		getTaskFunc: func(ctx context.Context, taskID string) (*controlplane.Task, error) {
			return nil, errors.New("persistent network partition")
		},
	}

	sup := &Supervisor{
		cp:           cp,
		clock:        time.Now,
		pollInterval: 5 * time.Millisecond,
	}

	reason := sup.awaitOutcome(context.Background(), child, "t-fail", "worker-fail", "tok", taskRequest{Timeout: "10s"}, 1)
	if reason == "" {
		t.Fatal("expected termination on persistent failure, got empty reason")
	}
	if !strings.Contains(reason, "3 consecutive transient errors") {
		t.Fatalf("expected reason to mention '3 consecutive transient errors', got %q", reason)
	}
}

// TestWorker_GetInstanceID verifies that GetInstanceID exposes the pathutil instance ID
// and caches it across calls (#465).
func TestWorker_GetInstanceID(t *testing.T) {
	id1 := GetInstanceID()
	if len(id1) != 32 {
		t.Fatalf("GetInstanceID() length = %d, want 32 (got %q)", len(id1), id1)
	}
	id2 := GetInstanceID()
	if id1 != id2 {
		t.Fatalf("GetInstanceID() unstable across calls: %q vs %q", id1, id2)
	}
	// Verify it matches pathutil.InstanceID() for the active state dir
	pathID := pathutil.InstanceID()
	if id1 != pathID {
		t.Fatalf("GetInstanceID() %q does not match pathutil.InstanceID() %q", id1, pathID)
	}
}
