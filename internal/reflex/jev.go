// Package reflex implements a System 1 non-autoregressive decision gate
// for g8s subagent mutation triage, write receipt fast-pathing, and
// instant circuit-breaking on sandbox escapes.
//
// Complies with Zero-CGO Constitution Axiom and Zero-Leakage DLP Protocol.
package reflex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TriageAction defines the concrete operational decision emitted by the reflex gate.
type TriageAction string

const (
	ActionGrantReceipt TriageAction = "grant_receipt"
	ActionEscalateHITL TriageAction = "escalate_hitl"
	ActionInstantKill  TriageAction = "instant_kill"
)

// TriageRequest carries the mutation context from a subagent.
type TriageRequest struct {
	TaskID        string   `json:"task_id"`
	FilesModified []string `json:"files_modified"`
	DiffSummary   string   `json:"diff_summary"`
	AllowedPaths  []string `json:"allowed_paths"`
}

// TriageVerdict represents the sub-100ms structured decision.
type TriageVerdict struct {
	Action      TriageAction `json:"action"`
	RiskScore   float64      `json:"risk_score"`
	BreachProb  float64      `json:"breach_prob"`
	Confidence  float64      `json:"confidence"`
	LatencyMs   int64        `json:"latency_ms"`
	Reason      string       `json:"reason"`
	KeyUsed     string       `json:"key_used"`
	IsFallback  bool         `json:"is_fallback"`
}

// Answer represents a typed Jev question response.
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Usage captures token telemetry from Jev.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// JevResponse represents the complete payload from api.typesafe.ai/v1/systemone.
type JevResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// ReflexGate coordinates System 1 evaluations.
type ReflexGate struct {
	endpoint   string
	model      string
	keys       []string
	keyIdx     int
	mu         sync.Mutex
	client     *http.Client
	useEnvFile bool
}

// Option configures ReflexGate.
type Option func(*ReflexGate)

func WithEndpoint(endpoint string) Option {
	return func(g *ReflexGate) {
		if endpoint != "" {
			g.endpoint = endpoint
		}
	}
}

func WithKeys(keys []string) Option {
	return func(g *ReflexGate) {
		g.keys = keys
	}
}

func WithEnvFile(load bool) Option {
	return func(g *ReflexGate) {
		g.useEnvFile = load
	}
}

func WithTimeout(d time.Duration) Option {
	return func(g *ReflexGate) {
		g.client.Timeout = d
	}
}

// NewReflexGate returns an initialized ReflexGate with key pool and failover.
func NewReflexGate(opts ...Option) *ReflexGate {
	gate := &ReflexGate{
		endpoint:   "https://api.typesafe.ai/v1/systemone",
		model:      "jev-latest",
		client:     &http.Client{Timeout: 2500 * time.Millisecond},
		useEnvFile: true,
	}

	for _, opt := range opts {
		opt(gate)
	}

	if len(gate.keys) == 0 {
		gate.keys = gate.loadKeys()
	}

	return gate
}

func (g *ReflexGate) loadKeys() []string {
	var keys []string

	if raw := os.Getenv("TYPESAFE_API_KEYS"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k != "" && !contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}

	if primary := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")); primary != "" && !contains(keys, primary) {
		keys = append(keys, primary)
	}

	if fallback := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY_FALLBACK")); fallback != "" && !contains(keys, fallback) {
		keys = append(keys, fallback)
	}

	if len(keys) == 0 && g.useEnvFile {
		// Read from local gitignored .env (support walking up from package dirs)
		for _, d := range []string{".", "..", "../.."} {
			envPath := filepath.Join(d, ".env")
			data, err := os.ReadFile(envPath)
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(bytes.NewReader(data))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				k := strings.TrimSpace(parts[0])
				v := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
				if k == "TYPESAFE_API_KEYS" {
					for _, subk := range strings.Split(v, ",") {
						subk = strings.TrimSpace(subk)
						if subk != "" && !contains(keys, subk) {
							keys = append(keys, subk)
						}
					}
				} else if (k == "TYPESAFE_API_KEY" || k == "TYPESAFE_API_KEY_FALLBACK") && v != "" && !contains(keys, v) {
					keys = append(keys, v)
				}
			}
			if len(keys) > 0 {
				break
			}
		}
	}

	return keys
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func (g *ReflexGate) currentKey() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.keys) == 0 {
		return ""
	}
	return g.keys[g.keyIdx%len(g.keys)]
}

func (g *ReflexGate) rotateKey() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.keys) == 0 {
		return ""
	}
	g.keyIdx = (g.keyIdx + 1) % len(g.keys)
	return g.keys[g.keyIdx]
}

