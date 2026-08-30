package cli

import (
	"flag"
	"io"
)

// AddCommonFlags adds standard --actor, --trace-id, --jsonl, and --json (default true) flags to fs.
func AddCommonFlags(fs *flag.FlagSet) (actor *string, traceID *string, jsonl *bool, jsonMode *bool) {
	return AddCommonFlagsWithDefaults(fs, true)
}

// AddCommonFlagsWithDefaults adds standard flags with a configurable default for --json.
func AddCommonFlagsWithDefaults(fs *flag.FlagSet, defaultJSON bool) (actor *string, traceID *string, jsonl *bool, jsonMode *bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	actor = fs.String("actor", "operator", "actor identity performing the operation")
	traceID = fs.String("trace-id", GenerateTraceID(), "correlation trace ID (UUID v7)")
	jsonl = fs.Bool("jsonl", false, "emit output as single-line JSONL envelope")
	jsonMode = fs.Bool("json", defaultJSON, "emit machine-readable JSON envelope")
	return actor, traceID, jsonl, jsonMode
}
