package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

func failedAttempts(n int) []AttemptRecord {
	out := make([]AttemptRecord, n)
	for i := range out {
		out[i] = AttemptRecord{
			ApproachIdx:   0,
			AttemptIdx:    i,
			StartedAt:     time.Now(),
			FinishedAt:    time.Now(),
			Receipt:       orchestrator.Receipt{OK: false, TaskID: "fail-" + intStr(i), ReturnCode: 1},
			ReviewVerdict: VerdictRevise,
			ReviewReason:  "fail",
		}
	}
	return out
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestRCAThreeFailuresConfidenceSeven(t *testing.T) {
	rec, err := StubRCA(context.Background(), failedAttempts(3))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rec.Confidence < 0.69 || rec.Confidence > 0.71 {
		t.Errorf("expected confidence ~0.7, got %f", rec.Confidence)
	}
}

func TestRCAFiveFailuresLowConfidence(t *testing.T) {
	rec, err := StubRCA(context.Background(), failedAttempts(5))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rec.Confidence >= 0.6 {
		t.Errorf("expected confidence < 0.6 for 5 failures, got %f", rec.Confidence)
	}
	if rec.Symptom != "low confidence RCA, escalation needed" {
		t.Errorf("expected low-confidence symptom, got %q", rec.Symptom)
	}
}

func TestRCAEmptyAttemptsConfidenceOne(t *testing.T) {
	rec, err := StubRCA(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rec.Confidence != 1.0 {
		t.Errorf("expected confidence=1.0 for empty attempts, got %f", rec.Confidence)
	}
}
