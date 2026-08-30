package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockProvider struct {
	name      string
	binary    string
	version   string
	available error
	spawnErr  error
}

func (m *mockProvider) Name() string                                      { return m.name }
func (m *mockProvider) Binary() string                                    { return m.binary }
func (m *mockProvider) Version(_ context.Context) (string, error)         { return m.version, nil }
func (m *mockProvider) Available(_ context.Context) error                 { return m.available }
func (m *mockProvider) Spawn(_ context.Context, _ Spec) (Handle, error) { return nil, m.spawnErr }

func TestRegistry_NewAndDefaultProviders(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	expected := []string{"agy", "codex", "claude", "ollama"}
	for _, name := range expected {
		p, err := reg.Get(name)
		if err != nil {
			t.Errorf("expected provider %q registered, got err: %v", name, err)
		}
		if p.Name() != name {
			t.Errorf("expected name %q, got %q", name, p.Name())
		}
	}
}

func TestRegistry_GetCaseInsensitive(t *testing.T) {
	reg := NewRegistry()

	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"agy", "agy", false},
		{"AGY", "agy", false},
		{"Claude", "claude", false},
		{"OLLAMA", "ollama", false},
		{"unknown", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			p, err := reg.Get(tc.input)
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrProviderNotFound) {
					t.Fatalf("expected ErrProviderNotFound for %q, got %v", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if p.Name() != tc.want {
				t.Errorf("got %q, want %q", p.Name(), tc.want)
			}
		})
	}
}

func TestRegistry_AutoDetect(t *testing.T) {
	p1 := &mockProvider{name: "p1", available: nil}
	p2 := &mockProvider{name: "p2", available: errors.New("missing binary")}
	p3 := &mockProvider{name: "p3", available: nil}

	reg := NewRegistry(p1, p2, p3)
	available := reg.AutoDetect(context.Background())

	if len(available) != 2 {
		t.Fatalf("expected 2 available providers, got %d", len(available))
	}
	if available[0].Name() != "p1" || available[1].Name() != "p3" {
		t.Errorf("unexpected available slice: %v", available)
	}
}

func TestRegistry_List(t *testing.T) {
	p1 := &mockProvider{name: "agy", binary: "/usr/local/bin/agy", version: "v0.7.0", available: nil}
	p2 := &mockProvider{name: "codex", binary: "codex", available: errors.New("not found in PATH")}

	reg := NewRegistry(p1, p2)
	statuses := reg.List(context.Background())

	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	if statuses[0].Name != "agy" || statuses[0].Status != "OK" || statuses[0].BinaryPath != "/usr/local/bin/agy" || statuses[0].Version != "v0.7.0" {
		t.Errorf("unexpected status for agy: %+v", statuses[0])
	}

	if statuses[1].Name != "codex" || statuses[1].Status != "NO" || statuses[1].Reason != "not found in PATH" {
		t.Errorf("unexpected status for codex: %+v", statuses[1])
	}
}

func TestAgyProvider_AvailableAndVersion(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "agy")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"agy v0.7.0\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	t.Setenv("AGY_BIN", mockBin)
	p := NewAgyProvider()

	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available() failed: %v", err)
	}

	ver, err := p.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if ver != "agy v0.7.0" {
		t.Errorf("expected 'agy v0.7.0', got %q", ver)
	}
	if p.Binary() != mockBin {
		t.Errorf("expected binary %q, got %q", mockBin, p.Binary())
	}
}

func TestAgyProvider_MissingBinary(t *testing.T) {
	t.Setenv("AGY_BIN", "")
	p := NewAgyProvider().WithLookPath(func(string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	})

	err := p.Available(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("unexpected error message: %v", err)
	}

	_, spawnErr := p.Spawn(context.Background(), Spec{Brief: "hello"})
	if spawnErr == nil || !errors.Is(spawnErr, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable on spawn, got: %v", spawnErr)
	}
}

func TestCodexProvider_Stub(t *testing.T) {
	t.Setenv("CODEX_BIN", "")
	p := NewCodexProvider().WithLookPath(func(string) (string, error) {
		return "", errors.New("not found")
	})

	if p.Name() != "codex" {
		t.Errorf("got %q, want codex", p.Name())
	}
	if err := p.Available(context.Background()); err == nil {
		t.Fatal("expected Available() to fail for codex stub")
	}
	if _, err := p.Spawn(context.Background(), Spec{Brief: "task"}); err == nil {
		t.Fatal("expected Spawn() to fail for codex stub")
	}
}

func TestClaudeProvider_AvailableAndVersion(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "claude")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo \"claude 1.0.0\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	t.Setenv("CLAUDE_BIN", mockBin)
	p := NewClaudeProvider()

	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available() failed: %v", err)
	}

	ver, err := p.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if ver != "claude 1.0.0" {
		t.Errorf("expected 'claude 1.0.0', got %q", ver)
	}
}

func TestOllamaProvider_Probing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/version":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version":"0.3.14"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	t.Setenv("OLLAMA_HOST", ts.URL)
	p := NewOllamaProvider()

	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available() failed against mock server: %v", err)
	}

	ver, err := p.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() failed: %v", err)
	}
	if ver != "0.3.14" {
		t.Errorf("expected '0.3.14', got %q", ver)
	}

	// Test unreachable host
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:59999")
	p2 := NewOllamaProvider()
	if err := p2.Available(context.Background()); err == nil {
		t.Fatal("expected error against unreachable host, got nil")
	}
}

func TestHandle_ProcessExecutionAndReceipt(t *testing.T) {
	echoBin, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo binary not found")
	}

	cmd := exec.Command(echoBin, "hello world")
	configureSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	handle := newProcessHandle("echo-test", cmd, start, &stdoutBuf, &stderrBuf)
	if handle.PID() <= 0 {
		t.Errorf("expected PID > 0, got %d", handle.PID())
	}

	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("handle.Wait failed: %v", err)
	}

	if receipt.Status != "COMPLETED" {
		t.Errorf("expected status COMPLETED, got %q", receipt.Status)
	}
	if receipt.ExitCode != 0 {
		t.Errorf("expected ExitCode 0, got %d", receipt.ExitCode)
	}
	if !strings.Contains(receipt.Stdout, "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got %q", receipt.Stdout)
	}

	stream := handle.StdoutStream()
	if stream != nil {
		content, rErr := io.ReadAll(stream)
		if rErr != nil {
			t.Errorf("StdoutStream read error: %v", rErr)
		}
		if !strings.Contains(string(content), "hello world") {
			t.Errorf("StdoutStream content mismatch: %q", string(content))
		}
	}
}
