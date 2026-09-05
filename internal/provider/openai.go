package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// OpenAIProvider implements the Provider interface against an OpenAI
// Chat Completions compatible HTTP endpoint. Used by 9router and any
// other OpenAI-compatible gateway (OpenRouter, local llama.cpp, etc).
type OpenAIProvider struct {
	mu         sync.RWMutex
	name       string
	baseURL    string
	authEnv    string
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
		httpClient: &http.Client{Timeout: 30 * time.Second},
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

// Available probes the /models endpoint with a 2s timeout.
func (p *OpenAIProvider) Available(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("invalid base URL %s: %w", p.baseURL, err)
	}
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("no model server at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("no model server at %s (status %d)", p.baseURL, resp.StatusCode)
	}
	return nil
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

// Spawn issues a /chat/completions request and returns a Handle that
// resolves with the assistant message on completion.
func (p *OpenAIProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := p.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	model := spec.Model
	if model == "" {
		return nil, fmt.Errorf("openai provider requires non-empty model in spec")
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": buildMessages(spec),
	}
	if spec.Timeout > 0 {
		reqBody["timeout"] = int(spec.Timeout.Seconds())
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.authEnv != "" {
		if tok := os.Getenv(p.authEnv); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	start := time.Now()
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	stream := newHTTPStream(p.name, resp, start)
	stdout.WriteString(stream.content)

	if stream.err != nil {
		stderr.WriteString(stream.err.Error())
		return newImmediateHandle(p.name, "FAILED", stream.content, stream.err.Error(), 1, start), nil
	}
	return newImmediateHandle(p.name, "COMPLETED", stream.content, "", 0, start), nil
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

// httpStream wraps an http.Response body and accumulates the assistant
// content. Sync-blocking only; streaming lives in a future tranche.
type httpStream struct {
	content string
	err     error
}

func newHTTPStream(name string, resp *http.Response, start time.Time) *httpStream {
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return &httpStream{
			err: fmt.Errorf("%s returned status %d: %s", name, resp.StatusCode, string(body)),
		}
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return &httpStream{err: fmt.Errorf("decode chat response: %w", err)}
	}
	if chatResp.Error != nil {
		return &httpStream{err: fmt.Errorf("chat api error: %s", chatResp.Error.Message)}
	}
	if len(chatResp.Choices) == 0 {
		return &httpStream{err: fmt.Errorf("no choices in chat response")}
	}
	return &httpStream{content: chatResp.Choices[0].Message.Content}
}

// immediateHandle resolves synchronously without an OS subprocess.
// Used by HTTP providers where the "process" is the request itself.
type immediateHandle struct {
	mu       sync.Mutex
	provider string
	status   string
	stdout   string
	stderr   string
	exitCode int
	start    time.Time
	done     chan struct{}
}

func newImmediateHandle(provider, status, stdout, stderr string, exitCode int, start time.Time) *immediateHandle {
	h := &immediateHandle{
		provider: provider,
		status:   status,
		stdout:   stdout,
		stderr:   stderr,
		exitCode: exitCode,
		start:    start,
		done:     make(chan struct{}),
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
		Provider:   h.provider,
		Status:     h.status,
		Stdout:     h.stdout,
		Stderr:     h.stderr,
		ExitCode:   h.exitCode,
		DurationMs: time.Since(h.start).Milliseconds(),
	}, err
}

func (h *immediateHandle) Cancel(_ context.Context) error { return nil }

func (h *immediateHandle) StdoutStream() io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(h.stdout)))
}
