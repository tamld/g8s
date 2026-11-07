package reflection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/testenv"
)

func packetReceipt(t *testing.T) *ProcessReflectionReceipt {
	t.Helper()
	r, err := ValidateProcessReflectionReceipt([]byte(goldenReceipt))
	if err != nil {
		t.Fatal(err)
	}
	r.Verdicts = nil
	for _, artifact := range []string{"docs/a.md", "internal/b.go", "README.md"} {
		r.AddList = append(r.AddList, artifact)
		r.Verdicts = append(r.Verdicts, Verdict{ArtifactPath: artifact, Dimension: "drift", Verdict: "drift", EvidenceRefs: []string{"run:gate-123"}})
	}
	return r
}

func TestBuildWorkPacketsDeterministicBoundedScopes(t *testing.T) {
	r := packetReceipt(t)
	r.PruneList = []string{"docs/prune.md"}
	r.PruneRatio = r.ComputedPruneRatio()
	opts := PacketOptions{Provider: "codex", Model: "test-model", Effort: "high"}
	packets, err := BuildWorkPackets(r, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 3 {
		t.Fatalf("got %d packets", len(packets))
	}
	for i, p := range packets {
		parent := []string{"docs/", "internal/", "./"}[i]
		if p.IdempotencyKey != fmt.Sprintf("reflex-%s-%d", r.ReceiptID, i) || !reflect.DeepEqual(p.AllowedPaths, []string{r.AddList[i], parent}) {
			t.Fatalf("bad identity/scope: %+v", p)
		}
		if p.Permission != "workspace_write" || p.Timeout != "45m" || p.MaxAttempts != 1 || p.Role != "implementer" || p.Provider != opts.Provider || p.Model != opts.Model || p.Effort != opts.Effort {
			t.Fatalf("bad defaults/routing: %+v", p)
		}
		for _, want := range []string{r.ReceiptID, p.Artifact, "run:gate-123", "drift"} {
			if !strings.Contains(p.Prompt, want) {
				t.Fatalf("prompt missing %q", want)
			}
		}
	}
	again, err := BuildWorkPackets(r, opts)
	if err != nil || !reflect.DeepEqual(packets, again) {
		t.Fatalf("nondeterministic: %v", err)
	}
	capped, err := BuildWorkPackets(r, PacketOptions{MaxPackets: 2, Role: "test-runner"})
	if err != nil || len(capped) != 2 || capped[1].IdempotencyKey != packets[1].IdempotencyKey || capped[0].Role != "test-runner" {
		t.Fatalf("bad cap/role: %+v, %v", capped, err)
	}
	for i := 0; i < 3; i++ {
		artifact := fmt.Sprintf("docs/extra%d.md", i)
		r.AddList = append(r.AddList, artifact)
		r.Verdicts = append(r.Verdicts, Verdict{ArtifactPath: artifact, Dimension: "drift", Verdict: "drift", EvidenceRefs: []string{"run:gate"}})
	}
	r.PruneRatio = r.ComputedPruneRatio()
	defaults, err := BuildWorkPackets(r, PacketOptions{})
	if err != nil || len(defaults) != 5 {
		t.Fatalf("default cap: %d, %v", len(defaults), err)
	}
}

func TestBuildWorkPacketsRejectsUnscopedAndUnevidenced(t *testing.T) {
	t.Run("blank", func(t *testing.T) {
		r := packetReceipt(t)
		r.AddList[2] = " "
		_, err := BuildWorkPackets(r, PacketOptions{MaxPackets: 1})
		if !errors.Is(err, ErrPacketUnscoped) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("S2 evidence", func(t *testing.T) {
		r := packetReceipt(t)
		r.Verdicts[0].EvidenceRefs = nil
		_, err := BuildWorkPackets(r, PacketOptions{})
		if !errors.Is(err, ErrUnevidencedVerdict) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unmatched beyond cap", func(t *testing.T) {
		r := packetReceipt(t)
		r.Verdicts = r.Verdicts[:2]
		_, err := BuildWorkPackets(r, PacketOptions{MaxPackets: 1})
		if !errors.Is(err, ErrUnevidencedVerdict) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		if _, err := BuildWorkPackets(nil, PacketOptions{}); err == nil {
			t.Fatal("accepted nil")
		}
	})
}

type recordingSubmitter struct {
	requests []controlplane.SubmitTaskRequest
	failAt   int
	err      error
}

func (s *recordingSubmitter) SubmitTask(_ context.Context, req controlplane.SubmitTaskRequest) (*controlplane.Task, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == s.failAt {
		return nil, s.err
	}
	return &controlplane.Task{TaskID: fmt.Sprintf("task-%d", len(s.requests))}, nil
}

func TestSubmitPacketsOrderedRequestsAndStopAtFirstError(t *testing.T) {
	packets, err := BuildWorkPackets(packetReceipt(t), PacketOptions{Provider: "codex", Model: "test-model", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	for _, failAt := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			boom := errors.New("queue unavailable")
			s := &recordingSubmitter{failAt: failAt, err: boom}
			ids, err := SubmitPackets(context.Background(), s, packets)
			count, calls := 3, 3
			if failAt != 0 {
				count, calls = failAt-1, failAt
				if !errors.Is(err, boom) {
					t.Fatalf("lost error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(ids) != count || len(s.requests) != calls {
				t.Fatalf("ids %v, calls %d", ids, len(s.requests))
			}
			for i, id := range ids {
				if id != fmt.Sprintf("task-%d", i+1) {
					t.Fatalf("order: %v", ids)
				}
			}
			for i, req := range s.requests {
				p := packets[i]
				if req.IdempotencyKey != p.IdempotencyKey || !reflect.DeepEqual(req.AllowedPaths, p.AllowedPaths) || req.Permission != p.Permission || req.Timeout != p.Timeout || req.MaxAttempts != p.MaxAttempts || req.Role != p.Role || req.Provider != p.Provider || req.Model != p.Model || req.Effort != p.Effort {
					t.Fatalf("request mismatch: %+v", req)
				}
				var payload map[string]any
				if err := json.Unmarshal(req.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["receipt_id"] != p.ReceiptID || payload["artifact"] != p.Artifact || payload["verdict"] != p.Verdict || payload["instruction"] != p.Prompt || len(payload["evidence_refs"].([]any)) == 0 {
					t.Fatalf("payload: %s", req.Payload)
				}
			}
		})
	}
}

// TestIssue714AcceptanceTickQueuesRealTask exercises the CLI handler against a
// physical isolated store. It does not claim execution or write authorization.
func TestIssue714AcceptanceTickQueuesRealTask(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "g8s")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../../cmd/g8s")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	for _, dry := range []bool{false, true} {
		name := "queued"
		if dry {
			name = "DryRunQueuesNothingAndPrintsSpecs"
		}
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			r := packetReceipt(t)
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			receiptFile := filepath.Join(t.TempDir(), "receipt.json")
			if err := os.WriteFile(receiptFile, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"autopilot", "tick", "-reflection-receipt", receiptFile}
			if dry {
				args = append(args, "-dry-run")
			}
			// Match the repo's testenv jail without changing the worker's own
			// ambient environment; only this child receives delegated-write opt-in.
			blocked := append(testenv.AmbientDelegationEnvVars(), "G8S_STATE_DIR", "G8S_DB")
			var env []string
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !containsEnvKey(blocked, key) {
					env = append(env, entry)
				}
			}
			env = append(env, "G8S_STATE_DIR="+state)
			if !dry {
				env = append(env, "AGY_MCP_ALLOW_WORKSPACE_WRITE=1")
			}
			cmd := exec.Command(bin, args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("tick: %v\n%s", err, out)
			}
			if dry {
				if _, err := os.Stat(filepath.Join(state, "g8s.db")); !os.IsNotExist(err) {
					t.Fatalf("dry-run opened database: %v", err)
				}
				for _, p := range []string{"reflex-" + r.ReceiptID + "-0", "docs/a.md", "docs/", "prompt", "run:gate-123"} {
					if !strings.Contains(string(out), p) {
						t.Fatalf("dry output missing %q: %s", p, out)
					}
				}
			}
			store, err := controlplane.NewControlPlane(filepath.Join(state, "g8s.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			tasks, err := store.ListTasks(context.Background(), controlplane.TaskFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if dry {
				if len(tasks) != 0 {
					t.Fatalf("dry-run queued %d tasks", len(tasks))
				}
				return
			}
			if len(tasks) != 3 {
				t.Fatalf("queued %d tasks, output %s", len(tasks), out)
			}
			for _, task := range tasks {
				if task.TaskID == "" || !strings.Contains(string(out), task.TaskID) || task.State != controlplane.StateQueued {
					t.Fatalf("invalid queued task: %+v", task)
				}
				var payload map[string]any
				if err := json.Unmarshal(task.Request, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["permission"] != "workspace_write" || payload["receipt_id"] != r.ReceiptID || len(payload["allowed_paths"].([]any)) == 0 {
					t.Fatalf("invalid payload: %v", payload)
				}
			}
			// Replaying the same receipt must not insert additional rows.
			replay := exec.Command(bin, args...)
			replay.Env = env
			if out, err := replay.CombinedOutput(); err != nil {
				t.Fatalf("replay: %v\n%s", err, out)
			}
			tasks, err = store.ListTasks(context.Background(), controlplane.TaskFilter{})
			if err != nil || len(tasks) != 3 {
				t.Fatalf("replay tasks %d: %v", len(tasks), err)
			}
		})
	}
}

func containsEnvKey(keys []string, want string) bool {
	for _, key := range keys {
		if strings.EqualFold(key, want) {
			return true
		}
	}
	return false
}
