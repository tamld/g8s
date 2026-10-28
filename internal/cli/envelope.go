// Package cli defines the unified JSON envelope and standard flag parsing
// conventions for the g8s command-line interface per DEBT-30.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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
	CodeConflict = "E_CONFLICT"
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

// ErrPayload carries structured machine-readable error details (#687).
type ErrPayload struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Cause   string            `json:"cause,omitempty"`
	Context map[string]string `json:"context,omitempty"`
}

// ActiveRuntimeContext captures active containment/delegation environment variables
// so F0 supervisors and F1 workers immediately see scope boundaries on error (#687).
func ActiveRuntimeContext() map[string]string {
	ctx := make(map[string]string)
	if v := strings.TrimSpace(os.Getenv("G8S_TIER")); v != "" {
		ctx["tier"] = v
	}
	if v := strings.TrimSpace(os.Getenv("G8S_STRATUM")); v != "" {
		ctx["stratum"] = v
	}
	if v := strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT")); v != "" {
		ctx["scope_root"] = v
	}
	if v := strings.TrimSpace(os.Getenv("G8S_RECEIPT_ID")); v != "" {
		ctx["receipt_id"] = v
	}
	if len(ctx) == 0 {
		return nil
	}
	return ctx
}

// DefaultHintForCode supplies a deterministic, actionable remediation hint when a
// caller omits an explicit hint (#687).
func DefaultHintForCode(code, cmd, sub string) string {
	target := strings.TrimSpace(cmd)
	if sub != "" {
		target += " " + strings.TrimSpace(sub)
	}
	switch code {
	case CodeUsage:
		if target != "" {
			return fmt.Sprintf("Run 'g8s %s --help' to inspect valid flags and arguments", target)
		}
		return "Run 'g8s --help' to inspect available subcommands"
	case CodeNotFound:
		return "Verify the target ID or file path exists in the active workspace or control-plane state"
	case CodeDenied:
		return "Check G8S_TIER, G8S_SCOPE_ROOT, and your WriteReceipt allowed_paths before retrying"
	case CodeConflict:
		return "Re-read the target file ('g8s read <path>') to refresh its SHA-256 and current line contents"
	case CodeTimeout:
		return "Increase --timeout or narrow the query/test scope"
	case CodeIO:
		return "Verify file permissions, disk space, and G8S_STATE_DIR accessibility"
	default:
		if target != "" {
			return fmt.Sprintf("Run 'g8s doctor --json' or 'g8s %s --help' to diagnose runtime state", target)
		}
		return "Run 'g8s doctor --json' to inspect environment health"
	}
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
	if strings.TrimSpace(hint) == "" {
		hint = DefaultHintForCode(code, cmd, sub)
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
			Context: ActiveRuntimeContext(),
		},
		At: time.Now().UTC(),
	}
}

// WriteJSON serializes an Envelope as formatted multi-line JSON with 2-space indentation.
func WriteJSON(w io.Writer, env Envelope) error {
	if sel := DefaultSelector(); sel != "" && env.Kind != "error" {
		raw, err := json.Marshal(env)
		if err == nil {
			if selected, selErr := SelectPath(raw, sel); selErr == nil {
				_, writeErr := fmt.Fprintln(w, selected)
				return writeErr
			}
		}
	}
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
	if sel := DefaultSelector(); sel != "" && env.Kind != "error" {
		raw, err := json.Marshal(env)
		if err == nil {
			if selected, selErr := SelectPath(raw, sel); selErr == nil {
				_, writeErr := fmt.Fprintln(w, selected)
				return writeErr
			}
		}
	}
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
