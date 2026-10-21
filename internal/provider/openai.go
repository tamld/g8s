package provider

import (
	"bytes"
	"context"
	"encoding/json"
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

// OpenAIProvider implements the Provider interface against an OpenAI
// Chat Completions compatible HTTP endpoint or an Anthropic Messages endpoint.
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

// Spawn issues a chat or messages request and returns a Handle that
// resolves with the assistant message and token usage on completion.
func (p *OpenAIProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := p.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	model := spec.Model
	if model == "" {
		return nil, fmt.Errorf("openai provider requires non-empty model in spec")
	}

	isAnthropic := p.protocol == "anthropic" || (p.protocol == "9router" && strings.HasPrefix(strings.ToLower(spec.Model), "anthropic-messages/"))
	if isAnthropic {
		return p.spawnAnthropic(ctx, spec)
	}
	return p.spawnOpenAI(ctx, spec)
}

func (p *OpenAIProvider) spawnAnthropic(ctx context.Context, spec Spec) (Handle, error) {
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

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
	if spec.SystemPrompt != "" {
		reqBody["system"] = spec.SystemPrompt
	}
	if spec.Temperature != nil {
		reqBody["temperature"] = *spec.Temperature
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("x-api-key", tok)
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fullErr := fmt.Sprintf("%s returned status %d: %s", p.name, resp.StatusCode, string(respBody))
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fullErr, 1, start, "", 0, 0, 0), nil
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

	if err := json.NewDecoder(resp.Body).Decode(&anthResp); err != nil {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fmt.Sprintf("decode anthropic response: %v", err), 1, start, "", 0, 0, 0), nil
	}
	if anthResp.Error != nil {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fmt.Sprintf("anthropic api error: %s", anthResp.Error.Message), 1, start, "", 0, 0, 0), nil
	}
	if len(anthResp.Content) == 0 {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", "no content in anthropic response", 1, start, "", 0, 0, 0), nil
	}

	var sb strings.Builder
	for _, block := range anthResp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	content := sb.String()
	modelResolved := anthResp.Model
	inputTokens := anthResp.Usage.InputTokens
	outputTokens := anthResp.Usage.OutputTokens
	totalTokens := inputTokens + outputTokens

	return newImmediateHandleWithMetadata(p.name, "COMPLETED", content, "", 0, start, modelResolved, inputTokens, outputTokens, totalTokens), nil
}

func (p *OpenAIProvider) spawnOpenAI(ctx context.Context, spec Spec) (Handle, error) {
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	baseURL := p.EffectiveBaseURL()
	endpoint := baseURL + "/chat/completions"

	reqBody := map[string]any{
		"model":    spec.Model,
		"messages": buildMessages(spec),
		"stream":   false,
	}
	if spec.MaxTokens > 0 {
		reqBody["max_tokens"] = spec.MaxTokens
	}
	if spec.Temperature != nil {
		reqBody["temperature"] = *spec.Temperature
	}
	if spec.Effort != "" {
		effort := strings.ToLower(strings.TrimSpace(spec.Effort))
		switch effort {
		case "xhigh", "max", "ultra":
			effort = "high"
		case "minimal":
			effort = "low"
		}
		if effort == "low" || effort == "medium" || effort == "high" {
			reqBody["reasoning_effort"] = effort
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fullErr := fmt.Sprintf("%s returned status %d: %s", p.name, resp.StatusCode, string(respBody))
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fullErr, 1, start, "", 0, 0, 0), nil
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

	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fmt.Sprintf("decode chat response: %v", err), 1, start, "", 0, 0, 0), nil
	}
	if chatResp.Error != nil {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", fmt.Sprintf("chat api error: %s", chatResp.Error.Message), 1, start, "", 0, 0, 0), nil
	}
	if len(chatResp.Choices) == 0 {
		return newImmediateHandleWithMetadata(p.name, "FAILED", "", "no choices in chat response", 1, start, "", 0, 0, 0), nil
	}

	content := chatResp.Choices[0].Message.Content
	if content == "" {
		content = chatResp.Choices[0].Message.ReasoningContent
	}
	modelResolved := chatResp.Model
	var inputTokens, outputTokens, totalTokens int
	if chatResp.Usage != nil {
		inputTokens = chatResp.Usage.PromptTokens
		outputTokens = chatResp.Usage.CompletionTokens
		totalTokens = chatResp.Usage.TotalTokens
	}

	return newImmediateHandleWithMetadata(p.name, "COMPLETED", content, "", 0, start, modelResolved, inputTokens, outputTokens, totalTokens), nil
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

// immediateHandle resolves synchronously without an OS subprocess.
// Used by HTTP providers where the "process" is the request itself.
type immediateHandle struct {
	provider      string
	status        string
	stdout        string
	stderr        string
	exitCode      int
	start         time.Time
	modelResolved string
	inputTokens   int
	outputTokens  int
	totalTokens   int
	done          chan struct{}
}

func newImmediateHandle(provider, status, stdout, stderr string, exitCode int, start time.Time) *immediateHandle {
	return newImmediateHandleWithMetadata(provider, status, stdout, stderr, exitCode, start, "", 0, 0, 0)
}

func newImmediateHandleWithMetadata(provider, status, stdout, stderr string, exitCode int, start time.Time, modelResolved string, inputTokens, outputTokens, totalTokens int) *immediateHandle {
	h := &immediateHandle{
		provider:      provider,
		status:        status,
		stdout:        stdout,
		stderr:        stderr,
		exitCode:      exitCode,
		start:         start,
		modelResolved: modelResolved,
		inputTokens:   inputTokens,
		outputTokens:  outputTokens,
		totalTokens:   totalTokens,
		done:          make(chan struct{}),
	}
	close(h.done)
	return h
}

func (h *immediateHandle) PID() int { return 0 }

func (h *immediateHandle) Wait(_ context.Context) (Receipt, error) {
	<-h.done
	var err error
	if h.status != "COMPLETED" {
		err = fmt.Errorf("%s", h.stderr)
	}
	return Receipt{
		Provider:      h.provider,
		Status:        h.status,
		Stdout:        h.stdout,
		Stderr:        h.stderr,
		ExitCode:      h.exitCode,
		DurationMs:    time.Since(h.start).Milliseconds(),
		ModelResolved: h.modelResolved,
		InputTokens:   h.inputTokens,
		OutputTokens:  h.outputTokens,
		TotalTokens:   h.totalTokens,
	}, err
}

func (h *immediateHandle) Cancel(_ context.Context) error { return nil }

func (h *immediateHandle) StdoutStream() io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(h.stdout)))
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
