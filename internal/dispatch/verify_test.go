package dispatch

import (
	"errors"
	"testing"
)

func TestParseWorkerEnvelopeError(t *testing.T) {
	tests := []struct {
		name        string
		stdout      string
		wantCode    string
		wantMessage string
	}{
		{
			name: "full json error envelope",
			stdout: `{
  "v": 1,
  "kind": "error",
  "cmd": "g8s",
  "error": {
    "code": "E_USAGE",
    "message": "unknown command \"--prompt-file\"",
    "hint": "Run 'g8s help' for usage."
  }
}`,
			wantCode:    "E_USAGE",
			wantMessage: "unknown command \"--prompt-file\"",
		},
		{
			name:        "jsonl error envelope",
			stdout:      `{"v":1,"kind":"error","cmd":"g8s","error":{"code":"E_INVALID","message":"invalid flag"}}`,
			wantCode:    "E_INVALID",
			wantMessage: "invalid flag",
		},
		{
			name: "fenced json error block",
			stdout: "Some preliminary logs\n```json\n" + `{
  "kind": "error",
  "error": {
    "code": "E_RUNTIME",
    "message": "fatal crash"
  }
}` + "\n```\nTrailing logs",
			wantCode:    "E_RUNTIME",
			wantMessage: "fatal crash",
		},
		{
			name:        "error envelope with default code",
			stdout:      `{"kind":"error"}`,
			wantCode:    "E_RUNTIME",
			wantMessage: "worker returned error envelope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParseWorkerEnvelope([]byte(tt.stdout))
			if err == nil {
				t.Fatalf("expected error from ParseWorkerEnvelope, got nil")
			}
			var envErr *WorkerEnvelopeError
			if !errors.As(err, &envErr) {
				t.Fatalf("expected *WorkerEnvelopeError, got %T (%v)", err, err)
			}
			if envErr.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", envErr.Code, tt.wantCode)
			}
			if envErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", envErr.Message, tt.wantMessage)
			}
		})
	}
}

func TestParseWorkerEnvelopeSuccess(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
	}{
		{
			name:   "empty stdout",
			stdout: "",
		},
		{
			name:   "plain text success",
			stdout: "All tests passed successfully.\nGenerated 3 files.\n",
		},
		{
			name:   "success envelope",
			stdout: `{"v":1,"kind":"task","cmd":"submit","data":{"task_id":"123","state":"SUCCEEDED"}}`,
		},
		{
			name:   "arbitrary json object",
			stdout: `{"status":"ok","count":42}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ParseWorkerEnvelope([]byte(tt.stdout))
			if err != nil {
				t.Fatalf("expected nil from ParseWorkerEnvelope for %s, got %v", tt.name, err)
			}
		})
	}
}
