package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeMockWorkerScript(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"mock " + name + " v1.0.0\"\n  exit 0\nfi\necho \"mock worker run completed\"\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write mock worker script: %v", err)
	}
	return path
}

func TestCatalogAndRecommend(t *testing.T) {
	entries := Catalog()
	if len(entries) < 5 {
		t.Fatalf("expected at least 5 catalog entries, got %d", len(entries))
	}

	reg := NewRegistry()
	cat := reg.Recommend(context.Background())
	if len(cat) != len(entries) {
		t.Errorf("expected %d recommended entries, got %d", len(entries), len(cat))
	}

	// Verify probeOne for registered vs unregistered
	// Execution smoke for a registered provider: may succeed (installed)
	// or error (absent binary) — the guarantee is only that it must not panic.
	_ = reg.probeOne(context.Background(), "agy")
	if err := reg.probeOne(context.Background(), "non-existent-provider"); err == nil {
		t.Error("expected error for non-existent provider in probeOne")
	}

	// RegisterHTTP
	reg.RegisterHTTP("9router-test", "http://localhost:20128/v1", "TEST_KEY")
	p, err := reg.Get("9router-test")
	if err != nil {
		t.Fatalf("expected 9router-test to be registered, got %v", err)
	}
	if p.Name() != "9router-test" {
		t.Errorf("expected name 9router-test, got %s", p.Name())
	}
}

func TestClaudeProvider_Extended(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock worker is a POSIX shell script — exec semantics differ on Windows")
	}
	tmpDir := t.TempDir()
	mockBin := writeMockWorkerScript(t, tmpDir, "claude")

	p := NewClaudeProvider()
	if p.Binary() != "claude" {
		t.Errorf("expected default binary claude, got %s", p.Binary())
	}

	p.WithLookPath(func(file string) (string, error) {
		return mockBin, nil
	})

	t.Setenv("CLAUDE_BIN", "")
	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available failed: %v", err)
	}

	if p.Binary() != mockBin {
		t.Errorf("expected binary %s, got %s", mockBin, p.Binary())
	}

	// Version cached
	ver, err := p.Version(context.Background())
	if err != nil || !strings.Contains(ver, "claude") {
		t.Errorf("Version unexpected: %s, %v", ver, err)
	}

	// Spawn
	spec := Spec{
		Brief:       "refactor module",
		WorktreeDir: tmpDir,
	}
	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if receipt.Status != "COMPLETED" {
		t.Errorf("expected COMPLETED, got %s", receipt.Status)
	}

	// Unavailable spawn failure
	pFail := NewClaudeProvider().WithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	})
	t.Setenv("CLAUDE_BIN", "")
	_, err = pFail.Spawn(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error on spawn when unavailable")
	}
}

func TestAgyProvider_Extended(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock worker is a POSIX shell script — exec semantics differ on Windows")
	}
	tmpDir := t.TempDir()
	mockBin := writeMockWorkerScript(t, tmpDir, "agy")

	p := NewAgyProvider()
	if p.Binary() != "agy" {
		t.Errorf("expected default binary agy, got %s", p.Binary())
	}

	t.Setenv("AGY_BIN", mockBin)
	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available failed: %v", err)
	}

	// Cached version
	ver, err := p.Version(context.Background())
	if err != nil || !strings.Contains(ver, "agy") {
		t.Errorf("Version unexpected: %s, %v", ver, err)
	}

	// Spawn with all fields
	spec := Spec{
		Model:       "Gemini 3.8 Flash (High)",
		Brief:       "analyze diff",
		AddDirs:     []string{tmpDir},
		WorktreeDir: tmpDir,
	}
	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if receipt.Status != "COMPLETED" {
		t.Errorf("expected COMPLETED, got %s", receipt.Status)
	}
}

func TestCodexProvider_Extended(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock worker is a POSIX shell script — exec semantics differ on Windows")
	}
	tmpDir := t.TempDir()
	mockBin := writeMockWorkerScript(t, tmpDir, "codex")

	p := NewCodexProvider()
	if p.Binary() != "codex" {
		t.Errorf("expected default binary codex, got %s", p.Binary())
	}

	t.Setenv("CODEX_BIN", mockBin)
	if err := p.Available(context.Background()); err != nil {
		t.Fatalf("Available failed: %v", err)
	}
	if p.Binary() != mockBin {
		t.Errorf("expected binary %s, got %s", mockBin, p.Binary())
	}

	ver, err := p.Version(context.Background())
	if err != nil || !strings.Contains(ver, "codex") {
		t.Errorf("Version unexpected: %s, %v", ver, err)
	}

	_, err = p.Spawn(context.Background(), Spec{Brief: "test"})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented error from codex spawn, got %v", err)
	}
}

