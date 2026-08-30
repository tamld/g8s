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

// ClaudeProvider manages Claude Code CLI worker execution.
type ClaudeProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewClaudeProvider creates a new ClaudeProvider instance.
func NewClaudeProvider() *ClaudeProvider {
	return &ClaudeProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (c *ClaudeProvider) WithLookPath(lp func(string) (string, error)) *ClaudeProvider {
	c.lookPath = lp
	return c
}

// Name returns the canonical provider name.
func (c *ClaudeProvider) Name() string {
	return "claude"
}

// Binary returns the resolved path or executable name.
func (c *ClaudeProvider) Binary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.binary != "" {
		return c.binary
	}
	return "claude"
}

// Available checks whether claude is resolvable in PATH or via CLAUDE_BIN and verifies --version.
func (c *ClaudeProvider) Available(ctx context.Context) error {
	bin := os.Getenv("CLAUDE_BIN")
	if bin == "" {
		p, err := c.lookPath("claude")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude --version: %w", err)
	}

	c.mu.Lock()
	c.binary = bin
	c.version = strings.TrimSpace(string(out))
	c.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (c *ClaudeProvider) Version(ctx context.Context) (string, error) {
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

// Spawn launches a claude subprocess with the requested spec.
func (c *ClaudeProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := c.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	c.mu.RLock()
	bin := c.binary
	c.mu.RUnlock()

	var args []string
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
		return nil, fmt.Errorf("failed to start claude process: %w", err)
	}

	return newProcessHandle("claude", cmd, start, &stdoutBuf, &stderrBuf), nil
}
