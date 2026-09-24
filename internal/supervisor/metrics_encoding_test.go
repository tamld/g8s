package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMetricsEncodingDecodingRoundtrip(t *testing.T) {
	orig := Metrics{
		EnvelopeScore:        0.875,
		FirstAttemptSuccess:  true,
		AttemptsToSuccess:    2,
		ApproachesToSuccess:  1,
		RCAConfidenceAvg:     0.94,
		CycleDurationSeconds: 42.125,
		EscalationCount:      1,
		FalseEscalationRate:  0.05,
	}

	encoded, err := EncodeMetrics(orig)
	if err != nil {
		t.Fatalf("EncodeMetrics failed: %v", err)
	}

	if encoded == "" {
		t.Fatal("expected non-empty JSON string")
	}

	decoded, err := DecodeMetrics(encoded)
	if err != nil {
		t.Fatalf("DecodeMetrics failed: %v", err)
	}

	if decoded != orig {
		t.Errorf("roundtrip mismatch:\ngot:  %+v\nwant: %+v", decoded, orig)
	}
}

func TestDecodeMetrics_EdgeCases(t *testing.T) {
	// 1. Empty string returns zero-value Metrics and nil error
	empty, err := DecodeMetrics("")
	if err != nil {
		t.Fatalf("DecodeMetrics(\"\") returned error: %v", err)
	}
	if empty != (Metrics{}) {
		t.Errorf("expected zero Metrics, got %+v", empty)
	}

	// 2. Malformed JSON returns error
	_, err = DecodeMetrics("{invalid-json-payload")
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
	if !strings.HasPrefix(err.Error(), "supervisor: decode metrics: ") {
		t.Errorf("expected error prefix 'supervisor: decode metrics: ', got: %v", err)
	}
}

func TestSQLMetricsStore_Lifecycle(t *testing.T) {
	// 1. Constructor clock fallback
	storeNil := NewSQLMetricsStore(nil)
	if storeNil == nil || storeNil.clock == nil {
		t.Fatal("expected non-nil store with fallback clock")
	}

	fixedTime := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	storeCustom := NewSQLMetricsStore(func() time.Time { return fixedTime })
	if got := storeCustom.clock(); !got.Equal(fixedTime) {
		t.Errorf("expected custom clock %v, got %v", fixedTime, got)
	}

	// 2. Context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	testM := Metrics{EnvelopeScore: 0.5, FirstAttemptSuccess: false}
	if err := storeCustom.SaveMetrics(ctx, "task-1", testM); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from SaveMetrics, got %v", err)
	}
	if _, err := storeCustom.GetMetrics(ctx, "task-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from GetMetrics, got %v", err)
	}

	// 3. Normal Save and Get
	validCtx := context.Background()
	m, err := storeCustom.GetMetrics(validCtx, "unknown-task")
	if err != nil {
		t.Fatalf("GetMetrics unknown task returned error: %v", err)
	}
	if m != (Metrics{}) {
		t.Errorf("expected zero Metrics for unknown task, got %+v", m)
	}

	if err := storeCustom.SaveMetrics(validCtx, "task-1", testM); err != nil {
		t.Fatalf("SaveMetrics failed: %v", err)
	}
	m, err = storeCustom.GetMetrics(validCtx, "task-1")
	if err != nil {
		t.Fatalf("GetMetrics task-1 failed: %v", err)
	}
	if m != testM {
		t.Errorf("GetMetrics mismatch: got %+v, want %+v", m, testM)
	}
}
