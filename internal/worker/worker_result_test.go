package worker

// #383: worker result envelope schema validation + deliverable surfacing.
// Live-proven by dogfood (plans/260926-s2-concurrent-dispatch/plan.md): the
// agy stream-json terminal event carried a substantive response both runs,
// and parseAGYResult discarded it. RED-first per ALDC.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// agyStream wraps a response payload in the stream-json terminal event shape
// the agy CLI emits on stdout.
func agyStream(response string) string {
	return fmt.Sprintf("{\"event\":\"init\",\"conversation_id\":\"c1\",\"init\":{\"model\":\"m\"}}\n"+
		"{\"event\":\"step_update\",\"step_update\":{\"step_index\":0,\"state\":\"DONE\",\"step_type\":\"user_input\"}}\n"+
		"{\"event\":\"result\",\"result\":{\"conversation_id\":\"c1\",\"status\":\"SUCCESS\",\"response\":%q,\"duration_seconds\":1.0}}\n", response)
}

func TestReadWorkerResultCarriesResponseFromStream(t *testing.T) {
	stdout := agyStream("{\"parse_sites\":[],\"summary\":\"real recon\"}")
	wr := readWorkerResult(nil, stdout, 0)
	if !wr.OK || wr.Status != "succeeded" {
		t.Fatalf("stream SUCCESS must parse as succeeded, got %+v", wr)
	}
	if wr.Response != "{\"parse_sites\":[],\"summary\":\"real recon\"}" {
		t.Fatalf("model response text was discarded, got %q", wr.Response)
	}
}

func TestReadWorkerResultRejectsDegenerateStreamResponse(t *testing.T) {
	for _, degenerate := range []string{"{}", "  {}  "} {
		wr := readWorkerResult(nil, agyStream(degenerate), 0)
		if wr.OK {
			t.Fatalf("response %q must not be accepted as a deliverable: %+v", degenerate, wr)
		}
		if wr.Status != "failed" {
			t.Fatalf("degenerate response must finish failed, got %q", wr.Status)
		}
	}
	// An absent response field is not a rejection — legacy fixtures and
	// synthetic streams may omit it (the envelope carries ok/status only).
	wr := readWorkerResult(nil, "{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\"}}", 0)
	if !wr.OK || wr.Status != "succeeded" {
		t.Fatalf("absent response must stay succeeded, got %+v", wr)
	}
}

// The money test: a worker-written result file without the required envelope
// fields must fail validation. Worker-layer attempts end WORKER_COMPLETED by
// design (supervisor acceptance owns terminal states), so the poisoning
// protection is the envelope verdict (ok=false) plus the recorded
// result_validation — downstream consumers must never see a clean result.
func TestRunOnceRejectsRawResultWithoutOkOrStatus(t *testing.T) {
	env := newWorkerEnv(t, nil)
	task := submitTask(t, env, "schema-383", 1, nil)
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"unexpected":1}`, 5*time.Millisecond)
		return child
	}
	if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-383", LeaseSeconds: 60}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := env.store.GetTask(context.Background(), task.TaskID)
	if err != nil || got == nil {
		t.Fatalf("get task: %v", err)
	}
	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("stored result is not JSON: %v", err)
	}
	if okVal, present := resultMap["ok"]; !present || okVal != false {
		t.Fatalf("stored result must carry ok=false for a schema-invalid worker payload, got %v", resultMap["ok"])
	}
	if got.ResultValidation == nil || got.ResultValidation.Valid {
		t.Fatalf("result_validation must be recorded as invalid, got %+v", got.ResultValidation)
	}
	schemaErr := false
	for _, e := range got.ResultValidation.SchemaErrors {
		if e != "" {
			schemaErr = true
		}
	}
	if !schemaErr {
		t.Fatalf("expected a schema error recorded, got %+v", got.ResultValidation)
	}
}

// The stored envelope must surface the deliverable, not bury it in stdout.
func TestRunOnceStoresResponseInResultEnvelope(t *testing.T) {
	wr := readWorkerResult(nil, agyStream("{\"verdict\":\"ok\"}"), 0)
	raw := string(mustResultJSON(wr, "", ""))
	if !strings.Contains(raw, `"response"`) {
		t.Fatalf("stored envelope lacks the response deliverable: %s", raw)
	}
	if !strings.Contains(raw, "verdict") || !strings.Contains(raw, "ok") {
		t.Fatalf("stored envelope response text mangled: %s", raw)
	}
}

// --- #383 review-hardening pins ---

// The response is model-controlled content that reaches result_json and the
// Evidence Lake: it must pass the same central sanitization as stdout.
func TestStoredResponseIsSanitized(t *testing.T) {
	wr := readWorkerResult(nil, "", 0)
	wr.OK = true
	wr.Status = "succeeded"
	wr.Response = "connect postgresql://admin:hunter2@db.internal/prod"
	raw := string(mustResultJSON(wr, "", ""))
	if strings.Contains(raw, "hunter2") {
		t.Fatalf("response bypassed central sanitization: %s", raw)
	}
}

func TestRawFileSchemaVariants(t *testing.T) {
	env := newWorkerEnv(t, nil)
	cases := []struct {
		name   string
		file   string
		wantOK bool // expect a schema-invalid outcome
	}{
		{"missing status", `{"ok":true}`, true},
		{"non-boolean ok", `{"ok":"yes","status":"succeeded"}`, true},
		{"unexpected field", `{"ok":true,"status":"succeeded","inject":"x"}`, true},
		{"wrapper mimic exempt", `{"ok":true,"exit_code":0}`, false},
		{"valid worker file", `{"ok":true,"status":"succeeded"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			submitTask(t, env, "schema-"+strings.ReplaceAll(tc.name, " ", "-"), 1, nil)
			env.runner.factory = func(opts SpawnOptions) Child {
				child := newFakeChild(0)
				child.finishLater(opts.ResultPath, tc.file, 5*time.Millisecond)
				return child
			}
			got, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-schema", LeaseSeconds: 60})
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			var resultMap map[string]any
			if err := json.Unmarshal(got.Result, &resultMap); err != nil {
				t.Fatalf("stored result not JSON: %v", err)
			}
			storedOK, _ := resultMap["ok"].(bool)
			if tc.wantOK && storedOK {
				t.Fatalf("%s: stored envelope must carry ok=false, got %s", tc.name, got.Result)
			}
			if !tc.wantOK && !storedOK {
				t.Fatalf("%s: legitimate file must not be rejected, got %s", tc.name, got.Result)
			}
		})
	}
}

// The rejection rewrite must strip the worker's response text — a poisoned
// payload's content must not survive into the rejected envelope.
func TestRejectionRewriteStripsResponse(t *testing.T) {
	env := newWorkerEnv(t, nil)
	submitTask(t, env, "rewrite-383", 1, nil)
	env.runner.factory = func(opts SpawnOptions) Child {
		child := newFakeChild(0)
		child.finishLater(opts.ResultPath, `{"ok":true,"status":"succeeded","response":"poison payload","smuggled":1}`, 5*time.Millisecond)
		return child
	}
	got, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "w-rewrite", LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if strings.Contains(string(got.Result), "poison payload") {
		t.Fatalf("rejection envelope must strip the worker response: %s", got.Result)
	}
	var resultMap map[string]any
	if err := json.Unmarshal(got.Result, &resultMap); err != nil {
		t.Fatalf("stored result not JSON: %v", err)
	}
	if okVal, _ := resultMap["ok"].(bool); okVal {
		t.Fatalf("rejection envelope must carry ok=false: %s", got.Result)
	}
}
