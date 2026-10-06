package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
)

// TestSubmitEffort_DefaultMediumLandsInPayload asserts that omitting --effort
// defaults to "medium" and records effort, effort_requested, and effort_applied in payload.
func TestSubmitEffort_DefaultMediumLandsInPayload(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "default_effort.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "default-effort-task",
		"--prompt", "Task prompt without explicit effort flag",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "default-effort-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["effort"] != "medium" {
		t.Errorf("effort = %v, want medium", payload["effort"])
	}
	if payload["effort_requested"] != "medium" {
		t.Errorf("effort_requested = %v, want medium", payload["effort_requested"])
	}
	if payload["effort_applied"] != "medium" {
		t.Errorf("effort_applied = %v, want medium", payload["effort_applied"])
	}
}

// TestSubmitEffort_BadLevel_UsageError asserts that an effort outside the canonical ladder
// triggers a typed usage exit naming the allowed ladder.
func TestSubmitEffort_BadLevel_UsageError(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "bad_effort.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	badLevels := []string{"invalid", "superhigh", "turbo", "fast", ""}
	for _, lvl := range badLevels {
		t.Run("bad_level_"+lvl, func(t *testing.T) {
			env, code, raw := runSubmitCLI(t, binPath, envVars,
				"--idempotency-key", "bad-effort-"+lvl,
				"--prompt", "Test invalid effort flag",
				"--effort", lvl,
			)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (E_USAGE); raw: %s", code, raw)
			}
			if env.Error == nil {
				t.Fatalf("expected error envelope, got nil; raw: %s", raw)
			}
			if env.Error.Code != cli.CodeUsage {
				t.Errorf("error.code = %q, want %q (E_USAGE)", env.Error.Code, cli.CodeUsage)
			}
			if !strings.Contains(env.Error.Message, "invalid --effort") {
				t.Errorf("expected error message to mention 'invalid --effort', got %q", env.Error.Message)
			}
			if !strings.Contains(env.Error.Message, "allowed ladder") {
				t.Errorf("expected error message to name allowed ladder, got %q", env.Error.Message)
			}
		})
	}
}

// TestSubmitEffort_AppliedRecorded asserts that effort_applied and advisory signals
// (effort_mismatch, effort_budget_tokens) are recorded in the payload when the adapter maps levels.
func TestSubmitEffort_AppliedRecorded(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "applied.db")

	manifestPath := filepath.Join(tempDir, "providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "toggle-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "toggle-model",
						"effort_style": "toggle",
						"supported_efforts": ["medium", "high"],
						"default_effort": "medium"
					}
				]
			},
			{
				"name": "budget-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "budget-model",
						"effort_style": "budget",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "medium",
						"effort_budget_map": {
							"low": 2048,
							"high": 32768
						}
					}
				]
			},
			{
				"name": "named-prov",
				"class": "platform_dispatch",
				"models": [
					{
						"id": "named-model",
						"effort_style": "named",
						"supported_efforts": ["low", "high"],
						"default_effort": "low"
					}
				]
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test providers.json: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	// 1. Toggle model requested low -> maps to nearest medium with mismatch=true
	t.Run("toggle_nearest_mismatch", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "toggle-task",
			"--prompt", "Test toggle nearest mapping",
			"--provider", "toggle-prov",
			"--model", "toggle-model",
			"--effort", "low",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "toggle-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["effort"] != "medium" {
			t.Errorf("effort = %v, want medium", payload["effort"])
		}
		if payload["effort_requested"] != "low" {
			t.Errorf("effort_requested = %v, want low", payload["effort_requested"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("effort_applied = %v, want medium", payload["effort_applied"])
		}
		if payload["effort_mismatch"] != true {
			t.Errorf("effort_mismatch = %v, want true", payload["effort_mismatch"])
		}
	})

	// 2. Budget model requested medium (missing key) -> maps to nearest low with tokens=2048 and mismatch=true
	t.Run("budget_nearest_tokens_recorded", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "budget-task",
			"--prompt", "Test budget nearest mapping with tokens",
			"--provider", "budget-prov",
			"--model", "budget-model",
			"--effort", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "budget-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["effort"] != "low" {
			t.Errorf("effort = %v, want low", payload["effort"])
		}
		if payload["effort_requested"] != "medium" {
			t.Errorf("effort_requested = %v, want medium", payload["effort_requested"])
		}
		if payload["effort_applied"] != "low" {
			t.Errorf("effort_applied = %v, want low", payload["effort_applied"])
		}
		if payload["effort_mismatch"] != true {
			t.Errorf("effort_mismatch = %v, want true", payload["effort_mismatch"])
		}
		if tokens, ok := payload["effort_budget_tokens"].(float64); !ok || int(tokens) != 2048 {
			t.Errorf("effort_budget_tokens = %v, want 2048", payload["effort_budget_tokens"])
		}
	})

	// 3. Named model requested medium (distance to low and high equal) -> tie picks lower (low)
	t.Run("named_tie_picks_lower", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "named-task",
			"--prompt", "Test named tie-picks-lower",
			"--provider", "named-prov",
			"--model", "named-model",
			"--effort", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "named-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["effort"] != "low" {
			t.Errorf("effort = %v, want low", payload["effort"])
		}
		if payload["effort_requested"] != "medium" {
			t.Errorf("effort_requested = %v, want medium", payload["effort_requested"])
		}
		if payload["effort_applied"] != "low" {
			t.Errorf("effort_applied = %v, want low", payload["effort_applied"])
		}
		if payload["effort_mismatch"] != true {
			t.Errorf("effort_mismatch = %v, want true", payload["effort_mismatch"])
		}
	})
}

