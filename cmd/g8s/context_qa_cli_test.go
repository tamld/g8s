package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextPackAndQAReportCLI(t *testing.T) {
	tmp := t.TempDir()
	sampleGo := "package demo\n\n// Add sums two integers.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	if err := os.WriteFile(filepath.Join(tmp, "demo.go"), []byte(sampleGo), 0o600); err != nil {
		t.Fatal(err)
	}

	outMD := filepath.Join(tmp, "packed.md")
	runContextPack([]string{"--root", tmp, "--budget", "500", "--focus", "Add", "--out", outMD, "--json"})
	data, err := os.ReadFile(outMD)
	if err != nil || !strings.Contains(string(data), "demo.go") {
		t.Fatalf("expected packed.md to contain demo.go, err=%v", err)
	}

	specPath := filepath.Join(tmp, "spec.md")
	specContent := strings.Join([]string{
		"# QA Spec",
		"- US-01 / REQ-FUNC-01",
		"- AC-01: Given boundary 0 and empty input, When concurrent workers retry on timeout, Then FSM transitions QUEUED -> RUNNING -> COMPLETED and secrets are redacted.",
	}, "\n")
	if err := os.WriteFile(specPath, []byte(specContent), 0o600); err != nil {
		t.Fatal(err)
	}

	runQAReport([]string{
		"--spec", specPath,
		"--holdout-paths", "holdout/**",
		"--allowed-paths", "internal/**",
		"--ac-bindings", "AC-01=TestAdd",
		"--json",
	})
	_ = exec.Command
}
