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
	} {
		if _, ok := axes[axis]; !ok {
			t.Errorf("missing DELTA-45 axis %q in scorecard output", axis)
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
