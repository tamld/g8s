package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// execResult captures one subprocess run.
type execResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
	TimedOut bool
	StartErr error
	PID      int
}

// runCmd executes bin with args in dir, stdin closed, inside its own
// process group; on timeout the whole group is SIGKILLed.
func runCmd(ctx context.Context, dir string, timeout time.Duration, bin string, args ...string) execResult {
	// #nosec G204 -- argv comes from compiled-in provider profiles, never task payloads.
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	configurePgid(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return execResult{ExitCode: -1, StartErr: err, Duration: time.Since(start)}
	}
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	res := execResult{PID: pid}
	var waitErr error
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		res.TimedOut = true
		if kErr := killGroup(pid); kErr != nil {
			if pErr := cmd.Process.Kill(); pErr != nil {
				fmt.Fprintf(os.Stderr, "[warn] conformance: kill pid %d: %v\n", pid, pErr)
			}
		}
		waitErr = <-done
	}
	res.Duration = time.Since(start)
	res.Stdout = stdout.Bytes()
	res.Stderr = stderr.Bytes()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
		}
	}
	if res.TimedOut {
		res.ExitCode = -1
	}
	return res
}

// renderArgv renders argv for evidence, truncating long arguments so
// prompts never dominate the record.
func renderArgv(bin string, args []string) string {
	parts := []string{filepath.Base(bin)}
	for _, a := range args {
		if len(a) > 72 {
			a = a[:69] + "..."
		}
		if strings.ContainsAny(a, " \t\"'{}") {
			a = fmt.Sprintf("%q", a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func evidenceFromExec(cmdline string, r execResult, version string) *Evidence {
	return &Evidence{
		Command:         cmdline,
		ExitCode:        r.ExitCode,
		StdoutSHA256:    sha256Hex(r.Stdout),
		DurationMs:      r.Duration.Milliseconds(),
		ProviderVersion: version,
		ObservedAt:      nowRFC3339(),
	}
}

// parseJSONStream parses stdout as a single JSON document or as NDJSON.
// It returns the decoded values and ok=false when any non-empty line fails.
func parseJSONStream(b []byte) ([]any, bool) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, false
	}
	var whole any
	if err := json.Unmarshal(trimmed, &whole); err == nil {
		return []any{whole}, true
	}
	var vals []any
	sc := bufio.NewScanner(bytes.NewReader(trimmed))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, false
		}
		vals = append(vals, v)
	}
	if err := sc.Err(); err != nil {
		return nil, false
	}
	return vals, len(vals) > 0
}

// walkJSON visits every value recursively; string values that themselves
// contain a JSON object/array are decoded and visited too.
func walkJSON(v any, path string, visit func(path string, v any) bool) bool {
	if visit(path, v) {
		return true
	}
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if walkJSON(child, path+"."+k, visit) {
				return true
			}
		}
	case []any:
		for i, child := range t {
			if walkJSON(child, fmt.Sprintf("%s[%d]", path, i), visit) {
				return true
			}
		}
	case string:
		s := strings.TrimSpace(t)
		if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
			var inner any
			if err := json.Unmarshal([]byte(s), &inner); err == nil {
				return walkJSON(inner, path+"<json>", visit)
			}
		}
	}
	return false
}

var usageKeys = map[string]bool{
	"input_tokens":      true,
	"output_tokens":     true,
	"total_tokens":      true,
	"prompt_tokens":     true,
	"completion_tokens": true,
	"inputtokens":       true,
	"outputtokens":      true,
	"totaltokens":       true,
}

// findUsage returns the JSON path of the first positive token counter.
func findUsage(vals []any) (string, bool) {
	var found string
	for _, v := range vals {
		hit := walkJSON(v, "$", func(path string, node any) bool {
			m, ok := node.(map[string]any)
			if !ok {
				return false
			}
			for k, child := range m {
				lk := strings.ToLower(k)
				if usageKeys[lk] && positiveNumber(child) {
					found = path + "." + k
					return true
				}
				if lk == "tokens" {
					if inner, ok := child.(map[string]any); ok {
						for ik, iv := range inner {
							lik := strings.ToLower(ik)
							if (lik == "input" || lik == "output" || lik == "total") && positiveNumber(iv) {
								found = path + ".tokens." + ik
								return true
							}
						}
					}
				}
			}
			return false
		})
		if hit {
			return found, true
		}
	}
	return "", false
}

func positiveNumber(v any) bool {
	f, ok := v.(float64)
	return ok && f > 0
}

// findSchemaAnswer reports whether any decoded value (or JSON embedded in a
// string, or the raw text itself) is an object with answer == "PONG".
func findSchemaAnswer(stdout []byte) bool {
	vals, ok := parseJSONStream(stdout)
	if !ok {
		// Fall back to the last {...} span in plain-text output.
		s := string(stdout)
		i := strings.LastIndex(s, "{")
		j := strings.LastIndex(s, "}")
		if i < 0 || j <= i {
			return false
		}
		var v any
		if err := json.Unmarshal([]byte(s[i:j+1]), &v); err != nil {
			return false
		}
		vals = []any{v}
	}
	for _, v := range vals {
		if walkJSON(v, "$", func(_ string, node any) bool {
			m, ok := node.(map[string]any)
			if !ok {
				return false
			}
			ans, ok := m["answer"].(string)
			return ok && strings.EqualFold(strings.TrimSpace(ans), "PONG")
		}) {
			return true
		}
	}
	return false
}

// probeSchemaJSON is the output schema used by P7 for both classes.
const probeSchemaJSON = `{"type":"object","properties":{"answer":{"type":"string","enum":["PONG"]}},"required":["answer"],"additionalProperties":false}`

// newScratchRepo creates a temporary git repository with one commit so
// worktree-diff verification (VerifyWorktreeDiffScope) has a clean base.
func newScratchRepo(ctx context.Context) (string, func(), error) {
	dir, err := os.MkdirTemp("", "g8s-conformance-")
	if err != nil {
		return "", func() {}, fmt.Errorf("mkdir scratch repo: %w", err)
	}
	cleanup := func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] conformance: remove scratch %s: %v\n", dir, rmErr)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("g8s conformance scratch\n"), 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("seed scratch repo: %w", err)
	}
	steps := [][]string{
		{"init", "-q"},
		{"add", "README.md"},
		{"-c", "user.email=conformance@g8s.invalid", "-c", "user.name=g8s-conformance", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed"},
	}
	for _, s := range steps {
		r := runCmd(ctx, dir, 30*time.Second, "git", s...)
		if r.StartErr != nil || r.ExitCode != 0 {
			cleanup()
			return "", func() {}, fmt.Errorf("git %s failed: exit=%d %s", strings.Join(s, " "), r.ExitCode, strings.TrimSpace(string(r.Stderr)))
		}
	}
	return dir, cleanup, nil
}
