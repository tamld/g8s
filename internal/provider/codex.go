package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// CodexProvider is a stub provider for OpenAI Codex CLI workers.
type CodexProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewCodexProvider creates a new CodexProvider stub instance.
func NewCodexProvider() *CodexProvider {
	return &CodexProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (c *CodexProvider) WithLookPath(lp func(string) (string, error)) *CodexProvider {
	c.lookPath = lp
	return c
}

// Name returns the canonical provider name.
func (c *CodexProvider) Name() string {
	return "codex"
}

// Binary returns the resolved path or executable name.
func (c *CodexProvider) Binary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.binary != "" {
		return c.binary
	}
	return "codex"
}

// Available checks whether codex is resolvable in PATH or via CODEX_BIN.
func (c *CodexProvider) Available(ctx context.Context) error {
	bin := os.Getenv("CODEX_BIN")
	if bin == "" {
		p, err := c.lookPath("codex")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex --version: %w", err)
	}

	c.mu.Lock()
	c.binary = bin
	c.version = strings.TrimSpace(string(out))
	c.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (c *CodexProvider) Version(ctx context.Context) (string, error) {
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

// Spawn is a stub for future Codex implementation.
func (c *CodexProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := c.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return nil, fmt.Errorf("codex provider spawn not implemented")
}