func TestOllamaProvider_Extended(t *testing.T) {
	p := NewOllamaProvider()
	if p.Binary() != "ollama" {
		t.Errorf("expected binary ollama, got %s", p.Binary())
	}

	p.WithHTTPClient(&http.Client{Timeout: 1 * time.Second})

	t.Setenv("OLLAMA_HOST", "http://custom-host:11434/")
	if p.getHost() != "http://custom-host:11434" {
		t.Errorf("expected trimmed host, got %s", p.getHost())
	}

	// Available error when server returns 500
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()

	t.Setenv("OLLAMA_HOST", srv500.URL)
	if err := p.Available(context.Background()); err == nil {
		t.Error("expected error for status 500")
	}

	// Available error with bad URL
	t.Setenv("OLLAMA_HOST", "http://bad url")
	_ = p.Available(context.Background())

	// Version fallback when server returns 500
	t.Setenv("OLLAMA_HOST", srv500.URL)
	p500 := NewOllamaProvider()
	ver, err := p500.Version(context.Background())
	if err != nil || ver != "v0.1.0" {
		t.Errorf("expected fallback version v0.1.0, got %s, err: %v", ver, err)
	}

	// Version fallback when server returns invalid JSON
	srvBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srvBadJSON.Close()
	t.Setenv("OLLAMA_HOST", srvBadJSON.URL)
	pBadJSON := NewOllamaProvider()
	ver, err = pBadJSON.Version(context.Background())
	if err != nil || ver != "v0.1.0" {
		t.Errorf("expected fallback version v0.1.0, got %s, err: %v", ver, err)
	}

	// Spawn returns not implemented when available
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srvOK.Close()
	t.Setenv("OLLAMA_HOST", srvOK.URL)
	pOK := NewOllamaProvider()
	_, err = pOK.Spawn(context.Background(), Spec{Brief: "test"})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestOpenAIProvider_Extended(t *testing.T) {
	p := NewOpenAIProvider("test-openai", "http://localhost:1234/v1", "TEST_AUTH")
	if p.Name() != "test-openai" {
		t.Errorf("expected name test-openai, got %s", p.Name())
	}
	if p.Binary() != "openai" {
		t.Errorf("expected binary openai, got %s", p.Binary())
	}

	p.WithHTTPClient(&http.Client{Timeout: 2 * time.Second})

	ver, err := p.Version(context.Background())
	if err != nil || ver != "openai-compatible-v1" {
		t.Errorf("Version unexpected: %s, %v", ver, err)
	}
	// Call Version again to hit cached branch
	p.mu.Lock()
	p.version = "cached-ver"
	p.mu.Unlock()
	verCached, _ := p.Version(context.Background())
	if verCached != "cached-ver" {
		t.Errorf("expected cached-ver, got %s", verCached)
	}

	// buildMessages test
	specWithSys := Spec{SystemPrompt: "You are an assistant", Brief: "Hello"}
	msgs := buildMessages(specWithSys)
	if len(msgs) != 2 || msgs[0]["role"] != "system" || msgs[1]["role"] != "user" {
		t.Errorf("buildMessages with system prompt failed: %+v", msgs)
	}

	// Available error on 500
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	pErr := NewOpenAIProvider("test-err", srv500.URL, "")
	if err := pErr.Available(context.Background()); err == nil {
		t.Error("expected error for 500 in Available")
	}

	// Available with bad URL
	pBadURL := NewOpenAIProvider("bad", "http://bad url", "")
	_ = pBadURL.Available(context.Background())

	// Spawn with API error response body
	srvAPIError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error": {"message": "model overloaded"}}`))
	}))
	defer srvAPIError.Close()

	pAPIError := NewOpenAIProvider("test-overloaded", srvAPIError.URL, "")
	h, err := pAPIError.Spawn(context.Background(), Spec{Model: "gpt-4o", Brief: "hi"})
	if err != nil {
		t.Fatalf("unexpected spawn error: %v", err)
	}
	receipt, err := h.Wait(context.Background())
	if err == nil || receipt.Status != "FAILED" {
		t.Errorf("expected failed receipt on api error: %+v, err: %v", receipt, err)
	}

	// Spawn with empty choices
	srvEmptyChoices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices": []}`))
	}))
	defer srvEmptyChoices.Close()

	pEmpty := NewOpenAIProvider("test-empty", srvEmptyChoices.URL, "")
	hEmpty, err := pEmpty.Spawn(context.Background(), Spec{Model: "gpt-4o", Brief: "hi"})
	if err != nil {
		t.Fatalf("unexpected spawn error: %v", err)
	}
	receipt, err = hEmpty.Wait(context.Background())
	if err == nil || receipt.Status != "FAILED" {
		t.Errorf("expected failed receipt on empty choices: %+v, err: %v", receipt, err)
	}

	// Spawn with HTTP 500 error in chat request
	srvChat500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srvChat500.Close()
	pChat500 := NewOpenAIProvider("test-500", srvChat500.URL, "")
	h500, err := pChat500.Spawn(context.Background(), Spec{Model: "gpt-4o", Brief: "hi"})
	if err != nil {
		t.Fatalf("unexpected spawn error: %v", err)
	}
	receipt, err = h500.Wait(context.Background())
	if err == nil || receipt.Status != "FAILED" {
		t.Errorf("expected failed receipt on 500: %+v, err: %v", receipt, err)
	}

	// immediateHandle coverage: PID, Cancel, StdoutStream
	imm := newImmediateHandle("test-prov", "COMPLETED", "output text", "", 0, time.Now())
	if imm.PID() != 0 {
		t.Errorf("expected PID 0, got %d", imm.PID())
	}
	if err := imm.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel returned error: %v", err)
	}
	stream := imm.StdoutStream()
	body, _ := io.ReadAll(stream)
	if string(body) != "output text" {
		t.Errorf("unexpected StdoutStream: %s", string(body))
	}
}

