package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/provider"
)

func captureStdoutWithPterm(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	pterm.SetDefaultOutput(w)
	defer func() {
		os.Stdout = oldStdout
		pterm.SetDefaultOutput(oldStdout)
	}()

	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestRunProviders_TextTable(t *testing.T) {
	out := captureStdoutWithPterm(t, func() {
		runProviders([]string{})
	})

	expectedProviders := []string{"agy", "codex", "claude", "ollama"}
	for _, p := range expectedProviders {
		if !strings.Contains(strings.ToLower(out), p) {
			t.Errorf("expected output to contain provider %q, got:\n%s", p, out)
		}
	}
}

func TestRunProviders_JSONEnvelope(t *testing.T) {
	out := captureStdoutWithPterm(t, func() {
		runProviders([]string{"--json"})
	})

	var env cli.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("failed to parse JSON envelope: %v\nOutput: %s", err, out)
	}

	if env.Kind != "providers_list" {
		t.Errorf("expected kind 'providers_list', got %q", env.Kind)
	}
	if env.Command != "providers" {
		t.Errorf("expected command 'providers', got %q", env.Command)
	}

	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("failed to remarshal data: %v", err)
	}

	var statuses []provider.ProviderStatus
	if err := json.Unmarshal(raw, &statuses); err != nil {
		t.Fatalf("failed to parse ProviderStatus slice: %v", err)
	}

	if len(statuses) < 4 {
		t.Fatalf("expected at least 4 providers, got %d", len(statuses))
	}

	names := make(map[string]bool)
	for _, st := range statuses {
		names[st.Name] = true
		if st.Status != "OK" && st.Status != "NO" {
			t.Errorf("unexpected status %q for provider %s", st.Status, st.Name)
		}
	}

	for _, p := range []string{"agy", "codex", "claude", "ollama"} {
		if !names[p] {
			t.Errorf("missing provider %q in statuses", p)
		}
	}
}

func TestOrchestrate_ProviderFlagBackwardCompatibility(t *testing.T) {
	t.Setenv("G8S_DB", t.TempDir()+"/test.db")
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()

	var workerCalled bool
	orchestratorWorkerCtor = func() orchestrator.Worker {
		workerCalled = true
		return &trackingStubWorker{}
	}

	// 1. Without --provider (backward compat, defaults to agy)
	captureStdout(t, func() {
		runOrchestrate([]string{"--from-intent", "echo test", "--json"})
	})

	if !workerCalled {
		t.Error("expected default orchestratorWorkerCtor to be invoked without --provider flag")
	}

	// 2. With explicit --provider agy
	workerCalled = false
	captureStdout(t, func() {
		runOrchestrate([]string{"--provider", "agy", "--from-intent", "echo test", "--json"})
	})

	if !workerCalled {
		t.Error("expected default orchestratorWorkerCtor to be invoked with --provider agy")
	}
}
