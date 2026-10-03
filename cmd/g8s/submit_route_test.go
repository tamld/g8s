package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/tamld/g8s/internal/cli"
)

func runSubmitCLI(t *testing.T, binPath string, envVars []string, args ...string) (testEnvelope, int, string) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"submit", "--json"}, args...)...)
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

func getTaskRequestJSON(t *testing.T, dbPath string, idempotencyKey string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db %s: %v", dbPath, err)
	}
	defer db.Close()

	var reqJSON string
	err = db.QueryRow("SELECT request_json FROM tasks WHERE idempotency_key = ?", idempotencyKey).Scan(&reqJSON)
	if err != nil {
		t.Fatalf("query request_json for key %s: %v", idempotencyKey, err)
	}
	return reqJSON
}

// TestSubmitRoute_GoldenByteIdentity tests requirement 1 and 4:
// With --route absent vs --route manual, request_json stored in SQLite is byte-for-byte identical.
func TestSubmitRoute_GoldenByteIdentity(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "golden.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_ROUTER_MODE=",
		"TYPESAFE_API_KEYS=",
	}

	// Submit task without --route flag
	envAbsent, codeAbsent, rawAbsent := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "golden-absent",
		"--prompt", "Perform codebase sanity verification",
		"--actor", "golden-tester",
		"--permission", "read_only",
		"--timeout", "45s",
	)
	if codeAbsent != 0 {
		t.Fatalf("submit (route absent) failed: code=%d err=%+v raw=%s", codeAbsent, envAbsent.Error, rawAbsent)
	}

	// Submit task with explicit --route manual
	envManual, codeManual, rawManual := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "golden-manual",
		"--prompt", "Perform codebase sanity verification",
		"--actor", "golden-tester",
		"--permission", "read_only",
		"--timeout", "45s",
		"--route", "manual",
	)
	if codeManual != 0 {
		t.Fatalf("submit (route manual) failed: code=%d err=%+v raw=%s", codeManual, envManual.Error, rawManual)
	}

	jsonAbsent := getTaskRequestJSON(t, dbPath, "golden-absent")
	jsonManual := getTaskRequestJSON(t, dbPath, "golden-manual")

	if !bytes.Equal([]byte(jsonAbsent), []byte(jsonManual)) {
		t.Fatalf("golden byte identity proof failed:\nabsent: %s\nmanual: %s", jsonAbsent, jsonManual)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(jsonAbsent), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Verify legacy payload fields are intact and no router fields leak into manual mode
	if payload["prompt"] != "Perform codebase sanity verification" {
		t.Errorf("prompt = %v, want expected prompt", payload["prompt"])
	}
	if payload["model"] != "gemini-3.8-flash-high" {
		t.Errorf("model = %v, want gemini-3.8-flash-high", payload["model"])
	}
	if payload["role"] != "collector" {
		t.Errorf("role = %v, want collector", payload["role"])
	}
	if _, ok := payload["route_source"]; ok {
		t.Errorf("route_source must NOT be present in manual mode: %v", payload["route_source"])
	}
	if _, ok := payload["route_reason"]; ok {
		t.Errorf("route_reason must NOT be present in manual mode: %v", payload["route_reason"])
	}
}

