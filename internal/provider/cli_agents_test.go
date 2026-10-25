package provider

import (
	"context"
	"os"
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
	codexBin := writeMockWorkerScript(t, tmpDir, "codex")

	reg := NewRegistry()
	for _, name := range []string{"opencode", "commandcode", "codex"} {
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
}
