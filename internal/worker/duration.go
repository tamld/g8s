package worker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// compoundPattern matches optional hour/minute/second components such as
// "2h", "1m2s", or "90s". At least one component must be present.
var compoundPattern = regexp.MustCompile(`^(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?((?:\d+(?:\.\d+)?)s)?$`)

// ParseDurationSeconds converts a worker timeout expression into seconds.
// Accepted forms: "<n>ms" milliseconds, and compound "NhNmNs" with at least
// one component. Zero, negative, empty, and malformed values are rejected so
// a task can never carry an unbounded execution window.
func ParseDurationSeconds(value string) (float64, error) {
	if strings.HasSuffix(value, "ms") {
		millis, err := strconv.ParseFloat(strings.TrimSuffix(value, "ms"), 64)
		if err != nil || millis <= 0 {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		return millis / 1000, nil
	}
	match := compoundPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	hours, minutes, seconds := match[1], match[2], match[3]
	if hours == "" && minutes == "" && seconds == "" {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	total := 0.0
	for _, part := range []struct {
		raw    string
		factor float64
	}{
		{hours, 3600},
		{minutes, 60},
		{strings.TrimSuffix(seconds, "s"), 1},
	} {
		if part.raw == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(part.raw, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		total += parsed * part.factor
	}
	if total <= 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	return total, nil
}
