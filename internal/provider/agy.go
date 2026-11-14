package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
			return codedError(codeBinaryNotFound, "not found in PATH")
		}
		bin = p
	}

	out, err := boundedProbe(ctx, bin, "--version")
	if err != nil {
		return codedError(codeProbeFailed, "agy --version: %w", err)
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

// DiscoverModels returns the list of available models from agy CLI.
func (a *AgyProvider) DiscoverModels(ctx context.Context) ([]string, error) {
	if err := a.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	a.mu.RLock()
	bin := a.binary
	a.mu.RUnlock()

	probeCtx, cancel := context.WithTimeout(ctx, cliProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, bin, "models").Output()
	if err != nil {
		return nil, codedError(codeProbeFailed, "agy models: %w", err)
	}

	seen := make(map[string]bool)
	var models []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "available") || strings.HasPrefix(lower, "usage") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		model := fields[0]
		modelLower := strings.ToLower(model)
		if modelLower != model {
			continue
		}
		if !strings.ContainsAny(modelLower, "abcdefghijklmnopqrstuvwxyz0123456789._/-") {
			continue
		}
		if len(model) > 0 && (model[0] < 'a' || model[0] > 'z') && (model[0] < '0' || model[0] > '9') {
			continue
		}
		if !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	return models, nil
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
	if spec.Effort != "" && !strings.EqualFold(spec.Effort, "none") {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.Env != nil {
		if v, ok := spec.Env["G8S_SANDBOX"]; ok && v == "1" {
			args = append(args, "--sandbox")
		}
	}

	cmd := exec.Command(bin, args...)
	if spec.WorktreeDir != "" {
		cmd.Dir = spec.WorktreeDir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append([]string{}, os.Environ()...)
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd.Env = append(cmd.Env, k+"="+spec.Env[k])
		}
	}
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, codedError(codeSpawnFailed, "failed to start agy process: %w", err)
	}

	return newProcessHandle("agy", cmd, start, &stdoutBuf, &stderrBuf), nil
}
