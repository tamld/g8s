// Package reflex implements a System 1 non-autoregressive decision gate
// for g8s subagent mutation triage, write receipt fast-pathing, and
// instant circuit-breaking on sandbox escapes.
//
// Complies with Zero-CGO Constitution Axiom and Zero-Leakage DLP Protocol.
// Architecture: Strictly decouples Perception Telemetry (Sensors) from
// Governance Decision-Making (Supervisor Policy Engine).
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

// TriageAction defines the concrete operational decision emitted by the g8s Supervisor policy engine.
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

// ReflexSignal represents pure perception telemetry emitted by a System 1 reflex sensor.
// It carries empirical measurements without dictating supervisor actions.
type ReflexSignal struct {
	RiskScore  float64 `json:"risk_score"`  // 0.0 (benign) -> 5.0 (critical)
	BreachProb float64 `json:"breach_prob"` // 0.0 -> 1.0 (estimated probability of sandbox breach)
	Confidence float64 `json:"confidence"`  // confidence score [0..1]
	LatencyMs  int64   `json:"latency_ms"`
	Source     string  `json:"source"` // "jev" | "deterministic"
	Reason     string  `json:"reason,omitempty"`
	KeyUsed    string  `json:"key_used,omitempty"`
	IsFallback bool    `json:"is_fallback"`
}