// TestSubmitEffort_HardRefuse_ExitsBeforeQueueing asserts that requesting effort "none"
// on a mandatory-reasoning model hard-refuses locally and exits BEFORE queuing any task in SQLite.
func TestSubmitEffort_HardRefuse_ExitsBeforeQueueing(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "hard_refuse.db")

	manifestPath := filepath.Join(tempDir, "providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "refuse-prov",
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
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test providers.json: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "hard-refuse-task",
		"--prompt", "Must fail and not queue task",
		"--provider", "refuse-prov",
		"--model", "mandatory-model",
		"--effort", "none",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code on hard-refuse, got %d; raw: %s", code, raw)
	}
	if env.Error == nil {
		t.Fatalf("expected error envelope on hard-refuse, got nil; raw: %s", raw)
	}
	if !strings.Contains(env.Error.Message, "mandatory") || !strings.Contains(env.Error.Message, "refused") {
		t.Errorf("unexpected error message: %q", env.Error.Message)
	}

	// Verify no task was queued in the database
	db, err := sql.Open("sqlite", dbPath)
	if err == nil {
		defer db.Close()
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE idempotency_key = 'hard-refuse-task'").Scan(&count)
		if count > 0 {
			t.Fatalf("task was queued despite hard-refuse; expected zero queued tasks")
		}
	}
}

// TestSubmitEffort_SiblingRegistryWins asserts that dispatches into a sibling repo
// resolve the sibling's effort-classes.yml registry instead of defaulting to unregistered.
func TestSubmitEffort_SiblingRegistryWins(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sibling_effort.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	sibDir := t.TempDir()
	g8sDir := filepath.Join(sibDir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sibClasses := `schema_version: "effort-classes.v1"
classes:
  - name: sibling-docs
    default_effort: low
    priority: 10
    paths:
      - "docs/**"
`
	if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(sibClasses), 0o600); err != nil {
		t.Fatal(err)
	}

	docFile := filepath.Join(sibDir, "docs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(docFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docFile, []byte("# Guide"), 0o644); err != nil {
		t.Fatal(err)
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "sibling-task",
		"--prompt", "Doc audit task in sibling repo",
		"--scope-root", sibDir,
		"--add-dir", docFile,
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "sibling-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["effort_class"] != "sibling-docs" {
		t.Errorf("effort_class = %v, want sibling-docs", payload["effort_class"])
	}
	if payload["effort"] != "low" {
		t.Errorf("effort = %v, want low", payload["effort"])
	}
}

