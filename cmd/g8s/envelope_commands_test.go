package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
)

type testEnvelope struct {
	V          int             `json:"v"`
	Kind       string          `json:"kind"`
	Command    string          `json:"cmd"`
	Subcommand string          `json:"sub,omitempty"`
	Data       json.RawMessage `json:"data"`
	Error      *cli.ErrPayload `json:"error,omitempty"`
	TraceID    string          `json:"trace_id,omitempty"`
	At         string          `json:"at"`
}

func buildG8sBinary(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "g8s")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build g8s: %v\nOutput: %s", err, string(out))
	}
	return binPath
}

func TestUnifiedEnvelopeCommands(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "envelope-test.db")

	runCmd := func(args ...string) (testEnvelope, int, string) {
		cmd := exec.Command(binPath, args...)
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
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
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("failed to unmarshal envelope for %v: %v\nRaw output:\n%s", args, err, raw)
		}
		return env, exitCode, raw
	}

	t.Run("version JSON envelope", func(t *testing.T) {
		env, code, _ := runCmd("version", "--json", "--trace-id", "019154a1-0000-7000-8000-000000000001")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if env.V != 1 || env.Kind != "version" || env.Command != "version" {
			t.Errorf("unexpected headers: %+v", env)
		}
		if env.TraceID != "019154a1-0000-7000-8000-000000000001" {
			t.Errorf("TraceID = %q, want custom trace ID", env.TraceID)
		}
		var data map[string]any
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		if data["version"] != Version {
			t.Errorf("version = %v, want %q", data["version"], Version)
		}
	})

	t.Run("version JSONL envelope", func(t *testing.T) {
		env, code, raw := runCmd("version", "--jsonl")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if strings.Contains(raw, "\n") {
			t.Errorf("jsonl output should not contain newlines within single item: %q", raw)
		}
		if env.V != 1 || env.Kind != "version" || env.Command != "version" {
			t.Errorf("unexpected headers: %+v", env)
		}
		if env.TraceID == "" {
			t.Errorf("expected auto-generated trace ID")
		}
	})

	t.Run("roles JSON envelope", func(t *testing.T) {
		env, code, _ := runCmd("roles", "--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if env.V != 1 || env.Kind != "roles" || env.Command != "roles" {
			t.Errorf("unexpected headers: %+v", env)
		}
		var roles []map[string]any
		if err := json.Unmarshal(env.Data, &roles); err != nil {
			t.Fatalf("unmarshal roles data: %v", err)
		}
		if len(roles) == 0 {
			t.Errorf("expected non-empty roles list")
		}
	})

	t.Run("permissions JSON envelope", func(t *testing.T) {
		env, code, _ := runCmd("permissions", "--json")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if env.V != 1 || env.Kind != "permissions" || env.Command != "permissions" {
			t.Errorf("unexpected headers: %+v", env)
		}
	})

	t.Run("submit, get, tasks, cancel, lineage lifecycle", func(t *testing.T) {
		// Submit
		env, code, raw := runCmd("submit", "--idempotency-key", "test-key-1", "--prompt", "Implement unified envelope", "--role", "collector", "--permission", "read_only", "--actor", "lead-dev", "--model", "gemini-3.8-flash-high")
		if code != 0 {
			t.Fatalf("submit exit code = %d, want 0; error: %+v; raw: %s", code, env.Error, raw)
		}
		if env.V != 1 || env.Kind != "task" || env.Command != "submit" {
			t.Errorf("submit headers: %+v", env)
		}
		var submitData map[string]any
		if err := json.Unmarshal(env.Data, &submitData); err != nil {
			t.Fatalf("unmarshal submit data: %v", err)
		}
		taskID, ok := submitData["task_id"].(string)
		if !ok || taskID == "" {
			t.Fatalf("missing task_id in submit data: %+v", submitData)
		}

		// Get
		getEnv, getCode, _ := runCmd("get", "--task-id", taskID)
		if getCode != 0 {
			t.Fatalf("get exit code = %d, want 0", getCode)
		}
		if getEnv.V != 1 || getEnv.Kind != "task" || getEnv.Command != "get" {
			t.Errorf("get headers: %+v", getEnv)
		}
		var getData map[string]any
		if err := json.Unmarshal(getEnv.Data, &getData); err != nil {
			t.Fatalf("unmarshal get data: %v", err)
		}
		if getData["task_id"] != taskID {
			t.Errorf("task_id mismatch: %v vs %v", getData["task_id"], taskID)
		}

		// Tasks list
		tasksEnv, tasksCode, _ := runCmd("tasks")
		if tasksCode != 0 {
			t.Fatalf("tasks exit code = %d, want 0", tasksCode)
		}
		if tasksEnv.V != 1 || tasksEnv.Kind != "tasks" || tasksEnv.Command != "tasks" {
			t.Errorf("tasks headers: %+v", tasksEnv)
		}

		// Lineage
		linEnv, linCode, _ := runCmd("lineage", "--task-id", taskID)
		if linCode != 0 {
			t.Fatalf("lineage exit code = %d, want 0", linCode)
		}
		if linEnv.V != 1 || linEnv.Kind != "lineage" || linEnv.Command != "lineage" {
			t.Errorf("lineage headers: %+v", linEnv)
		}

		// Children
		childEnv, childCode, _ := runCmd("children", "--parent-task-id", taskID)
		if childCode != 0 {
			t.Fatalf("children exit code = %d, want 0", childCode)
		}
		if childEnv.V != 1 || childEnv.Kind != "children" || childEnv.Command != "children" {
			t.Errorf("children headers: %+v", childEnv)
		}

		// Cancel
		cancelEnv, cancelCode, _ := runCmd("cancel", "--task-id", taskID, "--reason", "spec changed")
		if cancelCode != 0 {
			t.Fatalf("cancel exit code = %d, want 0", cancelCode)
		}
		if cancelEnv.V != 1 || cancelEnv.Kind != "task" || cancelEnv.Command != "cancel" {
			t.Errorf("cancel headers: %+v", cancelEnv)
		}
		var cancelData map[string]any
		if err := json.Unmarshal(cancelEnv.Data, &cancelData); err != nil {
			t.Fatalf("unmarshal cancel data: %v", err)
		}
		if cancelData["cancelled"] != true {
			t.Errorf("cancelled = %v, want true", cancelData["cancelled"])
		}
	})

	t.Run("receipt lifecycle", func(t *testing.T) {
		// Receipt issue
		issEnv, issCode, issRaw := runCmd("receipt", "issue", "--path", "/workspace/test.go", "--actor", "receipt-agent")
		if issCode != 0 {
			t.Fatalf("receipt issue exit code = %d, want 0; raw: %s", issCode, issRaw)
		}
		if issEnv.V != 1 || issEnv.Kind != "receipt" || issEnv.Command != "receipt" || issEnv.Subcommand != "issue" {
			t.Errorf("receipt issue headers: %+v", issEnv)
		}
		var issData map[string]any
		if err := json.Unmarshal(issEnv.Data, &issData); err != nil {
			t.Fatalf("unmarshal receipt data: %v", err)
		}
		receiptID, ok := issData["receipt_id"].(string)
		if !ok || receiptID == "" {
			t.Fatalf("missing receipt_id in issData: %+v", issData)
		}
		if issData["issuer"] != "receipt-agent" {
			t.Errorf("issuer = %v, want receipt-agent", issData["issuer"])
		}

		// Receipt show
		showEnv, showCode, _ := runCmd("receipt", "show", "--receipt-id", receiptID)
		if showCode != 0 {
			t.Fatalf("receipt show exit code = %d, want 0", showCode)
		}
		if showEnv.V != 1 || showEnv.Kind != "receipt" || showEnv.Command != "receipt" || showEnv.Subcommand != "show" {
			t.Errorf("receipt show headers: %+v", showEnv)
		}

		// Receipt list
		listEnv, listCode, _ := runCmd("receipt", "list")
		if listCode != 0 {
			t.Fatalf("receipt list exit code = %d, want 0", listCode)
		}
		if listEnv.V != 1 || listEnv.Kind != "receipts" || listEnv.Command != "receipt" || listEnv.Subcommand != "list" {
			t.Errorf("receipt list headers: %+v", listEnv)
		}

		// Receipt verify
		verEnv, verCode, _ := runCmd("receipt", "verify", "--receipt-id", receiptID)
		if verCode != 0 {
			t.Fatalf("receipt verify exit code = %d, want 0", verCode)
		}
		if verEnv.V != 1 || verEnv.Kind != "receipt_verification" || verEnv.Command != "receipt" || verEnv.Subcommand != "verify" {
			t.Errorf("receipt verify headers: %+v", verEnv)
		}

		// Receipt revoke
		revEnv, revCode, _ := runCmd("receipt", "revoke", "--receipt-id", receiptID)
		if revCode != 0 {
			t.Fatalf("receipt revoke exit code = %d, want 0", revCode)
		}
		if revEnv.V != 1 || revEnv.Kind != "receipt" || revEnv.Command != "receipt" || revEnv.Subcommand != "revoke" {
			t.Errorf("receipt revoke headers: %+v", revEnv)
		}
	})

	t.Run("vault operations", func(t *testing.T) {
		// Store
		stEnv, stCode, stRaw := runCmd("vault", "store", "--id", "DELTA-99", "--title", "Envelope Test", "--package", "cli", "--file", "envelope.go", "--problem", "Unification", "--trade-off", "Structured envelope", "--root-cause", "Legacy output")
		if stCode != 0 {
			t.Fatalf("vault store exit code = %d, want 0; raw: %s", stCode, stRaw)
		}
		if stEnv.V != 1 || stEnv.Kind != "vault_record" || stEnv.Command != "vault" || stEnv.Subcommand != "store" {
			t.Errorf("vault store headers: %+v", stEnv)
		}

		// Get
		gtEnv, gtCode, _ := runCmd("vault", "get", "DELTA-99")
		if gtCode != 0 {
			t.Fatalf("vault get exit code = %d, want 0", gtCode)
		}
		if gtEnv.V != 1 || gtEnv.Kind != "vault_record" || gtEnv.Command != "vault" || gtEnv.Subcommand != "get" {
			t.Errorf("vault get headers: %+v", gtEnv)
		}

		// List
		lsEnv, lsCode, _ := runCmd("vault", "list")
		if lsCode != 0 {
			t.Fatalf("vault list exit code = %d, want 0", lsCode)
		}
		if lsEnv.V != 1 || lsEnv.Kind != "vault_records" || lsEnv.Command != "vault" || lsEnv.Subcommand != "list" {
			t.Errorf("vault list headers: %+v", lsEnv)
		}

		// Delete
		delEnv, delCode, _ := runCmd("vault", "delete", "DELTA-99")
		if delCode != 0 {
			t.Fatalf("vault delete exit code = %d, want 0", delCode)
		}
		if delEnv.V != 1 || delEnv.Kind != "vault_delete" || delEnv.Command != "vault" || delEnv.Subcommand != "delete" {
			t.Errorf("vault delete headers: %+v", delEnv)
		}
	})

	t.Run("config operations", func(t *testing.T) {
		// Set
		setEnv, setCode, _ := runCmd("config", "set", "default_model", "gemini-3.7-flash", "--json")
		if setCode != 0 {
			t.Fatalf("config set exit code = %d, want 0", setCode)
		}
		if setEnv.V != 1 || setEnv.Kind != "config" || setEnv.Command != "config" || setEnv.Subcommand != "set" {
			t.Errorf("config set headers: %+v", setEnv)
		}

		// Get
		getEnv, getCode, _ := runCmd("config", "get", "default_model", "--json")
		if getCode != 0 {
			t.Fatalf("config get exit code = %d, want 0", getCode)
		}
		if getEnv.V != 1 || getEnv.Kind != "config" || getEnv.Command != "config" || getEnv.Subcommand != "get" {
			t.Errorf("config get headers: %+v", getEnv)
		}

		// List
		listEnv, listCode, _ := runCmd("config", "list", "--json")
		if listCode != 0 {
			t.Fatalf("config list exit code = %d, want 0", listCode)
		}
		if listEnv.V != 1 || listEnv.Kind != "config" || listEnv.Command != "config" || listEnv.Subcommand != "list" {
			t.Errorf("config list headers: %+v", listEnv)
		}

		// Unset
		unsetEnv, unsetCode, _ := runCmd("config", "unset", "default_model", "--json")
		if unsetCode != 0 {
			t.Fatalf("config unset exit code = %d, want 0", unsetCode)
		}
		if unsetEnv.V != 1 || unsetEnv.Kind != "config" || unsetEnv.Command != "config" || unsetEnv.Subcommand != "unset" {
			t.Errorf("config unset headers: %+v", unsetEnv)
		}
	})

	t.Run("doctor envelope", func(t *testing.T) {
		docEnv, _, _ := runCmd("doctor", "--json")
		if docEnv.V != 1 || docEnv.Kind != "doctor_report" || docEnv.Command != "doctor" {
			t.Errorf("doctor headers: %+v", docEnv)
		}
	})

	t.Run("doctor attention check envelope", func(t *testing.T) {
		docEnv, docCode, _ := runCmd("doctor", "--attention-check", "--actor", "attn-worker")
		if docCode != 0 {
			t.Fatalf("doctor attention-check exit code = %d, want 0", docCode)
		}
		if docEnv.V != 1 || docEnv.Kind != "attention_check" || docEnv.Command != "doctor" || docEnv.Subcommand != "attention-check" {
			t.Errorf("doctor attention check headers: %+v", docEnv)
		}
		var data map[string]any
		if err := json.Unmarshal(docEnv.Data, &data); err != nil {
			t.Fatalf("unmarshal attention check data: %v", err)
		}
		qs, ok := data["questions"].([]any)
		if !ok || len(qs) != 5 {
			t.Fatalf("expected 5 questions in attention check, got %+v", data)
		}
	})

	t.Run("init envelope", func(t *testing.T) {
		initEnv, initCode, _ := runCmd("init", "--agent", "--json")
		if initCode != 0 {
			t.Fatalf("init exit code = %d, want 0", initCode)
		}
		if initEnv.V != 1 || initEnv.Kind != "init_result" || initEnv.Command != "init" {
			t.Errorf("init headers: %+v", initEnv)
		}
	})

	t.Run("completion envelope", func(t *testing.T) {
		compEnv, compCode, _ := runCmd("completion", "bash", "--json")
		if compCode != 0 {
			t.Fatalf("completion exit code = %d, want 0", compCode)
		}
		if compEnv.V != 1 || compEnv.Kind != "completion" || compEnv.Command != "completion" {
			t.Errorf("completion headers: %+v", compEnv)
		}
	})

	t.Run("service status envelope", func(t *testing.T) {
		srvEnv, srvCode, _ := runCmd("service", "status", "--json")
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			if srvCode != 0 {
				t.Fatalf("service status exit code = %d, want 0", srvCode)
			}
			if srvEnv.V != 1 || srvEnv.Kind != "service_status" || srvEnv.Command != "service" || srvEnv.Subcommand != "status" {
				t.Errorf("service status headers: %+v", srvEnv)
			}
		} else {
			if srvEnv.V != 1 || srvEnv.Kind != "error" || srvEnv.Command != "service" {
				t.Errorf("service status unsupported headers: %+v", srvEnv)
			}
		}
	})

	t.Run("analyze envelope", func(t *testing.T) {
		analyzeTestFile := filepath.Join(tempDir, "sample.go")
		_ = os.WriteFile(analyzeTestFile, []byte("package sample\nfunc Foo() {}\n"), 0o600)
		anEnv, anCode, anRaw := runCmd("analyze", "--file", analyzeTestFile, "--root", tempDir)
		if anCode != 0 {
			t.Fatalf("analyze exit code = %d, want 0; raw: %s", anCode, anRaw)
		}
		if anEnv.V != 1 || anEnv.Kind != "blast_radius_report" || anEnv.Command != "analyze" {
			t.Errorf("analyze headers: %+v", anEnv)
		}
	})

	t.Run("status envelope", func(t *testing.T) {
		statEnv, statCode, _ := runCmd("status", "--json")
		if statCode != 0 {
			t.Fatalf("status exit code = %d, want 0", statCode)
		}
		if statEnv.V != 1 || statEnv.Kind != "status_report" || statEnv.Command != "status" {
			t.Errorf("status headers: %+v", statEnv)
		}
	})

	t.Run("cleanup envelope", func(t *testing.T) {
		clEnv, clCode, clRaw := runCmd("cleanup", "--target", "stale-receipt", "--dry-run", "--json")
		if clCode != 0 {
			t.Fatalf("cleanup exit code = %d, want 0; raw: %s", clCode, clRaw)
		}
		if clEnv.V != 1 || clEnv.Kind != "cleanup_report" || clEnv.Command != "cleanup" {
			t.Errorf("cleanup headers: %+v", clEnv)
		}
	})

	t.Run("cleanup-worktrees envelope", func(t *testing.T) {
		cwtEnv, cwtCode, cwtRaw := runCmd("cleanup-worktrees", "--dry-run", "--json")
		if cwtCode != 0 {
			t.Fatalf("cleanup-worktrees exit code = %d, want 0; raw: %s", cwtCode, cwtRaw)
		}
		if cwtEnv.V != 1 || cwtEnv.Kind != "cleanup_report" || cwtEnv.Command != "cleanup-worktrees" {
			t.Errorf("cleanup-worktrees headers: %+v", cwtEnv)
		}
	})

	t.Run("supervisor-metrics envelope", func(t *testing.T) {
		smEnv, smCode, _ := runCmd("supervisor-metrics", "--aggregate", "--json")
		if smCode != 0 {
			t.Fatalf("supervisor-metrics exit code = %d, want 0", smCode)
		}
		if smEnv.V != 1 || smEnv.Kind != "supervisor_metrics" || smEnv.Command != "supervisor-metrics" {
			t.Errorf("supervisor-metrics headers: %+v", smEnv)
		}
	})

	t.Run("backward-compatibility data field wrapper", func(t *testing.T) {
		// Verify submit -> get -> tasks unmarshaling into wrapper struct
		subEnv, subCode, _ := runCmd("submit", "--idempotency-key", "compat-key-1", "--prompt", "Compat check", "--role", "collector", "--permission", "read_only", "--model", "gemini-3.8-flash-high")
		if subCode != 0 {
			t.Fatalf("submit failed: %d", subCode)
		}
		var wrapper struct {
			Data struct {
				TaskID string `json:"task_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(subEnv.Data), &wrapper.Data); err != nil {
			t.Fatalf("unmarshal wrapper.data: %v", err)
		}
		if wrapper.Data.TaskID == "" {
			t.Errorf("expected task_id inside data field")
		}
	})

	t.Run("error envelope on usage error exits with 2", func(t *testing.T) {
		env, code, _ := runCmd("get")
		if code != 2 {
			t.Fatalf("usage error exit code = %d, want 2", code)
		}
		if env.V != 1 || env.Kind != "error" || env.Command != "get" {
			t.Errorf("usage error headers: %+v", env)
		}
		if env.Error == nil || env.Error.Code != cli.CodeUsage {
			t.Errorf("expected E_USAGE code, got: %+v", env.Error)
		}
	})

	t.Run("error envelope on runtime error exits with 1", func(t *testing.T) {
		env, code, _ := runCmd("get", "--task-id", "task-nonexistent-12345")
		if code != 1 {
			t.Fatalf("runtime error exit code = %d, want 1", code)
		}
		if env.V != 1 || env.Kind != "error" || env.Command != "get" {
			t.Errorf("runtime error headers: %+v", env)
		}
		if env.Error == nil || env.Error.Code != cli.CodeNotFound {
			t.Errorf("expected E_NOTFOUND code, got: %+v", env.Error)
		}
	})

	t.Run("doctor --tdd-trap-check returns envelope", func(t *testing.T) {
		env, code, _ := runCmd("doctor", "--tdd-trap-check", "--json")
		if code != 0 {
			t.Fatalf("clean repo tdd trap exit code = %d, want 0", code)
		}
		if env.Command != "doctor" || env.Subcommand != "tdd-trap-check" {
			t.Errorf("expected cmd=doctor sub=tdd-trap-check, got %s/%s", env.Command, env.Subcommand)
		}
	})

	t.Run("doctor --anti-pattern-catalog returns envelope", func(t *testing.T) {
		env, code, _ := runCmd("doctor", "--anti-pattern-catalog", "--json")
		if code != 0 {
			t.Fatalf("anti-pattern catalog exit code = %d, want 0", code)
		}
		if env.Command != "doctor" || env.Subcommand != "anti-pattern-catalog" || env.Kind != "anti_pattern_catalog" {
			t.Errorf("expected cmd=doctor sub=anti-pattern-catalog kind=anti_pattern_catalog, got %s/%s kind=%s", env.Command, env.Subcommand, env.Kind)
		}
		var data struct {
			Rules        []map[string]any `json:"rules"`
			TotalRules   int              `json:"total_rules"`
			Last24hFires int              `json:"last_24h_fires"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("unmarshal catalog data: %v", err)
		}
		if data.TotalRules != 11 || len(data.Rules) != 11 {
			t.Errorf("expected 11 rules, got total_rules=%d, len=%d", data.TotalRules, len(data.Rules))
		}
	})
}