func TestExecHandle_Extended(t *testing.T) {
	// Zero/nil cmd handle
	hNil := &execHandle{}
	if hNil.PID() != 0 {
		t.Errorf("expected PID 0 for nil cmd, got %d", hNil.PID())
	}
	if err := hNil.Cancel(context.Background()); err != nil {
		t.Errorf("expected nil error on cancel for 0 PID, got %v", err)
	}
	stream := hNil.StdoutStream()
	buf, _ := io.ReadAll(stream)
	if len(buf) != 0 {
		t.Errorf("expected empty stream, got %q", string(buf))
	}

	// Process timeout cancellation
	cmd := exec.Command("sleep", "10")
	configureSysProcAttr(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Skip("unable to start sleep command")
	}
	h := newProcessHandle("sleep-test", cmd, time.Now(), &stdout, &stderr)

	ctxTimeout, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	receipt, err := h.Wait(ctxTimeout)
	if receipt.Status != "TIMEOUT" {
		t.Errorf("expected TIMEOUT status, got %s", receipt.Status)
	}
	if receipt.ExitCode != -1 {
		t.Errorf("expected ExitCode -1, got %d", receipt.ExitCode)
	}
	if err == nil {
		t.Error("expected error on timeout wait")
	}

	// Non-zero exit code process
	cmdFail := exec.Command("sh", "-c", "exit 42")
	configureSysProcAttr(cmdFail)
	var stdoutFail, stderrFail bytes.Buffer
	cmdFail.Stdout = &stdoutFail
	cmdFail.Stderr = &stderrFail
	if err := cmdFail.Start(); err != nil {
		t.Skip("unable to start sh command")
	}
	hFail := newProcessHandle("fail-test", cmdFail, time.Now(), &stdoutFail, &stderrFail)
	receiptFail, waitErr := hFail.Wait(context.Background())
	if receiptFail.Status != "FAILED" {
		t.Errorf("expected FAILED status, got %s", receiptFail.Status)
	}
	if receiptFail.ExitCode != 42 {
		t.Errorf("expected ExitCode 42, got %d", receiptFail.ExitCode)
	}
	if waitErr == nil {
		t.Error("expected wait error for non-zero exit")
	}

	// killProcessGroup with <= 0
	if err := killProcessGroup(0); err == nil {
		t.Error("expected error from killProcessGroup(0)")
	}
}

func TestPoolRegistry_GetProvider_Cached(t *testing.T) {
	reg := NewPoolRegistry(DefaultConfigs(), nil, nil)

	// Unknown provider
	_, err := reg.GetProvider("non-existent")
	if err == nil {
		t.Error("expected error for unknown provider in GetProvider")
	}

	// First call probes lazily
	info1, err := reg.GetProvider("agy")
	if err != nil {
		t.Fatalf("first GetProvider(agy) failed: %v", err)
	}
	if info1.Name != "agy" {
		t.Errorf("expected name agy, got %s", info1.Name)
	}

	// Second call hits cached branch (neverProbed is false)
	info2, err := reg.GetProvider("agy")
	if err != nil {
		t.Fatalf("second GetProvider(agy) failed: %v", err)
	}
	if info2.Name != "agy" {
		t.Errorf("expected name agy, got %s", info2.Name)
	}
}
