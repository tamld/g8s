package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tamld/g8s/internal/dispatch"
)

// ReceiptVerifier validates a receipt's stdout payload and
// overrides OK if the payload is an error envelope, even when
// the process exit code was 0.
type ReceiptVerifier interface {
	VerifyReceipt(ctx context.Context, receipt *Receipt) error
}

// StdoutEnvelopeVerifier validates receipts against stdout JSON error envelopes.
type StdoutEnvelopeVerifier struct{}

// VerifyReceipt checks receipt stdout and overrides OK to false if it contains an error envelope.
func (v *StdoutEnvelopeVerifier) VerifyReceipt(ctx context.Context, receipt *Receipt) error {
	if receipt == nil {
		return nil
	}
	raw := receipt.RawStdout
	if len(raw) == 0 && len(receipt.Stdout) > 0 {
		raw = []byte(receipt.Stdout)
	}
	if len(raw) == 0 {
		return nil
	}
	if receipt.OK {
		var env struct {
			Kind  string `json:"kind"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err == nil {
			if env.Kind == "error" {
				receipt.OK = false
				if env.Error != nil {
					receipt.LastError = fmt.Sprintf("%s: %s", env.Error.Code, env.Error.Message)
				} else {
					receipt.LastError = "worker returned error envelope"
				}
				if receipt.ReturnCode == 0 {
					receipt.ReturnCode = 1
				}
				return nil
			}
		}

		if envErr := dispatch.ParseWorkerEnvelope(raw); envErr != nil {
			receipt.OK = false
			var envE *dispatch.WorkerEnvelopeError
			if errors.As(envErr, &envE) && envE.Code != "" && envE.Message != "" {
				receipt.LastError = fmt.Sprintf("%s: %s", envE.Code, envE.Message)
			} else {
				receipt.LastError = envErr.Error()
			}
			if receipt.ReturnCode == 0 {
				receipt.ReturnCode = 1
			}
			return nil
		}
	}
	return nil
}