// TestSubmitEffort_SiblingRegistryMalformed_FailsLoudly asserts that an explicitly
// malformed sibling registry fails submit loudly (non-zero exit code + typed error).
func TestSubmitEffort_SiblingRegistryMalformed_FailsLoudly(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sibling_malformed.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	sibDir := t.TempDir()
	g8sDir := filepath.Join(sibDir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brokenYAML := `schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: not-a-number
    paths: ["docs/**"]
`
	if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(brokenYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	docFile := filepath.Join(sibDir, "docs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(docFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docFile, []byte("# Guide"), 0o644); err != nil {
		t.Fatal(err)
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "broken-sibling-task",
		"--prompt", "Doc task with broken sibling registry",
		"--scope-root", sibDir,
		"--add-dir", docFile,
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code for broken sibling registry, got 0; raw: %s", raw)
	}
	if env.Error == nil {
		t.Fatalf("expected error envelope on malformed registry, got nil; raw: %s", raw)
	}
	if !strings.Contains(env.Error.Message, "effort classes") {
		t.Errorf("expected error message to mention 'effort classes', got %q", env.Error.Message)
	}
}

// TestSubmitEffort_SiblingWithoutRegistry_FallsBackToRepoRegistry asserts that
// dispatches into a sibling without its own registry fall back to the repo registry.
func TestSubmitEffort_SiblingWithoutRegistry_FallsBackToRepoRegistry(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sibling_noreg.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	sibDir := t.TempDir()
	codeFile := filepath.Join(sibDir, "src", "worker.go")
	if err := os.MkdirAll(filepath.Dir(codeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codeFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "noreg-sibling-task",
		"--prompt", "Worker code in sibling without registry",
		"--scope-root", sibDir,
		"--add-dir", codeFile,
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "noreg-sibling-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Falls back to repo registry -> external Go code is unregistered / medium
	if payload["effort_class"] != "unregistered" {
		t.Errorf("effort_class = %v, want unregistered", payload["effort_class"])
	}
	if payload["effort"] != "medium" {
		t.Errorf("effort = %v, want medium", payload["effort"])
	}
}

// TestSubmitEffort_BakedNameAlignmentMatrix tests the 3 matrix branches:
// 1. Agree / Pass-Through (baked-name model matching effort)
// 2. Disagree / Rewrite + Record (baked-name model differing from effort)
// 3. Non-Baked Pass-Through (provider is not baked-name)
func TestSubmitEffort_BakedNameAlignmentMatrix(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "baked_alignment.db")

	manifestPath := filepath.Join(tempDir, "providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "agy",
				"class": "platform_dispatch",
				"effort_style": "baked-name",
				"models": [
					{
						"id": "gemini-3.8-flash",
						"effort_style": "baked-name",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "high"
					}
				]
			},
			{
				"name": "named-prov",
				"class": "platform_dispatch",
				"effort_style": "named",
				"models": [
					{
						"id": "named-model-low",
						"effort_style": "named",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "low"
					}
				]
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test providers.json: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	// 1. Agree / Pass-Through: gemini-3.8-flash-medium with --effort medium
	t.Run("agree_pass_through", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "baked-agree-task",
			"--prompt", "Test agree pass through",
			"--provider", "agy",
			"--model", "gemini-3.8-flash-medium",
			"--effort", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "baked-agree-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["model"] != "gemini-3.8-flash-medium" {
			t.Errorf("model = %v, want gemini-3.8-flash-medium", payload["model"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("effort_applied = %v, want medium", payload["effort_applied"])
		}
		if _, exists := payload["model_requested"]; exists {
			t.Errorf("model_requested should not exist on agree, got %v", payload["model_requested"])
		}
		if payload["model_realigned"] == true {
			t.Errorf("model_realigned should not be true on agree")
		}
	})

	// 2. Disagree / Rewrite + Record: gemini-3.8-flash-low with --effort medium (dogfood Ground Truth)
	t.Run("disagree_rewrite_and_record", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "baked-disagree-task",
			"--prompt", "Test disagree rewrite and record",
			"--provider", "agy",
			"--model", "gemini-3.8-flash-low",
			"--effort", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "baked-disagree-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["model"] != "gemini-3.8-flash-medium" {
			t.Errorf("rewritten model = %v, want gemini-3.8-flash-medium", payload["model"])
		}
		if payload["model_requested"] != "gemini-3.8-flash-low" {
			t.Errorf("model_requested = %v, want gemini-3.8-flash-low", payload["model_requested"])
		}
		if payload["model_realigned"] != true {
			t.Errorf("model_realigned = %v, want true", payload["model_realigned"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("effort_applied = %v, want medium", payload["effort_applied"])
		}

		// Verify that payload stored in SQLite request_json has rewritten model and requested model
		if payload["model"] != "gemini-3.8-flash-medium" {
			t.Errorf("task payload model = %v, want gemini-3.8-flash-medium", payload["model"])
		}
	})

	// 2b. Default model (gemini-3.8-flash-high) with default effort (medium) realigns to medium
	t.Run("default_model_and_effort_realigns", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "default-realign-task",
			"--prompt", "Test default model and effort realigns",
			"--provider", "agy",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "default-realign-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["model"] != "gemini-3.8-flash-medium" {
			t.Errorf("rewritten model = %v, want gemini-3.8-flash-medium", payload["model"])
		}
		if payload["model_requested"] != "gemini-3.8-flash-high" {
			t.Errorf("model_requested = %v, want gemini-3.8-flash-high", payload["model_requested"])
		}
		if payload["model_realigned"] != true {
			t.Errorf("model_realigned = %v, want true", payload["model_realigned"])
		}
	})

	// 3. Non-Baked Pass-Through: named provider model with -low suffix does NOT rewrite
	t.Run("non_baked_pass_through", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "non-baked-task",
			"--prompt", "Test non-baked pass through",
			"--provider", "named-prov",
			"--model", "named-model-low",
			"--effort", "high",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "non-baked-task")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["model"] != "named-model-low" {
			t.Errorf("model = %v, want named-model-low", payload["model"])
		}
		if _, exists := payload["model_requested"]; exists {
			t.Errorf("model_requested should not exist on non-baked provider, got %v", payload["model_requested"])
		}
		if payload["model_realigned"] == true {
			t.Errorf("model_realigned should not be true on non-baked provider")
		}
		if payload["effort_applied"] != "high" {
			t.Errorf("effort_applied = %v, want high", payload["effort_applied"])
		}
	})
}

