package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func TestSubmitSuppliedText(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "supplied_text.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "supplied-text-task",
		"--prompt", "Analyze architecture without tools",
		"--permission", "supplied_text",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "supplied-text-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["permission"] != "supplied_text" {
		t.Errorf("permission = %v, want supplied_text", payload["permission"])
	}
	if payload["tool_policy"] != "none" {
		t.Errorf("tool_policy = %v, want none", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyNone(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_none.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-none-task",
		"--prompt", "Read-only task with tool policy none",
		"--permission", "read_only",
		"--tool-policy", "none",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "tool-policy-none-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["tool_policy"] != "none" {
		t.Errorf("tool_policy = %v, want none", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyDefault(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_default.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-default-task",
		"--prompt", "Read-only task with tool policy default",
		"--permission", "read_only",
		"--tool-policy", "default",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "tool-policy-default-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["tool_policy"] != "default" {
		t.Errorf("tool_policy = %v, want default", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyInvalid_UsageError(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_invalid.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	_, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-invalid-task",
		"--prompt", "Task with invalid tool policy",
		"--tool-policy", "invalid_policy",
	)
	if code == 0 {
		t.Fatal("expected non-zero exit code for invalid --tool-policy")
	}
	if !strings.Contains(raw, "invalid --tool-policy") {
		t.Errorf("expected error mentioning invalid --tool-policy, got: %s", raw)
	}
}

func TestSubmitSuppliedTextUnsupportedProvider_FailsClosed(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "unsupported_provider.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	_, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "unsupported-provider-task",
		"--prompt", "Task prompt",
		"--permission", "supplied_text",
		"--provider", "codex",
	)
	if code == 0 {
		t.Fatal("expected submit to fail closed when provider does not support tool_policy=none")
	}
	if !strings.Contains(raw, "does not support tool_policy=none") {
		t.Errorf("expected error mentioning tool_policy=none unsupported, got: %s", raw)
	}
}

func createTestReceipt(t *testing.T, rcDBPath string, ttl time.Duration, allowedPaths []string) *receipt.WriteReceipt {
	t.Helper()
	mgr, err := receipt.NewReceiptManager(rcDBPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer mgr.Close()
	rc, err := mgr.IssueReceipt("brain-supervisor", allowedPaths, ttl,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "test-receipt",
		}),
		receipt.WithProvenanceContext("g8s/test", "HEAD", "test-receipt"),
	)
	if err != nil {
		t.Fatalf("IssueReceipt: %v", err)
	}
	return rc
}

// TestSubmitTimeout_TableDriven_ParseValidation verifies R1 (#721) and Eval Contract E1:
// -timeout 900 (bare) is accepted per policy, -timeout banana fails at parse with hint showing valid forms.
func TestSubmitTimeout_TableDriven_ParseValidation(t *testing.T) {
	binPath := buildG8sBinary(t)

	tests := []struct {
		name        string
		timeoutFlag string
		wantCode    int
		wantErrCode string
		wantTimeout string
		hintContain string
	}{
		{
			name:        "bare positive integer 900 accepted as seconds (E1)",
			timeoutFlag: "900",
			wantCode:    0,
			wantTimeout: "900s",
		},
		{
			name:        "standard duration 15m accepted",
			timeoutFlag: "15m",
			wantCode:    0,
			wantTimeout: "15m",
		},
		{
			name:        "invalid string banana fails at parse time (E1)",
			timeoutFlag: "banana",
			wantCode:    2,
			wantErrCode: "E_USAGE",
			hintContain: "900s",
		},
		{
			name:        "zero duration rejected at parse time",
			timeoutFlag: "0s",
			wantCode:    2,
			wantErrCode: "E_USAGE",
			hintContain: "900s",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			dbPath := filepath.Join(tempDir, "timeout_test.db")
			envVars := []string{"G8S_DB=" + dbPath}
			key := fmt.Sprintf("timeout-task-%d", i)

			env, code, raw := runSubmitCLI(t, binPath, envVars,
				"--idempotency-key", key,
				"--prompt", "Task for timeout testing",
				"--timeout", tc.timeoutFlag,
			)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (raw: %s)", code, tc.wantCode, raw)
			}

			if tc.wantCode != 0 {
				if env.Error == nil {
					t.Fatalf("expected Error payload in envelope, got nil: %s", raw)
				}
				if env.Error.Code != tc.wantErrCode {
					t.Errorf("error code = %q, want %q", env.Error.Code, tc.wantErrCode)
				}
				if tc.hintContain != "" && !strings.Contains(env.Error.Hint, tc.hintContain) {
					t.Errorf("expected hint to contain %q, got: %q", tc.hintContain, env.Error.Hint)
				}
				return
			}

			// For successful submits, check envelope and db echo
			var dataMap map[string]any
			if err := json.Unmarshal(env.Data, &dataMap); err != nil {
				t.Fatalf("unmarshal env.Data: %v", err)
			}
			reqMap, ok := dataMap["request"].(map[string]any)
			if !ok {
				t.Fatalf("expected data.request object in envelope: %v", dataMap)
			}
			if reqMap["timeout"] != tc.wantTimeout {
				t.Errorf("data.request.timeout = %v, want %q", reqMap["timeout"], tc.wantTimeout)
			}

			dbJSON := getTaskRequestJSON(t, dbPath, key)
			var payload map[string]any
			if err := json.Unmarshal([]byte(dbJSON), &payload); err != nil {
				t.Fatalf("unmarshal db payload: %v", err)
			}
			if payload["timeout"] != tc.wantTimeout {
				t.Errorf("db request.timeout = %v, want %q", payload["timeout"], tc.wantTimeout)
			}
		})
	}
}

// TestSubmitWorkspaceWrite_ScopeRootGitCheck_Warnings verifies R2 (#722):
// When -permission workspace_write, scope root not inside a git work tree attaches a non-fatal warning to data.warnings[].
func TestSubmitWorkspaceWrite_ScopeRootGitCheck_Warnings(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "controlplane.db")
	rcDBPath := filepath.Join(tempDir, "receipts.db")

	rc := createTestReceipt(t, rcDBPath, 10*time.Minute, []string{"*"})
	nonGitDir := t.TempDir()

	envVars := []string{
		"G8S_DB=" + dbPath,
		"AGY_MCP_ALLOW_WORKSPACE_WRITE=1",
	}

	t.Run("non-git scope root produces warning in envelope", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "ww-nongit-scope-root",
			"--prompt", "Write task in non-git dir",
			"--permission", "workspace_write",
			"--receipt-id", rc.ReceiptID,
			"--scope-root", nonGitDir,
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}

		var dataMap map[string]any
		if err := json.Unmarshal(env.Data, &dataMap); err != nil {
			t.Fatalf("unmarshal env.Data: %v", err)
		}
		warnings, ok := dataMap["warnings"].([]any)
		if !ok || len(warnings) == 0 {
			t.Fatalf("expected data.warnings array in envelope, got: %v", dataMap["warnings"])
		}
		found := false
		for _, w := range warnings {
			if s, ok := w.(string); ok && strings.Contains(s, "is not a git repository") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning mentioning 'is not a git repository', got: %v", warnings)
		}
	})

	t.Run("git repo scope root has no warnings", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "ww-git-scope-root",
			"--prompt", "Write task in git repo dir",
			"--permission", "workspace_write",
			"--receipt-id", rc.ReceiptID,
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}

		var dataMap map[string]any
		if err := json.Unmarshal(env.Data, &dataMap); err != nil {
			t.Fatalf("unmarshal env.Data: %v", err)
		}
		if warnings, exists := dataMap["warnings"]; exists && warnings != nil {
			if list, ok := warnings.([]any); ok && len(list) > 0 {
				t.Errorf("expected no warnings for git scope root, got: %v", list)
			}
		}
	})
}

