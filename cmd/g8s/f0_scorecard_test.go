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

func TestCLISurfaceContractParity(t *testing.T) {
	// Capture printUsage() output and verify every canonical command (except internal plumbing)
	// appears in `g8s help`.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	printUsage()
	_ = w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	helpOut := buf.String()

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
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runScorecard([]string{"status", "--json"})
	_ = w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal scorecard json: %v (raw=%s)", err, buf.String())
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
	} {
		if _, ok := axes[axis]; !ok {
			t.Errorf("missing DELTA-45 axis %q in scorecard output", axis)
		}
	}

	// 2. Test `g8s f0 gate check --repo ../.. --json`
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe2: %v", err)
	}
	os.Stdout = w2
	runF0([]string{"gate", "check", "--repo", "../..", "--json"})
	_ = w2.Close()
	os.Stdout = origStdout

	var buf2 bytes.Buffer
	_, _ = io.Copy(&buf2, r2)
	var gateEnv map[string]any
	if err := json.Unmarshal(buf2.Bytes(), &gateEnv); err != nil {
		t.Fatalf("unmarshal f0 gate json: %v (raw=%s)", err, buf2.String())
	}
	if gateEnv["kind"] != "f0_gate_check" {
		t.Errorf("expected kind=f0_gate_check, got %v", gateEnv["kind"])
	}
	gateData, _ := gateEnv["data"].(map[string]any)
	if gateData["verdict"] != "PASS" {
		t.Errorf("expected verdict=PASS, got %v", gateData["verdict"])
	}
}