func TestBakedNameAlignmentHelpers(t *testing.T) {
	// 1. parseBakedModelSuffix
	tests := []struct {
		model    string
		wantBase string
		wantLvl  string
		wantOk   bool
	}{
		{"gemini-3.8-flash-low", "gemini-3.8-flash", "low", true},
		{"gemini-3.8-flash-medium", "gemini-3.8-flash", "medium", true},
		{"gemini-3.8-flash-high", "gemini-3.8-flash", "high", true},
		{"gemini-3.8-flash-xhigh", "gemini-3.8-flash", "xhigh", true},
		{"gemini-3.8-flash", "", "", false},
		{"gpt-5.5", "", "", false},
		{"claude-3-opus", "", "", false},
	}
	for _, tt := range tests {
		base, lvl, ok := parseBakedModelSuffix(tt.model)
		if ok != tt.wantOk || base != tt.wantBase || lvl != tt.wantLvl {
			t.Errorf("parseBakedModelSuffix(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.model, base, lvl, ok, tt.wantBase, tt.wantLvl, tt.wantOk)
		}
	}

	// 2. isBakedNameProvider
	if !isBakedNameProvider(nil, "agy", "gemini-3.8-flash-low") {
		t.Errorf("expected isBakedNameProvider for agy to be true")
	}
	// Provider-less submits decide from the model id alone (issue #568): the
	// catalog carries the agy baked-name variants, so flash-low resolves true.
	if !isBakedNameProvider(nil, "", "gemini-3.8-flash-low") {
		t.Errorf("expected isBakedNameProvider for empty provider with a catalog baked id to be true")
	}
	if isBakedNameProvider(nil, "openai", "gpt-5.5-low") {
		t.Errorf("expected isBakedNameProvider for openai to be false")
	}
}

// TestSubmitEffort_BadBlastAndLoc_UsageError asserts that invalid --blast-radius
// or non-positive --loc-estimate trigger typed usage exits naming allowed values.
func TestSubmitEffort_BadBlastAndLoc_UsageError(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "bad_signals.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	badBlasts := []string{"invalid", "extreme", "superhigh", ""}
	for _, b := range badBlasts {
		t.Run("bad_blast_"+b, func(t *testing.T) {
			env, code, raw := runSubmitCLI(t, binPath, envVars,
				"--idempotency-key", "bad-blast-"+b,
				"--prompt", "Test invalid blast flag",
				"--blast-radius", b,
			)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (E_USAGE); raw: %s", code, raw)
			}
			if env.Error == nil {
				t.Fatalf("expected error envelope, got nil; raw: %s", raw)
			}
			if env.Error.Code != cli.CodeUsage {
				t.Errorf("error.code = %q, want %q (E_USAGE)", env.Error.Code, cli.CodeUsage)
			}
			if !strings.Contains(env.Error.Message, "invalid --blast-radius") {
				t.Errorf("expected error message to mention 'invalid --blast-radius', got %q", env.Error.Message)
			}
			if !strings.Contains(env.Error.Message, "low, medium, high") {
				t.Errorf("expected error message to mention allowed values, got %q", env.Error.Message)
			}
		})
	}

	badLocs := []string{"0", "-1", "-100"}
	for _, l := range badLocs {
		t.Run("bad_loc_"+l, func(t *testing.T) {
			env, code, raw := runSubmitCLI(t, binPath, envVars,
				"--idempotency-key", "bad-loc-"+l,
				"--prompt", "Test invalid loc flag",
				"--loc-estimate", l,
			)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (E_USAGE); raw: %s", code, raw)
			}
			if env.Error == nil {
				t.Fatalf("expected error envelope, got nil; raw: %s", raw)
			}
			if env.Error.Code != cli.CodeUsage {
				t.Errorf("error.code = %q, want %q (E_USAGE)", env.Error.Code, cli.CodeUsage)
			}
			if !strings.Contains(env.Error.Message, "invalid --loc-estimate") {
				t.Errorf("expected error message to mention 'invalid --loc-estimate', got %q", env.Error.Message)
			}
		})
	}
}

