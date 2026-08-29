package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

	var issued struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		PayloadMD string `json:"payload_md"`
		DodMD     string `json:"dod_md"`
		IssuedBy  string `json:"issued_by"`
	}
	if err := json.Unmarshal(issueOut, &issued); err != nil {
		t.Fatalf("unmarshal brief-issue output: %v\nOutput: %s", err, string(issueOut))
	}

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

	var consumed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(consumeOut, &consumed); err != nil {
		t.Fatalf("unmarshal brief-consume output: %v\nOutput: %s", err, string(consumeOut))
	}

	if consumed.ID != issued.ID {
		t.Fatalf("consumed ID = %q, want %q", consumed.ID, issued.ID)
	}
	if consumed.Status != "consumed" {
		t.Fatalf("expected status 'consumed', got %q", consumed.Status)
	}
}
