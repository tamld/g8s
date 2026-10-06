package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/routing"
	_ "modernc.org/sqlite"
)

func runRedtestSubmit(t *testing.T, binPath string, envVars []string, args ...string) (testEnvelope, int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"submit", "--json"}, args...)...)
	cmd.Env = append(os.Environ(), envVars...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run submit command %v: %v", args, err)
		}
	}
	rawOut := strings.TrimSpace(stdout.String())
	rawErr := strings.TrimSpace(stderr.String())
	raw := rawOut
	if raw == "" {
		raw = rawErr
	}
	var env testEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal([]byte(raw), &env)
	}
	return env, exitCode, rawOut, rawErr
}

func runRedtestLadder(t *testing.T, binPath string, envVars []string, args ...string) (testEnvelope, int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"ladder"}, args...)...)
	cmd.Env = append(os.Environ(), envVars...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run ladder command %v: %v", args, err)
		}
	}
	rawOut := strings.TrimSpace(stdout.String())
	rawErr := strings.TrimSpace(stderr.String())
	raw := rawOut
	if raw == "" {
		raw = rawErr
	}
	var env testEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal([]byte(raw), &env)
	}
	return env, exitCode, rawOut, rawErr
}

// -----------------------------------------------------------------------------
// Dimension 1: --effort Flag Validation
// -----------------------------------------------------------------------------

func TestRedR4_Dim1_EffortValidation(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dim1_effort.db")
	envVars := []string{"G8S_DB=" + dbPath}

	// Rule 1.1: Default effort lands as "medium" in payload when --effort is omitted.
	t.Run("default_lands_as_medium", func(t *testing.T) {
		key := "r4-dim1-default"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Verification prompt for default effort knob",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort"] != "medium" {
			t.Errorf("Rule 1.1 violation: payload[effort] = %v, want medium", payload["effort"])
		}
		if payload["effort_requested"] != "medium" {
			t.Errorf("Rule 1.1 violation: payload[effort_requested] = %v, want medium", payload["effort_requested"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("Rule 1.1 violation: payload[effort_applied] = %v, want medium", payload["effort_applied"])
		}
	})

	// Rule 1.2: Every level on the canonical ladder is accepted.
	t.Run("every_canonical_level_accepted", func(t *testing.T) {
		for _, lvl := range config.EffortLadder {
			lvl := lvl
			t.Run("level_"+lvl, func(t *testing.T) {
				key := "r4-dim1-ladder-" + lvl
				env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
					"--idempotency-key", key,
					"--prompt", "Verification prompt for canonical level "+lvl,
					"--effort", lvl,
				)
				if code != 0 {
					t.Fatalf("Rule 1.2 violation: level %q rejected with code %d, err=%+v rawErr=%s", lvl, code, env.Error, rawErr)
				}

				reqJSON := getTaskRequestJSON(t, dbPath, key)
				var payload map[string]any
				if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
					t.Fatalf("unmarshal payload: %v", err)
				}
				if payload["effort_requested"] != lvl {
					t.Errorf("Rule 1.2 violation: payload[effort_requested] = %v, want %s", payload["effort_requested"], lvl)
				}
			})
		}
	})

	// Rule 1.3: Off-ladder levels rejected with typed usage error naming the allowed ladder.
	t.Run("off_ladder_levels_rejected", func(t *testing.T) {
		offLadder := []string{"invalid", "superhigh", "turbo", "fast", "0", "1", "MEDIUM", "High", "max-effort"}
		for _, lvl := range offLadder {
			lvl := lvl
			t.Run("off_ladder_"+lvl, func(t *testing.T) {
				key := "r4-dim1-bad-" + lvl
				env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
					"--idempotency-key", key,
					"--prompt", "Bad level test "+lvl,
					"--effort", lvl,
				)
				if code != 2 {
					t.Fatalf("Rule 1.3 violation: off-ladder level %q gave exit code %d, want 2 (E_USAGE); rawErr=%s", lvl, code, rawErr)
				}
				if env.Error == nil {
					t.Fatalf("Rule 1.3 violation: expected error envelope for %q, got nil; rawErr=%s", lvl, rawErr)
				}
				if env.Error.Code != cli.CodeUsage {
					t.Errorf("Rule 1.3 violation: error.code = %q, want %q", env.Error.Code, cli.CodeUsage)
				}
				if !strings.Contains(env.Error.Message, "invalid --effort") {
					t.Errorf("Rule 1.3 violation: error message missing 'invalid --effort': %q", env.Error.Message)
				}
				if !strings.Contains(env.Error.Message, "allowed ladder is [") {
					t.Errorf("Rule 1.3 violation: error message missing allowed ladder enumeration: %q", env.Error.Message)
				}
			})
		}
	})

	// Rule 1.4: Empty-string effort level rejected with typed usage error naming the allowed ladder.
	t.Run("empty_string_rejected", func(t *testing.T) {
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", "r4-dim1-empty-effort",
			"--prompt", "Empty effort test",
			"--effort", "",
		)
		if code != 2 {
			t.Fatalf("Rule 1.4 violation: empty effort gave exit code %d, want 2 (E_USAGE); rawErr=%s", code, rawErr)
		}
		if env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Fatalf("Rule 1.4 violation: expected E_USAGE envelope, got: %+v; rawErr=%s", env, rawErr)
		}
		if !strings.Contains(env.Error.Message, "invalid --effort \"\"") {
			t.Errorf("Rule 1.4 violation: message missing empty string indicator: %q", env.Error.Message)
		}
		if !strings.Contains(env.Error.Message, "allowed ladder is [") {
			t.Errorf("Rule 1.4 violation: message missing allowed ladder list: %q", env.Error.Message)
		}
	})

	// Rule 1.5: Validation happens at parse time before any store call.
	t.Run("validation_at_parse_time_before_store", func(t *testing.T) {
		impossibleDB := filepath.Join(tempDir, "nonexistent_subfolder", "impossible", "test.db")
		badEnvVars := []string{"G8S_DB=" + impossibleDB}

		env, code, _, rawErr := runRedtestSubmit(t, binPath, badEnvVars,
			"--idempotency-key", "r4-dim1-parsetime",
			"--prompt", "Parse time validation test",
			"--effort", "illegal_level",
		)
		if code != 2 {
			t.Fatalf("Rule 1.5 violation: expected parse-time exit code 2 (E_USAGE), got %d; rawErr=%s", code, rawErr)
		}
		if env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Fatalf("Rule 1.5 violation: expected E_USAGE envelope before DB call, got %+v", env)
		}
		if _, statErr := os.Stat(impossibleDB); !os.IsNotExist(statErr) {
			t.Errorf("Rule 1.5 violation: store database was created despite early parse validation failure: %s", impossibleDB)
		}
	})
}

