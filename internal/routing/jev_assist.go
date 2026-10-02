package routing

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

	"github.com/tamld/g8s/internal/harness"
)

type contextKey string

const (
	ctxKeyRouterMode  = contextKey("g8s_router_mode")
	ctxKeyJevEndpoint = contextKey("jev_endpoint")
	ctxKeyJevKeys     = contextKey("jev_keys")
)

// WithRouterMode returns a context with an overridden router mode.
func WithRouterMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, ctxKeyRouterMode, mode)
}

// WithJevAssist returns a context configured for Jev assist with the given endpoint and keys.
func WithJevAssist(ctx context.Context, endpoint string, keys ...string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyRouterMode, "jev_assisted")
	ctx = context.WithValue(ctx, ctxKeyJevEndpoint, endpoint)
	return context.WithValue(ctx, ctxKeyJevKeys, keys)
}

func getRouterMode(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRouterMode).(string); ok && v != "" {
		return v
	}
	return os.Getenv("G8S_ROUTER_MODE")
}

func getJevEndpoint(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyJevEndpoint).(string); ok && v != "" {
		return v
	}
	if ep := os.Getenv("TYPESAFE_ENDPOINT"); ep != "" {
		return ep
	}
	return "https://api.typesafe.ai/v1/systemone"
}

var (
	keyPoolMu  sync.Mutex
	keyPoolIdx int
)

func loadJevKeys(ctx context.Context) []string {
	if v, ok := ctx.Value(ctxKeyJevKeys).([]string); ok && len(v) > 0 {
		return v
	}

	var keys []string
	addKey := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !containsStr(keys, k) {
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

	if len(keys) == 0 {
		for _, d := range []string{".", "..", "../.."} {
			data, err := os.ReadFile(filepath.Join(d, ".env"))
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

func containsStr(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func getActiveKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	keyPoolMu.Lock()
	defer keyPoolMu.Unlock()
	return keys[keyPoolIdx%len(keys)]
}

func rotateKey(keys []string) {
	if len(keys) <= 1 {
		return
	}
	keyPoolMu.Lock()
	defer keyPoolMu.Unlock()
	keyPoolIdx = (keyPoolIdx + 1) % len(keys)
}

// Answer represents a typed Jev question response.
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// JevResponse represents the typed response payload from Jev System 1.
type JevResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
}

func jevAssist(ctx context.Context, req RouteRequest, layer1 Decision, candidates []candidateModel) (Decision, error) {
	keys := loadJevKeys(ctx)
	if len(keys) == 0 {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist enabled but no API keys found; fallback to %s", layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	activeKey := getActiveKey(keys)
	endpoint := getJevEndpoint(ctx)

	// Build choice sets
	providerSet := make(map[string]struct{})
	modelSet := make(map[string]struct{})
	for _, c := range candidates {
		providerSet[c.provider] = struct{}{}
		modelSet[c.model] = struct{}{}
	}
	var providerChoices []string
	for p := range providerSet {
		providerChoices = append(providerChoices, p)
	}
	var modelChoices []string
	for m := range modelSet {
		modelChoices = append(modelChoices, m)
	}
	roleChoices := harness.RoleNames()

	payload := map[string]any{
		"model": "jev-latest",
		"state": map[string]any{
			"prompt":       req.Prompt,
			"paths":        req.Paths,
			"summary":      req.Summary,
			"timeout_hint": req.TimeoutHint,
		},
		"questions": map[string]any{
			"provider": map[string]any{
				"type":         "choice",
				"instructions": "Suggest the best provider for this task.",
				"choices":      providerChoices,
			},
			"model": map[string]any{
				"type":         "choice",
				"instructions": "Suggest the best model for this task.",
				"choices":      modelChoices,
			},
			"role": map[string]any{
				"type":         "choice",
				"instructions": "Suggest the worker role profile.",
				"choices":      roleChoices,
			},
			"reason": map[string]any{
				"type":         "choice",
				"instructions": "Reason for the suggestion.",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (marshal error: %v); fallback to %s", err, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (request error: %v); fallback to %s", err, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+activeKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2500 * time.Millisecond}
	resp, err := client.Do(httpReq)
	if err != nil {
		rotateKey(keys)
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (%v); fallback to %s", err, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		rotateKey(keys)
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (HTTP 429 rate limit rotated); fallback to %s", layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	if resp.StatusCode != http.StatusOK {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (HTTP %d from Jev); fallback to %s", resp.StatusCode, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	var jevResp JevResponse
	if err := json.NewDecoder(resp.Body).Decode(&jevResp); err != nil {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev assist unavailable (decode error: %v); fallback to %s", err, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	sugProvider := strings.TrimSpace(jevResp.Answers["provider"].Choice)
	sugModel := strings.TrimSpace(jevResp.Answers["model"].Choice)
	sugRole := strings.TrimSpace(jevResp.Answers["role"].Choice)
	sugReason := strings.TrimSpace(jevResp.Answers["reason"].Choice)

	// Deterministic Validation of Jev suggestion
	if sugProvider == "" || sugModel == "" || sugRole == "" {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev suggestion rejected (empty fields: provider=%q, model=%q, role=%q); fallback to %s",
			sugProvider, sugModel, sugRole, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	// Validate role against registered harness roles
	if _, err := harness.GetRole(sugRole); err != nil {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev suggestion rejected (unknown role %q); fallback to %s", sugRole, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	// Validate (provider, model) against active manifest candidates
	validCandidate := false
	for _, c := range candidates {
		if c.provider == sugProvider && c.model == sugModel {
			validCandidate = true
			break
		}
	}
	if !validCandidate {
		layer1.IsFallback = true
		layer1.Reason = fmt.Sprintf("Jev suggestion rejected (unknown provider/model %s/%s); fallback to %s",
			sugProvider, sugModel, layer1.Reason)
		layer1.Source = "deterministic"
		return layer1, nil
	}

	// Compute composite confidence bounded by weakest link
	conf := jevResp.Answers["model"].Confidence
	if pConf := jevResp.Answers["provider"].Confidence; pConf > 0 && (conf <= 0 || pConf < conf) {
		conf = pConf
	}
	if rConf := jevResp.Answers["role"].Confidence; rConf > 0 && (conf <= 0 || rConf < conf) {
		conf = rConf
	}
	if conf <= 0 {
		conf = 0.85
	}

	if sugReason == "" {
		sugReason = fmt.Sprintf("Jev suggested provider=%s model=%s role=%s", sugProvider, sugModel, sugRole)
	}

	return Decision{
		Provider:   sugProvider,
		Model:      sugModel,
		Role:       sugRole,
		Source:     "jev",
		IsFallback: false,
		Reason:     sugReason,
		Confidence: conf,
	}, nil
}
