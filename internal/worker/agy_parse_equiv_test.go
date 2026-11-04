package worker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// legacyAgyStreamErrorTail is the pre-optimization Split-based implementation kept for equivalence checks.
func legacyAgyStreamErrorTail(stdoutText string) bool {
	lastWasError := false
	for _, line := range strings.Split(stdoutText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Event string `json:"event"`
			Step  *struct {
				StepType string `json:"step_type"`
			} `json:"step_update"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		switch probe.Event {
		case "result":
			lastWasError = false
		case "step_update":
			if probe.Step != nil && probe.Step.StepType != "" {
				lastWasError = probe.Step.StepType == "error_message"
			}
		}
	}
	return lastWasError
}

// legacyParseAGYResult is the pre-optimization Split-based implementation kept for equivalence checks.
func legacyParseAGYResult(stdoutText string) *agyResult {
	lines := strings.Split(stdoutText, "\n")
	var lastResult *agyResult
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ar agyResult
		if json.Unmarshal([]byte(line), &ar) == nil && ar.Result.Status != "" {
			lastResult = &ar
		}
	}
	return lastResult
}

// legacyExtractAGYUsage is the pre-optimization Split-based implementation kept for equivalence checks.
func legacyExtractAGYUsage(stdoutText string) *agyUsage {
	lines := strings.Split(stdoutText, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ar agyResult
		if json.Unmarshal([]byte(line), &ar) == nil && ar.Result.Usage != nil {
			return ar.Result.Usage
		}
		var probe struct {
			Usage *agyUsage `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Usage != nil {
			return probe.Usage
		}
	}

	var sum agyUsage
	foundStepUsage := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Step *struct {
				Usage *agyUsage `json:"usage"`
			} `json:"step_update"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Step != nil && probe.Step.Usage != nil {
			u := probe.Step.Usage
			sum.InputTokens += u.InputTokens
			sum.OutputTokens += u.OutputTokens
			sum.ThinkingTokens += u.ThinkingTokens
			sum.CacheReadTokens += u.CacheReadTokens
			sum.TotalTokens += u.TotalTokens
			foundStepUsage = true
		}
	}
	if foundStepUsage {
		return &sum
	}
	return nil
}

func agyResultsEqual(a, b *agyResult) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(*a, *b)
}

func agyUsageEqual(a, b *agyUsage) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(*a, *b)
}

func buildLargeAGYStream(lineCount int) string {
	var b strings.Builder
	for i := 0; i < lineCount-1; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	b.WriteString(`{"event":"result","result":{"status":"SUCCESS","response":"done"}}`)
	return b.String()
}

func TestParseAGYResultMatchesLegacy(t *testing.T) {
	first := `{"event":"result","result":{"status":"SUCCESS","response":"first"}}`
	second := `{"event":"result","result":{"status":"SUCCESS","response":"second"}}`

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "single line", in: first},
		{name: "trailing newline", in: first + "\n"},
		{name: "crlf", in: strings.ReplaceAll(first+"\n"+`noise`, "\n", "\r\n")},
		{name: "multiple results last wins", in: first + "\n" + second},
		{name: "non json noise", in: "not json\n" + first + "\nstill not json"},
		{name: "large 10k lines", in: buildLargeAGYStream(10_000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := legacyParseAGYResult(tc.in)
			got := parseAGYResult(tc.in)
			if !agyResultsEqual(legacy, got) {
				t.Fatalf("parseAGYResult mismatch: legacy=%+v got=%+v", legacy, got)
			}
		})
	}
}

func TestExtractAGYUsageMatchesLegacy(t *testing.T) {
	step1 := `{"event":"step_update","step_update":{"step_index":1,"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}}`
	step2 := `{"event":"step_update","step_update":{"step_index":2,"usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22}}}`
	terminal := `{"event":"result","result":{"status":"SUCCESS","usage":{"input_tokens":30,"output_tokens":3,"total_tokens":33}}}`

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "single step_update", in: step1},
		{name: "trailing newline", in: step1 + "\n"},
		{name: "crlf steps", in: strings.ReplaceAll(step1+"\n"+step2, "\n", "\r\n")},
		{name: "terminal result wins over steps", in: step1 + "\n" + step2 + "\n" + terminal},
		{name: "two terminal usages last wins", in: terminal + "\n" + `{"event":"result","result":{"status":"SUCCESS","usage":{"input_tokens":99,"output_tokens":9,"total_tokens":108}}}`},
		{name: "non json noise", in: "noise\n" + step1 + "\n"},
		{name: "large 10k lines", in: buildLargeStepStream(10_000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := legacyExtractAGYUsage(tc.in)
			got := extractAGYUsage(tc.in)
			if !agyUsageEqual(legacy, got) {
				t.Fatalf("extractAGYUsage mismatch: legacy=%+v got=%+v", legacy, got)
			}
		})
	}
}

