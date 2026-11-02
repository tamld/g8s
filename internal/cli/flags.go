package cli

import (
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// AddCommonFlags adds standard --actor, --trace-id, --jsonl, and --json (default true) flags to fs.
func AddCommonFlags(fs *flag.FlagSet) (actor *string, traceID *string, jsonl *bool, jsonMode *bool) {
	return AddCommonFlagsWithDefaults(fs, true)
}

// AddCommonFlagsWithDefaults adds standard flags with a configurable default for --json.
func AddCommonFlagsWithDefaults(fs *flag.FlagSet, defaultJSON bool) (actor *string, traceID *string, jsonl *bool, jsonMode *bool) {
	actor = fs.String("actor", "operator", "actor identity performing the operation")
	traceID = fs.String("trace-id", GenerateTraceID(), "correlation trace ID (UUID v7)")
	jsonl = fs.Bool("jsonl", false, "emit output as single-line JSONL envelope")
	jsonMode = fs.Bool("json", defaultJSON, "emit machine-readable JSON envelope")
	return actor, traceID, jsonl, jsonMode
}

// ParseTimeoutDuration parses a raw timeout string into a time.Duration and its
// effective duration string representation (#721). It accepts standard Go duration
// formats (e.g. "900s", "15m") as well as bare positive integers representing
// seconds. Returns an error if the value is empty, non-positive, or unparseable.
func ParseTimeoutDuration(raw string) (time.Duration, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, "", fmt.Errorf("timeout cannot be empty: specify a positive duration such as '900s' or '15m' (bare integer is treated as seconds)")
	}
	// Try parsing standard Go duration first
	d, err := time.ParseDuration(trimmed)
	if err == nil {
		if d <= 0 {
			return 0, "", fmt.Errorf("invalid timeout %q: duration must be positive (e.g. '900s', '15m')", raw)
		}
		return d, trimmed, nil
	}
	// Fallback policy: bare positive integer = seconds
	if n, intErr := strconv.ParseInt(trimmed, 10, 64); intErr == nil {
		if n <= 0 {
			return 0, "", fmt.Errorf("invalid timeout %q: duration must be positive (e.g. '900s', '15m')", raw)
		}
		const maxBareSeconds = math.MaxInt64 / int64(time.Second)
		if n > maxBareSeconds {
			return 0, "", fmt.Errorf("invalid timeout %q: duration exceeds maximum representable seconds (max %ds)", raw, maxBareSeconds)
		}
		d := time.Duration(n) * time.Second
		return d, fmt.Sprintf("%ds", n), nil
	}
	return 0, "", fmt.Errorf("invalid timeout %q: must be a valid duration (e.g. '900s', '15m'; bare integer is treated as seconds)", raw)
}
