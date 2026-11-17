package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCLIInDir(t *testing.T, binPath, dir string, envVars []string, args ...string) (testEnvelope, int, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), envVars...)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run command %v failed: %v", args, err)
		}
	}
	raw := strings.TrimSpace(string(out))
	var env testEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal([]byte(raw), &env)
	}
	return env, exitCode, raw
}

func issueLaneSTestReceipt(t *testing.T, binPath, root string, envVars []string, pathGlob string) string {
	t.Helper()
	env, code, raw := runCLIInDir(t, binPath, root, envVars,
		"receipt", "issue",
		"--actor", "f0-test",
		"--path", pathGlob,
		"--ttl", "3600",
		"--json",
	)
	if code != 0 {
		t.Fatalf("receipt issue failed (code=%d): %s", code, raw)
	}
	var data struct {
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.ReceiptID == "" {
		t.Fatalf("parse receipt_id failed: %v raw=%s", err, raw)
	}
	return data.ReceiptID
}

func assertStateEventContains(t *testing.T, binPath, root string, envVars []string, taskID, wantEvent, wantReasonSub string) {
	t.Helper()
	env, code, raw := runCLIInDir(t, binPath, root, envVars,
		"state", "show", "--json", taskID,
	)
	if code != 0 {
		t.Fatalf("state show failed (code=%d): %s", code, raw)
	}
	var data struct {
		Events []struct {
			Event  string `json:"event"`
			Reason string `json:"reason"`
		} `json:"events"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode state show data: %v raw=%s", err, raw)
	}
	for _, ev := range data.Events {
		if ev.Event == wantEvent && strings.Contains(ev.Reason, wantReasonSub) {
			return
		}
	}
	t.Fatalf("expected event=%q with reason containing %q in events %+v", wantEvent, wantReasonSub, data.Events)
}

func TestSubmit_LaneS_AutoEscalatesPremiseAndConformance(t *testing.T) {
	binPath := buildG8sBinary(t)
	root := findRepoRoot(t)
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "controlplane.db")
	envVars := []string{
		"G8S_STATE_DIR=" + stateDir,
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(stateDir, "absent-providers.json"),
		"AGY_MCP_ALLOW_WORKSPACE_WRITE=1",
		"G8S_ROUTER_MODE=",
		"G8S_PREMISE_CHECK=",
		"G8S_ENFORCE_CONFORMANCE=",
	}

	sandboxReceipt := issueLaneSTestReceipt(t, binPath, root, envVars, "internal/sandbox/**")
	signingReceipt := issueLaneSTestReceipt(t, binPath, root, envVars, "internal/signing/**")

	t.Run("AutoConformanceBlockOnTrustBoundary", func(t *testing.T) {
		env, code, raw := runCLIInDir(t, binPath, root, envVars,
			"submit", "--json",
			"--idempotency-key", "lane-s-conf-block",
			"--permission", "workspace_write",
			"--receipt-id", sandboxReceipt,
			"--provider", "opencode",
			"--prompt", "Audit sandbox module",
		)
		if code == 0 {
			t.Fatalf("expected non-zero exit when non-conformant provider targets trust boundary, got 0: %s", raw)
		}
		if env.Error == nil || env.Error.Code != "E_PROVIDER_CONFORMANCE_FAIL" {
			t.Fatalf("expected E_PROVIDER_CONFORMANCE_FAIL, got err=%+v raw=%s", env.Error, raw)
		}
	})

	t.Run("AutoPremiseBlockOnTrustBoundary", func(t *testing.T) {
		env, code, raw := runCLIInDir(t, binPath, root, envVars,
			"submit", "--json",
			"--idempotency-key", "lane-s-prem-block",
			"--permission", "workspace_write",
			"--receipt-id", signingReceipt,
			"--provider", "9router",
			"--model", "reasoning-free",
			"--prompt", "File `internal/signing/nonexistent_xyz.go` exists. Update it.",
		)
		if code == 0 {
			t.Fatalf("expected non-zero exit on contradicted premise targeting trust boundary, got 0: %s", raw)
		}
		if env.Error == nil || env.Error.Code != "E_PREMISE_CONTRADICTED" {
			t.Fatalf("expected E_PREMISE_CONTRADICTED, got err=%+v raw=%s", env.Error, raw)
		}
	})

	t.Run("AuditedOverrideReasonUnblocksAndLogsStateEvents", func(t *testing.T) {
		confOverrideReceipt := issueLaneSTestReceipt(t, binPath, root, envVars, "internal/sandbox/**")
		envConf, codeConf, rawConf := runCLIInDir(t, binPath, root, envVars,
			"submit", "--json",
			"--idempotency-key", "lane-s-conf-override",
			"--permission", "workspace_write",
			"--receipt-id", confOverrideReceipt,
			"--provider", "opencode",
			"--prompt", "Audit sandbox module",
			"--override-reason", "emergency F0 conformance drill",
		)
		if codeConf != 0 {
			t.Fatalf("conformance override submit failed (code=%d): %s", codeConf, rawConf)
		}
		var taskConf struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(envConf.Data, &taskConf); err != nil || taskConf.TaskID == "" {
			t.Fatalf("decode task_id failed: %v raw=%s", err, rawConf)
		}
		reqConf := getTaskRequestJSON(t, dbPath, "lane-s-conf-override")
		var payloadConf map[string]any
		if err := json.Unmarshal([]byte(reqConf), &payloadConf); err != nil {
			t.Fatalf("decode payloadConf: %v", err)
		}
		if payloadConf["routed_lane"] != "S" {
			t.Fatalf("routed_lane = %v, want S", payloadConf["routed_lane"])
		}
		if _, ok := payloadConf["conformance_override"]; !ok {
			t.Fatalf("expected conformance_override in payload: %v", payloadConf)
		}
		assertStateEventContains(t, binPath, root, envVars, taskConf.TaskID, "conformance_override", "emergency F0 conformance drill")

		premOverrideReceipt := issueLaneSTestReceipt(t, binPath, root, envVars, "internal/signing/**")
		envPrem, codePrem, rawPrem := runCLIInDir(t, binPath, root, envVars,
			"submit", "--json",
			"--idempotency-key", "lane-s-prem-override",
			"--permission", "workspace_write",
			"--receipt-id", premOverrideReceipt,
			"--provider", "9router",
			"--model", "reasoning-free",
			"--prompt", "File `internal/signing/nonexistent_xyz.go` exists. Update it.",
			"--override-reason", "emergency F0 premise drill",
		)
		if codePrem != 0 {
			t.Fatalf("premise override submit failed (code=%d): %s", codePrem, rawPrem)
		}
		var taskPrem struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(envPrem.Data, &taskPrem); err != nil || taskPrem.TaskID == "" {
			t.Fatalf("decode task_id failed: %v raw=%s", err, rawPrem)
		}
		reqPrem := getTaskRequestJSON(t, dbPath, "lane-s-prem-override")
		var payloadPrem map[string]any
		if err := json.Unmarshal([]byte(reqPrem), &payloadPrem); err != nil {
			t.Fatalf("decode payloadPrem: %v", err)
		}
		if payloadPrem["routed_lane"] != "S" {
			t.Fatalf("routed_lane = %v, want S", payloadPrem["routed_lane"])
		}
		if _, ok := payloadPrem["premise_check"]; !ok {
			t.Fatalf("expected premise_check in payload: %v", payloadPrem)
		}
		assertStateEventContains(t, binPath, root, envVars, taskPrem.TaskID, "premise_override", "emergency F0 premise drill")
	})

	t.Run("NonTrustBoundaryPathPreservesAdvisoryDefault", func(t *testing.T) {
		env, code, raw := runCLIInDir(t, binPath, root, envVars,
			"submit", "--json",
			"--idempotency-key", "lane-d-ordinary",
			"--add-dir", "docs",
			"--provider", "opencode",
			"--prompt", "Summarize documentation files",
		)
		if code != 0 {
			t.Fatalf("ordinary Lane D submit failed (code=%d): %s", code, raw)
		}
		_ = env
		reqJSON := getTaskRequestJSON(t, dbPath, "lane-d-ordinary")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["routed_lane"] != "D" {
			t.Fatalf("routed_lane = %v, want D", payload["routed_lane"])
		}
	})
}
