package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// OpenCodeProvider manages OpenCode CLI worker execution (DELTA-39).
type OpenCodeProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewOpenCodeProvider creates a new OpenCodeProvider instance.
func NewOpenCodeProvider() *OpenCodeProvider {
	return &OpenCodeProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (o *OpenCodeProvider) WithLookPath(lp func(string) (string, error)) *OpenCodeProvider {
	o.lookPath = lp
	return o
}

// Name returns the canonical provider name.
func (o *OpenCodeProvider) Name() string {
	return "opencode"
}

// Binary returns the resolved path or executable name.
func (o *OpenCodeProvider) Binary() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.binary != "" {
		return o.binary
	}
	return "opencode"
}

// Available checks whether opencode is resolvable in PATH or via OPENCODE_BIN and verifies --version.
func (o *OpenCodeProvider) Available(ctx context.Context) error {
	bin := os.Getenv("OPENCODE_BIN")
	if bin == "" {
		p, err := o.lookPath("opencode")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("opencode --version: %w", err)
	}

	o.mu.Lock()
	o.binary = bin
	o.version = strings.TrimSpace(string(out))
	o.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (o *OpenCodeProvider) Version(ctx context.Context) (string, error) {
	o.mu.RLock()
	v := o.version
	o.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if err := o.Available(ctx); err != nil {
		return "", err
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.version, nil
}

// Spawn launches an opencode run subprocess with the requested spec.
func (o *OpenCodeProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := o.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	o.mu.RLock()
	bin := o.binary
	o.mu.RUnlock()

	args := []string{"run"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Brief != "" {
		args = append(args, spec.Brief)
	}

	cmd := exec.Command(bin, args...)
	if spec.WorktreeDir != "" {
		cmd.Dir = spec.WorktreeDir
	}
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start opencode process: %w", err)
	}

	return newProcessHandle("opencode", cmd, start, &stdoutBuf, &stderrBuf), nil
}

// CommandCodeProvider manages CommandCode CLI worker execution (DELTA-39).
type CommandCodeProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewCommandCodeProvider creates a new CommandCodeProvider instance.
func NewCommandCodeProvider() *CommandCodeProvider {
	return &CommandCodeProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (c *CommandCodeProvider) WithLookPath(lp func(string) (string, error)) *CommandCodeProvider {
	c.lookPath = lp
	return c
}

// Name returns the canonical provider name.
func (c *CommandCodeProvider) Name() string {
	return "commandcode"
}

// Binary returns the resolved path or executable name.
func (c *CommandCodeProvider) Binary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.binary != "" {
		return c.binary
	}
	return "commandcode"
}

// Available checks whether commandcode is resolvable in PATH or via COMMANDCODE_BIN and verifies --version.
func (c *CommandCodeProvider) Available(ctx context.Context) error {
	bin := os.Getenv("COMMANDCODE_BIN")
	if bin == "" {
		p, err := c.lookPath("commandcode")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("commandcode --version: %w", err)
	}

	c.mu.Lock()
	c.binary = bin
	c.version = strings.TrimSpace(string(out))
	c.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (c *CommandCodeProvider) Version(ctx context.Context) (string, error) {
	c.mu.RLock()
	v := c.version
	c.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if err := c.Available(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version, nil
}

// Spawn launches a commandcode headless subprocess with the requested spec.
func (c *CommandCodeProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := c.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	c.mu.RLock()
	bin := c.binary
	c.mu.RUnlock()

	args := []string{"--no-session", "--skip-onboarding", "--no-auto-update"}
	if strings.ToLower(strings.TrimSpace(spec.Permission)) != "read_only" {
		args = append(args, "--yolo")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.Brief != "" {
		args = append(args, "-p", spec.Brief)
	}

	cmd := exec.Command(bin, args...)
	if spec.WorktreeDir != "" {
		cmd.Dir = spec.WorktreeDir
	}
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start commandcode process: %w", err)
	}

	return newProcessHandle("commandcode", cmd, start, &stdoutBuf, &stderrBuf), nil
}

// QwenProvider manages Qwen Code CLI worker execution.
type QwenProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewQwenProvider creates a new QwenProvider instance.
func NewQwenProvider() *QwenProvider {
	return &QwenProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (q *QwenProvider) WithLookPath(lp func(string) (string, error)) *QwenProvider {
	q.lookPath = lp
	return q
}

// Name returns the canonical provider name.
func (q *QwenProvider) Name() string {
	return "qwen"
}

// Binary returns the resolved path or executable name.
func (q *QwenProvider) Binary() string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.binary != "" {
		return q.binary
	}
	return "qwen"
}

// Available checks whether qwen is resolvable in PATH or via QWEN_BIN and verifies --version.
func (q *QwenProvider) Available(ctx context.Context) error {
	bin := os.Getenv("QWEN_BIN")
	if bin == "" {
		p, err := q.lookPath("qwen")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qwen --version: %w", err)
	}

	q.mu.Lock()
	q.binary = bin
	q.version = strings.TrimSpace(string(out))
	q.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (q *QwenProvider) Version(ctx context.Context) (string, error) {
	q.mu.RLock()
	v := q.version
	q.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if err := q.Available(ctx); err != nil {
		return "", err
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.version, nil
}

// Spawn launches a qwen non-interactive subprocess with the requested spec.
func (q *QwenProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := q.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	q.mu.RLock()
	bin := q.binary
	q.mu.RUnlock()

	var args []string
	if strings.ToLower(strings.TrimSpace(spec.Permission)) == "read_only" {
		args = append(args, "--approval-mode", "plan")
	} else {
		args = append(args, "--yolo")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Brief != "" {
		args = append(args, "-p", spec.Brief)
	}

	cmd := exec.Command(bin, args...)
	if spec.WorktreeDir != "" {
		cmd.Dir = spec.WorktreeDir
	}
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start qwen process: %w", err)
	}

	return newProcessHandle("qwen", cmd, start, &stdoutBuf, &stderrBuf), nil
}

// ResolveCursorAPIKey resolves CURSOR_API_KEY from environment or local .env files
// without exposing secrets in logs or CLI flags (#683).
func ResolveCursorAPIKey() string {
	if key := strings.TrimSpace(os.Getenv("CURSOR_API_KEY")); key != "" {
		return key
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, ".env"))
	}
	if scopeRoot := strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT")); scopeRoot != "" {
		candidates = append(candidates, filepath.Join(scopeRoot, ".env"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "g8s", ".env"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "export ")
			if strings.HasPrefix(trimmed, "CURSOR_API_KEY=") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "CURSOR_API_KEY="))
				val = strings.Trim(val, `"'`)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// CursorProvider manages Cursor Agent CLI and Cloud Agent API execution (#683).
type CursorProvider struct {
	mu             sync.RWMutex
	binary         string
	version        string
	lookPath       func(string) (string, error)
	customLookPath bool
	httpClient     *http.Client
	apiBaseURL     string
}

// NewCursorProvider creates a new CursorProvider instance.
func NewCursorProvider() *CursorProvider {
	baseURL := strings.TrimSpace(os.Getenv("CURSOR_API_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.cursor.com"
	}
	return &CursorProvider{
		lookPath:   exec.LookPath,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiBaseURL: strings.TrimRight(baseURL, "/"),
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (c *CursorProvider) WithLookPath(lp func(string) (string, error)) *CursorProvider {
	c.lookPath = lp
	c.customLookPath = true
	return c
}

// WithHTTPClient overrides the HTTP client for testing.
func (c *CursorProvider) WithHTTPClient(client *http.Client) *CursorProvider {
	c.httpClient = client
	return c
}

// WithAPIBaseURL overrides the Cloud Agent API base URL for testing.
func (c *CursorProvider) WithAPIBaseURL(baseURL string) *CursorProvider {
	c.apiBaseURL = strings.TrimRight(baseURL, "/")
	return c
}

// Name returns the canonical provider name.
func (c *CursorProvider) Name() string {
	return "cursor"
}

// Binary returns the resolved path or executable name.
func (c *CursorProvider) Binary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.binary != "" {
		return c.binary
	}
	return "cursor-agent"
}

// Available checks whether cursor-agent is resolvable in PATH, via CURSOR_BIN, or ~/.local/bin/cursor-agent.
func (c *CursorProvider) Available(ctx context.Context) error {
	bin := strings.TrimSpace(os.Getenv("CURSOR_BIN"))
	if bin == "" {
		p, err := c.lookPath("cursor-agent")
		if err != nil {
			if !c.customLookPath {
				if home, hErr := os.UserHomeDir(); hErr == nil && home != "" {
					candidate := filepath.Join(home, ".local", "bin", "cursor-agent")
					if st, sErr := os.Stat(candidate); sErr == nil && !st.IsDir() {
						p = candidate
						err = nil
					}
				}
			}
			if err != nil {
				return fmt.Errorf("not found in PATH")
			}
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cursor-agent --version: %w", err)
	}

	c.mu.Lock()
	c.binary = bin
	c.version = strings.TrimSpace(string(out))
	c.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (c *CursorProvider) Version(ctx context.Context) (string, error) {
	c.mu.RLock()
	v := c.version
	c.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if err := c.Available(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version, nil
}

// DiscoverModels queries GET https://api.cursor.com/v0/models (when CURSOR_API_KEY is available)
// and/or cursor-agent --list-models to return all available Cursor models (#683).
func (c *CursorProvider) DiscoverModels(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var models []string
	var lastErr error

	apiKey := ResolveCursorAPIKey()
	if apiKey != "" && c.httpClient != nil && c.apiBaseURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/v0/models", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+apiKey)
			resp, doErr := c.httpClient.Do(req)
			if doErr == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var payload struct {
						Models []string `json:"models"`
						Data   []struct {
							ID string `json:"id"`
						} `json:"data"`
					}
					if json.Unmarshal(body, &payload) == nil {
						for _, m := range payload.Models {
							m = strings.TrimSpace(m)
							if m != "" && !seen[m] {
								seen[m] = true
								models = append(models, m)
							}
						}
						for _, item := range payload.Data {
							m := strings.TrimSpace(item.ID)
							if m != "" && !seen[m] {
								seen[m] = true
								models = append(models, m)
							}
						}
					}
				} else {
					lastErr = fmt.Errorf("cursor /v0/models returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
				}
			} else {
				lastErr = doErr
			}
		}
	}

	if err := c.Available(ctx); err == nil {
		c.mu.RLock()
		bin := c.binary
		c.mu.RUnlock()
		cmd := exec.CommandContext(ctx, bin, "--list-models")
		if apiKey != "" && os.Getenv("CURSOR_API_KEY") == "" {
			cmd.Env = append(os.Environ(), "CURSOR_API_KEY="+apiKey)
		}
		if out, cmdErr := cmd.Output(); cmdErr == nil {
			for _, line := range strings.Split(string(out), "\n") {
				trimmed := strings.TrimSpace(line)
				if idx := strings.Index(trimmed, " - "); idx > 0 {
					id := strings.TrimSpace(trimmed[:idx])
					if id != "" && !strings.ContainsAny(id, " \t:") && !seen[id] {
						seen[id] = true
						models = append(models, id)
					}
				}
			}
		} else if len(models) == 0 {
			lastErr = fmt.Errorf("cursor-agent --list-models: %w", cmdErr)
		}
	} else if len(models) == 0 && lastErr == nil {
		lastErr = err
	}

	if len(models) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no models discovered for cursor provider")
	}
	sort.Strings(models)
	return models, nil
}

// Spawn launches a non-interactive cursor-agent subprocess with the requested spec (#683).
func (c *CursorProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := c.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	c.mu.RLock()
	bin := c.binary
	c.mu.RUnlock()

	args := []string{"--print", "--trust"}
	if strings.ToLower(strings.TrimSpace(spec.Permission)) == "read_only" {
		args = append(args, "--mode", "plan")
	} else {
		args = append(args, "--yolo")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	for _, dir := range spec.AddDirs {
		if strings.TrimSpace(dir) != "" {
			args = append(args, "--add-dir", dir)
		}
	}
	if spec.WorktreeDir != "" {
		args = append(args, "--workspace", spec.WorktreeDir)
	}
	prompt := spec.Brief
	if spec.SystemPrompt != "" {
		if prompt != "" {
			prompt = spec.SystemPrompt + "\n\n" + prompt
		} else {
			prompt = spec.SystemPrompt
		}
	}
	if prompt != "" {
		args = append(args, prompt)
	}

	cmd := exec.Command(bin, args...)
	if spec.WorktreeDir != "" {
		cmd.Dir = spec.WorktreeDir
	}
	if os.Getenv("CURSOR_API_KEY") == "" {
		if apiKey := ResolveCursorAPIKey(); apiKey != "" {
			cmd.Env = append(os.Environ(), "CURSOR_API_KEY="+apiKey)
		}
	}
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start cursor process: %w", err)
	}

	return newProcessHandle("cursor", cmd, start, &stdoutBuf, &stderrBuf), nil
}
