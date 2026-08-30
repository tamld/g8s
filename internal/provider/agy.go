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

// AgyProvider manages Antigravity (agy) CLI worker execution.
type AgyProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewAgyProvider creates a new AgyProvider instance.
func NewAgyProvider() *AgyProvider {
	return &AgyProvider{
		lookPath: exec.LookPath,
	}
}

// WithLookPath overrides the binary lookup function for testing.
func (a *AgyProvider) WithLookPath(lp func(string) (string, error)) *AgyProvider {
	a.lookPath = lp
	return a
}

// Name returns the canonical provider name.
func (a *AgyProvider) Name() string {
	return "agy"
}

// Binary returns the resolved path or executable name.
func (a *AgyProvider) Binary() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.binary != "" {
		return a.binary
	}
	return "agy"
}

// Available checks whether agy is resolvable in PATH or via AGY_BIN and verifies --version.
func (a *AgyProvider) Available(ctx context.Context) error {
	bin := os.Getenv("AGY_BIN")
	if bin == "" {
		p, err := a.lookPath("agy")
		if err != nil {
			return fmt.Errorf("not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("agy --version: %w", err)
	}

	a.mu.Lock()
	a.binary = bin
	a.version = strings.TrimSpace(string(out))
	a.mu.Unlock()

	return nil
}

// Version returns the discovered version string.
func (a *AgyProvider) Version(ctx context.Context) (string, error) {
	a.mu.RLock()
	v := a.version
	a.mu.RUnlock()
	if v != "" {
		return v, nil
	}
	if err := a.Available(ctx); err != nil {
		return "", err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.version, nil
}

// Spawn launches an agy subprocess with the requested spec.
func (a *AgyProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := a.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	a.mu.RLock()
	bin := a.binary
	a.mu.RUnlock()

	var args []string
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Brief != "" {
		args = append(args, "--prompt", spec.Brief)
	}
	for _, dir := range spec.AddDirs {
		args = append(args, "--add-dir", dir)
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
		return nil, fmt.Errorf("failed to start agy process: %w", err)
	}

	return newProcessHandle("agy", cmd, start, &stdoutBuf, &stderrBuf), nil
}
