package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/config"
)

// OpenAIProvider implements the Provider and RequestTransport interfaces against
// an OpenAI Chat Completions compatible HTTP endpoint or an Anthropic Messages endpoint.
// Used by 9router and any other HTTP LLM gateway.
type OpenAIProvider struct {
	mu         sync.RWMutex
	name       string
	baseURL    string
	baseURLEnv string
	authEnv    string
	protocol   string
	headers    map[string]string
	version    string
	httpClient *http.Client
	catalog    *config.Catalog
}

// NewOpenAIProvider creates an OpenAI-compatible HTTP provider. name is
// the registry key (e.g. "9router"). baseURL is the OpenAI-compatible
// root (e.g. "http://localhost:20128/v1"). authEnv is the env var
// carrying the bearer token; empty disables auth.
func NewOpenAIProvider(name, baseURL, authEnv string) *OpenAIProvider {
	return &OpenAIProvider{
		name:       name,
		baseURL:    strings.TrimRight(baseURL, "/"),
		authEnv:    authEnv,
		protocol:   "openai",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewHTTPProviderFromConfig wires an HTTP provider from a config.ProviderEntry.
func NewHTTPProviderFromConfig(entry config.ProviderEntry) *OpenAIProvider {
	headers := make(map[string]string, len(entry.Headers))
	for k, v := range entry.Headers {
		headers[k] = v
	}
	return &OpenAIProvider{
		name:       entry.Name,
		baseURL:    entry.EffectiveBaseURL(),
		baseURLEnv: entry.BaseURLEnv,
		authEnv:    entry.AuthEnv,
		protocol:   entry.EffectiveProtocol(),
		headers:    headers,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithHTTPClient overrides the HTTP client for testing.
func (p *OpenAIProvider) WithHTTPClient(client *http.Client) *OpenAIProvider {
	p.httpClient = client
	return p
}

// WithCatalog attaches an agent-models catalog for pre-dispatch capability checks (#940).
func (p *OpenAIProvider) WithCatalog(cat *config.Catalog) *OpenAIProvider {
	p.catalog = cat
	return p
}

// TransportKind returns TransportKindRequest for HTTP gateways.
func (p *OpenAIProvider) TransportKind() TransportKind { return TransportKindRequest }

// Name returns the canonical provider name.
func (p *OpenAIProvider) Name() string { return p.name }

// Binary returns the literal "openai" sentinel for HTTP-based providers.
func (p *OpenAIProvider) Binary() string { return "openai" }

// EffectiveBaseURL returns the resolved base URL, checking baseURLEnv first.
func (p *OpenAIProvider) EffectiveBaseURL() string {
	if p.baseURLEnv != "" {
		if envVal := os.Getenv(p.baseURLEnv); envVal != "" {
			return strings.TrimRight(envVal, "/")
		}
	}
	return strings.TrimRight(p.baseURL, "/")
}

// Available probes the /models endpoint with a 3s timeout.
// For 9router, if /models returns non-200, it falls back to /api/health.
// For Anthropic, if /models returns 404/405, it succeeds as long as the server responded.
func (p *OpenAIProvider) Available(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	baseURL := p.EffectiveBaseURL()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("invalid base URL %s: %w", baseURL, err)
	}
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
			if p.protocol == "anthropic" {
				req.Header.Set("x-api-key", tok)
			}
		}
	}
	if p.protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("no model server at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	if p.protocol == "anthropic" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
		return nil
	}

	if p.protocol == "9router" || strings.EqualFold(p.name, "9router") {
		root := strings.TrimSuffix(baseURL, "/v1")
		healthReq, hErr := http.NewRequestWithContext(probeCtx, http.MethodGet, root+"/api/health", nil)
		if hErr == nil {
			if p.authEnv != "" {
				if tok := os.Getenv(p.authEnv); tok != "" {
					healthReq.Header.Set("Authorization", "Bearer "+tok)
				}
			}
			for k, v := range p.headers {
				healthReq.Header.Set(k, v)
			}
			hResp, doErr := p.httpClient.Do(healthReq)
			if doErr == nil {
				defer hResp.Body.Close()
				if hResp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}

	return fmt.Errorf("no model server at %s (status %d)", baseURL, resp.StatusCode)
}

// DiscoverModels queries GET <effectiveBaseURL>/models and returns sorted model IDs.
func (p *OpenAIProvider) DiscoverModels(ctx context.Context) ([]string, error) {
	baseURL := p.EffectiveBaseURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %s: %w", baseURL, err)
	}
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
			if p.protocol == "anthropic" {
				req.Header.Set("x-api-key", tok)
			}
		}
	}
	if p.protocol == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	ids := make([]string, 0, len(data.Data))
	for _, item := range data.Data {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Version returns "openai-compatible-v1".
func (p *OpenAIProvider) Version(_ context.Context) (string, error) {
	p.mu.RLock()
	v := p.version
	p.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	return "openai-compatible-v1", nil
}

type requestEffortResolution struct {
	Requested  string
	Applied    string
	Degraded   bool
	DegradeMsg string
}

func resolveCatalogCapabilities(providerName, modelID string, cat *config.Catalog) (config.ModelCapabilities, bool) {
	if cat == nil {
		return config.ModelCapabilities{}, false
	}
	prov, ok := cat.FindProvider(providerName)
	if !ok {
		return config.ModelCapabilities{}, false
	}
	trimmed := strings.TrimPrefix(modelID, "anthropic-messages/")
	if m, found := prov.FindModel(trimmed); found {
		return config.EffectiveCapabilities(prov, m), true
	}
	if prov.Capabilities != nil {
		return *prov.Capabilities, true
	}
	return config.ModelCapabilities{}, false
}

func resolveRequestCapabilitiesAndEffort(providerName string, isAnthropic bool, spec Spec, cat *config.Catalog) (requestEffortResolution, error) {
	var res requestEffortResolution
	if spec.Effort != "" {
		res.Requested = spec.Effort
	}

	if caps, ok := resolveCatalogCapabilities(providerName, spec.Model, cat); ok {
		if len(spec.Tools) > 0 && caps.ToolCalling != nil && !*caps.ToolCalling {
			return res, fmt.Errorf("%w: model %q (provider %q) does not support tool_calling", ErrCapabilityUnsupported, spec.Model, providerName)
		}
		if len(spec.ResponseFormat) > 0 && caps.StructuredOutput != nil && !*caps.StructuredOutput {
			return res, fmt.Errorf("%w: model %q (provider %q) does not support structured_output", ErrCapabilityUnsupported, spec.Model, providerName)
		}
		if spec.Stream && caps.Streaming != nil && !*caps.Streaming {
			return res, fmt.Errorf("%w: model %q (provider %q) does not support streaming", ErrCapabilityUnsupported, spec.Model, providerName)
		}
		if spec.Effort != "" && caps.ReasoningEffort != nil && !*caps.ReasoningEffort {
			if spec.StrictCapabilities || !spec.AllowEffortDegrade {
				return res, fmt.Errorf("%w: model %q (provider %q) does not support reasoning_effort", ErrCapabilityUnsupported, spec.Model, providerName)
			}
			res.Degraded = true
			res.DegradeMsg = fmt.Sprintf("model %q does not support reasoning_effort; dropped per fallback policy", spec.Model)
			return res, nil
		}
	}

	if spec.Effort == "" {
		return res, nil
	}

	eff := strings.ToLower(strings.TrimSpace(spec.Effort))
	if isAnthropic {
		switch eff {
		case "low", "medium", "high", "xhigh", "max":
			res.Applied = eff
		case "minimal":
			if spec.StrictCapabilities {
				return res, fmt.Errorf("%w: anthropic messages does not support effort %q", ErrCapabilityUnsupported, spec.Effort)
			}
			res.Applied = "low"
			res.Degraded = true
			res.DegradeMsg = "anthropic messages mapped effort 'minimal' to 'low'"
		case "ultra":
			if spec.StrictCapabilities {
				return res, fmt.Errorf("%w: anthropic messages does not support effort %q", ErrCapabilityUnsupported, spec.Effort)
			}
			res.Applied = "max"
			res.Degraded = true
			res.DegradeMsg = "anthropic messages mapped effort 'ultra' to 'max'"
		default:
			if spec.StrictCapabilities {
				return res, fmt.Errorf("%w: unsupported anthropic effort %q", ErrCapabilityUnsupported, spec.Effort)
			}
			res.Degraded = true
			res.DegradeMsg = fmt.Sprintf("unsupported anthropic effort %q dropped", spec.Effort)
		}
		return res, nil
	}

	switch eff {
	case "low", "medium", "high":
		res.Applied = eff
	case "xhigh", "max", "ultra":
		if spec.StrictCapabilities {
			return res, fmt.Errorf("%w: openai chat-completions does not support effort %q", ErrCapabilityUnsupported, spec.Effort)
		}
		res.Applied = "high"
		res.Degraded = true
		res.DegradeMsg = fmt.Sprintf("openai chat-completions mapped effort %q to 'high'", spec.Effort)
	case "minimal":
		if spec.StrictCapabilities {
			return res, fmt.Errorf("%w: openai chat-completions does not support effort %q", ErrCapabilityUnsupported, spec.Effort)
		}
		res.Applied = "low"
		res.Degraded = true
		res.DegradeMsg = "openai chat-completions mapped effort 'minimal' to 'low'"
	default:
		if spec.StrictCapabilities {
			return res, fmt.Errorf("%w: unsupported openai effort %q", ErrCapabilityUnsupported, spec.Effort)
		}
		res.Degraded = true
		res.DegradeMsg = fmt.Sprintf("unsupported openai effort %q dropped", spec.Effort)
	}
	return res, nil
}

// StartRequest starts an asynchronous HTTP chat/messages request and returns a typed RequestHandle (#940).
func (p *OpenAIProvider) StartRequest(ctx context.Context, spec Spec) (RequestHandle, error) {
	if err := p.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	model := spec.Model
	if model == "" {
		return nil, fmt.Errorf("openai provider requires non-empty model in spec")
	}

	// ⚡ Bolt Optimization: Use EqualFold on a sliced substring for zero-allocation case-insensitive prefix checking
	// instead of strings.HasPrefix(strings.ToLower()) which allocates a new string on the heap.
	isAnthropic := p.protocol == "anthropic" || (p.protocol == "9router" && len(spec.Model) >= len("anthropic-messages/") && strings.EqualFold(spec.Model[:len("anthropic-messages/")], "anthropic-messages/"))
	effRes, err := resolveRequestCapabilitiesAndEffort(p.name, isAnthropic, spec, p.catalog)
	if err != nil {
		return nil, err
	}

	if isAnthropic {
		return p.startAnthropicRequest(ctx, spec, effRes)
	}
	return p.startOpenAIRequest(ctx, spec, effRes)
}

// Spawn starts an HTTP request asynchronously and adapts RequestHandle for legacy Handle callers.
func (p *OpenAIProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	rh, err := p.StartRequest(ctx, spec)
	if err != nil {
		return nil, err
	}
	return newRequestHandleAdapter(rh), nil
}

func (p *OpenAIProvider) clientForTimeout(timeout time.Duration) *http.Client {
	if p.httpClient == nil {
		return &http.Client{Timeout: timeout}
	}
	if timeout > p.httpClient.Timeout {
		c := *p.httpClient
		c.Timeout = timeout
		return &c
	}
	return p.httpClient
}

func isJSONStreamStart(br *bufio.Reader) bool {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return true
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		_ = br.UnreadByte()
		return b == '{'
	}
}

func (p *OpenAIProvider) applyEffortTelemetry(rc *Receipt, effRes requestEffortResolution) {
	rc.EffortRequested = effRes.Requested
	rc.EffortApplied = effRes.Applied
	rc.EffortDegraded = effRes.Degraded
	rc.EffortDegradeMsg = effRes.DegradeMsg
}

func (p *OpenAIProvider) startAnthropicRequest(ctx context.Context, spec Spec, effRes requestEffortResolution) (RequestHandle, error) {
	baseURL := p.EffectiveBaseURL()
	var endpoint string
	if strings.HasSuffix(baseURL, "/v1") {
		endpoint = baseURL + "/messages"
	} else {
		endpoint = baseURL + "/v1/messages"
	}

	model := strings.TrimPrefix(spec.Model, "anthropic-messages/")
	maxTokens := spec.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": spec.Brief},
		},
	}
	if spec.Stream {
		reqBody["stream"] = true
	}
	if spec.SystemPrompt != "" {
		reqBody["system"] = spec.SystemPrompt
	}
	if spec.Temperature != nil {
		reqBody["temperature"] = *spec.Temperature
	}
	if len(spec.Tools) > 0 {
		reqBody["tools"] = spec.Tools
	}
	if effRes.Applied != "" {
		reqBody["output_config"] = map[string]any{"effort": effRes.Applied}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	var (
		reqCtx context.Context
		cancel context.CancelFunc
	)
	if spec.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	} else {
		reqCtx, cancel = context.WithCancel(ctx)
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if spec.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("x-api-key", tok)
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	h := newAsyncRequestHandle(p.name, cancel)
	client := p.clientForTimeout(spec.Timeout)

	go func() {
		resp, err := client.Do(req)
		if err != nil {
			rc := Receipt{Status: "FAILED", Stderr: err.Error(), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			if errors.Is(reqCtx.Err(), context.Canceled) {
				rc.Status = "CANCELLED"
				rc.ExitCode = -1
				h.complete(rc, context.Canceled)
				return
			}
			if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
				rc.Status = "TIMEOUT"
				rc.ExitCode = -1
				h.complete(rc, reqCtx.Err())
				return
			}
			h.complete(rc, fmt.Errorf("anthropic request failed: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			fullErr := fmt.Sprintf("%s returned status %d: %s", p.name, resp.StatusCode, string(respBody))
			rc := Receipt{Status: "FAILED", Stderr: fullErr, ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		useSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
		br := bufio.NewReader(resp.Body)
		if !useSSE && spec.Stream && !isJSONStreamStart(br) {
			useSSE = true
		}
		if useSSE {
			rc := p.consumeAnthropicSSE(br, h)
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		var anthResp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Model string `json:"model"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}

		if err := json.NewDecoder(br).Decode(&anthResp); err != nil {
			rc := Receipt{Status: "FAILED", Stderr: fmt.Sprintf("decode anthropic response: %v", err), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}
		if anthResp.Error != nil {
			rc := Receipt{Status: "FAILED", Stderr: fmt.Sprintf("anthropic api error: %s", anthResp.Error.Message), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}
		if len(anthResp.Content) == 0 {
			rc := Receipt{Status: "FAILED", Stderr: "no content in anthropic response", ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		var sb strings.Builder
		for _, block := range anthResp.Content {
			if block.Type == "text" {
				sb.WriteString(block.Text)
			}
		}
		content := sb.String()
		h.streamBuf.WriteString(content)
		inputTokens := anthResp.Usage.InputTokens
		outputTokens := anthResp.Usage.OutputTokens
		rc := Receipt{
			Status:        "COMPLETED",
			Stdout:        content,
			ExitCode:      0,
			ModelResolved: anthResp.Model,
			InputTokens:   inputTokens,
			OutputTokens:  outputTokens,
			TotalTokens:   inputTokens + outputTokens,
		}
		p.applyEffortTelemetry(&rc, effRes)
		h.complete(rc, nil)
	}()

	return h, nil
}

func (p *OpenAIProvider) consumeAnthropicSSE(r io.Reader, h *asyncRequestHandle) Receipt {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	var (
		sb            strings.Builder
		modelResolved string
		inputTokens   int
		outputTokens  int
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var evt struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("decode anthropic stream event: %v", err), ExitCode: 1, Streamed: true}
		}
		if evt.Error != nil && evt.Error.Message != "" {
			return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("anthropic stream error: %s", evt.Error.Message), ExitCode: 1, Streamed: true}
		}
		switch evt.Type {
		case "message_start":
			if evt.Message.Model != "" {
				modelResolved = evt.Message.Model
			}
			if evt.Message.Usage.InputTokens > 0 {
				inputTokens = evt.Message.Usage.InputTokens
			}
		case "content_block_delta":
			if evt.Delta.Text != "" {
				sb.WriteString(evt.Delta.Text)
				h.streamBuf.WriteString(evt.Delta.Text)
			}
		case "message_delta":
			if evt.Usage.OutputTokens > 0 {
				outputTokens = evt.Usage.OutputTokens
			}
			if evt.Usage.InputTokens > 0 {
				inputTokens = evt.Usage.InputTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("read anthropic stream: %v", err), ExitCode: 1, Streamed: true}
	}
	return Receipt{
		Status:        "COMPLETED",
		Stdout:        sb.String(),
		ExitCode:      0,
		ModelResolved: modelResolved,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		TotalTokens:   inputTokens + outputTokens,
		Streamed:      true,
	}
}

func (p *OpenAIProvider) startOpenAIRequest(ctx context.Context, spec Spec, effRes requestEffortResolution) (RequestHandle, error) {
	baseURL := p.EffectiveBaseURL()
	endpoint := baseURL + "/chat/completions"

	reqBody := map[string]any{
		"model":    spec.Model,
		"messages": buildMessages(spec),
		"stream":   spec.Stream,
	}
	if spec.Stream {
		reqBody["stream_options"] = map[string]any{"include_usage": true}
	}
	if spec.MaxTokens > 0 {
		reqBody["max_tokens"] = spec.MaxTokens
	}
	if spec.Temperature != nil {
		reqBody["temperature"] = *spec.Temperature
	}
	if len(spec.Tools) > 0 {
		reqBody["tools"] = spec.Tools
	}
	if len(spec.ResponseFormat) > 0 {
		reqBody["response_format"] = spec.ResponseFormat
	}
	if effRes.Applied != "" {
		reqBody["reasoning_effort"] = effRes.Applied
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	var (
		reqCtx context.Context
		cancel context.CancelFunc
	)
	if spec.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	} else {
		reqCtx, cancel = context.WithCancel(ctx)
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if spec.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	h := newAsyncRequestHandle(p.name, cancel)
	client := p.clientForTimeout(spec.Timeout)

	go func() {
		resp, err := client.Do(req)
		if err != nil {
			rc := Receipt{Status: "FAILED", Stderr: err.Error(), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			if errors.Is(reqCtx.Err(), context.Canceled) {
				rc.Status = "CANCELLED"
				rc.ExitCode = -1
				h.complete(rc, context.Canceled)
				return
			}
			if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
				rc.Status = "TIMEOUT"
				rc.ExitCode = -1
				h.complete(rc, reqCtx.Err())
				return
			}
			h.complete(rc, fmt.Errorf("chat request failed: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			bodyStr := string(respBody)
			fullErr := fmt.Sprintf("%s returned status %d: %s", p.name, resp.StatusCode, bodyStr)

			if resp.StatusCode == http.StatusBadRequest {
				lowerBody := strings.ToLower(bodyStr)
				if strings.Contains(lowerBody, "unsupported ide tool") || strings.Contains(lowerBody, "protocol mismatch") {
					fullErr += " (hint: upstream returned HTTP 400 protocol/tool mismatch on the chat-completions surface; verify the provider protocol or use a native CLI adapter / compatible chat-completions model)"
				}
			}
			rc := Receipt{Status: "FAILED", Stderr: fullErr, ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		useSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
		br := bufio.NewReader(resp.Body)
		if !useSSE && spec.Stream && !isJSONStreamStart(br) {
			useSSE = true
		}
		if useSSE {
			rc := p.consumeOpenAISSE(br, h)
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		var chatResp struct {
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage,omitempty"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}

		if err := json.NewDecoder(br).Decode(&chatResp); err != nil {
			rc := Receipt{Status: "FAILED", Stderr: fmt.Sprintf("decode chat response: %v", err), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}
		if chatResp.Error != nil {
			rc := Receipt{Status: "FAILED", Stderr: fmt.Sprintf("chat api error: %s", chatResp.Error.Message), ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}
		if len(chatResp.Choices) == 0 {
			rc := Receipt{Status: "FAILED", Stderr: "no choices in chat response", ExitCode: 1}
			p.applyEffortTelemetry(&rc, effRes)
			h.complete(rc, nil)
			return
		}

		content := chatResp.Choices[0].Message.Content
		if content == "" {
			content = chatResp.Choices[0].Message.ReasoningContent
		}
		h.streamBuf.WriteString(content)
		modelResolved := chatResp.Model
		var inputTokens, outputTokens, totalTokens int
		if chatResp.Usage != nil {
			inputTokens = chatResp.Usage.PromptTokens
			outputTokens = chatResp.Usage.CompletionTokens
			totalTokens = chatResp.Usage.TotalTokens
		}

		rc := Receipt{
			Status:        "COMPLETED",
			Stdout:        content,
			ExitCode:      0,
			ModelResolved: modelResolved,
			InputTokens:   inputTokens,
			OutputTokens:  outputTokens,
			TotalTokens:   totalTokens,
		}
		p.applyEffortTelemetry(&rc, effRes)
		h.complete(rc, nil)
	}()

	return h, nil
}

func (p *OpenAIProvider) consumeOpenAISSE(r io.Reader, h *asyncRequestHandle) Receipt {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	var (
		sb            strings.Builder
		modelResolved string
		inputTokens   int
		outputTokens  int
		totalTokens   int
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage,omitempty"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("decode chat stream chunk: %v", err), ExitCode: 1, Streamed: true}
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("chat stream error: %s", chunk.Error.Message), ExitCode: 1, Streamed: true}
		}
		if chunk.Model != "" {
			modelResolved = chunk.Model
		}
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
			totalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta == "" {
				delta = chunk.Choices[0].Delta.ReasoningContent
			}
			if delta != "" {
				sb.WriteString(delta)
				h.streamBuf.WriteString(delta)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Receipt{Status: "FAILED", Stderr: fmt.Sprintf("read chat stream: %v", err), ExitCode: 1, Streamed: true}
	}
	return Receipt{
		Status:        "COMPLETED",
		Stdout:        sb.String(),
		ExitCode:      0,
		ModelResolved: modelResolved,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		TotalTokens:   totalTokens,
		Streamed:      true,
	}
}

// buildMessages constructs the OpenAI messages array from a Spec.
// SystemPrompt goes first (role=system), Brief becomes role=user.
func buildMessages(spec Spec) []map[string]string {
	msgs := make([]map[string]string, 0, 2)
	if spec.SystemPrompt != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": spec.SystemPrompt})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": spec.Brief})
	return msgs
}

// ExecuteOptions configures a direct call via ExecuteWithResult.
type ExecuteOptions struct {
	SystemPrompt string
	MaxTokens    int
	Temperature  *float64
	Effort       string
}

// ExecuteResult represents the result of a direct LLM execution.
type ExecuteResult struct {
	Output           string `json:"output"`
	ResolvedModel    string `json:"resolved_model"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	DurationMs       int64  `json:"duration_ms"`
}

// ExecuteWithResult executes a prompt synchronously and returns structured metrics.
func (p *OpenAIProvider) ExecuteWithResult(ctx context.Context, prompt, model string, opts ExecuteOptions) (*ExecuteResult, error) {
	spec := Spec{
		Brief:        prompt,
		Model:        model,
		Effort:       opts.Effort,
		MaxTokens:    opts.MaxTokens,
		Temperature:  opts.Temperature,
		SystemPrompt: opts.SystemPrompt,
	}
	handle, err := p.Spawn(ctx, spec)
	if err != nil {
		return nil, err
	}
	rc, err := handle.Wait(ctx)
	if err != nil {
		return nil, err
	}
	resolvedModel := rc.ModelResolved
	if resolvedModel == "" {
		resolvedModel = model
	}
	return &ExecuteResult{
		Output:           rc.Stdout,
		ResolvedModel:    resolvedModel,
		PromptTokens:     rc.InputTokens,
		CompletionTokens: rc.OutputTokens,
		TotalTokens:      rc.TotalTokens,
		DurationMs:       rc.DurationMs,
	}, nil
}
