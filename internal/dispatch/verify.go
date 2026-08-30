package dispatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/tamld/g8s/internal/cli"
)

var verifyFencedJSONPattern = regexp.MustCompile(`(?s)(?:` + "```" + `|` + "`" + `)(?:json)?\s*(\{.*?\})\s*(?:` + "```" + `|` + "`" + `)`)

// WorkerEnvelopeError represents a structured error extracted from worker stdout.
type WorkerEnvelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Cause   string `json:"cause,omitempty"`
}

func (e *WorkerEnvelopeError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return e.Message
}

// ParseWorkerEnvelope inspects stdout bytes. If the output contains a JSON envelope with
// kind="error", it returns a *WorkerEnvelopeError.
// Otherwise, it returns nil.
func ParseWorkerEnvelope(stdout []byte) error {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil
	}

	// 1. Try full envelope unmarshal
	var env cli.Envelope
	if err := json.Unmarshal(trimmed, &env); err == nil {
		if env.Kind == "error" {
			return extractEnvelopeError(&env)
		}
	}

	// 2. Try line-by-line inspection (JSONL or mixed output)
	lines := bytes.Split(trimmed, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' || line[len(line)-1] != '}' {
			continue
		}
		var lEnv cli.Envelope
		if err := json.Unmarshal(line, &lEnv); err == nil && lEnv.Kind == "error" {
			return extractEnvelopeError(&lEnv)
		}
	}

	// 3. Try fenced JSON extraction (```json ... ```)
	for _, match := range verifyFencedJSONPattern.FindAllSubmatch(trimmed, -1) {
		if len(match) > 1 {
			var fEnv cli.Envelope
			if err := json.Unmarshal(bytes.TrimSpace(match[1]), &fEnv); err == nil && fEnv.Kind == "error" {
				return extractEnvelopeError(&fEnv)
			}
		}
	}

	return nil
}

func extractEnvelopeError(env *cli.Envelope) *WorkerEnvelopeError {
	code := cli.CodeRuntime
	msg := "worker returned error envelope"
	var hint, cause string
	if env.Error != nil {
		if env.Error.Code != "" {
			code = env.Error.Code
		}
		if env.Error.Message != "" {
			msg = env.Error.Message
		}
		hint = env.Error.Hint
		cause = env.Error.Cause
	}
	return &WorkerEnvelopeError{
		Code:    code,
		Message: msg,
		Hint:    hint,
		Cause:   cause,
	}
}
