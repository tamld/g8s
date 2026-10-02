package receipt

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func FuzzReceiptEnvelope(f *testing.F) {
	// Seed 1: A valid canonical envelope
	f.Add([]byte(`{"schema_uri":"g8s://envelope/write-receipt/v1","field_order":["receipt_id","issuer","allowed_paths","expires_at"],"required_fields":["receipt_id","issuer"]}`))

	// Seed 2: Escape-pair payload from the #434 class (escaped newline/quote soup)
	f.Add([]byte(`{"schema_uri":"g8s://envelope/write-receipt/v1\n","field_order":["receipt_id\"\\n","issuer"],"required_fields":["abc\"def\"","token = False\n"]}`))

	// Seed 3: Truncated JSON
	f.Add([]byte(`{"schema_uri":"g8s://envelope/write-receipt/v1","field_order":[`))

	// Seed 4: Full WriteReceipt with embedded canonical envelope
	f.Add([]byte(`{"receipt_id":"rcpt-fuzz-01","issuer":"brain","allowed_paths":["src/**"],"expires_at":"2026-10-02T12:00:00Z","consumed":false,"canonical_envelope":{"schema_uri":"g8s://envelope/write-receipt/v1","field_order":["receipt_id","issuer","allowed_paths","expires_at"],"required_fields":["receipt_id","issuer"]}}`))

	// Seed 5: Empty input
	f.Add([]byte(``))

	// Seed 6: Escape soup inside WriteReceipt
	f.Add([]byte(`{"receipt_id":"rcpt\"\\n","issuer":"brain\n","allowed_paths":["src/\"path\"\n"],"canonical_envelope":{"schema_uri":"g8s://envelope/write-receipt/v1","field_order":["receipt_id"]}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic in FuzzReceiptEnvelope: %v", r)
				}
				close(done)
			}()

			// 1. Direct CanonicalEnvelope decode path
			var env CanonicalEnvelope
			if err := json.Unmarshal(data, &env); err == nil {
				// If decode succeeds with non-empty fields, verify consistency
				if env.SchemaURI != "" || len(env.FieldOrder) > 0 || len(env.RequiredFields) > 0 {
					valErr := validateCanonicalEnvelope(&env)
					if strings.HasPrefix(env.SchemaURI, envelopeSchemaPrefix) && len(env.FieldOrder) > 0 {
						if valErr != nil {
							t.Errorf("consistent envelope failed validation: %v", valErr)
						}
					} else {
						if valErr == nil {
							t.Errorf("inconsistent envelope passed validation: %+v", env)
						}
					}
				}
			}

			// 2. Full WriteReceipt envelope decode path
			var wr WriteReceipt
			if err := json.Unmarshal(data, &wr); err == nil {
				if wr.CanonicalEnvelope != nil {
					valErr := validateCanonicalEnvelope(wr.CanonicalEnvelope)
					if strings.HasPrefix(wr.CanonicalEnvelope.SchemaURI, envelopeSchemaPrefix) && len(wr.CanonicalEnvelope.FieldOrder) > 0 {
						if valErr != nil {
							t.Errorf("consistent embedded envelope failed validation: %v", valErr)
						}
					} else {
						if valErr == nil {
							t.Errorf("inconsistent embedded envelope passed validation: %+v", wr.CanonicalEnvelope)
						}
					}
				}
			}

			// 3. Database hydration decode path (scanIntoReceipt JSON parsing)
			_, _ = scanIntoReceipt(
				"rcpt-fuzz",
				string(data),
				1700000000, 0, sql.NullString{}, 1700000000,
				sql.NullInt64{}, sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{},
				sql.NullString{String: "g8s://envelope/write-receipt/v1", Valid: true},
				sql.NullString{String: string(data), Valid: true},
				sql.NullString{String: string(data), Valid: true},
				sql.NullString{}, sql.NullString{}, sql.NullString{},
				sql.NullString{}, sql.NullString{}, sql.NullString{},
				sql.NullString{String: string(data), Valid: true},
				sql.NullString{},
			)
		}()

		timer := time.NewTimer(5 * time.Second)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			t.Fatal("decode hung past 5s deadline")
		}
	})
}
