package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Typed provider-boundary refusal codes surfaced on worker results (#825).
const (
	ErrCodeProviderQuotaExhausted   = "E_PROVIDER_QUOTA_EXHAUSTED"
	ErrCodeProviderAuth             = "E_PROVIDER_AUTH"
	ErrCodeProviderModelUnavailable = "E_PROVIDER_MODEL_UNAVAILABLE"
)

// ProviderRefusal is a typed failure decoded from provider CLI stderr.
type ProviderRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	RetryAt string `json:"retry_at,omitempty"`
}

// Reason returns the canonical reason string stored in result_json.reason.
func (r ProviderRefusal) Reason() string {
	if strings.TrimSpace(r.Message) == "" {
		return r.Code
	}
	out := fmt.Sprintf("%s: %s", r.Code, r.Message)
	if strings.TrimSpace(r.RetryAt) != "" {
		out += fmt.Sprintf("; retry_at=%s", strings.TrimSpace(r.RetryAt))
	}
	return out
}

var providerRetryAtPattern = regexp.MustCompile(`(?i)try\s+again\s+at\s+(.+?)(?:\.|\s*$)`)

func isCodexTurnContinuationMarker(rawLine string) bool {
	return rawLine == "--------" ||
		rawLine == "user" ||
		rawLine == "thinking" ||
		rawLine == "exec" ||
		rawLine == "codex" ||
		rawLine == "patch_apply" ||
		strings.HasPrefix(rawLine, "hook: ") ||
		strings.HasPrefix(rawLine, "mcp:") ||
		strings.HasPrefix(rawLine, "mcp startup:")
}

func isUsageLimitLine(rawLine string) bool {
	return strings.HasPrefix(rawLine, "You've hit your usage limit") ||
		strings.HasPrefix(rawLine, "You hit your usage limit")
}

func isTopLevelCodexRefusalLine(rawLine string) bool {
	return strings.HasPrefix(rawLine, "ERROR:") ||
		strings.HasPrefix(rawLine, "Error:") ||
		isUsageLimitLine(rawLine)
}

