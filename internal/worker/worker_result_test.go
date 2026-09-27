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
	wr := readWorkerResult("", stdout, 0)
	if !wr.OK || wr.Status != "succeeded" {
		t.Fatalf("stream SUCCESS must parse as succeeded, got %+v", wr)
	}
	if wr.Response != "{\"parse_sites\":[],\"summary\":\"real recon\"}" {
		t.Fatalf("model response text was discarded, got %q", wr.Response)
	}
}

func TestReadWorkerResultRejectsDegenerateStreamResponse(t *testing.T) {
	for _, degenerate := range []string{"{}", "  {}  "} {
		wr := readWorkerResult("", agyStream(degenerate), 0)
		if wr.OK {
			t.Fatalf("response %q must not be accepted as a deliverable: %+v", degenerate, wr)
		}
		if wr.Status != "failed" {
			t.Fatalf("degenerate response must finish failed, got %q", wr.Status)
		}
	}
	// An absent response field is not a rejection — legacy fixtures and
	// synthetic streams may omit it (the envelope carries ok/status only).
	wr := readWorkerResult("", "{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\"}}", 0)
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
	wr := readWorkerResult("", agyStream("{\"verdict\":\"ok\"}"), 0)
	raw := string(mustResultJSON(wr, "", ""))
	if !strings.Contains(raw, `"response"`) {
		t.Fatalf("stored envelope lacks the response deliverable: %s", raw)
	}
	if !strings.Contains(raw, "verdict") || !strings.Contains(raw, "ok") {
		t.Fatalf("stored envelope response text mangled: %s", raw)
	}
}