// TestSubmitRoute_Table tests requirement 4:
// - docs-paths payload -> summarizer class
// - code payload -> default class
// - trust-boundary payload -> test-runner class
// - --route absent -> byte-identical request_json to today
func TestSubmitRoute_Table(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "table.db")

	manifestPath := filepath.Join(tempDir, "providers.json")
	manifestJSON := `{
		"providers": [
			{
				"name": "agy",
				"class": "platform_dispatch",
				"models": [
					{"id": "gemini-3.8-flash-high", "context_window": 1000000}
				]
			},
			{
				"name": "claude",
				"class": "platform_dispatch",
				"models": [
					{"id": "claude-haiku-4-5", "context_window": 200000}
				]
			},
			{
				"name": "ollama",
				"class": "platform_dispatch",
				"models": [
					{"id": "llama3.1", "context_window": 128000}
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
		"G8S_ROUTER_MODE=",
		"TYPESAFE_API_KEYS=",
	}

	tests := []struct {
		name         string
		key          string
		prompt       string
		routeFlag    string
		extraArgs    []string
		wantRole     string
		wantProvider string
		wantModel    string
		wantSource   string
		checkBytesTo string // key of another task to assert byte identity with
	}{
		{
			name:         "docs-paths payload routes to summarizer class (cheapest model: claude-haiku-4-5)",
			key:          "tbl-docs",
			prompt:       "Review and summarize markdown documentation",
			routeFlag:    "auto",
			extraArgs:    []string{"--add-dir", "docs"},
			wantRole:     "summarizer",
			wantProvider: "claude",
			wantModel:    "claude-haiku-4-5",
			wantSource:   "deterministic",
		},
		{
			name:         "code payload routes to default class (agy/gemini-3.8-flash-high/collector)",
			key:          "tbl-code",
			prompt:       "Refactor command line options",
			routeFlag:    "auto",
			extraArgs:    []string{"--add-dir", "cmd/g8s"},
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantSource:   "deterministic",
		},
		{
			name:         "trust-boundary path routes to test-runner class with largest context window",
			key:          "tbl-trust",
			prompt:       "Verify security verification in receipts",
			routeFlag:    "auto",
			extraArgs:    []string{"--add-dir", "internal/receipt"},
			wantRole:     "test-runner",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantSource:   "deterministic",
		},
		{
			name:         "empty-paths default payload routes to manifest default",
			key:          "tbl-empty",
			prompt:       "General maintenance job",
			routeFlag:    "auto",
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantSource:   "deterministic",
		},
		{
			name:       "--route absent defaults to manual without router fields",
			key:        "tbl-absent",
			prompt:     "Legacy submit behavior",
			wantRole:   "collector",
			wantModel:  "gemini-3.8-flash-high",
			wantSource: "", // none
		},
		{
			name:         "--route manual is byte-identical to --route absent",
			key:          "tbl-manual",
			prompt:       "Legacy submit behavior",
			routeFlag:    "manual",
			wantRole:     "collector",
			wantModel:    "gemini-3.8-flash-high",
			wantSource:   "", // none
			checkBytesTo: "tbl-absent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{
				"--idempotency-key", tc.key,
				"--prompt", tc.prompt,
			}
			if tc.routeFlag != "" {
				args = append(args, "--route", tc.routeFlag)
			}
			args = append(args, tc.extraArgs...)

			env, code, raw := runSubmitCLI(t, binPath, envVars, args...)
			if code != 0 {
				t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
			}

			reqJSON := getTaskRequestJSON(t, dbPath, tc.key)
			var payload map[string]any
			if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}

			if tc.wantRole != "" && payload["role"] != tc.wantRole {
				t.Errorf("role = %v, want %q", payload["role"], tc.wantRole)
			}
			if tc.wantProvider != "" && payload["provider"] != tc.wantProvider {
				t.Errorf("provider = %v, want %q", payload["provider"], tc.wantProvider)
			}
			if tc.wantModel != "" && payload["model"] != tc.wantModel {
				t.Errorf("model = %v, want %q", payload["model"], tc.wantModel)
			}
			if tc.wantSource != "" {
				if payload["route_source"] != tc.wantSource {
					t.Errorf("route_source = %v, want %q", payload["route_source"], tc.wantSource)
				}
				if reason, ok := payload["route_reason"].(string); !ok || reason == "" {
					t.Errorf("expected non-empty route_reason, got %v", payload["route_reason"])
				}
			} else {
				if _, ok := payload["route_source"]; ok {
					t.Errorf("expected no route_source in manual/absent route mode")
				}
			}

			if tc.checkBytesTo != "" {
				targetJSON := getTaskRequestJSON(t, dbPath, tc.checkBytesTo)
				if reqJSON != targetJSON {
					t.Errorf("byte mismatch between %s and %s:\ncurrent: %s\ntarget:  %s", tc.key, tc.checkBytesTo, reqJSON, targetJSON)
				}
			}
		})
	}
}

// TestSubmitRoute_DeterminismGuard asserts requirement 3:
// With G8S_ROUTER_MODE unset and no keys, auto routing produces Source=deterministic
// with zero network calls (verified by setting TYPESAFE_ENDPOINT to an unreachable address).
func TestSubmitRoute_DeterminismGuard(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "guard.db")

	// Even if TYPESAFE_ENDPOINT points to a dead socket, zero network calls must occur
	// because G8S_ROUTER_MODE is unset and no keys are supplied.
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_ROUTER_MODE=",
		"TYPESAFE_API_KEYS=",
		"TYPESAFE_API_KEY=",
		"TYPESAFE_ENDPOINT=http://127.0.0.1:1", // dead port: network attempt would immediately fail/refuse
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "guard-task",
		"--prompt", "Evaluate task placement with determinism guard",
		"--route", "auto",
		"--add-dir", "docs",
	)
	if code != 0 {
		t.Fatalf("submit failed under determinism guard: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "guard-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["route_source"] != "deterministic" {
		t.Fatalf("route_source = %v, want deterministic", payload["route_source"])
	}
	if reason, ok := payload["route_reason"].(string); !ok || !strings.Contains(reason, "documentation") {
		t.Fatalf("route_reason = %v, want documentation rule match", payload["route_reason"])
	}
}

// TestSubmitRoute_InvalidFlagValue tests error envelope on invalid --route values.
func TestSubmitRoute_InvalidFlagValue(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "err.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	for _, invalid := range []string{"invalid", "unknown", "smart", "ai"} {
		t.Run("invalid_flag_"+invalid, func(t *testing.T) {
			env, code, raw := runSubmitCLI(t, binPath, envVars,
				"--idempotency-key", "err-task",
				"--prompt", "Testing invalid route",
				"--route", invalid,
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
			if !strings.Contains(env.Error.Message, "invalid --route") {
				t.Errorf("unexpected error message: %q", env.Error.Message)
			}
		})
	}
}

// TestSubmitRoute_CorruptManifest_NoFallback asserts requirement 2:
// An auto route error emits a clean error envelope and never silently falls back to manual.
func TestSubmitRoute_CorruptManifest_NoFallback(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "corrupt.db")

	brokenManifestPath := filepath.Join(tempDir, "broken_providers.json")
	if err := os.WriteFile(brokenManifestPath, []byte(`{"providers": "not-an-array"}`), 0o644); err != nil {
		t.Fatalf("write broken providers.json: %v", err)
	}

	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + brokenManifestPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "corrupt-task",
		"--prompt", "Must fail loudly and not fall back to manual",
		"--route", "auto",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code on corrupt manifest, got %d (raw: %s)", code, raw)
	}
	if env.Error == nil {
		t.Fatalf("expected error envelope on corrupt manifest; raw: %s", raw)
	}

	// Verify no task was queued in the database
	db, err := sql.Open("sqlite", dbPath)
	if err == nil {
		defer db.Close()
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE idempotency_key = 'corrupt-task'").Scan(&count)
		if count > 0 {
			t.Fatalf("task was queued despite corrupt manifest; expected zero queued tasks")
		}
	}
}
