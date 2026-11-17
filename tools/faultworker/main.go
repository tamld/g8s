// Command faultworker is a deterministic, zero-CGO fault-injecting worker test
// fixture used by cross-platform CI and E2E suites without external LLM keys.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const specMarker = "FAULTWORKER_SPEC:"

type faultSpec struct {
	Mode      string `json:"mode"`
	Target    string `json:"target"`
	Content   string `json:"content"`
	Message   string `json:"message"`
	NotifyURL string `json:"notify_url"`
	Sleep     string `json:"sleep"`
}

type notifyRequest struct {
	PID           int    `json:"pid"`
	GrandchildPID int    `json:"grandchild_pid,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	Mode          string `json:"mode"`
	Ready         bool   `json:"ready,omitempty"`
}

type notifyResponse struct {
	Action       string `json:"action"`
	DirtyTarget  string `json:"dirty_target,omitempty"`
	DirtyContent string `json:"dirty_content,omitempty"`
	Target       string `json:"target,omitempty"`
	Content      string `json:"content,omitempty"`
	Sleep        string `json:"sleep,omitempty"`
}

var (
	executablePathFunc = os.Executable
	spawnChildFunc     = spawnDetachedSleepChild
	httpClient         = &http.Client{Timeout: 10 * time.Second}
	sleepFunc          = time.Sleep
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("faultworker", flag.ContinueOnError)
	fs.SetOutput(stderr)

	sleepChild := fs.String("sleep-child", "", "run as detached sleep child for duration and exit")
	modeFlag := fs.String("mode", "", "execution mode")
	targetFlag := fs.String("target", "", "relative target file path")
	contentFlag := fs.String("content", "", "content to write for clean-write mode")
	messageFlag := fs.String("message", "", "error message for stdout-error-rc0 mode")
	notifyURLFlag := fs.String("notify-url", "", "control/notification callback URL")
	sleepFlag := fs.String("sleep", "", "sleep duration for hang modes")
	promptFlag := fs.String("prompt", "", "raw or harness-wrapped FAULTWORKER_SPEC JSON prompt")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*sleepChild) != "" {
		dur, err := time.ParseDuration(strings.TrimSpace(*sleepChild))
		if err != nil || dur <= 0 {
			dur = 5 * time.Second
		}
		sleepFunc(dur)
		return 0
	}

	var spec faultSpec
	if strings.TrimSpace(*promptFlag) != "" {
		parsed, err := parsePromptSpec(*promptFlag)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "faultworker: parse prompt: %v\n", err)
			return 2
		}
		spec = parsed
	}

	if *modeFlag != "" {
		spec.Mode = *modeFlag
	}
	if *targetFlag != "" {
		spec.Target = *targetFlag
	}
	if *contentFlag != "" {
		spec.Content = *contentFlag
	}
	if *messageFlag != "" {
		spec.Message = *messageFlag
	}
	if *notifyURLFlag != "" {
		spec.NotifyURL = *notifyURLFlag
	}
	if *sleepFlag != "" {
		spec.Sleep = *sleepFlag
	}

	switch strings.TrimSpace(spec.Mode) {
	case "clean-write":
		return runCleanWrite(spec.Target, spec.Content, stdout, stderr)
	case "stdout-error-rc0":
		return runStdoutErrorRC0(spec.Message, stdout)
	case "hang-with-grandchild":
		return runHangWithGrandchild(spec, stderr)
	case "notify-controlled":
		return runNotifyControlled(spec, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "faultworker: unsupported mode %q\n", spec.Mode)
		return 2
	}
}

func parsePromptSpec(prompt string) (faultSpec, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return faultSpec{}, fmt.Errorf("empty prompt")
	}

	var candidates []string
	if idx := strings.LastIndex(trimmed, specMarker); idx >= 0 {
		tail := strings.TrimSpace(trimmed[idx+len(specMarker):])
		if brace := strings.IndexByte(tail, '{'); brace >= 0 {
			candidates = append(candidates, tail[brace:])
		}
	}
	if strings.HasPrefix(trimmed, "{") {
		candidates = append(candidates, trimmed)
	}
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] == '{' {
			candidates = append(candidates, trimmed[i:])
		}
	}

	var lastErr error
	for _, cand := range candidates {
		var spec faultSpec
		dec := json.NewDecoder(strings.NewReader(cand))
		if err := dec.Decode(&spec); err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(spec.Mode) != "" {
			return spec, nil
		}
	}
	if lastErr != nil {
		return faultSpec{}, lastErr
	}
	return faultSpec{}, fmt.Errorf("no valid FAULTWORKER_SPEC JSON with mode found")
}

func runCleanWrite(target, content string, stdout, stderr io.Writer) int {
	if err := writeRelativeFile(target, content); err != nil {
		_, _ = fmt.Fprintf(stderr, "faultworker: clean-write %q: %v\n", target, err)
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"ok":      true,
		"status":  "completed",
		"summary": "clean write to " + target,
	})
	return 0
}

func runStdoutErrorRC0(message string, stdout io.Writer) int {
	if strings.TrimSpace(message) == "" {
		message = "simulated upstream provider failure with exit code 0"
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"v":    1,
		"kind": "error",
		"cmd":  "faultworker",
		"error": map[string]any{
			"code":    "E_UPSTREAM_FAULT",
			"message": message,
		},
	})
	return 0
}

func runHangWithGrandchild(spec faultSpec, stderr io.Writer) int {
	dur := parseSleepDuration(spec.Sleep, 30*time.Second)
	gcPID, err := startGrandchild(dur)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "faultworker: spawn grandchild: %v\n", err)
		return 1
	}
	if strings.TrimSpace(spec.NotifyURL) != "" {
		cwd, _ := os.Getwd()
		_, _ = postNotify(spec.NotifyURL, notifyRequest{
			PID:           os.Getpid(),
			GrandchildPID: gcPID,
			CWD:           cwd,
			Mode:          "hang-with-grandchild",
		})
	}
	sleepFunc(dur)
	return 0
}

func runNotifyControlled(spec faultSpec, stdout, stderr io.Writer) int {
	if strings.TrimSpace(spec.NotifyURL) == "" {
		_, _ = fmt.Fprintln(stderr, "faultworker: notify-controlled requires notify_url")
		return 2
	}
	cwd, _ := os.Getwd()
	ctrl, err := postNotify(spec.NotifyURL, notifyRequest{
		PID:  os.Getpid(),
		CWD:  cwd,
		Mode: "notify-controlled",
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "faultworker: notify controller: %v\n", err)
		return 1
	}

	switch ctrl.Action {
	case "dirty_hang":
		if strings.TrimSpace(ctrl.DirtyTarget) != "" {
			if werr := writeRelativeFile(ctrl.DirtyTarget, ctrl.DirtyContent); werr != nil {
				_, _ = fmt.Fprintf(stderr, "faultworker: write dirty target: %v\n", werr)
				return 1
			}
		}
		dur := parseSleepDuration(ctrl.Sleep, parseSleepDuration(spec.Sleep, 30*time.Second))
		gcPID, serr := startGrandchild(dur)
		if serr != nil {
			_, _ = fmt.Fprintf(stderr, "faultworker: spawn dirty_hang grandchild: %v\n", serr)
			return 1
		}
		readyURL := appendQueryParam(spec.NotifyURL, "phase=ready")
		if _, rerr := postNotify(readyURL, notifyRequest{
			PID:           os.Getpid(),
			GrandchildPID: gcPID,
			CWD:           cwd,
			Mode:          "notify-controlled",
			Ready:         true,
		}); rerr != nil {
			_, _ = fmt.Fprintf(stderr, "faultworker: notify ready: %v\n", rerr)
			return 1
		}
		sleepFunc(dur)
		return 0
	case "clean_write":
		target := ctrl.Target
		if strings.TrimSpace(target) == "" {
			target = spec.Target
		}
		content := ctrl.Content
		if content == "" {
			content = spec.Content
		}
		return runCleanWrite(target, content, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "faultworker: unknown controller action %q\n", ctrl.Action)
		return 2
	}
}

func startGrandchild(d time.Duration) (int, error) {
	exe, err := executablePathFunc()
	if err != nil {
		return 0, err
	}
	return spawnChildFunc(exe, d)
}

func postNotify(targetURL string, payload notifyRequest) (notifyResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return notifyResponse{}, err
	}
	resp, err := httpClient.Post(targetURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return notifyResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return notifyResponse{}, fmt.Errorf("notify status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return notifyResponse{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return notifyResponse{}, nil
	}
	var out notifyResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return notifyResponse{}, err
	}
	return out, nil
}

func writeRelativeFile(relTarget, content string) error {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relTarget)))
	if clean == "." || clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return fmt.Errorf("invalid relative target path %q", relTarget)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dest := filepath.Join(cwd, clean)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	return os.WriteFile(dest, []byte(content), 0o600)
}

func parseSleepDuration(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func appendQueryParam(rawURL, kv string) string {
	if strings.Contains(rawURL, "?") {
		return rawURL + "&" + kv
	}
	return rawURL + "?" + kv
}
