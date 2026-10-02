package worker

import (
	"encoding/json"
	"fmt"
	"testing"
)

func FuzzWorkerResultParse(f *testing.F) {
	// Seed 1: #443 echo-poisoning stream shape
	f.Add([]byte("{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"diff --git a/internal/worker/worker_test.go ... text_delta=\\\"This request was blocked by Gemini's filters.\\\"\"}}\n" +
		"{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"--- PASS: TestReadWorkerResultProviderRefusal\"}}\n" +
		"{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"Implemented the repair packet exactly as written. All tests pass.\"}}\n"))

	// Seed 2: Fenced-JSON variant (succeeded)
	f.Add([]byte("Task execution:\n```json\n{\"ok\": true, \"status\": \"succeeded\", \"response\": \"task completed clean\"}\n```\nDone."))

	// Seed 3: Fenced-JSON variant (blocked)
	f.Add([]byte("Task execution:\n```json\n{\"ok\": false, \"status\": \"BLOCKED\", \"reason\": \"needs write receipt\"}\n```\nDone."))

	// Seed 4: Empty input
	f.Add([]byte(""))

	// Seed 5: Prompt-level refusal without result event (#443)
	f.Add([]byte("{\"event\":\"init\",\"init\":{\"model\":\"gemini-3.8-flash-high\"}}\n" +
		"{\"event\":\"step_update\",\"step_update\":{\"step_type\":\"agent_response\",\"text_delta\":\"This request was blocked by Gemini's filters.\"}}\n"))

	// Seed 6: Valid taskRequest JSON
	f.Add([]byte(`{"prompt":"echo test","model":"gemini-3.8-flash-high","role":"collector","permission":"read_only","timeout":"5m"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		text := string(data)

		// 1. Never panic on worker result decoding
		wrStdout := readWorkerResult(nil, text, 0)
		_ = readWorkerResult(data, text, 0)
		_ = readWorkerResult(nil, text, 1)

		// 2. Never panic on taskRequest decoding
		var req taskRequest
		_ = json.Unmarshal(data, &req)

		// 3. Pin #443 invariant: the decoded output never re-reads as a refusal block
		// for a clean final response.
		if wrStdout.Status == "succeeded" && wrStdout.Response != "" {
			if providerRefusalDetected(wrStdout.Response) {
				t.Fatalf("#443 invariant: succeeded response must not be a provider refusal: %q", wrStdout.Response)
			}
		}

		// Also directly test the #443 invariant: prepending stream text (which may
		// echo refusal boilerplate) before a clean SUCCESS result event must NEVER
		// re-read as a refusal block.
		cleanStream := fmt.Sprintf("%s\n{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"Implemented the repair packet exactly as written. All tests pass.\"}}\n", text)
		wrClean := readWorkerResult(nil, cleanStream, 0)
		if wrClean.Status == "blocked" {
			t.Fatalf("#443 invariant violated: clean final response classified as blocked due to preceding stream text: %+v", wrClean)
		}
	})
}
