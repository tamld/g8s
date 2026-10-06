package ladder

import (
	"fmt"
	"regexp"
	"strings"
)

// FailureShape denotes the root category of a task failure.
type FailureShape string

const (
	// ShapeEffort represents a failure where the task ran but failed its class checks.
	// This is the quality ladder's trigger by construction.
	ShapeEffort FailureShape = "effort-shaped"

	// ShapeBrief represents a failure caused by contract violations, harness/scope rejections,
	// missing inputs, or prompt/validation errors. Routes to HITL immediately.
	ShapeBrief FailureShape = "brief-shaped"

	// ShapeEnv represents a failure caused by transport/spawn failures, timeouts,
	// or binary/CLI errors (ADR-0029 signature classes). Routes to HITL immediately.
	ShapeEnv FailureShape = "env-shaped"
)

// IsEffortShaped reports whether the shape is effort-shaped.
func (s FailureShape) IsEffortShaped() bool {
	return s == ShapeEffort
}

// FailureEvidence gathers observable symptoms and error metadata for classification.
type FailureEvidence struct {
	ErrorText         string   `json:"error_text,omitempty"`
	ExitCode          *int     `json:"exit_code,omitempty"`
	ContractViolation string   `json:"contract_violation,omitempty"`
	ChecksFailed      []string `json:"checks_failed,omitempty"`
	Model             string   `json:"model,omitempty"`
	Effort            string   `json:"effort,omitempty"`
	ProviderResponse  string   `json:"provider_response,omitempty"`
}

// ClassifierVerdict is the output of the failure-shape classifier.
type ClassifierVerdict struct {
	Shape    FailureShape `json:"shape"`
	Reason   string       `json:"reason"`
	StaleTag string       `json:"stale_tag,omitempty"` // e.g. "catalog-stale(model, level)"
}

var (
	// env-shaped markers (ADR-0029 signature classes: timeouts, transport, spawn, binary/CLI crashes)
	envMarkers = []string{
		"timeout",
		"timed out",
		"e_timeout",
		"context deadline exceeded",
		"deadline exceeded",
		"execution timed out",
		"lease expired",
		"interrupted",
		"signal: interrupt",
		"signal: killed",
		"sigint",
		"sigterm",
		"process interrupted",
		"spawn-failure",
		"spawn failure",
		"failed to spawn",
		"unable to spawn",
		"exec: executable file not found",
		"fork/exec",
		"cannot run program",
		"unable to start child process",
		"provider transport",
		"transport error",
		"connection reset",
		"connection refused",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"internal server error",
		"500 internal server error",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
		"status 500",
		"status 502",
		"status 503",
		"status 504",
		"5xx",
		"e_io",
		"malformed function call",
		"malformed_function_call",
	}

	// brief-shaped markers (contract violations, harness/scope rejections, missing inputs, prompt/validation errors)
	briefMarkers = []string{
		"contract violation",
		"contract-violation",
		"contract_violation",
		"schema violation",
		"schema mismatch",
		"doc contract",
		"doc_contract",
		"output size violated",
		"output_size_violated",
		"scope rejection",
		"out of scope",
		"workspace_write required",
		"permission denied",
		"path outside write scope",
		"outside workspace",
		"path violation",
		"path_violation",
		"tool violation",
		"tool_violations",
		"receipt violation",
		"receipt bypass",
		"receipt-bypass",
		"invalid receipt",
		"harness rejection",
		"scope violation",
		"read-only violation",
		"read_only_violation",
		"missing input",
		"missing argument",
		"required parameter missing",
		"file not found",
		"no such file or directory",
		"input required",
		"missing dependency",
		"prompt error",
		"validation error",
		"e_usage",
		"e_invalid",
		"e_denied",
		"invalid argument",
		"unknown flag",
		"unknown command",
		"refusal",
		"provider refusal",
		"blocked by filters",
		"blocked by gemini's filters",
		"content filter",
		"safety policy",
		"ambiguous prompt",
		"conflicts with --effort",
		"invalid model selection",
	}
)