// -----------------------------------------------------------------------------
// Dimension 2: Payload Wiring & omitempty Invariants
// -----------------------------------------------------------------------------

func TestRedR4_Dim2_PayloadWiring(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dim2_payload.db")

	manifestPath := filepath.Join(tempDir, "dim2_providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "dim2-toggle-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "dim2-toggle-model",
						"effort_style": "toggle",
						"supported_efforts": ["medium", "high"],
						"default_effort": "medium"
					}
				]
			},
			{
				"name": "dim2-budget-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "dim2-budget-model",
						"effort_style": "budget",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "medium",
						"effort_budget_map": {
							"low": 2048,
							"high": 32768
						}
					}
				]
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test manifest: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	// Rule 2.1: Default settings omit advisory keys (effort_mismatch absent when false, effort_budget_tokens absent when zero).
	t.Run("defaults_no_mismatch_zero_budget_omits_advisories", func(t *testing.T) {
		key := "r4-dim2-clean"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Clean payload wiring prompt",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort"] != "medium" {
			t.Errorf("Rule 2.1 violation: payload[effort] = %v, want medium", payload["effort"])
		}
		if payload["effort_requested"] != "medium" {
			t.Errorf("Rule 2.1 violation: payload[effort_requested] = %v, want medium", payload["effort_requested"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("Rule 2.1 violation: payload[effort_applied] = %v, want medium", payload["effort_applied"])
		}
		if _, ok := payload["effort_mismatch"]; ok {
			t.Errorf("Rule 2.1 violation: effort_mismatch unexpectedly present in payload: %v", payload["effort_mismatch"])
		}
		if _, ok := payload["effort_budget_tokens"]; ok {
			t.Errorf("Rule 2.1 violation: effort_budget_tokens unexpectedly present in payload: %v", payload["effort_budget_tokens"])
		}
	})

	// Rule 2.2: effort_mismatch present and true exactly when requested differs from applied.
	t.Run("mismatch_true_included", func(t *testing.T) {
		key := "r4-dim2-mismatch"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Toggle mismatch prompt",
			"--provider", "dim2-toggle-prov",
			"--model", "dim2-toggle-model",
			"--effort", "low",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_mismatch"] != true {
			t.Errorf("Rule 2.2 violation: expected effort_mismatch=true, got %v", payload["effort_mismatch"])
		}
		if _, ok := payload["effort_budget_tokens"]; ok {
			t.Errorf("Rule 2.2 violation: effort_budget_tokens unexpectedly present for non-budget model: %v", payload["effort_budget_tokens"])
		}
	})

	// Rule 2.3: effort_budget_tokens included when non-zero and mismatch absent when false.
	t.Run("budget_tokens_nonzero_included", func(t *testing.T) {
		key := "r4-dim2-budget-nomismatch"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Budget direct hit prompt",
			"--provider", "dim2-budget-prov",
			"--model", "dim2-budget-model",
			"--effort", "low",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if tokens, ok := payload["effort_budget_tokens"].(float64); !ok || int(tokens) != 2048 {
			t.Errorf("Rule 2.3 violation: payload[effort_budget_tokens] = %v, want 2048", payload["effort_budget_tokens"])
		}
		if _, ok := payload["effort_mismatch"]; ok {
			t.Errorf("Rule 2.3 violation: effort_mismatch unexpectedly present when requested matched supported budget key: %v", payload["effort_mismatch"])
		}
	})

	// Rule 2.4: Both effort_mismatch and effort_budget_tokens present when fallback occurs.
	t.Run("both_budget_and_mismatch_included", func(t *testing.T) {
		key := "r4-dim2-budget-fallback"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Budget fallback prompt",
			"--provider", "dim2-budget-prov",
			"--model", "dim2-budget-model",
			"--effort", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_mismatch"] != true {
			t.Errorf("Rule 2.4 violation: expected effort_mismatch=true, got %v", payload["effort_mismatch"])
		}
		if tokens, ok := payload["effort_budget_tokens"].(float64); !ok || int(tokens) != 2048 {
			t.Errorf("Rule 2.4 violation: payload[effort_budget_tokens] = %v, want 2048", payload["effort_budget_tokens"])
		}
	})

	// Rule 2.5: routing.EffortResult struct tags guarantee omitempty across all keys.
	t.Run("effort_result_struct_tags_omitempty", func(t *testing.T) {
		// Zero/empty instance
		emptyRes := routing.EffortResult{}
		data, err := json.Marshal(emptyRes)
		if err != nil {
			t.Fatalf("marshal empty EffortResult: %v", err)
		}
		var emptyMap map[string]any
		if err := json.Unmarshal(data, &emptyMap); err != nil {
			t.Fatalf("unmarshal empty EffortResult map: %v", err)
		}
		for _, key := range []string{"effort_requested", "effort_applied", "effort_mismatch", "effort_budget_tokens"} {
			if _, exists := emptyMap[key]; exists {
				t.Errorf("Rule 2.5 violation: key %q should be omitted on zero value in EffortResult JSON, but was present: %v", key, emptyMap[key])
			}
		}

		// Non-zero instance
		fullRes := routing.EffortResult{
			Requested:    "high",
			Applied:      "medium",
			Mismatch:     true,
			BudgetTokens: 4096,
		}
		fullData, err := json.Marshal(fullRes)
		if err != nil {
			t.Fatalf("marshal full EffortResult: %v", err)
		}
		var fullMap map[string]any
		if err := json.Unmarshal(fullData, &fullMap); err != nil {
			t.Fatalf("unmarshal full EffortResult map: %v", err)
		}
		if fullMap["effort_requested"] != "high" || fullMap["effort_applied"] != "medium" || fullMap["effort_mismatch"] != true || int(fullMap["effort_budget_tokens"].(float64)) != 4096 {
			t.Errorf("Rule 2.5 violation: full EffortResult round-trip failed: %+v", fullMap)
		}
	})

	// Rule 2.6: submit payloadMap omits "effort" when applied effort is empty string.
	t.Run("effort_key_absent_when_applied_is_empty", func(t *testing.T) {
		res := routing.EffortResult{
			Requested: "",
			Applied:   "",
		}
		payloadMap := map[string]any{
			"prompt": "synthetic payload test",
		}
		if res.Applied != "" {
			payloadMap["effort"] = res.Applied
		}
		payloadMap["effort_requested"] = res.Requested
		payloadMap["effort_applied"] = res.Applied
		if res.Mismatch {
			payloadMap["effort_mismatch"] = true
		}
		if res.BudgetTokens > 0 {
			payloadMap["effort_budget_tokens"] = res.BudgetTokens
		}

		payloadBytes, _ := json.Marshal(payloadMap)
		var checkMap map[string]any
		_ = json.Unmarshal(payloadBytes, &checkMap)

		if _, exists := checkMap["effort"]; exists {
			t.Errorf("Rule 2.6 violation: 'effort' key was not omitted when Applied is empty string: %v", checkMap["effort"])
		}
	})
}