// TestSubmitEffort_NoSignals_ByteIdenticalPayload asserts that when no signals are declared,
// the resulting payload contains no effort_source or effort_signals, preserving byte-identical
// legacy output.
func TestSubmitEffort_NoSignals_ByteIdenticalPayload(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "no_signals.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "no-signals-task",
		"--prompt", "Task without signals declared",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "no-signals-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if _, exists := payload["effort_source"]; exists {
		t.Errorf("effort_source should not exist when no signals declared, got %v", payload["effort_source"])
	}
	if _, exists := payload["effort_signals"]; exists {
		t.Errorf("effort_signals should not exist when no signals declared, got %v", payload["effort_signals"])
	}
}

// TestSubmitEffort_SignalPrecedenceAndRecording verifies the precedence chain in submit:
// explicit wins over declared, declared wins over registry, registry over floor,
// and all suggestions are recorded in effort_signals.
func TestSubmitEffort_SignalPrecedenceAndRecording(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "signals_prec.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	// 1. Explicit wins over declared
	t.Run("explicit_wins_over_declared", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "explicit-over-declared",
			"--prompt", "Explicit wins over declared",
			"--effort", "high",
			"--blast-radius", "low",
			"--loc-estimate", "20",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "explicit-over-declared")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_source"] != "explicit" {
			t.Errorf("effort_source = %v, want explicit", payload["effort_source"])
		}
		if payload["effort_applied"] != "high" {
			t.Errorf("effort_applied = %v, want high", payload["effort_applied"])
		}
		sigMap, ok := payload["effort_signals"].(map[string]any)
		if !ok {
			t.Fatalf("effort_signals missing or not object: %v", payload["effort_signals"])
		}
		if sigMap["blast_radius"] != "low" {
			t.Errorf("blast_radius = %v, want low", sigMap["blast_radius"])
		}
		locVal, _ := sigMap["loc_estimate"].(float64)
		if int(locVal) != 20 {
			t.Errorf("loc_estimate = %v, want 20", sigMap["loc_estimate"])
		}
		if sigMap["declared_suggestion"] != "low" {
			t.Errorf("declared_suggestion = %v, want low", sigMap["declared_suggestion"])
		}
		if sigMap["override_down"] != false {
			t.Errorf("override_down = %v, want false (high > low)", sigMap["override_down"])
		}
	})

	// 2. Declared wins over registry
	t.Run("declared_wins_over_registry", func(t *testing.T) {
		// Set up sibling repo with docs class (default effort low)
		sibDir := t.TempDir()
		g8sDir := filepath.Join(sibDir, ".g8s")
		if err := os.MkdirAll(g8sDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sibClasses := `schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["docs/**"]
`
		if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(sibClasses), 0o600); err != nil {
			t.Fatal(err)
		}
		docFile := filepath.Join(sibDir, "docs", "arch.md")
		if err := os.MkdirAll(filepath.Dir(docFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docFile, []byte("# Arch"), 0o644); err != nil {
			t.Fatal(err)
		}

		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "declared-over-registry",
			"--prompt", "Declared high wins over registry low",
			"--scope-root", sibDir,
			"--add-dir", docFile,
			"--blast-radius", "high",
			"--loc-estimate", "250",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "declared-over-registry")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_source"] != "declared" {
			t.Errorf("effort_source = %v, want declared", payload["effort_source"])
		}
		if payload["effort_applied"] != "high" {
			t.Errorf("effort_applied = %v, want high", payload["effort_applied"])
		}
		sigMap, ok := payload["effort_signals"].(map[string]any)
		if !ok {
			t.Fatalf("effort_signals missing or not object: %v", payload["effort_signals"])
		}
		if sigMap["declared_suggestion"] != "high" {
			t.Errorf("declared_suggestion = %v, want high", sigMap["declared_suggestion"])
		}
		if sigMap["registry_suggestion"] != "low" {
			t.Errorf("registry_suggestion = %v, want low", sigMap["registry_suggestion"])
		}
	})

	// 3. Registry wins over floor when declared signal has no opinion (blast=medium)
	t.Run("registry_wins_over_floor_on_neutral_signal", func(t *testing.T) {
		sibDir := t.TempDir()
		g8sDir := filepath.Join(sibDir, ".g8s")
		if err := os.MkdirAll(g8sDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sibClasses := `schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["docs/**"]
`
		if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(sibClasses), 0o600); err != nil {
			t.Fatal(err)
		}
		docFile := filepath.Join(sibDir, "docs", "arch.md")
		if err := os.MkdirAll(filepath.Dir(docFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docFile, []byte("# Arch"), 0o644); err != nil {
			t.Fatal(err)
		}

		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "registry-over-floor-neutral",
			"--prompt", "Registry stands when signal is neutral",
			"--scope-root", sibDir,
			"--add-dir", docFile,
			"--blast-radius", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "registry-over-floor-neutral")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_source"] != "registry" {
			t.Errorf("effort_source = %v, want registry", payload["effort_source"])
		}
		if payload["effort_applied"] != "low" {
			t.Errorf("effort_applied = %v, want low", payload["effort_applied"])
		}
		sigMap, ok := payload["effort_signals"].(map[string]any)
		if !ok {
			t.Fatalf("effort_signals missing or not object: %v", payload["effort_signals"])
		}
		if sigMap["registry_suggestion"] != "low" {
			t.Errorf("registry_suggestion = %v, want low", sigMap["registry_suggestion"])
		}
		if sigMap["declared_suggestion"] != "" {
			t.Errorf("declared_suggestion = %v, want empty", sigMap["declared_suggestion"])
		}
	})

	// 4. Floor wins when unregistered and declared signal is neutral
	t.Run("floor_wins_when_unregistered_and_neutral_signal", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "floor-on-neutral",
			"--prompt", "Floor stands when unregistered and neutral signal",
			"--blast-radius", "medium",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "floor-on-neutral")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_source"] != "floor" {
			t.Errorf("effort_source = %v, want floor", payload["effort_source"])
		}
		if payload["effort_applied"] != "medium" {
			t.Errorf("effort_applied = %v, want medium", payload["effort_applied"])
		}
	})
}

