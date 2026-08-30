package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/cli"
)

func TestDogfoodE2E(t *testing.T) {
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "g8s")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\nOutput: %s", err, string(out))
	}

	dbPath := filepath.Join(tempDir, "g8s-dogfood.db")
	payloadPath := filepath.Join(tempDir, "payload.md")
	dodPath := filepath.Join(tempDir, "dod.md")

	if err := os.WriteFile(payloadPath, []byte("# CI Dogfood Payload\nTest payload"), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := os.WriteFile(dodPath, []byte("- [x] Dogfood DoD item"), 0o600); err != nil {
		t.Fatalf("write dod: %v", err)
	}

	// 1. brief-issue
	issueCmd := exec.Command(binPath, "brief-issue",
		"--title", "ci-self-dogfood",
		"--payload-file", payloadPath,
		"--dod-file", dodPath,
		"--issued-by", "ci-bot",
		"--ttl", "1h",
	)
	issueCmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	issueOut, err := issueCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("brief-issue failed: %v\nOutput: %s", err, string(issueOut))
	}

	var issueEnv struct {
		V       int    `json:"v"`
		Kind    string `json:"kind"`
		Command string `json:"cmd"`
		Data    struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Status    string `json:"status"`
			PayloadMD string `json:"payload_md"`
			DodMD     string `json:"dod_md"`
			IssuedBy  string `json:"issued_by"`
		} `json:"data"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(issueOut, &issueEnv); err != nil {
		t.Fatalf("unmarshal brief-issue output: %v\nOutput: %s", err, string(issueOut))
	}

	if issueEnv.V != cli.CurrentEnvelopeVersion || issueEnv.Kind != "brief" || issueEnv.Command != "brief-issue" {
		t.Errorf("unexpected envelope fields: %+v", issueEnv)
	}
	issued := issueEnv.Data
	if issued.ID == "" || !strings.HasPrefix(issued.ID, "brief-") {
		t.Fatalf("expected brief ID prefix 'brief-', got %q", issued.ID)
	}
	if issued.Status != "active" {
		t.Fatalf("expected status 'active', got %q", issued.Status)
	}
	if issued.Title != "ci-self-dogfood" {
		t.Fatalf("expected title 'ci-self-dogfood', got %q", issued.Title)
	}

	// 2. brief-consume
	consumeCmd := exec.Command(binPath, "brief-consume", "--id", issued.ID)
	consumeCmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	consumeOut, err := consumeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("brief-consume failed: %v\nOutput: %s", err, string(consumeOut))
	}

	var consumeEnv struct {
		V       int    `json:"v"`
		Kind    string `json:"kind"`
		Command string `json:"cmd"`
		Data    struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(consumeOut, &consumeEnv); err != nil {
		t.Fatalf("unmarshal brief-consume output: %v\nOutput: %s", err, string(consumeOut))
	}

	consumed := consumeEnv.Data
	if consumed.ID != issued.ID {
		t.Fatalf("consumed ID = %q, want %q", consumed.ID, issued.ID)
	}
	if consumed.Status != "consumed" {
		t.Fatalf("expected status 'consumed', got %q", consumed.Status)
	}

	// 3. orchestrate --brief-file
	briefFilePath := filepath.Join(tempDir, "dogfood-brief.md")
	briefMD := "# E2E Dogfood Task\nTask payload for dogfood.\n## DoD\n- [x] DoD item 1\n"
	if err := os.WriteFile(briefFilePath, []byte(briefMD), 0o600); err != nil {
		t.Fatalf("write brief file: %v", err)
	}

	orchCmd := exec.Command(binPath, "orchestrate",
		"--brief-file", briefFilePath,
		"--issued-by", "sisyphus",
		"--ttl", "1h",
	)
	orchCmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	orchOut, err := orchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrate --brief-file failed: %v\nOutput: %s", err, string(orchOut))
	}

	orchBriefID := strings.TrimSpace(string(orchOut))
	if !strings.HasPrefix(orchBriefID, "brief-") {
		t.Fatalf("expected brief ID prefix 'brief-', got %q", orchBriefID)
	}

	// 4. orchestrate --dispatch
	dispatchCmd := exec.Command(binPath, "orchestrate",
		"--dispatch", orchBriefID,
		"--ttl", "1h",
	)
	dispatchCmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	dispatchOut, err := dispatchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrate --dispatch failed: %v\nOutput: %s", err, string(dispatchOut))
	}

	dispatchedID := strings.TrimSpace(string(dispatchOut))
	if !strings.HasPrefix(dispatchedID, "brief-") {
		t.Fatalf("expected reissued brief ID prefix 'brief-', got %q", dispatchedID)
	}
	if dispatchedID == orchBriefID {
		t.Fatalf("expected new brief ID from dispatch, got identical %q", dispatchedID)
	}

	// 5. brief-consume on dispatched brief
	consumeDispatchedCmd := exec.Command(binPath, "brief-consume", "--id", dispatchedID)
	consumeDispatchedCmd.Env = append(os.Environ(), "G8S_DB="+dbPath)
	consumeDispatchedOut, err := consumeDispatchedCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("brief-consume dispatched failed: %v\nOutput: %s", err, string(consumeDispatchedOut))
	}

	var consumeDispatchedEnv struct {
		V       int    `json:"v"`
		Kind    string `json:"kind"`
		Command string `json:"cmd"`
		Data    struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			PayloadMD string `json:"payload_md"`
		} `json:"data"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(consumeDispatchedOut, &consumeDispatchedEnv); err != nil {
		t.Fatalf("unmarshal consumed dispatched output: %v\nOutput: %s", err, string(consumeDispatchedOut))
	}

	consumedDispatched := consumeDispatchedEnv.Data
	if consumedDispatched.ID != dispatchedID {
		t.Fatalf("consumed dispatched ID = %q, want %q", consumedDispatched.ID, dispatchedID)
	}
	if consumedDispatched.Status != "consumed" {
		t.Fatalf("expected status 'consumed', got %q", consumedDispatched.Status)
	}
	if !strings.Contains(consumedDispatched.PayloadMD, "Task payload for dogfood.") {
		t.Fatalf("consumed dispatched payload missing expected content: %q", consumedDispatched.PayloadMD)
	}
}