// sanitizeStderrForRefusal removes echoed user prompts, shell tool output blocks,
// echoed assistant responses (both with and without Codex hook markers), and
// unified git diff blocks from CLI stderr before refusal classification.
func sanitizeStderrForRefusal(stderr string) string {
	isCodexTranscript := strings.Contains(stderr, "OpenAI Codex") ||
		strings.Contains(stderr, "\n--------\nuser\n") ||
		strings.HasPrefix(stderr, "--------\nuser\n")

	const (
		secNone = iota
		secPromptOrTurn
		secToolOrAssistant
		secTokens
	)
	sec := secNone

	lines := strings.Split(stderr, "\n")
	hasLaterTurnMarker := make([]bool, len(lines))
	if isCodexTranscript {
		seenTurnMarker := false
		for i := len(lines) - 1; i >= 0; i-- {
			hasLaterTurnMarker[i] = seenTurnMarker
			if isCodexTurnContinuationMarker(lines[i]) {
				seenTurnMarker = true
			}
		}
	}

	var kept []string
	inGitDiff := false
	inDiffHunk := false
	for idx, rawLine := range lines {
		if isCodexTranscript {
			switch {
			case rawLine == "--------", strings.HasPrefix(rawLine, "hook: "):
				sec = secNone
				inGitDiff = false
				inDiffHunk = false
				continue
			case rawLine == "user", rawLine == "thinking":
				sec = secPromptOrTurn
				inGitDiff = false
				inDiffHunk = false
				continue
			case rawLine == "exec",
				rawLine == "codex",
				rawLine == "patch_apply",
				strings.HasPrefix(rawLine, "mcp:"),
				strings.HasPrefix(rawLine, "mcp startup:"):
				sec = secToolOrAssistant
				inGitDiff = false
				inDiffHunk = false
				continue
			case rawLine == "tokens used":
				sec = secTokens
				inGitDiff = false
				inDiffHunk = false
				continue
			}

			switch sec {
			case secPromptOrTurn:
				if !hasLaterTurnMarker[idx] && isTopLevelCodexRefusalLine(rawLine) {
					sec = secNone
				} else {
					continue
				}
			case secToolOrAssistant:
				// Echoed tool output (`exec`, `patch_apply`, `mcp:`) and echoed assistant
				// responses (`codex`) are stripped in full until the next explicit Codex
				// section, hook, or `tokens used` marker closes the block.
				continue
			case secTokens:
				if !hasLaterTurnMarker[idx] && isTopLevelCodexRefusalLine(rawLine) {
					sec = secNone
				} else {
					continue
				}
			}
		}

		if strings.HasPrefix(rawLine, "diff --git ") {
			inGitDiff = true
			inDiffHunk = false
			continue
		}
		if inGitDiff {
			if strings.HasPrefix(rawLine, "index ") ||
				strings.HasPrefix(rawLine, "--- ") ||
				strings.HasPrefix(rawLine, "+++ ") ||
				strings.HasPrefix(rawLine, "new file mode ") ||
				strings.HasPrefix(rawLine, "deleted file mode ") ||
				strings.HasPrefix(rawLine, "similarity index ") ||
				strings.HasPrefix(rawLine, "rename from ") ||
				strings.HasPrefix(rawLine, "rename to ") {
				inDiffHunk = false
				continue
			}
			if strings.HasPrefix(rawLine, "@@") {
				inDiffHunk = true
				continue
			}
			if inDiffHunk && (strings.HasPrefix(rawLine, " ") ||
				strings.HasPrefix(rawLine, "+") ||
				strings.HasPrefix(rawLine, "-") ||
				strings.HasPrefix(rawLine, `\`)) {
				continue
			}
			if !inDiffHunk && (strings.HasPrefix(rawLine, "+") || strings.HasPrefix(rawLine, "-")) {
				continue
			}
			inGitDiff = false
			inDiffHunk = false
		}
		kept = append(kept, rawLine)
	}
	return strings.Join(kept, "\n")
}

// ClassifyProviderStderrRefusal detects provider refusals printed to stderr while the
// CLI may still exit 0 with empty stdout (#825, #810).
func ClassifyProviderStderrRefusal(stderr string) (ProviderRefusal, bool) {
	cleaned := sanitizeStderrForRefusal(stderr)
	lower := strings.ToLower(strings.TrimSpace(cleaned))
	if lower == "" {
		return ProviderRefusal{}, false
	}

	if strings.Contains(lower, "you've hit your usage limit") || strings.Contains(lower, "you hit your usage limit") {
		return ProviderRefusal{
			Code:    ErrCodeProviderQuotaExhausted,
			Message: extractRefusalSummary(cleaned, "usage limit"),
			RetryAt: parseProviderRetryAt(cleaned, time.Now()),
		}, true
	}

	switch {
	case strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "insufficient credits"):
		return ProviderRefusal{
			Code:    ErrCodeProviderAuth,
			Message: extractRefusalSummary(cleaned, "auth"),
		}, true
	case strings.Contains(lower, "model not found"):
		return ProviderRefusal{
			Code:    ErrCodeProviderModelUnavailable,
			Message: extractRefusalSummary(cleaned, "model"),
		}, true
	case strings.Contains(lower, "does not exist") && strings.Contains(lower, "model"):
		return ProviderRefusal{
			Code:    ErrCodeProviderModelUnavailable,
			Message: extractRefusalSummary(cleaned, "model"),
		}, true
	default:
		return ProviderRefusal{}, false
	}
}

func extractRefusalSummary(stderr, kind string) string {
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		switch kind {
		case "usage limit":
			if strings.Contains(lower, "usage limit") {
				return trimmed
			}
		case "auth":
			if strings.Contains(lower, "invalid api key") ||
				strings.Contains(lower, "unauthorized") ||
				strings.Contains(lower, "insufficient credits") {
				return trimmed
			}
		case "model":
			if strings.Contains(lower, "model not found") ||
				(strings.Contains(lower, "does not exist") && strings.Contains(lower, "model")) {
				return trimmed
			}
		}
	}
	return strings.TrimSpace(firstNonEmptyLine(stderr))
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(text)
}

func parseProviderRetryAt(stderr string, now time.Time) string {
	m := providerRetryAtPattern.FindStringSubmatch(stderr)
	if len(m) < 2 {
		return ""
	}
	raw := strings.TrimSpace(m[1])
	if raw == "" {
		return ""
	}
	loc := now.Location()
	layouts := []string{"3:04 PM", "3:04PM", "15:04", "15:04:05"}
	for _, layout := range layouts {
		if ts, err := time.ParseInLocation(layout, raw, loc); err == nil {
			combined := time.Date(now.Year(), now.Month(), now.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, loc)
			if combined.Before(now) {
				combined = combined.Add(24 * time.Hour)
			}
			return combined.Format(time.RFC3339)
		}
	}
	return raw
}

// codexFinalizeReceipt applies typed refusal classification for codex subprocess receipts.
func codexFinalizeReceipt(exitCode int, status string, waitErr error, stderr string) (int, string, error) {
	refusal, ok := ClassifyProviderStderrRefusal(stderr)
	if !ok {
		return exitCode, status, waitErr
	}
	if exitCode == 0 && waitErr == nil {
		exitCode = 1
		status = "FAILED"
	}
	return exitCode, status, fmt.Errorf("%s", refusal.Reason())
}

// CodexProvider manages OpenAI Codex CLI worker execution.
type CodexProvider struct {
	mu       sync.RWMutex
	binary   string
	version  string
	lookPath func(string) (string, error)
}

// NewCodexProvider creates a new CodexProvider instance.
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
			return codedError(codeBinaryNotFound, "not found in PATH")
		}
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return codedError(codeProbeFailed, "codex --version: %w", err)
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

// Spawn launches a codex exec subprocess with the requested spec (DELTA-39).
func (c *CodexProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := c.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	c.mu.RLock()
	bin := c.binary
	c.mu.RUnlock()

	args := []string{"exec"}
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
		return nil, codedError(codeSpawnFailed, "failed to start codex process: %w", err)
	}

	return newProcessHandle("codex", cmd, start, &stdoutBuf, &stderrBuf), nil
}
