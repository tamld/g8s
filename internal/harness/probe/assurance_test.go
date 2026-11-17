package probe

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAssuranceProbes_AllPassAndEmitMetrics(t *testing.T) {
	suite := FilterSuite(DefaultSuite(), nil, string(CategoryAssurance))
	if len(suite.Probes) != 5 {
		t.Fatalf("FilterSuite(%q) returned %d probes, want 5", CategoryAssurance, len(suite.Probes))
	}

	pri, err := RunSuite(context.Background(), suite, &StaticProvider{Compliant: true})
	if err != nil {
		t.Fatalf("RunSuite failed: %v", err)
	}
	if pri.TotalProbes != 5 || pri.PassedProbes != 5 || pri.FailedProbes != 0 || pri.Score != 1.0 {
		t.Fatalf("expected 5/5 (score 1.0), got %+v", pri)
	}

	byID := make(map[string]ProbeResult, len(pri.Details))
	for _, r := range pri.Details {
		if !r.Passed {
			t.Fatalf("probe %s (%s) failed: err=%s actual=%s", r.ProbeID, r.Name, r.Error, r.ActualOutcome)
		}
		byID[r.ProbeID] = r
	}

	var esc AssuranceEscapeMetrics
	if err := json.Unmarshal([]byte(byID["as-001"].ActualOutcome), &esc); err != nil {
		t.Fatalf("unmarshal as-001: %v", err)
	}
	if esc.BlockRate != 1.0 || esc.VectorsBlocked != 5 || esc.SandboxMode == "" || esc.SandboxBackend == "" {
		t.Fatalf("unexpected as-001 metrics: %+v", esc)
	}

	var mut AssuranceMutantMetrics
	if err := json.Unmarshal([]byte(byID["as-002"].ActualOutcome), &mut); err != nil {
		t.Fatalf("unmarshal as-002: %v", err)
	}
	if mut.FalsePasses != 0 || mut.FalsePassRate != 0 || mut.MutantsCaught != 4 {
		t.Fatalf("unexpected as-002 metrics: %+v", mut)
	}

	var crash AssuranceCrashMetrics
	if err := json.Unmarshal([]byte(byID["as-003"].ActualOutcome), &crash); err != nil {
		t.Fatalf("unmarshal as-003: %v", err)
	}
	if !crash.Reclaimed || crash.Attempt != 2 || crash.RecoveryRTOMs < 0 {
		t.Fatalf("unexpected as-003 metrics: %+v", crash)
	}

	var ovh AssuranceOverheadMetrics
	if err := json.Unmarshal([]byte(byID["as-004"].ActualOutcome), &ovh); err != nil {
		t.Fatalf("unmarshal as-004: %v", err)
	}
	if ovh.Iterations != 10 || ovh.AvgSubmitClaimMs < 0 || ovh.AvgReceiptSignVerifyMs < 0 {
		t.Fatalf("unexpected as-004 metrics: %+v", ovh)
	}

	var aud AssuranceAuditMetrics
	if err := json.Unmarshal([]byte(byID["as-005"].ActualOutcome), &aud); err != nil {
		t.Fatalf("unmarshal as-005: %v", err)
	}
	if aud.CompletenessRatio != 1.0 || aud.ChecksPassed != 5 {
		t.Fatalf("unexpected as-005 metrics: %+v", aud)
	}
}