var (
	// 400 rejection pattern mentioning effort level
	re400Effort  = regexp.MustCompile(`(?i)(?:400|bad request|invalid[_\s-]?argument|unsupported[_\s-]?parameter|unknown[_\s-]?parameter|conflicts with --effort|invalid model selection).*(?:reasoning[_\s-]?effort|effort).*?\b(none|minimal|low|medium|high|xhigh|max)\b`)
	reEffort400  = regexp.MustCompile(`(?i)\b(none|minimal|low|medium|high|xhigh|max)\b.*(?:is not supported|unsupported|not allowed|invalid reasoning[_\s-]?effort)`)
	reEffortFlag = regexp.MustCompile(`(?i)(?:--effort[=\s]+|reasoning[_\s-]?effort[=\s':]+|effort[=\s':]+)\b(none|minimal|low|medium|high|xhigh|max)\b`)
)

// DetectStalenessTag detects when a provider dispatch fails with a 400-class error
// naming an effort level, returning "catalog-stale(model, level)" or empty string.
func DetectStalenessTag(text, model, defaultEffort string) string {
	combined := strings.ToLower(text)
	if m := re400Effort.FindStringSubmatch(combined); len(m) >= 2 {
		lvl := strings.ToLower(m[1])
		mdl := model
		if mdl == "" {
			mdl = "unknown-model"
		}
		return fmt.Sprintf("catalog-stale(%s, %s)", mdl, lvl)
	}
	if m := reEffort400.FindStringSubmatch(combined); len(m) >= 2 {
		lvl := strings.ToLower(m[1])
		mdl := model
		if mdl == "" {
			mdl = "unknown-model"
		}
		return fmt.Sprintf("catalog-stale(%s, %s)", mdl, lvl)
	}
	if m := reEffortFlag.FindStringSubmatch(combined); len(m) >= 2 && (strings.Contains(combined, "conflicts with --effort") || strings.Contains(combined, "invalid model selection") || strings.Contains(combined, "400") || strings.Contains(combined, "invalid_argument") || strings.Contains(combined, "bad request")) {
		lvl := strings.ToLower(m[1])
		mdl := model
		if mdl == "" {
			mdl = "unknown-model"
		}
		return fmt.Sprintf("catalog-stale(%s, %s)", mdl, lvl)
	}

	// Also check if text mentions HTTP 400 or invalid argument / refusal markers and an explicit effort level was given
	if (strings.Contains(combined, "400") || strings.Contains(combined, "invalid_argument") || strings.Contains(combined, "bad request") ||
		strings.Contains(combined, "conflicts with --effort") || strings.Contains(combined, "invalid model selection")) &&
		(strings.Contains(combined, "effort") || strings.Contains(combined, "reasoning") || strings.Contains(combined, "invalid model selection")) {
		for _, lvl := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			if strings.Contains(combined, lvl) {
				mdl := model
				if mdl == "" {
					mdl = "unknown-model"
				}
				return fmt.Sprintf("catalog-stale(%s, %s)", mdl, lvl)
			}
		}
		if defaultEffort != "" {
			mdl := model
			if mdl == "" {
				mdl = "unknown-model"
			}
			return fmt.Sprintf("catalog-stale(%s, %s)", mdl, defaultEffort)
		}
	}

	return ""
}

