package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/mcp"
	"github.com/tamld/g8s/internal/mesh"
)

func meshCLI(t *testing.T, tier string, args ...string) (testEnvelope, error) {
	t.Helper()
	cmd := exec.Command(buildG8sBinary(t), append([]string{"coordinator", "mesh"}, args...)...)
	for _, kv := range isolatedEnv() {
		if !strings.HasPrefix(kv, "G8S_TIER=") && !strings.HasPrefix(kv, "G8S_MESH_DIR=") && !strings.HasPrefix(kv, "G8S_STRATUM=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "G8S_TIER="+tier, "G8S_MESH_DIR=", "G8S_STRATUM=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	response := stdout.Bytes()
	if err != nil {
		if stdout.Len() != 0 {
			t.Fatalf("failed CLI command wrote success output: %s", stdout.String())
		}
		response = stderr.Bytes()
	}
	var env testEnvelope
	if decErr := json.Unmarshal(response, &env); decErr != nil {
		t.Fatalf("decode CLI response: %v; stdout=%s stderr=%s", decErr, stdout.String(), stderr.String())
	}
	return env, err
}

func TestCoordinatorMeshCLIF0Gate(t *testing.T) {
	for _, tier := range []string{"F1", "F2", " f1 "} {
		t.Run(tier, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "must-not-exist")
			env, err := meshCLI(t, tier, "--mesh-dir", dir, "--op", "snapshot", "--json")
			if err == nil || env.Error == nil || env.Error.Code != cli.CodeDenied || !strings.Contains(env.Error.Message, "F0-only") {
				t.Fatalf("worker accepted: error=%v response=%+v", err, env)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("denied command touched ledger directory: %v", err)
			}
		})
	}
	for _, tier := range []string{"F0", ""} {
		env, err := meshCLI(t, tier, "--mesh-dir", t.TempDir(), "--op", "snapshot", "--json")
		if err != nil || env.Error != nil || env.Kind != "coordinator_mesh" {
			t.Fatalf("F0/default denied: %v %+v", err, env)
		}
	}
}

func TestCoordinatorMeshCLIMeshDirFailClosed(t *testing.T) {
	t.Run("missing-explicit-directory", func(t *testing.T) {
		env, err := meshCLI(t, "F0", "--op", "snapshot", "--json")
		if err == nil || env.Error == nil || !strings.Contains(env.Error.Message, "--mesh-dir is required") {
			t.Fatalf("missing directory accepted: %v %+v", err, env)
		}
	})
	t.Run("file-instead-of-directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		env, err := meshCLI(t, "F0", "--mesh-dir", filepath.Join(path, "mesh"), "--op", "snapshot", "--json")
		if err == nil || env.Error == nil || !strings.Contains(env.Error.Message, "create dir") {
			t.Fatalf("unusable directory accepted: %v %+v", err, env)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "unchanged" {
			t.Fatalf("blocking file changed: %q %v", got, err)
		}
	})
	t.Run("unwritable-directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX directory modes do not enforce Windows ACLs")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		probe := filepath.Join(dir, "write-probe")
		if err := os.WriteFile(probe, nil, 0o600); err == nil {
			_ = os.Remove(probe)
			t.Skip("current user bypasses directory write permissions")
		} else if !os.IsPermission(err) {
			t.Fatal(err)
		}
		env, err := meshCLI(t, "F0", "--mesh-dir", dir, "--op", "snapshot", "--json")
		if err == nil || env.Error == nil || !strings.Contains(env.Error.Message, "init schema") {
			t.Fatalf("unwritable directory accepted: %v %+v", err, env)
		}
		if _, err := os.Stat(filepath.Join(dir, "ledger.db")); !os.IsNotExist(err) {
			t.Fatalf("failed operation created ledger: %v", err)
		}
	})
}

