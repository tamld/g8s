package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
)

func captureStdoutFn(t *testing.T, fn func()) []byte {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()

	os.Stdout = w
	fn()
	os.Stdout = origStdout
	_ = w.Close()

	out := <-done
	_ = r.Close()
	return out
}

func TestCLISurfaceContractParity(t *testing.T) {
	// Capture printUsage() output via concurrent reader goroutine (prevents 4KB Windows pipe buffer deadlock)
	// and verify every canonical command (except internal plumbing) appears in `g8s help`.
	helpOut := string(captureStdoutFn(t, func() {
		printUsage()
	}))

	for _, cmd := range cli.CanonicalCommands() {
		if cmd == "internal" {
			continue
		}
		if !strings.Contains(helpOut, "  "+cmd+" ") {
			t.Errorf("canonical command %q missing from printUsage() output", cmd)
		}
	}
}

func TestFiveAxisScorecardAndF0Gate(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())

	// 1. Test `g8s scorecard status --json`
	raw1 := captureStdoutFn(t, func() {
		runScorecard([]string{"status", "--json"})
	})
	var env map[string]any
	if err := json.Unmarshal(raw1, &env); err != nil {
		t.Fatalf("unmarshal scorecard json: %v (raw=%s)", err, string(raw1))
	}
	if env["kind"] != "scorecard_5axis" {
		t.Errorf("expected kind=scorecard_5axis, got %v", env["kind"])
	}
	data, _ := env["data"].(map[string]any)
	axes, _ := data["axes"].(map[string]any)
	for _, axis := range []string{
		"f1_f2_worker_yield",
		"write_receipt_tightness",
		"tool_mcp_friction_index",
		"skill_cross_audit",
		"jev_l1_l3_calibration",
		"compliance_vs_quality",
	} {
		if _, ok := axes[axis]; !ok {
			t.Errorf("missing DELTA-45 axis %q in scorecard output", axis)
		}
	}

	// 1b. compliance_vs_quality (#971 WP2) consumes the merged internal/dogfood
	// lane-split ledger: per-lane provenance rows, provenance cohort comparison,
	// per-lane escape rates, and the sandbox enforcement outcome metric.
	cq, ok := axes["compliance_vs_quality"].(map[string]any)
	if !ok {
		t.Fatalf("axes.compliance_vs_quality must be an object, got: %T", axes["compliance_vs_quality"])
	}
	laneLedger, ok := cq["lane_ledger"].([]any)
	if !ok || len(laneLedger) != 5 {
		t.Errorf("compliance_vs_quality.lane_ledger must carry 5 ALDC lanes (0,D,R,F,S), got: %v", cq["lane_ledger"])
	}
	for _, laneRow := range laneLedger {
		row, _ := laneRow.(map[string]any)
		if auditable, ok := row["auditable"].(bool); !ok || !auditable {
			t.Errorf("every lane ledger row is auditable (Lane D non-exempt, #971 WP2), got: %v", laneRow)
			break
		}
	}
	cohorts, ok := cq["cohort_metrics"].([]any)
	if !ok || len(cohorts) != 3 {
		t.Errorf("compliance_vs_quality.cohort_metrics must carry 3 provenance cohorts, got: %v", cq["cohort_metrics"])
	}
	for _, cohort := range cohorts {
		m, _ := cohort.(map[string]any)
		if label, ok := m["rate_label"].(string); !ok || (label != "measured" && label != "insufficient_sample") {
			t.Errorf("cohort rate_label must be measured|insufficient_sample, got: %v", cohort)
			break
		}
	}
	escapeRates, ok := cq["escape_rate_by_lane"].([]any)
	if !ok || len(escapeRates) != 5 {
		t.Errorf("compliance_vs_quality.escape_rate_by_lane must carry 5 ALDC lanes, got: %v", cq["escape_rate_by_lane"])
	}
	if _, hasSummary := cq["sandbox_enforcement"]; !hasSummary {
		if _, hasErr := cq["sandbox_enforcement_error"]; !hasErr {
			t.Error("compliance_vs_quality must carry sandbox_enforcement or an explicit sandbox_enforcement_error")
		}
	}

	// 2. Test `g8s f0 gate check --repo ../.. --json`
	raw2 := captureStdoutFn(t, func() {
		runF0([]string{"gate", "check", "--repo", "../..", "--json"})
	})
	var gateEnv map[string]any
	if err := json.Unmarshal(raw2, &gateEnv); err != nil {
		t.Fatalf("unmarshal f0 gate json: %v (raw=%s)", err, string(raw2))
	}
	if gateEnv["kind"] != "f0_gate_check" {
		t.Errorf("expected kind=f0_gate_check, got %v", gateEnv["kind"])
	}
	gateData, _ := gateEnv["data"].(map[string]any)
	if gateData["verdict"] != "PASS" {
		t.Errorf("expected verdict=PASS, got %v", gateData["verdict"])
	}
}