// ClassifyFailure determines whether a failure is env-shaped, brief-shaped, or effort-shaped.
//
// Rules:
//  1. Staleness sensor: provider 400 rejections naming an effort level are tagged catalog-stale(model, level).
//  2. If contract violation report is present and non-empty, verdict is brief-shaped.
//  3. If exit code is 126 or 127 (CLI/binary missing or not executable), verdict is env-shaped (binary/CLI error).
//  4. Evidence text is scanned for brief-shaped and env-shaped markers.
//  5. Ambiguous rule (pinned): If markers for BOTH brief-shaped and env-shaped defects appear,
//     brief-shaped takes precedence (the prompt/spec contract takes precedence over downstream transport),
//     routing to HITL immediately.
//  6. If only brief markers appear -> brief-shaped (routes to HITL).
//  7. If only env markers appear -> env-shaped (routes to HITL).
//  8. Everything else (the task ran and its class checks failed) = effort-shaped (ladder escalation trigger).
func ClassifyFailure(ev FailureEvidence) ClassifierVerdict {
	text := strings.ToLower(strings.TrimSpace(ev.ErrorText + " " + ev.ProviderResponse))

	// DoD 6 Staleness sensor
	staleTag := DetectStalenessTag(text, ev.Model, ev.Effort)

	// Explicit contract violation
	if strings.TrimSpace(ev.ContractViolation) != "" {
		return ClassifierVerdict{
			Shape:    ShapeBrief,
			Reason:   fmt.Sprintf("contract violation report: %s", strings.TrimSpace(ev.ContractViolation)),
			StaleTag: staleTag,
		}
	}

	// Exit code inspection
	hasEnvExitCode := false
	if ev.ExitCode != nil {
		switch *ev.ExitCode {
		case 126:
			return ClassifierVerdict{
				Shape:    ShapeEnv,
				Reason:   "binary/CLI execution failed (exit code 126)",
				StaleTag: staleTag,
			}
		case 127:
			return ClassifierVerdict{
				Shape:    ShapeEnv,
				Reason:   "binary/CLI not found (exit code 127)",
				StaleTag: staleTag,
			}
		case 2: // Timeout in worker convention
			hasEnvExitCode = true
		}
	}

	// Match markers
	var matchedBrief []string
	for _, m := range briefMarkers {
		if strings.Contains(text, m) {
			if m == "file not found" && strings.Contains(text, "executable file not found") {
				continue
			}
			matchedBrief = append(matchedBrief, m)
		}
	}

	var matchedEnv []string
	for _, m := range envMarkers {
		if strings.Contains(text, m) {
			matchedEnv = append(matchedEnv, m)
		}
	}
	if hasEnvExitCode && len(matchedEnv) == 0 {
		matchedEnv = append(matchedEnv, "timeout-exit-code-2")
	}

	// Staleness tag implies brief validation / provider parameter rejection
	if staleTag != "" && len(matchedBrief) == 0 {
		matchedBrief = append(matchedBrief, "provider-effort-rejection-400")
	}

	hasBrief := len(matchedBrief) > 0
	hasEnv := len(matchedEnv) > 0

	// Ambiguous marker resolution: brief-shaped takes precedence
	if hasBrief && hasEnv {
		return ClassifierVerdict{
			Shape:    ShapeBrief,
			Reason:   fmt.Sprintf("ambiguous failure: brief-shaped precedence pinned (brief markers: [%s], env markers: [%s])", strings.Join(matchedBrief, ", "), strings.Join(matchedEnv, ", ")),
			StaleTag: staleTag,
		}
	}

	if hasBrief {
		return ClassifierVerdict{
			Shape:    ShapeBrief,
			Reason:   fmt.Sprintf("brief-shaped defect (%s)", strings.Join(matchedBrief, ", ")),
			StaleTag: staleTag,
		}
	}

	if hasEnv {
		return ClassifierVerdict{
			Shape:    ShapeEnv,
			Reason:   fmt.Sprintf("env-shaped defect (%s)", strings.Join(matchedEnv, ", ")),
			StaleTag: staleTag,
		}
	}

	// Everything else is effort-shaped
	reason := "task executed and class checks failed (effort-shaped)"
	if len(ev.ChecksFailed) > 0 {
		reason = fmt.Sprintf("task executed and %d class check(s) failed: %s", len(ev.ChecksFailed), strings.Join(ev.ChecksFailed, ", "))
	}

	return ClassifierVerdict{
		Shape:    ShapeEffort,
		Reason:   reason,
		StaleTag: staleTag,
	}
}
