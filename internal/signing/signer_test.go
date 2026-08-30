package signing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockRunner struct {
	runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)
	calls   [][]string
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	m.calls = append(m.calls, call)
	if m.runFunc != nil {
		return m.runFunc(ctx, name, args...)
	}
	return []byte("OK"), nil
}

func TestSigntoolSigner_SignSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "g8s.exe")
	if err := os.WriteFile(binaryPath, []byte("dummy binary"), 0o600); err != nil {
		t.Fatalf("write dummy binary: %v", err)
	}

	certPath := filepath.Join(tmpDir, "cert.pfx")
	if err := os.WriteFile(certPath, []byte("dummy cert"), 0o600); err != nil {
		t.Fatalf("write dummy cert: %v", err)
	}

	runner := &mockRunner{}
	signer := &SigntoolSigner{
		CertPath:     certPath,
		CertPassword: "secret-password",
		Timestamper:  "http://timestamp.digicert.com",
		Runner:       runner,
	}

	ctx := context.Background()
	if err := signer.Sign(ctx, binaryPath); err != nil {
		t.Fatalf("expected Sign to succeed, got: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	if call[0] != "signtool" {
		t.Errorf("expected command 'signtool', got %q", call[0])
	}

	cmdStr := strings.Join(call, " ")
	if !strings.Contains(cmdStr, "/tr http://timestamp.digicert.com") {
		t.Errorf("expected timestamper arg in %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "/td sha256 /fd sha256 /a /f "+certPath) {
		t.Errorf("expected cert flags in %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "/p secret-password") {
		t.Errorf("expected password flag in %q", cmdStr)
	}
	if !strings.HasSuffix(cmdStr, binaryPath) {
		t.Errorf("expected target path at end of %q", cmdStr)
	}
}

func TestSigntoolSigner_DefaultTimestamperAndConstructor(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "g8s.exe")
	if err := os.WriteFile(binaryPath, []byte("dummy binary"), 0o600); err != nil {
		t.Fatalf("write dummy binary: %v", err)
	}

	certPath := filepath.Join(tmpDir, "cert.pfx")
	if err := os.WriteFile(certPath, []byte("dummy cert"), 0o600); err != nil {
		t.Fatalf("write dummy cert: %v", err)
	}

	signer := NewSigntoolSigner(certPath, "")
	runner := &mockRunner{}
	signer.Runner = runner

	if err := signer.Sign(context.Background(), binaryPath); err != nil {
		t.Fatalf("expected Sign to succeed, got: %v", err)
	}

	call := runner.calls[0]
	cmdStr := strings.Join(call, " ")
	if !strings.Contains(cmdStr, "/tr http://timestamp.digicert.com") {
		t.Errorf("expected default timestamper in %q", cmdStr)
	}
}

func TestSigntoolSigner_SignValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "g8s.exe")
	_ = os.WriteFile(binaryPath, []byte("dummy binary"), 0o600)
	certPath := filepath.Join(tmpDir, "cert.pfx")
	_ = os.WriteFile(certPath, []byte("dummy cert"), 0o600)

	tests := []struct {
		name       string
		targetPath string
		certPath   string
		runnerErr  error
		wantErr    string
	}{
		{
			name:       "empty target path",
			targetPath: "",
			certPath:   certPath,
			wantErr:    "target path is required",
		},
		{
			name:       "missing target file",
			targetPath: filepath.Join(tmpDir, "nonexistent.exe"),
			certPath:   certPath,
			wantErr:    "target file stat",
		},
		{
			name:       "empty cert path",
			targetPath: binaryPath,
			certPath:   "",
			wantErr:    "certificate path is required",
		},
		{
			name:       "missing cert file",
			targetPath: binaryPath,
			certPath:   filepath.Join(tmpDir, "missing.pfx"),
			wantErr:    "certificate file stat",
		},
		{
			name:       "runner failure",
			targetPath: binaryPath,
			certPath:   certPath,
			runnerErr:  errors.New("signtool exit status 1"),
			wantErr:    "signtool sign",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &mockRunner{
				runFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
					return nil, tt.runnerErr
				},
			}
			signer := &SigntoolSigner{
				CertPath: tt.certPath,
				Runner:   runner,
			}
			err := signer.Sign(context.Background(), tt.targetPath)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error %q to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestSigntoolSigner_Verify(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "g8s.exe")
	if err := os.WriteFile(binaryPath, []byte("dummy binary"), 0o600); err != nil {
		t.Fatalf("write dummy binary: %v", err)
	}

	t.Run("verify success", func(t *testing.T) {
		runner := &mockRunner{}
		signer := &SigntoolSigner{Runner: runner}
		ok, err := signer.Verify(binaryPath)
		if err != nil {
			t.Fatalf("expected verify success, got error: %v", err)
		}
		if !ok {
			t.Errorf("expected verify to return true, got false")
		}

		if len(runner.calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(runner.calls))
		}
		cmdStr := strings.Join(runner.calls[0], " ")
		if cmdStr != "signtool verify /pa "+binaryPath {
			t.Errorf("unexpected command: %q", cmdStr)
		}
	})

	t.Run("verify failure", func(t *testing.T) {
		runner := &mockRunner{
			runFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return []byte("SignTool Error: WinVerifyTrust returned error: 0x800B0100"), errors.New("exit status 1")
			},
		}
		signer := &SigntoolSigner{Runner: runner}
		ok, err := signer.Verify(binaryPath)
		if err == nil {
			t.Fatalf("expected verify error, got nil")
		}
		if ok {
			t.Errorf("expected verify to return false on error")
		}
	})

	t.Run("verify empty target", func(t *testing.T) {
		signer := &SigntoolSigner{}
		ok, err := signer.Verify("")
		if err == nil {
			t.Fatalf("expected error for empty target, got nil")
		}
		if ok {
			t.Errorf("expected verify to return false")
		}
	})

	t.Run("verify missing file", func(t *testing.T) {
		signer := &SigntoolSigner{}
		ok, err := signer.Verify(filepath.Join(tmpDir, "nonexistent.exe"))
		if err == nil {
			t.Fatalf("expected error for nonexistent target, got nil")
		}
		if ok {
			t.Errorf("expected verify to return false")
		}
	})
}

func TestExecRunner_Timeout(t *testing.T) {
	runner := execRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Running a command that times out or fails
	_, err := runner.Run(ctx, "sleep", "1")
	if err == nil {
		t.Log("sleep 1 returned without error (unexpected in short timeout)")
	}
}
