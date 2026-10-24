package controlplane

import (
	"context"
	"encoding/json"

	"github.com/tamld/g8s/internal/state"
)

// TaskQueueStore defines the task submission, retrieval, and result lifecycle
// operations used by queue producers and orchestrators.
type TaskQueueStore interface {
	SubmitTask(ctx context.Context, req SubmitTaskRequest) (*Task, error)
	GetTask(ctx context.Context, taskID string) (*Task, error)
	GetTaskInSession(ctx context.Context, taskID, sessionID string) (*Task, error)
	ListTasks(ctx context.Context, filter TaskFilter) ([]*Task, error)
	CompleteTask(ctx context.Context, taskID string, result TaskResult) error
	FailTask(ctx context.Context, taskID, reason string, exitCode int) error
	CancelTask(ctx context.Context, taskID, reason string) error
	ResubmitTask(ctx context.Context, origTaskID string, opts ResubmitOpts) (string, error)
	RecordReceiptExport(ctx context.Context, taskID string, status ReceiptExportStatus) error
}

// ClaimFilter parameterizes task claim selection with affinity constraints (issue #657).
type ClaimFilter struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// TaskLeaseStore defines atomic lease acquisition, heartbeat renewal, and
// attempt completion operations used by F1 worker loops.
type TaskLeaseStore interface {
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error)
	ClaimTaskProvider(ctx context.Context, workerID string, leaseDurationSeconds int, provider string) (*Task, error)
	ClaimTaskFiltered(ctx context.Context, workerID string, leaseDurationSeconds int, filter ClaimFilter) (*Task, error)
	ClaimTaskInSession(ctx context.Context, workerID, sessionID string, leaseDurationSeconds int) (*Task, error)
	StartTask(taskID, workerID, leaseToken string) bool
	RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error
	Heartbeat(ctx context.Context, taskID, workerID, leaseToken string, extensionSeconds int) error
	ExecutionSignal(taskID, workerID, leaseToken string) string
	FinishAttempt(taskID, workerID, leaseToken string, params FinishAttemptParams) (*Task, error)
	PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*Task, error)
	CheckpointTask(ctx context.Context, taskID, workerID, leaseToken string, checkpoint *CheckpointData) (*Task, error)
	ResumeFromCheckpoint(ctx context.Context, taskID, workerID, leaseToken string, newLeaseSeconds int) (*Task, error)
	ResumeTask(ctx context.Context, taskID string, resumedPayload json.RawMessage, reason string) (*Task, error)
	ReconcileExpired(ctx context.Context) (int, error)
}

// TaskLineageReader defines parent-child lineage and active-count queries
// used by the context broker, dashboard, and supervisor monitors.
type TaskLineageReader interface {
	ListChildTasks(ctx context.Context, parentTaskID string) ([]*Task, error)
	ListChildTasksInSession(ctx context.Context, parentTaskID, sessionID string) ([]*Task, error)
	GetTaskLineage(ctx context.Context, taskID string) ([]*Task, error)
	GetTaskLineageInSession(ctx context.Context, taskID, sessionID string) ([]*Task, error)
	ActiveTaskCount(ctx context.Context) (int, error)
	ActiveTaskCountInSession(ctx context.Context, sessionID string) (int, error)
}

// BriefRepository defines immutable brief persistence and query operations
// used by supervisor brief issuance and context enrichment.
type BriefRepository interface {
	CreateBrief(ctx context.Context, b BriefRow) error
	GetBrief(ctx context.Context, id string) (BriefRow, error)
	ListActiveBriefs(ctx context.Context) ([]BriefRow, error)
	ListBriefs(ctx context.Context, filter BriefFilter) ([]BriefRow, error)
	UpdateBriefStatus(ctx context.Context, id string, status string) error
}

// EventLogRepository defines task event and FSM state-event audit operations.
type EventLogRepository interface {
	Events(ctx context.Context, taskID string) ([]TaskEvent, error)
	LogStateEvent(ctx context.Context, subjectID string, subject state.Subject, from, to state.State, event state.Event, actor, reason string) error
	ReplayStateEvents(ctx context.Context, subjectID string) ([]state.EventRecord, error)
	ShowStateEvents(ctx context.Context, subjectID string, limit int) ([]state.EventRecord, error)
}

// SessionRepository defines supervisor session registration and quota tracking.
type SessionRepository interface {
	RegisterSession(ctx context.Context, sess Session) error
	HeartbeatSession(ctx context.Context, sessionID string) error
	MarkSessionDead(ctx context.Context, sessionID string) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	ListActiveSessions(ctx context.Context) ([]Session, error)
	GetSessionQuota(ctx context.Context, sessionID string) (*SessionQuota, error)
	SetSessionQuota(ctx context.Context, quota SessionQuota) error
	CheckSessionQuota(ctx context.Context, sessionID string) (bool, error)
}

// ControlPlaneRepository aggregates the domain repositories implemented by *Store.
type ControlPlaneRepository interface {
	TaskQueueStore
	TaskLeaseStore
	TaskLineageReader
	BriefRepository
	EventLogRepository
	SessionRepository
	Close() error
}

var (
	_ TaskQueueStore         = (*Store)(nil)
	_ TaskLeaseStore         = (*Store)(nil)
	_ TaskLineageReader      = (*Store)(nil)
	_ BriefRepository        = (*Store)(nil)
	_ EventLogRepository     = (*Store)(nil)
	_ SessionRepository      = (*Store)(nil)
	_ ControlPlaneRepository = (*Store)(nil)
)