// TestSubmitClocks_CrossValidation verifies R3 (#723) and Eval Contract E3:
// timeout >= receipt TTL is rejected at parse time naming both values;
// timeout < receipt TTL produces data.clocks in the envelope exposing {timeout_s, receipt_ttl_s}.
func TestSubmitClocks_CrossValidation(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "clocks.db")
	rcDBPath := filepath.Join(tempDir, "receipts.db")

	// Create receipt with TTL = 600s (10 minutes)
	rc := createTestReceipt(t, rcDBPath, 600*time.Second, []string{"*"})
	envVars := []string{
		"G8S_DB=" + dbPath,
		"AGY_MCP_ALLOW_WORKSPACE_WRITE=1",
	}

	t.Run("timeout greater than receipt TTL rejected with parse error naming both values", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "clocks-timeout-gt",
			"--prompt", "Task prompt",
			"--permission", "workspace_write",
			"--receipt-id", rc.ReceiptID,
			"--timeout", "900s",
		)
		if code == 0 {
			t.Fatal("expected submit to fail when timeout > receipt TTL")
		}
		if env.Error == nil || env.Error.Code != "E_USAGE" {
			t.Errorf("expected E_USAGE error, got: %+v (raw: %s)", env.Error, raw)
		}
		// Must name both values
		if !strings.Contains(raw, "900s") {
			t.Errorf("expected error to name timeout 900s, got: %s", raw)
		}
		if !strings.Contains(raw, "10m0s") && !strings.Contains(raw, "600s") {
			t.Errorf("expected error to name receipt TTL (10m0s or 600s), got: %s", raw)
		}
	})

	t.Run("timeout equal to receipt TTL rejected at parse", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "clocks-timeout-eq",
			"--prompt", "Task prompt",
			"--permission", "workspace_write",
			"--receipt-id", rc.ReceiptID,
			"--timeout", "600s",
		)
		if code == 0 {
			t.Fatal("expected submit to fail when timeout == receipt TTL")
		}
		if env.Error == nil || env.Error.Code != "E_USAGE" {
			t.Errorf("expected E_USAGE error, got: %+v (raw: %s)", env.Error, raw)
		}
		if !strings.Contains(raw, "600s") {
			t.Errorf("expected error to name timeout 600s, got: %s", raw)
		}
	})

	t.Run("timeout strictly less than receipt TTL succeeds and emits data.clocks", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "clocks-timeout-valid",
			"--prompt", "Task prompt",
			"--permission", "workspace_write",
			"--receipt-id", rc.ReceiptID,
			"--timeout", "300s",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}

		var dataMap map[string]any
		if err := json.Unmarshal(env.Data, &dataMap); err != nil {
			t.Fatalf("unmarshal env.Data: %v", err)
		}

		// Check data.clocks
		clocksMap, ok := dataMap["clocks"].(map[string]any)
		if !ok {
			t.Fatalf("expected data.clocks in envelope, got: %v", dataMap)
		}
		if clocksMap["timeout_s"] != float64(300) {
			t.Errorf("timeout_s = %v, want 300", clocksMap["timeout_s"])
		}
		if clocksMap["receipt_ttl_s"] != float64(600) {
			t.Errorf("receipt_ttl_s = %v, want 600", clocksMap["receipt_ttl_s"])
		}

		// Also check data.request.clocks
		reqMap, ok := dataMap["request"].(map[string]any)
		if !ok {
			t.Fatalf("expected data.request in envelope: %v", dataMap)
		}
		reqClocks, ok := reqMap["clocks"].(map[string]any)
		if !ok {
			t.Fatalf("expected data.request.clocks in envelope: %v", reqMap)
		}
		if reqClocks["timeout_s"] != float64(300) {
			t.Errorf("request.clocks.timeout_s = %v, want 300", reqClocks["timeout_s"])
		}
		if reqClocks["receipt_ttl_s"] != float64(600) {
			t.Errorf("request.clocks.receipt_ttl_s = %v, want 600", reqClocks["receipt_ttl_s"])
		}
	})

	t.Run("omitted timeout with effort-derived default exceeding receipt TTL is rejected", func(t *testing.T) {
		rcShort := createTestReceipt(t, rcDBPath, 60*time.Second, []string{"*"})
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "clocks-omitted-timeout-high-exceeds-ttl",
			"--prompt", "Task prompt",
			"--permission", "workspace_write",
			"--receipt-id", rcShort.ReceiptID,
			"--effort", "high",
		)
		if code == 0 {
			t.Fatal("expected submit to fail when omitted timeout effort-derived default > receipt TTL")
		}
		if env.Error == nil || env.Error.Code != "E_USAGE" {
			t.Errorf("expected E_USAGE error, got: %+v (raw: %s)", env.Error, raw)
		}
		if !strings.Contains(raw, "240s") {
			t.Errorf("expected error to name derived timeout 240s, got: %s", raw)
		}
		if !strings.Contains(raw, "1m0s") && !strings.Contains(raw, "60s") {
			t.Errorf("expected error to name receipt TTL (1m0s or 60s), got: %s", raw)
		}
	})

	t.Run("omitted timeout with medium effort default exceeding receipt TTL is rejected", func(t *testing.T) {
		rcShort := createTestReceipt(t, rcDBPath, 60*time.Second, []string{"*"})
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "clocks-omitted-timeout-medium-exceeds-ttl",
			"--prompt", "Task prompt",
			"--permission", "workspace_write",
			"--receipt-id", rcShort.ReceiptID,
			"--effort", "medium",
		)
		if code == 0 {
			t.Fatal("expected submit to fail when omitted timeout effort-derived default > receipt TTL")
		}
		if env.Error == nil || env.Error.Code != "E_USAGE" {
			t.Errorf("expected E_USAGE error, got: %+v (raw: %s)", env.Error, raw)
		}
		if !strings.Contains(raw, "90s") {
			t.Errorf("expected error to name derived timeout 90s, got: %s", raw)
		}
		if !strings.Contains(raw, "1m0s") && !strings.Contains(raw, "60s") {
			t.Errorf("expected error to name receipt TTL (1m0s or 60s), got: %s", raw)
		}
	})
}