// TestSubmitEffort_OverrideDownRecomputedAgainstWinningSource verifies that
// override_down is recomputed against the winning source, not the registry alone.
func TestSubmitEffort_OverrideDownRecomputedAgainstWinningSource(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "override_down_recomputed.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	// Sibling repo with docs class (low) and core class (high)
	sibDir := t.TempDir()
	g8sDir := filepath.Join(sibDir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sibClasses := `schema_version: "effort-classes.v1"
classes:
  - name: docs
    default_effort: low
    priority: 10
    paths: ["docs/**"]
  - name: core
    default_effort: high
    priority: 10
    paths: ["src/**"]
`
	if err := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(sibClasses), 0o600); err != nil {
		t.Fatal(err)
	}
	docFile := filepath.Join(sibDir, "docs", "arch.md")
	coreFile := filepath.Join(sibDir, "src", "engine.go")
	_ = os.MkdirAll(filepath.Dir(docFile), 0o755)
	_ = os.MkdirAll(filepath.Dir(coreFile), 0o755)
	_ = os.WriteFile(docFile, []byte("# Doc"), 0o644)
	_ = os.WriteFile(coreFile, []byte("package engine"), 0o644)

	// Case A: explicit=low against declared=high on docs (registry=low)
	// Against registry alone (low), explicit low is NOT override down.
	// But against winning declared (high), it IS override down!
	t.Run("explicit_low_against_declared_high_on_registry_low", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "override-down-case-a",
			"--prompt", "Explicit low against declared high on docs",
			"--scope-root", sibDir,
			"--add-dir", docFile,
			"--effort", "low",
			"--blast-radius", "high",
			"--loc-estimate", "300",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "override-down-case-a")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_override_down"] != true {
			t.Errorf("effort_override_down = %v, want true", payload["effort_override_down"])
		}
		sigMap, ok := payload["effort_signals"].(map[string]any)
		if !ok {
			t.Fatalf("effort_signals missing or not object: %v", payload["effort_signals"])
		}
		if sigMap["override_down"] != true {
			t.Errorf("effort_signals.override_down = %v, want true", sigMap["override_down"])
		}
	})

	// Case B: explicit=medium against declared=low on core (registry=high)
	// Against registry alone (high), explicit medium would be override down.
	// But against winning declared (low), it is NOT override down!
	t.Run("explicit_medium_against_declared_low_on_registry_high", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "override-down-case-b",
			"--prompt", "Explicit medium against declared low on core",
			"--scope-root", sibDir,
			"--add-dir", coreFile,
			"--effort", "medium",
			"--blast-radius", "low",
			"--loc-estimate", "20",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "override-down-case-b")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		if payload["effort_override_down"] == true {
			t.Errorf("effort_override_down = true, want nil/false")
		}
		sigMap, ok := payload["effort_signals"].(map[string]any)
		if !ok {
			t.Fatalf("effort_signals missing or not object: %v", payload["effort_signals"])
		}
		if sigMap["override_down"] != false {
			t.Errorf("effort_signals.override_down = %v, want false", sigMap["override_down"])
		}
	})
}

