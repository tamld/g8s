package mesh

import "encoding/json"

type Identity struct {
	NodeID      string `json:"node_id"`
	Incarnation string `json:"incarnation"`
}

type Node struct {
	NodeID       string         `json:"node_id"`
	Incarnation  string         `json:"incarnation"`
	Capabilities map[string]any `json:"capabilities"`
	ObservedAt   string         `json:"observed_at"`
}

type QuotaWindow struct {
	Name             string   `json:"name"`
	WindowMinutes    int      `json:"window_minutes"`
	UsedPercent      *float64 `json:"used_percent"`
	RemainingPercent *float64 `json:"remaining_percent"`
	ResetsAt         *string  `json:"resets_at"`
	Status           string   `json:"status"`
}

type QuotaObservation struct {
	Source     string        `json:"source"`
	ObservedAt string        `json:"observed_at"`
	Windows    []QuotaWindow `json:"windows"`
}

type Message struct {
	Seq          int64           `json:"seq"`
	MessageID    string          `json:"message_id"`
	Sender       string          `json:"sender"`
	SenderInc    string          `json:"sender_inc"`
	Recipient    string          `json:"recipient"`
	RecipientInc string          `json:"recipient_inc"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload"`
	Acked        bool            `json:"acked"`
	CreatedAt    string          `json:"created_at"`
}

type Claim struct {
	TaskID          string   `json:"task_id"`
	NodeID          string   `json:"node_id"`
	Incarnation     string   `json:"incarnation"`
	Resource        string   `json:"resource"`
	Paths           []string `json:"paths"`
	ContextRevision int64    `json:"context_revision"`
	ExpiresAt       int64    `json:"expires_at"`
	State           string   `json:"state"`
	ArtifactRef     string   `json:"artifact_ref"`
	ArtifactSHA256  string   `json:"artifact_sha256"`
}