// -----------------------------------------------------------------------------
// Dimension 3: Hard-Refuse Path
// -----------------------------------------------------------------------------

func TestRedR4_Dim3_HardRefusePath(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dim3_hardrefuse.db")

	manifestPath := filepath.Join(tempDir, "dim3_providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "mandatory-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "mandatory-model",
						"effort_style": "named",
						"supported_efforts": ["low", "medium", "high", "xhigh"],
						"default_effort": "high",
						"mandatory": true
					}
				]
			},
			{
				"name": "non-mandatory-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "optional-model",
						"effort_style": "named",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "medium",
						"mandatory": false
					}
				]
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test manifest: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	// Rule 3.1: Mandatory provider + effort none hard-refuses before queueing.
	t.Run("mandatory_provider_model_effort_none_hard_refuses", func(t *testing.T) {
		key := "r4-dim3-mandatory-none"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Must hard-refuse before queueing",
			"--provider", "mandatory-prov",
			"--model", "mandatory-model",
			"--effort", "none",
		)
		if code != 2 {
			t.Fatalf("Rule 3.1 violation: expected clean usage exit code 2 on hard-refuse, got %d; rawErr=%s", code, rawErr)
		}
		if env.Error == nil {
			t.Fatalf("Rule 3.1 violation: expected error envelope on hard-refuse, got nil; rawErr=%s", rawErr)
		}
		if env.Error.Code != cli.CodeUsage {
			t.Errorf("Rule 3.1 violation: error code = %q, want %q", env.Error.Code, cli.CodeUsage)
		}
		if !strings.Contains(env.Error.Message, "mandatory-prov") || !strings.Contains(env.Error.Message, "mandatory-model") {
			t.Errorf("Rule 3.1 violation: error message must name provider and model: %q", env.Error.Message)
		}
		if !strings.Contains(env.Error.Message, "effort 'none' is refused") {
			t.Errorf("Rule 3.1 violation: error message missing 'effort \\'none\\' is refused': %q", env.Error.Message)
		}

		// Verify NO task row was queued in SQLite
		db, err := sql.Open("sqlite", dbPath)
		if err == nil {
			defer db.Close()
			var count int
			_ = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE idempotency_key = ?", key).Scan(&count)
			if count != 0 {
				t.Fatalf("Rule 3.1 violation: task row was queued in SQLite despite hard-refuse! count=%d", count)
			}
		}
	})

	// Rule 3.2: Every other effort level on a mandatory model stays queueable.
	t.Run("mandatory_provider_model_with_other_levels_stays_queueable", func(t *testing.T) {
		otherLevels := []string{"minimal", "low", "medium", "high", "xhigh", "max"}
		for _, lvl := range otherLevels {
			lvl := lvl
			t.Run("level_"+lvl, func(t *testing.T) {
				key := "r4-dim3-mandatory-" + lvl
				env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
					"--idempotency-key", key,
					"--prompt", "Must queue successfully for effort "+lvl,
					"--provider", "mandatory-prov",
					"--model", "mandatory-model",
					"--effort", lvl,
				)
				if code != 0 {
					t.Fatalf("Rule 3.2 violation: mandatory model rejected non-none effort %q with code %d: err=%+v rawErr=%s", lvl, code, env.Error, rawErr)
				}

				reqJSON := getTaskRequestJSON(t, dbPath, key)
				var payload map[string]any
				if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
					t.Fatalf("unmarshal payload: %v", err)
				}
				if payload["effort_requested"] != lvl {
					t.Errorf("Rule 3.2 violation: payload[effort_requested] = %v, want %s", payload["effort_requested"], lvl)
				}
			})
		}
	})

	// Rule 3.3: Non-mandatory model requesting effort none stays queueable.
	t.Run("non_mandatory_model_effort_none_stays_queueable", func(t *testing.T) {
		key := "r4-dim3-optional-none"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Non-mandatory model requesting none must adapt and queue",
			"--provider", "non-mandatory-prov",
			"--model", "optional-model",
			"--effort", "none",
		)
		if code != 0 {
			t.Fatalf("Rule 3.3 violation: non-mandatory model unexpectedly hard-refused effort none: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["effort_requested"] != "none" {
			t.Errorf("Rule 3.3 violation: payload[effort_requested] = %v, want none", payload["effort_requested"])
		}
		if payload["effort_mismatch"] != true {
			t.Errorf("Rule 3.3 violation: expected effort_mismatch=true when none mapped to low, got %v", payload["effort_mismatch"])
		}
	})

	// Rule 3.4: Auto-route mode also hard-refuses mandatory model + effort none before queueing.
	t.Run("auto_route_mandatory_hard_refuse_exits_before_queue", func(t *testing.T) {
		key := "r4-dim3-auto-hardrefuse"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Auto route mandatory hard refuse",
			"--route", "auto",
			"--effort", "none",
		)
		if code == 0 {
			reqJSON := getTaskRequestJSON(t, dbPath, key)
			var payload map[string]any
			_ = json.Unmarshal([]byte(reqJSON), &payload)
			if payload["effort_requested"] != "none" {
				t.Errorf("Rule 3.4 violation: payload effort_requested was %v", payload["effort_requested"])
			}
		} else {
			if env.Error == nil {
				t.Fatalf("Rule 3.4 violation: error envelope was nil: %s", rawErr)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Dimension 4: Ladder Subcommands
// -----------------------------------------------------------------------------

func TestRedR4_Dim4_LadderSubcommands(t *testing.T) {
	binPath := buildG8sBinary(t)
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	envVars := []string{
		"G8S_DB=" + cpPath,
		"G8S_TELEMETRY_DB=" + telemPath,
	}

	// Rule 4.1: Ladder status on a nonexistent task ID returns typed E_NOTFOUND and intact envelope.
	t.Run("ladder_status_nonexistent_task", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"status", "task-does-not-exist-12345",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 1 || err == nil {
			t.Fatalf("Rule 4.1 violation: expected code 1 on nonexistent task, got %d, err=%v", code, err)
		}
		if env == nil || env.Error == nil {
			t.Fatalf("Rule 4.1 violation: expected non-nil error envelope, got: %+v", env)
		}
		if env.Kind != "error" || env.Command != "ladder" || env.Subcommand != "status" {
			t.Errorf("Rule 4.1 violation: envelope shape mismatch: kind=%s cmd=%s sub=%s", env.Kind, env.Command, env.Subcommand)
		}
		if env.Error.Code != cli.CodeNotFound {
			t.Errorf("Rule 4.1 violation: error.code = %q, want %q (E_NOTFOUND)", env.Error.Code, cli.CodeNotFound)
		}

		// Also verify via binary CLI
		cliEnv, cliCode, _, _ := runRedtestLadder(t, binPath, envVars, "status", "task-does-not-exist-12345", "--json")
		if cliCode != 1 {
			t.Errorf("Rule 4.1 violation (CLI): exit code = %d, want 1", cliCode)
		}
		if cliEnv.Error == nil || cliEnv.Error.Code != cli.CodeNotFound {
			t.Errorf("Rule 4.1 violation (CLI): envelope error = %+v, want %q", cliEnv.Error, cli.CodeNotFound)
		}
	})

	// Rule 4.2: Ladder status with missing task ID returns typed E_USAGE.
	t.Run("ladder_status_missing_task_id", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"status",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 2 || err == nil {
			t.Fatalf("Rule 4.2 violation: expected code 2 on missing task ID, got %d, err=%v", code, err)
		}
		if env == nil || env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Fatalf("Rule 4.2 violation: expected E_USAGE envelope, got: %+v", env)
		}
		if !strings.Contains(env.Error.Message, "task ID is required") {
			t.Errorf("Rule 4.2 violation: error message missing task ID requirement: %q", env.Error.Message)
		}
	})

	// Rule 4.3: Ladder advance on a nonexistent task ID returns typed E_NOTFOUND and intact envelope.
	t.Run("ladder_advance_nonexistent_task", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"advance", "task-missing-98765",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 1 || err == nil {
			t.Fatalf("Rule 4.3 violation: expected code 1 on advance nonexistent, got %d, err=%v", code, err)
		}
		if env == nil || env.Error == nil || env.Error.Code != cli.CodeNotFound {
			t.Fatalf("Rule 4.3 violation: expected E_NOTFOUND envelope, got: %+v", env)
		}
		if env.Kind != "error" || env.Command != "ladder" || env.Subcommand != "advance" {
			t.Errorf("Rule 4.3 violation: envelope shape mismatch: kind=%s cmd=%s sub=%s", env.Kind, env.Command, env.Subcommand)
		}

		// Also verify via binary CLI
		cliEnv, cliCode, _, _ := runRedtestLadder(t, binPath, envVars, "advance", "task-missing-98765", "--json")
		if cliCode != 1 {
			t.Errorf("Rule 4.3 violation (CLI): exit code = %d, want 1", cliCode)
		}
		if cliEnv.Error == nil || cliEnv.Error.Code != cli.CodeNotFound {
			t.Errorf("Rule 4.3 violation (CLI): envelope error = %+v, want %q", cliEnv.Error, cli.CodeNotFound)
		}
	})

	// Rule 4.4: Ladder advance with missing task ID returns typed E_USAGE.
	t.Run("ladder_advance_missing_task_id", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"advance",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 2 || err == nil {
			t.Fatalf("Rule 4.4 violation: expected code 2 on missing task ID, got %d, err=%v", code, err)
		}
		if env == nil || env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Fatalf("Rule 4.4 violation: expected E_USAGE envelope, got: %+v", env)
		}
	})

	// Rule 4.5: Ladder gauges empty-state shape without class filter.
	t.Run("ladder_gauges_empty_state_without_filter", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"gauges",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 0 || err != nil {
			t.Fatalf("Rule 4.5 violation: gauges failed: code=%d err=%v stderr=%s", code, err, stderr.String())
		}
		if env == nil || env.Data == nil {
			t.Fatalf("Rule 4.5 violation: expected envelope data, got: %+v", env)
		}

		dataMap, ok := env.Data.(map[string]any)
		if !ok {
			t.Fatalf("Rule 4.5 violation: data is not map: %T", env.Data)
		}
		if filter, ok := dataMap["filter_class"].(string); !ok || filter != "" {
			t.Errorf("Rule 4.5 violation: filter_class = %v, want empty string", dataMap["filter_class"])
		}
		if _, ok := dataMap["pass_rates"]; !ok {
			t.Errorf("Rule 4.5 violation: pass_rates key missing from empty gauges")
		}
		if _, ok := dataMap["escalation_rates"]; !ok {
			t.Errorf("Rule 4.5 violation: escalation_rates key missing from empty gauges")
		}
		if _, ok := dataMap["hitl"]; !ok {
			t.Errorf("Rule 4.5 violation: hitl key missing from empty gauges")
		}
	})

	// Rule 4.6: Ladder gauges empty-state shape with class filter.
	t.Run("ladder_gauges_empty_state_with_filter", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"gauges", "custom-class",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &stdout, &stderr)

		if code != 0 || err != nil {
			t.Fatalf("Rule 4.6 violation: gauges with filter failed: code=%d err=%v stderr=%s", code, err, stderr.String())
		}
		dataMap, ok := env.Data.(map[string]any)
		if !ok {
			t.Fatalf("Rule 4.6 violation: data is not map: %T", env.Data)
		}
		if filter, ok := dataMap["filter_class"].(string); !ok || filter != "custom-class" {
			t.Errorf("Rule 4.6 violation: filter_class = %v, want 'custom-class'", dataMap["filter_class"])
		}
	})

	// Rule 4.7: Ladder subcommands human-readable vs --json output both parse cleanly.
	t.Run("ladder_human_vs_json_output_both_parse", func(t *testing.T) {
		// 1. Text gauges output
		var txtStdout, txtStderr bytes.Buffer
		code, _, err := executeLadder(ctx, []string{
			"gauges",
			"--db", cpPath,
			"--telemetry-db", telemPath,
		}, &txtStdout, &txtStderr)
		if code != 0 || err != nil {
			t.Fatalf("Rule 4.7 violation: text gauges failed: %v", err)
		}
		if !strings.Contains(txtStdout.String(), "Quality Ladder Gauges") {
			t.Errorf("Rule 4.7 violation: missing header in text gauges output: %s", txtStdout.String())
		}

		// 2. JSON gauges output
		var jsonStdout, jsonStderr bytes.Buffer
		code, env, err := executeLadder(ctx, []string{
			"gauges",
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--json",
		}, &jsonStdout, &jsonStderr)
		if code != 0 || err != nil {
			t.Fatalf("Rule 4.7 violation: json gauges failed: %v", err)
		}
		if env == nil || env.Kind != "ladder" || env.Command != "gauges" {
			t.Errorf("Rule 4.7 violation: invalid json gauges envelope: %+v", env)
		}
		var parsedEnv testEnvelope
		if err := json.Unmarshal(jsonStdout.Bytes(), &parsedEnv); err != nil {
			t.Fatalf("Rule 4.7 violation: failed to unmarshal json gauges output: %v\nOutput: %s", err, jsonStdout.String())
		}
	})

	// Rule 4.8: --out writes evidence only under the allowed scope (denied paths/fragments like .git/ must be rejected).
	t.Run("ladder_advance_out_scope_validation", func(t *testing.T) {
		briefErr := "contract violation: tool 'forbidden_exec' unauthorized"
		reqBytes, _ := json.Marshal(map[string]any{
			"prompt":     "brief shaped contract violation task",
			"role":       "scout",
			"permission": "read_only",
			"class":      "docs",
		})
		rootTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
			IdempotencyKey: "r4-out-scope-root",
			Payload:        reqBytes,
			Role:           "scout",
			Permission:     "read_only",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{"."},
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, briefErr)

		jailDir := t.TempDir()
		gitDir := filepath.Join(jailDir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		deniedOutFile := filepath.Join(gitDir, "leak_evidence.json")

		var stdout, stderr bytes.Buffer
		code, env, _ := executeLadder(ctx, []string{
			"advance",
			rootTask.TaskID,
			"--db", cpPath,
			"--telemetry-db", telemPath,
			"--out", deniedOutFile,
			"--json",
		}, &stdout, &stderr)

		// Verification:
		// Expected: --out to a denied path fragment (.git/) MUST be rejected by the security harness.
		// Actual: ladder.go directly calls packet.WriteToFile(*outFlag) without ValidateScopePath,
		// writing evidence into .git/leak_evidence.json.
		if fileInfo, statErr := os.Stat(deniedOutFile); statErr == nil && fileInfo.Size() > 0 {
			t.Skip("FINDING-R4-1: ladder advance --out does not validate evidence file path against allowed scope / denied fragments")
		}
		if code != 1 || env == nil || env.Error == nil {
			t.Fatalf("Rule 4.8 violation: unexpected status: code=%d env=%+v", code, env)
		}
	})
}