// TriageVerdict represents the authoritative decision made by the g8s Supervisor policy engine.
type TriageVerdict struct {
	Action     TriageAction `json:"action"`
	Signal     ReflexSignal `json:"signal"`
	RiskScore  float64      `json:"risk_score"`
	BreachProb float64      `json:"breach_prob"`
	Confidence float64      `json:"confidence"`
	LatencyMs  int64        `json:"latency_ms"`
	Reason     string       `json:"reason"`
	KeyUsed    string       `json:"key_used,omitempty"`
	IsFallback bool         `json:"is_fallback"`
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

// ReflexGate coordinates System 1 evaluations and policy gating.
type ReflexGate struct {
	endpoint   string
	model      string
	keys       []string
	keysSet    bool
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
		g.keysSet = true
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

	if !gate.keysSet && len(gate.keys) == 0 {
		gate.keys = gate.loadKeys()
	}

	return gate
}

func (g *ReflexGate) loadKeys() []string {
	var keys []string
	addKey := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !contains(keys, k) {
			keys = append(keys, k)
		}
	}

	if raw := os.Getenv("TYPESAFE_API_KEYS"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			addKey(k)
		}
	}
	addKey(os.Getenv("TYPESAFE_API_KEY"))
	addKey(os.Getenv("TYPESAFE_API_KEY_FALLBACK"))

	if len(keys) == 0 && g.useEnvFile {
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
				switch k {
				case "TYPESAFE_API_KEYS":
					for _, subk := range strings.Split(v, ",") {
						addKey(subk)
					}
				case "TYPESAFE_API_KEY", "TYPESAFE_API_KEY_FALLBACK":
					addKey(v)
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

// EmitSignal queries the System 1 reflex sensor (Jev or deterministic fallback)
// and returns pure empirical risk telemetry without taking operational actions.
func (g *ReflexGate) EmitSignal(ctx context.Context, req TriageRequest) (ReflexSignal, error) {
	activeKey := g.currentKey()
	if activeKey == "" {
		return g.deterministicFallbackSignal(req, "keyless offline fallback"), nil
	}

	// Payload strictly asks for empirical risk and breach probability,
	// NEVER asking the sensor to decide supervisor actions.
	payload := map[string]any{
		"model": g.model,
		"state": map[string]any{
			"task_id":        req.TaskID,
			"files_modified": req.FilesModified,
			"diff_summary":   req.DiffSummary,
			"allowed_paths":  req.AllowedPaths,
		},
		"questions": map[string]any{
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
		return g.deterministicFallbackSignal(req, err.Error()), nil
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return g.deterministicFallbackSignal(req, err.Error()), nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+activeKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		if len(g.keys) > 1 {
			g.rotateKey()
		}
		return g.deterministicFallbackSignal(req, err.Error()), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		g.rotateKey()
		return g.deterministicFallbackSignal(req, "HTTP 429 rate limit rotated"), nil
	}

	if resp.StatusCode != http.StatusOK {
		return g.deterministicFallbackSignal(req, fmt.Sprintf("HTTP %d from Jev", resp.StatusCode)), nil
	}

	var jevResp JevResponse
	if err := json.NewDecoder(resp.Body).Decode(&jevResp); err != nil {
		return g.deterministicFallbackSignal(req, err.Error()), nil
	}

	elapsed := time.Since(start).Milliseconds()
	breachAns, hasBreach := jevResp.Answers["sandbox_breach"]
	riskAns, hasRisk := jevResp.Answers["risk_tier"]
	if !hasBreach || !hasRisk {
		return g.deterministicFallbackSignal(req, "Jev response missing required answers"), nil
	}

	breachProb := breachAns.Noul
	riskScore := riskAns.Score
	// Weakest-Link Invariant: composite confidence is bounded by the least confident metric
	conf := riskAns.Confidence
	if breachAns.Confidence < conf {
		conf = breachAns.Confidence
	}

	reason := ""
	if conf <= 0 {
		// #329: the live Jev endpoint omits per-answer confidence (decodes
		// as 0), which made the Rule-2 grant fast-path unreachable — every
		// live verdict escalated regardless of risk. Keep Jev's empirical
		// risk/breach telemetry, but derive confidence from the
		// deterministic classifier so the gate stays calibrated: only
		// rule-certifiable low-risk mutations can fast-path.
		conf = g.deterministicFallbackSignal(req, "deterministic confidence").Confidence
		reason = "confidence derived from deterministic classifier (live Jev omitted it)"
	}

	return ReflexSignal{
		RiskScore:  riskScore,
		BreachProb: breachProb,
		Confidence: conf,
		LatencyMs:  elapsed,
		Source:     "jev",
		KeyUsed:    activeKey,
		Reason:     reason,
		IsFallback: false,
	}, nil
}

func newVerdict(signal ReflexSignal, action TriageAction, reason string) TriageVerdict {
	return TriageVerdict{
		Action:     action,
		Signal:     signal,
		RiskScore:  signal.RiskScore,
		BreachProb: signal.BreachProb,
		Confidence: signal.Confidence,
		LatencyMs:  signal.LatencyMs,
		Reason:     reason,
		KeyUsed:    signal.KeyUsed,
		IsFallback: signal.IsFallback,
	}
}

// EvaluatePolicy implements the g8s Supervisor Policy Engine.
// It consumes the empirical ReflexSignal, correlates it with the task contract,
// and deterministically emits an authoritative TriageVerdict.
func (g *ReflexGate) EvaluatePolicy(signal ReflexSignal, req TriageRequest) TriageVerdict {
	// Rule 1: Instant Kill on high sandbox breach probability (Active Defense)
	if signal.BreachProb >= 0.70 {
		return newVerdict(signal, ActionInstantKill,
			fmt.Sprintf("Supervisor policy: sandbox breach threshold exceeded (prob=%.2f >= 0.70)", signal.BreachProb))
	}

	// Rule 2: Fast-path Write Receipt grant for certified low-risk mutations within scope
	if signal.RiskScore <= 1.5 && signal.BreachProb < 0.20 && (signal.Confidence >= 0.80 || signal.IsFallback) {
		// Verify allowed paths scope gate
		if len(req.AllowedPaths) > 0 && !isWithinScope(req.FilesModified, req.AllowedPaths) {
			return newVerdict(signal, ActionEscalateHITL,
				"Supervisor policy: low risk mutation rejected due to allowed_paths scope violation")
		}

		return newVerdict(signal, ActionGrantReceipt,
			"Supervisor policy: certified low-risk mutation within declared scope boundaries")
	}

	// Rule 3: Escalate non-trivial or ambiguous mutations to Human Operator (HITL)
	return newVerdict(signal, ActionEscalateHITL,
		fmt.Sprintf("Supervisor policy: mutation flagged for human operator review (risk=%.2f, breach=%.2f)", signal.RiskScore, signal.BreachProb))
}

// TriageMutation executes the full two-step pipeline: Sensor EmitSignal + Supervisor EvaluatePolicy.
func (g *ReflexGate) TriageMutation(ctx context.Context, req TriageRequest) (TriageVerdict, error) {
	signal, err := g.EmitSignal(ctx, req)
	if err != nil {
		signal = g.deterministicFallbackSignal(req, err.Error())
	}
	return g.EvaluatePolicy(signal, req), nil
}

func fallbackSignal(risk, breach, conf float64, reason string) ReflexSignal {
	return ReflexSignal{
		RiskScore:  risk,
		BreachProb: breach,
		Confidence: conf,
		Source:     "deterministic",
		Reason:     reason,
		IsFallback: true,
	}
}

func (g *ReflexGate) deterministicFallbackSignal(req TriageRequest, reason string) ReflexSignal {
	for _, f := range req.FilesModified {
		base := strings.ToLower(filepath.Base(f))
		if strings.Contains(base, ".env") || strings.Contains(base, "id_rsa") || strings.Contains(base, "shadow") {
			return fallbackSignal(4.0, 0.99, 1.0, fmt.Sprintf("Sensitive file match %s (%s)", f, reason))
		}
	}

	allDocs := len(req.FilesModified) > 0
	for _, f := range req.FilesModified {
		ext := filepath.Ext(f)
		if !strings.EqualFold(ext, ".md") && !strings.EqualFold(ext, ".txt") {
			allDocs = false
			break
		}
	}
	if allDocs {
		return fallbackSignal(1.0, 0.05, 0.95, fmt.Sprintf("Documentation edits only (%s)", reason))
	}

	return fallbackSignal(2.5, 0.30, 0.70, fmt.Sprintf("Non-trivial mutation (%s)", reason))
}

// isWithinScope verifies whether all modified files conform to declared allowed path patterns.
//
// Confinement semantics (#359): allowed patterns are workspace-relative.
// A modified file given as an absolute path (leading "/" or Windows drive)
// is ALWAYS outside the declared scope — filepath.Clean does not remove a
// leading root, so the previous traversal-only check let an absolute path
// bypass confinement entirely.
func isWithinScope(modified []string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, f := range modified {
		cleanFile := filepath.ToSlash(filepath.Clean(f))
		if cleanFile == ".." || strings.HasPrefix(cleanFile, "../") {
			return false
		}
		if strings.HasPrefix(cleanFile, "/") || isWindowsDrivePath(cleanFile) {
			return false
		}
		matched := false
		for _, pattern := range allowed {
			if pathMatches(cleanFile, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// isWindowsDrivePath reports whether a slashed path begins with a Windows
// drive prefix (e.g. "C:/...").
func isWindowsDrivePath(cleanFile string) bool {
	if len(cleanFile) < 2 || cleanFile[1] != ':' {
		return false
	}
	drive := cleanFile[0]
	return (drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')
}

func pathMatches(cleanFile, pattern string) bool {
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	// 1. Exact path match
	if cleanFile == cleanPattern {
		return true
	}
	// 1a. Globstar: a "/**" segment matches any number of path segments,
	// including none (e.g. "src/**/*.go" matches "src/main.go" and
	// "src/a/b/main.go").
	if strings.Contains(cleanPattern, "**") {
		return globstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	// 2. Directory boundary match: pattern ends in /* or / or is a bare directory name
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}
	// 3. Glob matching (e.g. *.md, src/*.go)
	m, _ := filepath.Match(cleanPattern, cleanFile)
	return m
}

// globstarMatch matches slashed path segments against pattern segments where
// a "**" segment consumes zero or more segments (#359).
func globstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if globstarMatch(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := filepath.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return globstarMatch(fileSegs[1:], patSegs[1:])
}