// Exercise real CLI parsing, the public MCP stdio transport, and the core seam
// on equivalent ledgers. Only wall-clock fields are normalized for comparison.
func TestCoordinatorMeshCLIAndMCPAllOperationsParity(t *testing.T) {
	t.Setenv("G8S_TIER", "F0")
	t.Setenv("G8S_STRATUM", "")
	cliDir, mcpDir := t.TempDir(), t.TempDir()
	core, err := mesh.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	steps := []struct{ op, input string }{
		{"register", `{"node_id":"a","incarnation":"i"}`},
		{"register", `{"node_id":"b","incarnation":"j"}`},
		{"quota-publish", `{"node_id":"a","incarnation":"i","observation":{"source":"test","observed_at":"2026-01-01T00:00:00Z","windows":[{"name":"hour","window_minutes":60,"used_percent":25,"remaining_percent":75,"status":"observed"}]}}`},
		{"send", `{"node_id":"a","incarnation":"i","message_id":"m","to_node":"b","to_incarnation":"j","kind":"result","payload":{"value":42}}`},
		{"inbox", `{"node_id":"b","incarnation":"j","limit":1}`},
		{"wait", `{"node_id":"b","incarnation":"j","timeout_ms":0}`},
		{"ack", `{"node_id":"b","incarnation":"j","message_id":"m"}`},
		{"wait", `{"node_id":"b","incarnation":"j","timeout_ms":0}`},
		{"claim", `{"node_id":"a","incarnation":"i","task_id":"released","resource":"repo","paths":["one"],"context_revision":0}`},
		{"release", `{"node_id":"a","incarnation":"i","task_id":"released"}`},
		{"claim", `{"node_id":"b","incarnation":"j","task_id":"accepted","resource":"repo","paths":["two"],"context_revision":0}`},
		{"accept", `{"node_id":"b","incarnation":"j","task_id":"accepted","context_revision":0,"artifact_ref":"test-result","artifact_sha256":"` + strings.Repeat("a", 64) + `"}`},
		{"snapshot", `{}`},
		{"prune", `{"dry_run":true}`},
	}
	seen := map[string]bool{}
	for i, step := range steps {
		t.Run(fmt.Sprintf("%02d-%s", i, step.op), func(t *testing.T) {
			want, err := core.Execute(context.Background(), step.op, json.RawMessage(step.input))
			if err != nil {
				t.Fatal(err)
			}
			env, err := meshCLI(t, "F0", "--mesh-dir", cliDir, "--op", step.op, "--input", step.input, "--json")
			if err != nil || env.Error != nil || env.Kind != "coordinator_mesh" {
				t.Fatalf("CLI %s: %v %+v", step.op, err, env)
			}
			request, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call",
				"params": map[string]any{"name": "g8s_coordinator_mesh", "arguments": map[string]any{
					"mesh_dir": mcpDir, "operation": step.op, "input": json.RawMessage(step.input),
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			server := mcp.NewServer(bytes.NewReader(append(request, '\n')), &out, nil, nil, nil)
			if err := server.ServeStdio(context.Background()); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Error  json.RawMessage `json:"error"`
				Result struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &response); err != nil || len(response.Error) != 0 || len(response.Result.Content) != 1 {
				t.Fatalf("MCP %s: %v %s", step.op, err, out.String())
			}
			for surface, raw := range map[string]json.RawMessage{"CLI": env.Data, "MCP": json.RawMessage(response.Result.Content[0].Text)} {
				if !reflect.DeepEqual(meshStableJSON(t, raw), meshStableJSON(t, want)) {
					t.Errorf("%s/core %s mismatch: got %s want %s", surface, step.op, raw, want)
				}
			}
			seen[step.op] = true
		})
	}
	for _, op := range mesh.Operations() {
		if !seen[op] {
			t.Errorf("operation %q lacks parity coverage", op)
		}
	}
}

func meshStableJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var normalize func(any)
	normalize = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				_, node := x["capabilities"]
				if k == "created_at" || k == "cutoff" || (k == "observed_at" && node) {
					stamp, ok := child.(string)
					if _, err := time.Parse(time.RFC3339, stamp); !ok || err != nil {
						t.Fatalf("invalid %s timestamp: %v", k, child)
					}
					x[k] = "<wall-clock>"
				} else if k == "expires_at" {
					expires, ok := child.(float64)
					if !ok || expires <= float64(time.Now().Unix()) {
						t.Fatalf("invalid claim expiry: %v", child)
					}
					x[k] = "<wall-clock>"
				} else {
					normalize(child)
				}
			}
		case []any:
			for _, child := range x {
				normalize(child)
			}
		}
	}
	normalize(v)
	return v
}
