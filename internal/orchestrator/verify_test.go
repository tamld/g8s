package orchestrator

import (
	"context"
	"testing"
)

func TestStdoutEnvelopeVerifier(t *testing.T) {
	tests := []struct {
		name        string
		receipt     Receipt
		wantOK      bool
		wantReturn  int
		wantErrorIn string
	}{
		{
			name: "success plain text",
			receipt: Receipt{
				OK:        true,
				Stdout:    "task completed successfully",
				RawStdout: []byte("task completed successfully"),
			},
			wantOK:     true,
			wantReturn: 0,
		},
		{
			name: "success json envelope",
			receipt: Receipt{
				OK:        true,
				Stdout:    `{"v":1,"kind":"task","cmd":"submit","data":{"state":"SUCCEEDED"}}`,
				RawStdout: []byte(`{"v":1,"kind":"task","cmd":"submit","data":{"state":"SUCCEEDED"}}`),
			},
			wantOK:     true,
			wantReturn: 0,
		},
		{
			name: "error json envelope with code and message",
			receipt: Receipt{
				OK:        true,
				Stdout:    `{"v":1,"kind":"error","cmd":"g8s","error":{"code":"E_USAGE","message":"unknown flag --invalid"}}`,
				RawStdout: []byte(`{"v":1,"kind":"error","cmd":"g8s","error":{"code":"E_USAGE","message":"unknown flag --invalid"}}`),
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "unknown flag",
		},
		{
			name: "error json envelope in RawStdout only",
			receipt: Receipt{
				OK:        true,
				RawStdout: []byte(`{"kind":"error","error":{"code":"E_RUNTIME","message":"subprocess crashed"}}`),
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "subprocess crashed",
		},
		{
			name: "fenced json error envelope",
			receipt: Receipt{
				OK:     true,
				Stdout: "Some output\n```json\n{\"kind\":\"error\",\"error\":{\"code\":\"E_HARNESS\",\"message\":\"permission violation\"}}\n```\nDone",
			},
			wantOK:      false,
			wantReturn:  1,
			wantErrorIn: "permission violation",
		},
	}

	verifier := &StdoutEnvelopeVerifier{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.receipt
			err := verifier.VerifyReceipt(ctx, &r)
			if err != nil {
				t.Fatalf("VerifyReceipt() returned unexpected error: %v", err)
			}
			if r.OK != tt.wantOK {
				t.Errorf("r.OK = %v, want %v", r.OK, tt.wantOK)
			}
			if tt.wantReturn != 0 && r.ReturnCode != tt.wantReturn {
				t.Errorf("r.ReturnCode = %d, want %d", r.ReturnCode, tt.wantReturn)
			}
			if tt.wantErrorIn != "" && (r.LastError == "" || !contains(r.LastError, tt.wantErrorIn)) {
				t.Errorf("r.LastError = %q, want containing %q", r.LastError, tt.wantErrorIn)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
