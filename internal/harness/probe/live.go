package probe

// LiveWorkerProvider (#379): runs the probe suite against the real worker
// CLI (agy/claude) — read_only dispatch, bounded timeout, redacted capture.
// Live runs are operator-invoked (the eval CLI requires naming the provider
// explicitly); mocks stay the CI default.

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
)

// LiveWorkerProvider executes prompts through a real worker CLI binary.
type LiveWorkerProvider struct {
	binary  string
	model   string
	timeout time.Duration
}

// NewLiveWorkerProvider builds a live adapter. timeout bounds each probe
// execution; 0 means rely on the caller's context alone.
func NewLiveWorkerProvider(binary, model string, timeout time.Duration) *LiveWorkerProvider {
	return &LiveWorkerProvider{binary: binary, model: model, timeout: timeout}
}

func (p *LiveWorkerProvider) Name() string  { return p.binary }
func (p *LiveWorkerProvider) Model() string { return p.model }

// Execute runs one prompt with the given role/permission. Only read_only is
// accepted — live probe dispatch never writes (bounded blast radius).
func (p *LiveWorkerProvider) Execute(ctx context.Context, prompt, role, permission string, addDirs []string, receiptID string) (string, error) {
	if permission != "read_only" {
		return "", fmt.Errorf("live provider %s supports read_only probes only, got %q", p.binary, permission)
	}
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	argv, err := p.argv(prompt)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	for _, dir := range addDirs {
		cmd.Args = append(cmd.Args, "--add-dir", dir)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("live provider %s: %w", p.binary, err)
	}
	// Redacted capture: the response enters the PRI report — same central
	// sanitization as worker stdout.
	return dispatch.SanitizeOutput(string(out)), nil
}

// argv builds the per-binary invocation. agy's non-interactive mode is
// --print; claude's is -p. Both accept --model.
func (p *LiveWorkerProvider) argv(prompt string) ([]string, error) {
	if _, err := exec.LookPath(p.binary); err != nil {
		return nil, fmt.Errorf("live provider binary %q not on PATH: %w", p.binary, err)
	}
	switch p.binary {
	case "agy":
		return []string{"agy", "--print=" + prompt, "--model", p.model, "--sandbox"}, nil
	case "claude":
		return []string{"claude", "-p", prompt, "--model", p.model}, nil
	default:
		return nil, fmt.Errorf("unsupported live provider binary %q (supported: agy, claude)", p.binary)
	}
}
