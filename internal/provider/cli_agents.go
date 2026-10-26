package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