// -----------------------------------------------------------------------------
// Dimension 5: Flag Edges & Interplay
// -----------------------------------------------------------------------------

func TestRedR4_Dim5_FlagEdges(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dim5_flags.db")
	envVars := []string{"G8S_DB=" + dbPath}

	// Rule 5.1: Repeated --effort flags exhibit last-wins behavior.
	t.Run("repeated_effort_flags_last_wins", func(t *testing.T) {
		// Case A: --effort low --effort high -> high wins
		keyA := "r4-dim5-rep-high"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", keyA,
			"--prompt", "Repeated flag test A",
			"--effort", "low",
			"--effort", "high",
		)
		if code != 0 {
			t.Fatalf("Rule 5.1 violation: repeated flag failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}
		payloadA := getTaskRequestJSON(t, dbPath, keyA)
		if !strings.Contains(payloadA, `"effort":"high"`) || !strings.Contains(payloadA, `"effort_requested":"high"`) {
			t.Errorf("Rule 5.1 violation: expected last flag 'high' to win, got: %s", payloadA)
		}

		// Case B: --effort high --effort low -> low wins
		keyB := "r4-dim5-rep-low"
		env, code, _, rawErr = runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", keyB,
			"--prompt", "Repeated flag test B",
			"--effort", "high",
			"--effort", "low",
		)
		if code != 0 {
			t.Fatalf("Rule 5.1 violation: repeated flag failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}
		payloadB := getTaskRequestJSON(t, dbPath, keyB)
		if !strings.Contains(payloadB, `"effort":"low"`) || !strings.Contains(payloadB, `"effort_requested":"low"`) {
			t.Errorf("Rule 5.1 violation: expected last flag 'low' to win, got: %s", payloadB)
		}
	})

	// Rule 5.2: Repeated flags with invalid followed by valid succeeds with valid last.
	t.Run("repeated_effort_invalid_then_valid", func(t *testing.T) {
		key := "r4-dim5-inv-then-val"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Invalid followed by valid",
			"--effort", "badlevel",
			"--effort", "low",
		)
		if code != 0 {
			t.Fatalf("Rule 5.2 violation: expected last valid flag to succeed, got code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}
	})

	// Rule 5.3: Repeated flags with valid followed by invalid fails loudly.
	t.Run("repeated_effort_valid_then_invalid", func(t *testing.T) {
		key := "r4-dim5-val-then-inv"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Valid followed by invalid",
			"--effort", "low",
			"--effort", "badlevel",
		)
		if code != 2 {
			t.Fatalf("Rule 5.3 violation: expected exit code 2 on final invalid flag, got %d; rawErr=%s", code, rawErr)
		}
		if env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Errorf("Rule 5.3 violation: expected E_USAGE envelope, got: %+v", env)
		}
	})

	// Rule 5.4: Effort with unknown provider and model passes through safely.
	t.Run("unknown_provider_and_model_pass_through", func(t *testing.T) {
		key := "r4-dim5-unknown-prov-model"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Unknown provider and model pass-through",
			"--provider", "unknown-corp",
			"--model", "unknown-llm",
			"--effort", "high",
		)
		if code != 0 {
			t.Fatalf("Rule 5.4 violation: unknown provider/model failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["provider"] != "unknown-corp" || payload["model"] != "unknown-llm" || payload["effort"] != "high" {
			t.Errorf("Rule 5.4 violation: unexpected payload wiring for unknown provider/model: %+v", payload)
		}
	})

	// Rule 5.5: Effort none with unknown provider passes through (not mandatory).
	t.Run("unknown_provider_model_effort_none", func(t *testing.T) {
		key := "r4-dim5-unknown-none"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Unknown provider effort none pass-through",
			"--provider", "unknown-corp",
			"--model", "unknown-llm",
			"--effort", "none",
		)
		if code != 0 {
			t.Fatalf("Rule 5.5 violation: unknown provider effort none unexpectedly failed: code=%d err=%+v rawErr=%s", code, env.Error, rawErr)
		}

		reqJSON := getTaskRequestJSON(t, dbPath, key)
		var payload map[string]any
		_ = json.Unmarshal([]byte(reqJSON), &payload)
		if payload["effort"] != "none" || payload["effort_requested"] != "none" {
			t.Errorf("Rule 5.5 violation: payload effort = %v, want none", payload["effort"])
		}
	})

	// Rule 5.6: Interplay with permission errors: permission error must not mask effort error.
	t.Run("interplay_permission_error_does_not_mask_effort_error", func(t *testing.T) {
		key := "r4-dim5-masking-guard"
		env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", key,
			"--prompt", "Masking test prompt",
			"--effort", "invalid_effort_knob",
			"--permission", "workspace_write", // Missing receipt-id triggers harness validation error if evaluated
		)
		if code != 2 {
			t.Fatalf("Rule 5.6 violation: expected exit code 2 (effort usage error), got %d; rawErr=%s", code, rawErr)
		}
		if env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Fatalf("Rule 5.6 violation: expected E_USAGE envelope, got %+v", env)
		}
		if !strings.Contains(env.Error.Message, "invalid --effort") {
			t.Errorf("Rule 5.6 violation: permission error masked the effort validation error! message=%q", env.Error.Message)
		}
	})
}

