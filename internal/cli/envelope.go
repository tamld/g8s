// Package cli defines the unified JSON envelope and standard flag parsing
// conventions for the g8s command-line interface per DEBT-30.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// CurrentEnvelopeVersion defines the active schema version for g8s CLI envelopes.
const CurrentEnvelopeVersion = 1

// Standard error codes per DEBT-30.
const (
	CodeNotFound = "E_NOTFOUND"
	CodeTimeout  = "E_TIMEOUT"
	CodeDenied   = "E_DENIED"
	CodeInvalid  = "E_INVALID"
	CodeIO       = "E_IO"
	CodePanic    = "E_PANIC"
	CodeUsage    = "E_USAGE"
	CodeRuntime  = "E_RUNTIME"
	CodeHarness  = "E_HARNESS"
)

// Envelope wraps every g8s command's stdout JSON.
type Envelope struct {
	V          int         `json:"v"`
	Kind       string      `json:"kind"`
	Command    string      `json:"cmd"`
	Subcommand string      `json:"sub,omitempty"`
	Data       any         `json:"data"`
	Error      *ErrPayload `json:"error,omitempty"`
	TraceID    string      `json:"trace_id,omitempty"`
	At         time.Time   `json:"at"`
}

// ErrPayload carries structured machine-readable error details.
type ErrPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Cause   string `json:"cause,omitempty"`
}

// GenerateTraceID generates a new UUID v7 string, falling back to v4 if unsupported.
func GenerateTraceID() string {
	u, err := uuid.NewV7()
	if err == nil {
		return u.String()
	}
	return uuid.NewString()
}

// NewEnvelope constructs a success Envelope with version 1 and current UTC timestamp.
func NewEnvelope(kind, cmd, sub string, data any) Envelope {
	return Envelope{
		V:          CurrentEnvelopeVersion,
		Kind:       kind,
		Command:    cmd,
		Subcommand: sub,
		Data:       data,
		At:         time.Now().UTC(),
	}
}

// NewErrorEnvelope constructs an error Envelope with version 1 and current UTC timestamp.
func NewErrorEnvelope(cmd, sub, traceID, code, msg, hint, cause string) Envelope {
	if code == "" {
		code = CodeRuntime
	}
	return Envelope{
		V:          CurrentEnvelopeVersion,
		Kind:       "error",
		Command:    cmd,
		Subcommand: sub,
		TraceID:    traceID,
		Error: &ErrPayload{
			Code:    code,
			Message: msg,
			Hint:    hint,
			Cause:   cause,
		},
		At: time.Now().UTC(),
	}
}

// WriteJSON serializes an Envelope as formatted multi-line JSON with 2-space indentation.
func WriteJSON(w io.Writer, env Envelope) error {
	if env.V == 0 {
		env.V = CurrentEnvelopeVersion
	}
	if env.At.IsZero() {
		env.At = time.Now().UTC()
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}
// WriteJSONL serializes an Envelope as a single JSON line without line breaks.
func WriteJSONL(w io.Writer, env Envelope) error {
	if env.V == 0 {
		env.V = CurrentEnvelopeVersion
	}
	if env.At.IsZero() {
		env.At = time.Now().UTC()
	}
	out, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}

// WriteResponse serializes an Envelope to w, choosing WriteJSONL if jsonl is true or WriteJSON otherwise.
func WriteResponse(w io.Writer, env Envelope, jsonl bool) error {
	if jsonl {
		return WriteJSONL(w, env)
	}
	return WriteJSON(w, env)
}

// WriteError serializes a simple error Envelope to w in formatted JSON.
func WriteError(w io.Writer, code, msg, hint string) error {
	env := Envelope{
		V:    CurrentEnvelopeVersion,
		Kind: "error",
		Error: &ErrPayload{
			Code:    code,
			Message: msg,
			Hint:    hint,
		},
		At: time.Now().UTC(),
	}
	return WriteJSON(w, env)
}

// WriteErrorL serializes a simple error Envelope to w in JSON or JSONL format.
func WriteErrorL(w io.Writer, code, msg, hint string, jsonl bool) error {
	env := Envelope{
		V:    CurrentEnvelopeVersion,
		Kind: "error",
		Error: &ErrPayload{
			Code:    code,
			Message: msg,
			Hint:    hint,
		},
		At: time.Now().UTC(),
	}
	return WriteResponse(w, env, jsonl)
}
