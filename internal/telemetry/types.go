package telemetry

import (
	"context"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// TraceEvent represents a single telemetry event from worker/task execution.
type TraceEvent struct {
	ID          string                 `json:"id"`
	TaskID      string                 `json:"task_id"`
	SupervisorTaskID *string           `json:"supervisor_task_id,omitempty"`
	EventType   TraceEventType         `json:"event_type"`
	Timestamp   time.Time              `json:"timestamp"`
	Payload     map[string]any         `json:"payload"`
	ExitCode    *int                   `json:"exit_code,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Duration    time.Duration          `json:"duration,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
}

type TraceEventType string

const (
	TraceEventTaskStarted      TraceEventType = "task_started"
	TraceEventTaskCompleted    TraceEventType = "task_completed"
	TraceEventTaskFailed       TraceEventType = "task_failed"
	TraceEventTaskTimeout      TraceEventType = "task_timeout"
	TraceEventTaskCancelled    TraceEventType = "task_cancelled"
	TraceEventWorkerStarted    TraceEventType = "worker_started"
	TraceEventWorkerCompleted  TraceEventType = "worker_completed"
	TraceEventWorkerFailed     TraceEventType = "worker_failed"
	TraceEventReceiptEmitted   TraceEventType = "receipt_emitted"
	TraceEventReceiptVerified  TraceEventType = "receipt_verified"
	TraceEventReceiptRejected  TraceEventType = "receipt_rejected"
	TraceEventPolicyViolation  TraceEventType = "policy_violation"
	TraceEventBlockedCommand   TraceEventType = "blocked_command"
	TraceEventAnomalyDetected  TraceEventType = "anomaly_detected"
)

// FailureMode classifies the type of failure for negative knowledge distillation.
type FailureMode string

const (
	FailureModeCrash           FailureMode = "crash"            // Exit code 1
	FailureModeTimeout         FailureMode = "timeout"          // Exit code 2
	FailureModeReadOnlyViolation FailureMode = "read_only_violation" // Exit code 3
	FailureModeBlockedCommand  FailureMode = "blocked_command"  // Exit code 4
	FailureModePolicyViolation FailureMode = "policy_violation"
	FailureModeReceiptRejected FailureMode = "receipt_rejected"
	FailureModeAmbiguousPrompt FailureMode = "ambiguous_prompt"
	FailureModeResourceExhausted FailureMode = "resource_exhausted"
	FailureModeUnknown         FailureMode = "unknown"
)

// NegativePattern represents a distilled failure pattern for the Knowledge Vault.
type NegativePattern struct {
	ID               string         `json:"id"`
	PatternType      FailureMode    `json:"pattern_type"`
	Title            string         `json:"title"`
	Description      string         `json:"description"`
	RootCause        string         `json:"root_cause"`
	Remediation      string         `json:"remediation"`
	OccurrenceCount  int            `json:"occurrence_count"`
	FirstSeen        time.Time      `json:"first_seen"`
	LastSeen         time.Time      `json:"last_seen"`
	AffectedPackages []string       `json:"affected_packages"`
	ExampleContexts  []string       `json:"example_contexts"`
	ConfidenceScore  float64        `json:"confidence_score"` // 0.0 - 1.0
	Status           PatternStatus  `json:"status"`           // DRAFT, VALIDATED, APPLIED, DEPRECATED
}

type PatternStatus string

const (
	PatternStatusDraft      PatternStatus = "DRAFT"
	PatternStatusValidated  PatternStatus = "VALIDATED"
	PatternStatusApplied    PatternStatus = "APPLIED"
	PatternStatusDeprecated PatternStatus = "DEPRECATED"
)

// TelemetryIngestor defines the interface for ingesting trace events.
type TelemetryIngestor interface {
	IngestEvent(ctx context.Context, event TraceEvent) error
	IngestEvents(ctx context.Context, events []TraceEvent) error
	QueryEvents(ctx context.Context, filter TraceFilter) ([]TraceEvent, error)
	GetEvent(ctx context.Context, id string) (*TraceEvent, error)
}

// NegativeKnowledgeDistiller defines the interface for distilling negative patterns.
type NegativeKnowledgeDistiller interface {
	DistillFailure(ctx context.Context, event TraceEvent) (*NegativePattern, error)
	DistillBatch(ctx context.Context, events []TraceEvent) ([]NegativePattern, error)
	GetPatterns(ctx context.Context, filter PatternFilter) ([]NegativePattern, error)
	UpdatePattern(ctx context.Context, pattern NegativePattern) error
}

// PreflightInjectionService defines the interface for supervisor prompt injection.
type PreflightInjectionService interface {
	GetRelevantPatterns(ctx context.Context, role string, path string, prompt string) ([]NegativePattern, error)
	InjectPreflightContext(ctx context.Context, brief *controlplane.BriefRow, patterns []NegativePattern) (string, error)
}

// TraceFilter defines filtering parameters for trace queries.
type TraceFilter struct {
	TaskID           *string
	SupervisorTaskID *string
	EventTypes       []TraceEventType
	FailureModes     []FailureMode
	Since            *time.Time
	Until            *time.Time
	Tags             []string
	Limit            int
	Offset           int
}

// PatternFilter defines filtering parameters for pattern queries.
type PatternFilter struct {
	PatternTypes   []FailureMode
	Statuses       []PatternStatus
	Packages       []string
	MinConfidence  float64
	Since          *time.Time
	Limit          int
	Offset         int
}

// TelemetryConfig holds configuration for the telemetry system.
type TelemetryConfig struct {
	DBPath              string
	BatchSize           int
	FlushInterval       time.Duration
	RetentionPeriod     time.Duration
	EnableDistillation  bool
	DistillationThreshold int // min occurrences before distilling
}

func DefaultTelemetryConfig() *TelemetryConfig {
	return &TelemetryConfig{
		DBPath:               "/tmp/g8s_telemetry.db",
		BatchSize:            100,
		FlushInterval:        30 * time.Second,
		RetentionPeriod:      30 * 24 * time.Hour,
		EnableDistillation:   true,
		DistillationThreshold: 3,
	}
}