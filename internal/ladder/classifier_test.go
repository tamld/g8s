package ladder

import (
	"testing"
)

func TestClassifierMatrix(t *testing.T) {
	exit127 := 127
	exit126 := 126
	exit1 := 1
	exit2 := 2
	exit0 := 0

	tests := []struct {
		name          string
		evidence      FailureEvidence
		wantShape     FailureShape
		wantStaleTag  string
		wantReasonSub string
	}{
		// 1. Env-shaped table tests
		{
			name: "env: timeout error text",
			evidence: FailureEvidence{
				ErrorText: "context deadline exceeded while awaiting provider response",
				ExitCode:  &exit2,
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "env-shaped defect",
		},
		{
			name: "env: transport 502 bad gateway",
			evidence: FailureEvidence{
				ErrorText: "provider transport: 502 bad gateway returned from API",
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "bad gateway",
		},
		{
			name: "env: spawn failure",
			evidence: FailureEvidence{
				ErrorText: "failed to spawn child worker process: exec: executable file not found",
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "spawn",
		},
		{
			name: "env: exit code 127 binary missing",
			evidence: FailureEvidence{
				ErrorText: "command returned non-zero",
				ExitCode:  &exit127,
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "exit code 127",
		},
		{
			name: "env: exit code 126 binary not executable",
			evidence: FailureEvidence{
				ErrorText: "permission issue running binary",
				ExitCode:  &exit126,
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "exit code 126",
		},
		{
			name: "env: process interrupted sigterm",
			evidence: FailureEvidence{
				ErrorText: "signal: killed / process interrupted",
			},
			wantShape:     ShapeEnv,
			wantReasonSub: "interrupted",
		},

		// 2. Brief-shaped table tests
		{
			name: "brief: contract violation report present",
			evidence: FailureEvidence{
				ContractViolation: "tool violation: tool 'bash_eval' called without authorization",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "contract violation report",
		},
		{
			name: "brief: scope rejection / workspace_write required",
			evidence: FailureEvidence{
				ErrorText: "scope rejection: path outside write scope 'internal/ladder/ladder.go'",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "scope rejection",
		},
		{
			name: "brief: missing input argument",
			evidence: FailureEvidence{
				ErrorText: "task failed: missing input parameter 'schema_path'",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "missing input",
		},
		{
			name: "brief: prompt validation error / e_usage",
			evidence: FailureEvidence{
				ErrorText: "E_USAGE: invalid argument --unknown-flag",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "e_usage",
		},
		{
			name: "brief: provider refusal / content filter",
			evidence: FailureEvidence{
				ErrorText: "provider refusal: blocked by gemini's filters",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "provider refusal",
		},

		// 3. Ambiguous rule pinned: both brief and env markers present
		{
			name: "ambiguous: prompt error AND timeout (brief takes precedence)",
			evidence: FailureEvidence{
				ErrorText: "prompt error: invalid parameter provided before context deadline exceeded",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "ambiguous failure: brief-shaped precedence pinned",
		},
		{
			name: "ambiguous: scope violation AND transport reset (brief takes precedence)",
			evidence: FailureEvidence{
				ErrorText: "scope violation detected and provider transport connection reset",
			},
			wantShape:     ShapeBrief,
			wantReasonSub: "ambiguous failure: brief-shaped precedence pinned",
		},

		// 4. Effort-shaped table tests (class checks failed, no env/brief defect)
		{
			name: "effort: unit test / class check failed",
			evidence: FailureEvidence{
				ErrorText:    "tests failed in internal/storage: TestStoreGet expected 2 got 1",
				ChecksFailed: []string{"check-unit-tests", "check-lint"},
				ExitCode:     &exit1,
			},
			wantShape:     ShapeEffort,
			wantReasonSub: "2 class check(s) failed",
		},
		{
			name: "effort: standard run failure without env/brief markers",
			evidence: FailureEvidence{
				ErrorText: "worker completed but verifier rejected code diff",
				ExitCode:  &exit1,
			},
			wantShape:     ShapeEffort,
			wantReasonSub: "effort-shaped",
		},

		// 5. Staleness sensor (DoD 6): 400 rejection naming effort level
		{
			name: "stale: 400 rejection naming high effort",
			evidence: FailureEvidence{
				ErrorText: "HTTP 400 Bad Request: reasoning_effort 'high' is not supported by model gemini-2.5-flash",
				Model:     "gemini-2.5-flash",
				Effort:    "high",
			},
			wantShape:     ShapeBrief,
			wantStaleTag:  "catalog-stale(gemini-2.5-flash, high)",
			wantReasonSub: "brief-shaped defect",
		},
		{
			name: "stale: 400 rejection naming low effort on claude",
			evidence: FailureEvidence{
				ErrorText: "invalid_argument: effort 'low' not allowed for claude-3-opus",
				Model:     "claude-3-opus",
				Effort:    "low",
			},
			wantShape:     ShapeBrief,
			wantStaleTag:  "catalog-stale(claude-3-opus, low)",
			wantReasonSub: "brief-shaped defect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := ClassifyFailure(tt.evidence)
			if verdict.Shape != tt.wantShape {
				t.Fatalf("Shape = %s, want %s (reason: %s)", verdict.Shape, tt.wantShape, verdict.Reason)
			}
			if tt.wantStaleTag != "" && verdict.StaleTag != tt.wantStaleTag {
				t.Errorf("StaleTag = %q, want %q", verdict.StaleTag, tt.wantStaleTag)
			}
			if tt.wantReasonSub != "" && !containsSubstring(verdict.Reason, tt.wantReasonSub) {
				t.Errorf("Reason = %q, want substring %q", verdict.Reason, tt.wantReasonSub)
			}
		})
	}
	_ = exit0
}

func containsSubstring(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || stringContains(s, sub)))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