func TestAgyStreamErrorTailMatchesLegacy(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "clean steps", in: `{"event":"step_update","step_update":{"step_type":"tool"}}` + "\n"},
		{name: "error tail", in: `{"event":"step_update","step_update":{"step_type":"error_message"}}` + "\n"},
		{name: "recovered", in: `{"event":"step_update","step_update":{"step_type":"error_message"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS"}}` + "\n"},
		{name: "large 10k lines", in: buildLargeAGYStream(10_000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := legacyAgyStreamErrorTail(tc.in)
			got := agyStreamErrorTail(tc.in)
			if legacy != got {
				t.Fatalf("agyStreamErrorTail mismatch: legacy=%v got=%v", legacy, got)
			}
		})
	}
}

func buildLargeStepStream(lineCount int) string {
	var b strings.Builder
	for i := 0; i < lineCount-1; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	b.WriteString(`{"event":"step_update","step_update":{"step_index":1,"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`)
	return b.String()
}

func buildLargeTerminalUsageStream(lineCount int) string {
	var b strings.Builder
	for i := 0; i < lineCount-1; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	b.WriteString(`{"event":"result","result":{"status":"SUCCESS","usage":{"input_tokens":30,"output_tokens":3,"total_tokens":33}}}`)
	return b.String()
}

const largeStreamTargetBytes = 10 * 1024 * 1024

func buildLargeStepStreamBytes(targetBytes int) string {
	var b strings.Builder
	i := 0
	for b.Len() < targetBytes-128 {
		fmt.Fprintf(&b, "log line %d\n", i)
		i++
	}
	b.WriteString(`{"event":"step_update","step_update":{"step_index":1,"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`)
	return b.String()
}

func buildLargeTerminalUsageStreamBytes(targetBytes int) string {
	var b strings.Builder
	i := 0
	for b.Len() < targetBytes-256 {
		fmt.Fprintf(&b, "log line %d\n", i)
		i++
	}
	b.WriteString(`{"event":"result","result":{"status":"SUCCESS","usage":{"input_tokens":30,"output_tokens":3,"total_tokens":33}}}`)
	return b.String()
}

func BenchmarkParseAGYResultLegacy(b *testing.B) {
	in := buildLargeAGYStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		legacyParseAGYResult(in)
	}
}

func BenchmarkParseAGYResult(b *testing.B) {
	in := buildLargeAGYStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		parseAGYResult(in)
	}
}

func BenchmarkExtractAGYUsageLegacy(b *testing.B) {
	in := buildLargeStepStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		legacyExtractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsage(b *testing.B) {
	in := buildLargeStepStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		extractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsageTerminal(b *testing.B) {
	in := buildLargeTerminalUsageStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		extractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsageLegacyTerminal(b *testing.B) {
	in := buildLargeTerminalUsageStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		legacyExtractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsage10MB(b *testing.B) {
	if testing.Short() {
		b.Skip("large fixture")
	}
	in := buildLargeStepStreamBytes(largeStreamTargetBytes)
	b.ReportAllocs()
	for b.Loop() {
		extractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsageLegacy10MB(b *testing.B) {
	if testing.Short() {
		b.Skip("large fixture")
	}
	in := buildLargeStepStreamBytes(largeStreamTargetBytes)
	b.ReportAllocs()
	for b.Loop() {
		legacyExtractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsageTerminal10MB(b *testing.B) {
	if testing.Short() {
		b.Skip("large fixture")
	}
	in := buildLargeTerminalUsageStreamBytes(largeStreamTargetBytes)
	b.ReportAllocs()
	for b.Loop() {
		extractAGYUsage(in)
	}
}

func BenchmarkExtractAGYUsageLegacyTerminal10MB(b *testing.B) {
	if testing.Short() {
		b.Skip("large fixture")
	}
	in := buildLargeTerminalUsageStreamBytes(largeStreamTargetBytes)
	b.ReportAllocs()
	for b.Loop() {
		legacyExtractAGYUsage(in)
	}
}

// Micro-benchmark: full forward IndexByte line walk vs full backward LastIndexByte walk.
func BenchmarkAGYLineWalkForwardIndexByte(b *testing.B) {
	in := buildLargeStepStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		rem := in
		for len(rem) > 0 {
			idx := strings.IndexByte(rem, '\n')
			if idx >= 0 {
				rem = rem[idx+1:]
			} else {
				break
			}
		}
	}
}

func BenchmarkAGYLineWalkBackwardLastIndexByte(b *testing.B) {
	in := buildLargeStepStream(10_000)
	b.ReportAllocs()
	for b.Loop() {
		rem := in
		for len(rem) > 0 {
			idx := strings.LastIndexByte(rem, '\n')
			if idx >= 0 {
				rem = rem[:idx]
			} else {
				break
			}
		}
	}
}