// TriageMutation evaluates a subagent write request.
func (g *ReflexGate) TriageMutation(ctx context.Context, req TriageRequest) (TriageVerdict, error) {
	activeKey := g.currentKey()
	if activeKey == "" {
		// Fallback to deterministic heuristic when keyless
		return g.deterministicFallback(req, "keyless offline fallback"), nil
	}

	payload := map[string]any{
		"model": g.model,
		"state": map[string]any{
			"task_id":        req.TaskID,
			"files_modified": req.FilesModified,
			"diff_summary":   req.DiffSummary,
			"allowed_paths":  req.AllowedPaths,
		},
		"questions": map[string]any{
			"action": map[string]any{
				"type":         "choice",
				"instructions": "What action should g8s supervisor take on this subagent workspace mutation?",
				"criteria": map[string]string{
					"grant_receipt":  "Safe, within declared scope and sandbox boundaries. Auto-grant Schema v3 Write Receipt.",
					"escalate_human": "Requires human operator sign-off via Telegram HITL card with HMAC signature.",
					"abort_process":  "Dangerous or malicious mutation violating security policy. Instant SIGKILL.",
				},
			},
			"risk_tier": map[string]any{
				"type":         "score",
				"instructions": "Rate the operational risk level of this mutation.",
				"criteria":     []string{"benign", "low", "medium", "high", "critical"},
			},
			"sandbox_breach": map[string]any{
				"type":         "noul",
				"instructions": "Does this mutation access or tamper with private credentials, root SSH keys, .env secrets, or escape sandbox boundaries?",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return g.deterministicFallback(req, err.Error()), nil
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return g.deterministicFallback(req, err.Error()), nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+activeKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		// Try rotating key once if network or timeout
		if len(g.keys) > 1 {
			g.rotateKey()
		}
		return g.deterministicFallback(req, err.Error()), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		g.rotateKey()
		return g.deterministicFallback(req, "HTTP 429 rate limit rotated"), nil
	}

	if resp.StatusCode != http.StatusOK {
		return g.deterministicFallback(req, fmt.Sprintf("HTTP %d from Jev", resp.StatusCode)), nil
	}

	var jevResp JevResponse
	if err := json.NewDecoder(resp.Body).Decode(&jevResp); err != nil {
		return g.deterministicFallback(req, err.Error()), nil
	}

	elapsed := time.Since(start).Milliseconds()
	breachProb := jevResp.Answers["sandbox_breach"].Noul
	riskScore := jevResp.Answers["risk_tier"].Score
	conf := jevResp.Answers["action"].Confidence

	// Decision Matrix:
	// 1. Sandbox breach >= 0.70 -> INSTANT KILL
	if breachProb >= 0.70 {
		return TriageVerdict{
			Action:      ActionInstantKill,
			RiskScore:   riskScore,
			BreachProb:  breachProb,
			Confidence:  conf,
			LatencyMs:   elapsed,
			Reason:      fmt.Sprintf("Jev sandbox breach detected (prob=%.2f)", breachProb),
			KeyUsed:     activeKey,
			IsFallback:  false,
		}, nil
	}

	// 2. Risk <= 1.5 AND breach < 0.20 AND confidence >= 0.85 -> AUTO GRANT
	if riskScore <= 1.5 && breachProb < 0.20 && conf >= 0.85 {
		return TriageVerdict{
			Action:      ActionGrantReceipt,
			RiskScore:   riskScore,
			BreachProb:  breachProb,
			Confidence:  conf,
			LatencyMs:   elapsed,
			Reason:      "Jev certified low-risk mutation within sandbox boundaries",
			KeyUsed:     activeKey,
			IsFallback:  false,
		}, nil
	}

	// 3. Otherwise -> ESCALATE TO TELEGRAM HITL
	return TriageVerdict{
		Action:      ActionEscalateHITL,
		RiskScore:   riskScore,
		BreachProb:  breachProb,
		Confidence:  conf,
		LatencyMs:   elapsed,
		Reason:      fmt.Sprintf("Jev flagged for human review (risk=%.2f, breach=%.2f)", riskScore, breachProb),
		KeyUsed:     activeKey,
		IsFallback:  false,
	}, nil
}

func (g *ReflexGate) deterministicFallback(req TriageRequest, reason string) TriageVerdict {
	// Inspect files modified for high-risk targets
	for _, f := range req.FilesModified {
		base := strings.ToLower(filepath.Base(f))
		if strings.Contains(base, ".env") || strings.Contains(base, "id_rsa") || strings.Contains(base, "shadow") {
			return TriageVerdict{
				Action:     ActionInstantKill,
				RiskScore:  4.0,
				BreachProb: 0.99,
				Confidence: 1.0,
				Reason:     fmt.Sprintf("Deterministic circuit-breaker: sensitive file match %s (%s)", f, reason),
				IsFallback: true,
			}
		}
	}

	// If only documentation files (.md, .txt)
	allDocs := true
	for _, f := range req.FilesModified {
		ext := strings.ToLower(filepath.Ext(f))
		if ext != ".md" && ext != ".txt" {
			allDocs = false
			break
		}
	}

	if allDocs && len(req.FilesModified) > 0 {
		return TriageVerdict{
			Action:     ActionGrantReceipt,
			RiskScore:  1.0,
			BreachProb: 0.05,
			Confidence: 0.95,
			Reason:     fmt.Sprintf("Deterministic fallback: documentation edits only (%s)", reason),
			IsFallback: true,
		}
	}

	return TriageVerdict{
		Action:     ActionEscalateHITL,
		RiskScore:  2.5,
		BreachProb: 0.30,
		Confidence: 0.70,
		Reason:     fmt.Sprintf("Deterministic fallback: non-trivial mutation (%s)", reason),
		IsFallback: true,
	}
}