// TestSubmitEffort_RealignmentConsumesWinningLevel asserts that baked-name model
// realignment rewrites to match the winning level decided by declared signals.
func TestSubmitEffort_RealignmentConsumesWinningLevel(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "realign_signal.db")

	manifestPath := filepath.Join(tempDir, "providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "agy",
				"class": "platform_dispatch",
				"effort_style": "baked-name",
				"models": [
					{
						"id": "gemini-3.8-flash",
						"effort_style": "baked-name",
						"supported_efforts": ["low", "medium", "high"],
						"default_effort": "high"
					}
				]
			}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write test providers.json: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + manifestPath,
	}

	// Model requested is default gemini-3.8-flash-high.
	// Declared signals say low: blast=low, loc=20.
	// Model should realign to gemini-3.8-flash-low with model_requested = gemini-3.8-flash-high.
	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "realign-declared-signal",
		"--prompt", "Test realignment consumes winning declared effort",
		"--provider", "agy",
		"--blast-radius", "low",
		"--loc-estimate", "20",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}
	reqJSON := getTaskRequestJSON(t, dbPath, "realign-declared-signal")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["effort_source"] != "declared" {
		t.Errorf("effort_source = %v, want declared", payload["effort_source"])
	}
	if payload["effort_applied"] != "low" {
		t.Errorf("effort_applied = %v, want low", payload["effort_applied"])
	}
	if payload["model"] != "gemini-3.8-flash-low" {
		t.Errorf("model = %v, want gemini-3.8-flash-low", payload["model"])
	}
	if payload["model_requested"] != "gemini-3.8-flash-high" {
		t.Errorf("model_requested = %v, want gemini-3.8-flash-high", payload["model_requested"])
	}
	if payload["model_realigned"] != true {
		t.Errorf("model_realigned = %v, want true", payload["model_realigned"])
	}
}
