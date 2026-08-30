package sleep

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

// Severities per DEBT-50
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// Standard Event Types
const (
	EventWorkerDead      = "worker_dead"
	EventBranchConflict  = "branch_conflict"
	EventHeartbeatStale  = "heartbeat_stale"
	EventReceiptSuccess  = "receipt_success"
	EventReceiptFailure  = "receipt_failure"
	EventWorktreeOrphan  = "worktree_orphan"
	EventSessionComplete = "session_complete"
	EventTaskDispatched  = "task_dispatched"
)

// Event describes a lifecycle or anomaly occurrence during an execution period.
type Event struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Severity  string            `json:"severity"` // critical | warning | info
	SessionID string            `json:"session_id,omitempty"`
	TaskID    string            `json:"task_id,omitempty"`
	Message   string            `json:"message"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Collector provides event collection and querying.
type Collector interface {
	Collect(ctx context.Context, e Event) error
	ListEventsSince(ctx context.Context, since time.Time) ([]Event, error)
	Clear(ctx context.Context) error
}

// FileCollector persists events to a JSON lines file.
type FileCollector struct {
	mu       sync.RWMutex
	filePath string
}

// NewFileCollector constructs a FileCollector at the given path or default state dir.
func NewFileCollector(path string) *FileCollector {
	if path == "" {
		path = filepath.Join(pathutil.DefaultStateDir(), "sleep_events.jsonl")
	}
	return &FileCollector{filePath: path}
}

// Collect stores an incoming event.
func (c *FileCollector) Collect(_ context.Context, e Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e.ID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		e.ID = hex.EncodeToString(b)
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}

	if err := os.MkdirAll(filepath.Dir(c.filePath), 0o755); err != nil {
		return fmt.Errorf("collector: mkdir: %w", err)
	}

	f, err := os.OpenFile(c.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("collector: open file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("collector: marshal event: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("collector: write event: %w", err)
	}
	return nil
}

// ListEventsSince retrieves all recorded events occurring at or after `since`.
func (c *FileCollector) ListEventsSince(_ context.Context, since time.Time) ([]Event, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	f, err := os.Open(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("collector: read file: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if since.IsZero() || ev.Timestamp.After(since) || ev.Timestamp.Equal(since) {
			events = append(events, ev)
		}
	}
	return events, scanner.Err()
}

// Clear truncates the event store.
func (c *FileCollector) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return os.Remove(c.filePath)
}
