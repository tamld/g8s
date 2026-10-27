package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUniversalCLIAgentAdapters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock CLI scripts require POSIX sh")
	}

	tmpDir := t.TempDir()
	opencodeBin := writeMockWorkerScript(t, tmpDir, "opencode")
	commandcodeBin := writeMockWorkerScript(t, tmpDir, "commandcode")
	qwenBin := writeMockWorkerScript(t, tmpDir, "qwen")
	codexBin := writeMockWorkerScript(t, tmpDir, "codex")

	reg := NewRegistry()
	for _, name := range []string{"opencode", "commandcode", "qwen", "codex", "cursor"} {
		if _, err := reg.Get(name); err != nil {
			t.Fatalf("expected provider %q registered in NewRegistry(), got %v", name, err)
		}
	}

	t.Run("opencode adapter", func(t *testing.T) {
		p := NewOpenCodeProvider()
		if p.Binary() != "opencode" {
			t.Fatalf("Binary() = %q, want opencode", p.Binary())
		}
		t.Setenv("OPENCODE_BIN", opencodeBin)
		if err := p.Available(context.Background()); err != nil {
			t.Fatalf("Available failed: %v", err)
		}
		ver, err := p.Version(context.Background())
		if err != nil || !strings.Contains(ver, "opencode") {
			t.Fatalf("Version = %q, err = %v", ver, err)
		}
		handle, err := p.Spawn(context.Background(), Spec{
			Model:       "opencode/free",
			Brief:       "scan repository",
			WorktreeDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rc, err := handle.Wait(context.Background())
		if err != nil || rc.Status != "COMPLETED" {
			t.Fatalf("Wait status = %q, err = %v", rc.Status, err)
		}

		pMissing := NewOpenCodeProvider().WithLookPath(func(string) (string, error) {
			return "", os.ErrNotExist
		})
		t.Setenv("OPENCODE_BIN", "")
		if _, err := pMissing.Spawn(context.Background(), Spec{Brief: "scan"}); err == nil {
			t.Fatal("expected error when opencode binary is missing")
		}
	})

	t.Run("commandcode adapter", func(t *testing.T) {
		p := NewCommandCodeProvider()
		if p.Binary() != "commandcode" {
			t.Fatalf("Binary() = %q, want commandcode", p.Binary())
		}
		t.Setenv("COMMANDCODE_BIN", commandcodeBin)
		if err := p.Available(context.Background()); err != nil {
			t.Fatalf("Available failed: %v", err)
		}
		ver, err := p.Version(context.Background())
		if err != nil || !strings.Contains(ver, "commandcode") {
			t.Fatalf("Version = %q, err = %v", ver, err)
		}
		handle, err := p.Spawn(context.Background(), Spec{
			Model:       "commandcode/free",
			Brief:       "verify tests",
			WorktreeDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rc, err := handle.Wait(context.Background())
		if err != nil || rc.Status != "COMPLETED" {
			t.Fatalf("Wait status = %q, err = %v", rc.Status, err)
		}

		pMissing := NewCommandCodeProvider().WithLookPath(func(string) (string, error) {
			return "", os.ErrNotExist
		})
		t.Setenv("COMMANDCODE_BIN", "")
		if _, err := pMissing.Spawn(context.Background(), Spec{Brief: "scan"}); err == nil {
			t.Fatal("expected error when commandcode binary is missing")
		}
	})

	t.Run("qwen adapter", func(t *testing.T) {
		p := NewQwenProvider()
		if p.Binary() != "qwen" {
			t.Fatalf("Binary() = %q, want qwen", p.Binary())
		}
		t.Setenv("QWEN_BIN", qwenBin)
		if err := p.Available(context.Background()); err != nil {
			t.Fatalf("Available failed: %v", err)
		}
		ver, err := p.Version(context.Background())
		if err != nil || !strings.Contains(ver, "qwen") {
			t.Fatalf("Version = %q, err = %v", ver, err)
		}
		handle, err := p.Spawn(context.Background(), Spec{
			Model:       "docs-retriever",
			Brief:       "verify tests",
			Permission:  "read_only",
			WorktreeDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rc, err := handle.Wait(context.Background())
		if err != nil || rc.Status != "COMPLETED" {
			t.Fatalf("Wait status = %q, err = %v", rc.Status, err)
		}

		pMissing := NewQwenProvider().WithLookPath(func(string) (string, error) {
			return "", os.ErrNotExist
		})
		t.Setenv("QWEN_BIN", "")
		if _, err := pMissing.Spawn(context.Background(), Spec{Brief: "scan"}); err == nil {
			t.Fatal("expected error when qwen binary is missing")
		}

		// #678: qwen exiting 0 with "[API Error: ...]" on stderr and empty stdout must fail closed.
		qwenSilentErrBin := filepath.Join(tmpDir, "qwen-api-err")
		script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'qwen 0.26.1'; exit 0; fi\necho '[API Error: 404 No active credentials for provider: openai]' >&2\nexit 0\n"
		if err := os.WriteFile(qwenSilentErrBin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("QWEN_BIN", qwenSilentErrBin)
		pErr := NewQwenProvider()
		hErr, err := pErr.Spawn(context.Background(), Spec{Model: "missing-model", Brief: "test"})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rcErr, wErr := hErr.Wait(context.Background())
		if wErr == nil || rcErr.Status != "FAILED" || rcErr.ExitCode == 0 {
			t.Fatalf("expected silent [API Error:] exit-0 to fail closed, got status=%q code=%d err=%v", rcErr.Status, rcErr.ExitCode, wErr)
		}
	})

	t.Run("codex exec adapter", func(t *testing.T) {
		p := NewCodexProvider()
		t.Setenv("CODEX_BIN", codexBin)
		handle, err := p.Spawn(context.Background(), Spec{
			Model:       "gpt-5-codex",
			Brief:       "fix bug",
			WorktreeDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rc, err := handle.Wait(context.Background())
		if err != nil || rc.Status != "COMPLETED" {
			t.Fatalf("Wait status = %q, err = %v", rc.Status, err)
		}
	})

	t.Run("cursor adapter", func(t *testing.T) {
		cursorBin := filepath.Join(tmpDir, "cursor-agent")
		cursorScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'cursor-agent 2026.08.25'; exit 0; fi\nif [ \"$1\" = \"--list-models\" ]; then echo 'composer-2.5 - Composer 2.5'; echo 'gemini-4-argon-high - Gemini 4 Argon High'; exit 0; fi\necho 'CURSOR_OK'\nexit 0\n"
		if err := os.WriteFile(cursorBin, []byte(cursorScript), 0o755); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v0/models" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"models":["composer-2.5","claude-opus-5-thinking-high"]}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		p := NewCursorProvider().WithAPIBaseURL(srv.URL).WithHTTPClient(srv.Client())
		if p.Binary() != "cursor-agent" {
			t.Fatalf("Binary() = %q, want cursor-agent", p.Binary())
		}
		t.Setenv("CURSOR_BIN", cursorBin)
		t.Setenv("CURSOR_API_KEY", "crsr_test_key")
		if err := p.Available(context.Background()); err != nil {
			t.Fatalf("Available failed: %v", err)
		}
		ver, err := p.Version(context.Background())
		if err != nil || !strings.Contains(ver, "cursor-agent") {
			t.Fatalf("Version = %q, err = %v", ver, err)
		}
		models, err := p.DiscoverModels(context.Background())
		if err != nil || len(models) < 3 {
			t.Fatalf("DiscoverModels = %v, err = %v", models, err)
		}
		handle, err := p.Spawn(context.Background(), Spec{
			Model:       "composer-2.5",
			Brief:       "verify cursor",
			Permission:  "read_only",
			WorktreeDir: tmpDir,
		})
		if err != nil {
			t.Fatalf("Spawn failed: %v", err)
		}
		rc, err := handle.Wait(context.Background())
		if err != nil || rc.Status != "COMPLETED" || !strings.Contains(rc.Stdout, "CURSOR_OK") {
			t.Fatalf("Wait status = %q, stdout = %q, err = %v", rc.Status, rc.Stdout, err)
		}

		pMissing := NewCursorProvider().WithLookPath(func(string) (string, error) {
			return "", os.ErrNotExist
		})
		t.Setenv("CURSOR_BIN", "")
		if _, err := pMissing.Spawn(context.Background(), Spec{Brief: "scan"}); err == nil {
			t.Fatal("expected error when cursor-agent binary is missing")
		}
	})
}