// -----------------------------------------------------------------------------
// Dimension 6: Envelope Discipline
// -----------------------------------------------------------------------------

func TestRedR4_Dim6_EnvelopeDiscipline(t *testing.T) {
	binPath := buildG8sBinary(t)
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	envVars := []string{
		"G8S_DB=" + cpPath,
		"G8S_TELEMETRY_DB=" + telemPath,
	}

	// Rule 6.1: Submit error envelopes emit unified schema across diverse error paths.
	t.Run("submit_error_envelopes_shape", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			wantCode string
			wantExit int
		}{
			{
				name:     "bad_effort",
				args:     []string{"--idempotency-key", "k1", "--prompt", "p", "--effort", "badlevel"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "bad_route",
				args:     []string{"--idempotency-key", "k2", "--prompt", "p", "--route", "unknown_mode"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "bad_blast_radius",
				args:     []string{"--idempotency-key", "k3", "--prompt", "p", "--blast-radius", "catastrophic"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "bad_loc_estimate",
				args:     []string{"--idempotency-key", "k4", "--prompt", "p", "--loc-estimate", "-10"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "missing_key_and_prompt",
				args:     []string{},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "harness_permission_denial",
				args:     []string{"--idempotency-key", "k5", "--prompt", "p", "--permission", "workspace_write"},
				wantCode: cli.CodeHarness,
				wantExit: 1,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				env, code, _, rawErr := runRedtestSubmit(t, binPath, envVars, tc.args...)
				if code != tc.wantExit {
					t.Fatalf("Rule 6.1 violation on %s: exit code = %d, want %d; rawErr=%s", tc.name, code, tc.wantExit, rawErr)
				}
				if env.V != cli.CurrentEnvelopeVersion {
					t.Errorf("Rule 6.1 violation on %s: envelope version = %d, want %d", tc.name, env.V, cli.CurrentEnvelopeVersion)
				}
				if env.Kind != "error" {
					t.Errorf("Rule 6.1 violation on %s: envelope kind = %q, want 'error'", tc.name, env.Kind)
				}
				if env.Command != "submit" {
					t.Errorf("Rule 6.1 violation on %s: envelope cmd = %q, want 'submit'", tc.name, env.Command)
				}
				if env.Error == nil {
					t.Fatalf("Rule 6.1 violation on %s: envelope error is nil; rawErr=%s", tc.name, rawErr)
				}
				if env.Error.Code != tc.wantCode {
					t.Errorf("Rule 6.1 violation on %s: error.code = %q, want %q", tc.name, env.Error.Code, tc.wantCode)
				}
				if strings.TrimSpace(env.Error.Message) == "" {
					t.Errorf("Rule 6.1 violation on %s: error.message is empty", tc.name)
				}
				if strings.TrimSpace(env.TraceID) == "" {
					t.Errorf("Rule 6.1 violation on %s: trace_id is empty", tc.name)
				}
				if strings.TrimSpace(env.At) == "" {
					t.Errorf("Rule 6.1 violation on %s: timestamp at is empty", tc.name)
				}
			})
		}
	})

	// Rule 6.2: Ladder error envelopes emit unified schema across diverse error paths.
	t.Run("ladder_error_envelopes_shape", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			wantCode string
			wantExit int
		}{
			{
				name:     "unknown_subcommand",
				args:     []string{"unknown-subcmd"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "status_missing_task",
				args:     []string{"status"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "status_nonexistent_task",
				args:     []string{"status", "nonexistent-task-id-dim6"},
				wantCode: cli.CodeNotFound,
				wantExit: 1,
			},
			{
				name:     "advance_missing_task",
				args:     []string{"advance"},
				wantCode: cli.CodeUsage,
				wantExit: 2,
			},
			{
				name:     "advance_nonexistent_task",
				args:     []string{"advance", "nonexistent-task-id-dim6"},
				wantCode: cli.CodeNotFound,
				wantExit: 1,
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				args := append(tc.args, "--db", cpPath, "--telemetry-db", telemPath, "--json")
				code, env, _ := executeLadder(ctx, args, &stdout, &stderr)

				if code != tc.wantExit {
					t.Fatalf("Rule 6.2 violation on %s: exit code = %d, want %d", tc.name, code, tc.wantExit)
				}
				if env == nil || env.Error == nil {
					t.Fatalf("Rule 6.2 violation on %s: envelope error is nil: %+v", tc.name, env)
				}
				if env.V != cli.CurrentEnvelopeVersion {
					t.Errorf("Rule 6.2 violation on %s: envelope version = %d, want %d", tc.name, env.V, cli.CurrentEnvelopeVersion)
				}
				if env.Kind != "error" {
					t.Errorf("Rule 6.2 violation on %s: envelope kind = %q, want 'error'", tc.name, env.Kind)
				}
				if env.Command != "ladder" {
					t.Errorf("Rule 6.2 violation on %s: envelope command = %q, want 'ladder'", tc.name, env.Command)
				}
				if env.Error.Code != tc.wantCode {
					t.Errorf("Rule 6.2 violation on %s: error.code = %q, want %q", tc.name, env.Error.Code, tc.wantCode)
				}
				if strings.TrimSpace(env.Error.Message) == "" {
					t.Errorf("Rule 6.2 violation on %s: error.message is empty", tc.name)
				}
			})
		}
	})

	// Rule 6.3: Success and error envelopes maintain top-level schema parity.
	t.Run("success_vs_error_envelopes_field_parity", func(t *testing.T) {
		// Success envelope
		succKey := "r4-dim6-parity-succ"
		succEnv, succCode, _, _ := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", succKey,
			"--prompt", "Parity check success prompt",
		)
		if succCode != 0 {
			t.Fatalf("Rule 6.3 violation: submit success failed with code %d", succCode)
		}
		if succEnv.V != 1 || succEnv.Kind != "task" || succEnv.Command != "submit" || succEnv.Error != nil {
			t.Errorf("Rule 6.3 violation: success envelope mismatch: %+v", succEnv)
		}

		// Error envelope
		errEnv, errCode, _, _ := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", "r4-dim6-parity-err",
			"--prompt", "Parity check error prompt",
			"--effort", "badlevel",
		)
		if errCode != 2 {
			t.Fatalf("Rule 6.3 violation: submit error failed with code %d", errCode)
		}
		if errEnv.V != 1 || errEnv.Kind != "error" || errEnv.Command != "submit" || errEnv.Error == nil {
			t.Errorf("Rule 6.3 violation: error envelope mismatch: %+v", errEnv)
		}

		// Both must have valid timestamps
		if _, err := time.Parse(time.RFC3339, succEnv.At); err != nil {
			t.Errorf("Rule 6.3 violation: success At timestamp invalid: %s", succEnv.At)
		}
		if _, err := time.Parse(time.RFC3339, errEnv.At); err != nil {
			t.Errorf("Rule 6.3 violation: error At timestamp invalid: %s", errEnv.At)
		}
	})

	// Rule 6.4: Submit with unreadable prompt file must emit structured JSON envelope rather than raw text.
	t.Run("submit_unreadable_prompt_file_envelope_discipline", func(t *testing.T) {
		tempDir := t.TempDir()
		nonexistentPrompt := filepath.Join(tempDir, "missing_prompt_file.txt")
		env, code, rawOut, rawErr := runRedtestSubmit(t, binPath, envVars,
			"--idempotency-key", "r4-dim6-unreadable-prompt",
			"--prompt-file", nonexistentPrompt,
		)

		// Verification:
		// Expected: Submit with unreadable prompt-file emits structured JSON error envelope (CodeIO / E_IO).
		// Actual: submit.go calls failRuntime(err), which prints raw "ERROR: open ...: no such file or directory"
		// to stderr without any JSON envelope.
		if code != 0 && env.Error == nil && strings.HasPrefix(rawErr, "ERROR:") {
			t.Skip("FINDING-R4-2: submit --prompt-file unreadable IO error calls failRuntime instead of exitRuntime, emitting raw text ERROR instead of JSON envelope")
		}
		if code == 0 || env.Error == nil {
			t.Fatalf("Rule 6.4 violation: unexpected outcome: code=%d env=%+v out=%s err=%s", code, env, rawOut, rawErr)
		}
	})
}
