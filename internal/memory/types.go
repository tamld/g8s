package memory

import (
	"context"
	"errors"
	"time"

	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/vault"
)

var (
	// ErrNotFound is returned when a requested record or task context does not exist.
	ErrNotFound = errors.New("memory: record not found")

	// ErrContextPurged is returned when attempting to read the raw prompt of a purged context.
	ErrContextPurged = errors.New("memory: context raw payload has been redacted and purged")

	// ErrDimensionMismatch is returned when vector similarity is attempted between vectors of unequal dimensions.
	ErrDimensionMismatch = errors.New("memory: vector dimension mismatch")

	// ErrEmptyVector is returned when an embedding vector is nil or length 0.
	ErrEmptyVector = errors.New("memory: empty vector")

	// ErrInvalidInput is returned when arguments fail basic validation.
	ErrInvalidInput = errors.New("memory: invalid input")
)

// WorkingContextStatus defines the lifecycle state of a working memory context.
type WorkingContextStatus string

const (
	StatusActive    WorkingContextStatus = "ACTIVE"
	StatusCompacted WorkingContextStatus = "COMPACTED"
	StatusPurged    WorkingContextStatus = "PURGED"
)

// WorkingContext models the ephemeral execution scratchpad and contract prompt for a task.
type WorkingContext struct {
	TaskID         string               `json:"task_id"`
	Prompt         string               `json:"prompt"`
	Role           string               `json:"role"`
	AllowedPaths   []string             `json:"allowed_paths"`
	Status         WorkingContextStatus `json:"status"`
	RedactedDigest string               `json:"redacted_digest,omitempty"` // sha256:<hex>
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

// RawPrompt safely returns the working context prompt or ErrContextPurged if already redacted.
func (w *WorkingContext) RawPrompt() (string, error) {
	if w == nil {
		return "", ErrInvalidInput
	}
	if w.Status == StatusPurged {
		return "", ErrContextPurged
	}
	return w.Prompt, nil
}

// EpisodicEvent models an event occurrence during task execution.
type EpisodicEvent struct {
	TaskID    string         `json:"task_id"`
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// LineageNode represents a single task entry in a causal execution graph.
type LineageNode struct {
	TaskID       string         `json:"task_id"`
	ParentTaskID string         `json:"parent_task_id,omitempty"`
	Role         string         `json:"role"`
	Status       string         `json:"status"`
	ExitCode     *int           `json:"exit_code,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	Children     []*LineageNode `json:"children,omitempty"`
}

// LineageDAG is a structured tree representing the causal ancestor and descendant tasks.
type LineageDAG struct {
	RootTaskID string         `json:"root_task_id"`
	TotalNodes int            `json:"total_nodes"`
	Nodes      []*LineageNode `json:"nodes"`
}

// VectorMatch represents a single candidate result returned from vector similarity ranking.
type VectorMatch struct {
	RecordID   string         `json:"record_id"`
	Score      float64        `json:"score"` // Cosine similarity in range [-1.0, 1.0]
	Dimensions int            `json:"dimensions"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// HybridQuery parameters for unified multi-modal memory retrieval.
type HybridQuery struct {
	Query    string    `json:"query,omitempty"`     // Lexical BM25 search term
	TaskID   string    `json:"task_id,omitempty"`   // Task ID for causal lineage traversal
	Vector   []float32 `json:"vector,omitempty"`    // Dense embedding vector for cosine ranking
	Limit    int       `json:"limit,omitempty"`     // Maximum results per retrieval dimension
	MinScore float64   `json:"min_score,omitempty"` // Minimum vector similarity score threshold
}

// HybridResult combines results across semantic, episodic, and vector cognitive dimensions.
type HybridResult struct {
	SemanticLessons []vault.SearchResult `json:"semantic_lessons,omitempty"`
	LineageTree     *LineageDAG          `json:"lineage_tree,omitempty"`
	VectorMatches   []VectorMatch        `json:"vector_matches,omitempty"`
}

// MemoryAdapter specifies the unified facade contract unifying all four cognitive memory taxonomies.
type MemoryAdapter interface {
	// Working Memory (Ephemeral, Prompt-Length Bounded & Redacted)
	StoreWorkingContext(ctx context.Context, taskID string, wCtx *WorkingContext) error
	LoadWorkingContext(ctx context.Context, taskID string) (*WorkingContext, error)
	PurgeWorkingContext(ctx context.Context, taskID string) error

	// Episodic Memory (Causal Lineage & Events)
	RecordEpisode(ctx context.Context, taskID string, event *EpisodicEvent) error
	GetLineageTree(ctx context.Context, taskID string) (*LineageDAG, error)

	// Semantic Memory (Knowledge Vault FTS5/BM25)
	StoreKnowledge(ctx context.Context, record *vault.DistillationRecord) error
	SearchKnowledge(ctx context.Context, query string, limit int) ([]vault.SearchResult, error)

	// Capability Memory (Single-Use Write Receipts)
	ValidateCapability(ctx context.Context, receiptID string, consumerTaskID string) (*receipt.WriteReceipt, error)

	// Pure-Go Vector Similarity Engine
	StoreVector(ctx context.Context, recordID string, vector []float32, metadata map[string]any) error
	SearchVector(ctx context.Context, queryVector []float32, limit int, minScore float64) ([]VectorMatch, error)

	// Hybrid Retrieval Engine
	HybridRetrieve(ctx context.Context, req HybridQuery) (*HybridResult, error)

	// Lifecycle
	Close() error
}
