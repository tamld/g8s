package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyExecutable(t *testing.T) {
	// Create a temporary executable for testing
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "test-tool")

	// Create a simple script that prints version
	content := `#!/bin/sh
echo "test-tool version 1.0.0"
`
	err := os.WriteFile(exePath, []byte(content), 0o755)
	if err != nil {
		t.Fatalf("Failed to create test executable: %v", err)
	}

	tests := []struct {
		name         string
		path         string
		opts         VerifyOptions
		wantErr      bool
		wantVerified bool
		wantBaseName string
	}{
		{
			name:         "valid executable with expected name",
			path:         exePath,
			opts:         VerifyOptions{ExpectedNames: []string{"test-tool"}},
			wantErr:      false,
			wantVerified: true,
			wantBaseName: "test-tool",
		},
		{
			name:         "valid executable without expected names",
			path:         exePath,
			opts:         VerifyOptions{},
			wantErr:      false,
			wantVerified: true,
			wantBaseName: "test-tool",
		},
		{
			name:         "non-existent executable",
			path:         filepath.Join(tmpDir, "nonexistent"),
			opts:         VerifyOptions{},
			wantErr:      true,
			wantVerified: false,
		},
		{
			name:         "mismatched expected name",
			path:         exePath,
			opts:         VerifyOptions{ExpectedNames: []string{"other-tool"}},
			wantErr:      true,
			wantVerified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := VerifyExecutable(tt.path, tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result.Verified != tt.wantVerified {
					t.Errorf("Verified = %v, want %v", result.Verified, tt.wantVerified)
				}
				if result.BaseName != tt.wantBaseName {
					t.Errorf("BaseName = %v, want %v", result.BaseName, tt.wantBaseName)
				}
			}
		})
	}
}

func TestVerifyExecutable_AllowedDirs(t *testing.T) {
	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	disallowedDir := filepath.Join(tmpDir, "disallowed")

	err := os.MkdirAll(allowedDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(disallowedDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	exePath := filepath.Join(allowedDir, "mytool")
	content := `#!/bin/sh
echo "mytool version 1.0"
`
	err = os.WriteFile(exePath, []byte(content), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	// Test with allowed dir
	result, err := VerifyExecutable(exePath, VerifyOptions{AllowedDirs: []string{allowedDir}})
	if err != nil {
		t.Errorf("Expected no error for allowed dir: %v", err)
	}
	if !result.Verified {
		t.Error("Expected verified for allowed dir")
	}

	// Test with disallowed dir
	_, err = VerifyExecutable(exePath, VerifyOptions{AllowedDirs: []string{disallowedDir}})
	if err == nil {
		t.Error("Expected error for disallowed dir")
	}
}

func TestVerifyCommandIdentity(t *testing.T) {
	// Test with a known system command
	// This will vary by system, so we just test it doesn't crash
	_, result, err := VerifyCommandIdentity("sh", []string{"-c", "echo test"})
	// On most systems, sh exists
	if err != nil {
		t.Logf("VerifyCommandIdentity for 'sh': %v (may be expected on some systems)", err)
	} else {
		if result.BaseName != "sh" {
			t.Errorf("Expected base name 'sh', got %s", result.BaseName)
		}
	}
}

func TestRunWithTimeout(t *testing.T) {
	// Test command that completes quickly
	stdout, stderr, err := RunWithTimeout(1*time.Second, "sh", "-c", "echo hello")
	if err != nil {
		t.Errorf("RunWithTimeout failed: %v", err)
	}
	if string(stdout) != "hello\n" {
		t.Errorf("Expected 'hello\\n', got %q", string(stdout))
	}
	if len(stderr) != 0 {
		t.Errorf("Expected empty stderr, got %q", string(stderr))
	}
}

func TestRunWithTimeout_Timeout(t *testing.T) {
	// Test command that times out
	_, _, err := RunWithTimeout(100*time.Millisecond, "sh", "-c", "sleep 2")
	if err != ErrCommandTimeout {
		t.Errorf("Expected ErrCommandTimeout, got %v", err)
	}
}

func TestRunWithTimeoutAndVerify(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "verify-tool")
	content := `#!/bin/sh
echo "verify-tool version 1.0"
`
	err := os.WriteFile(exePath, []byte(content), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	result, _, _, err := RunWithTimeoutAndVerify(5*time.Second, exePath, []string{}, VerifyOptions{
		ExpectedNames: []string{"verify-tool"},
		CheckShebang:  true,
	})
	if err != nil {
		t.Errorf("RunWithTimeoutAndVerify failed: %v", err)
	}
	if !result.Verified {
		t.Error("Expected verified result")
	}
	if result.BaseName != "verify-tool" {
		t.Errorf("Expected base name 'verify-tool', got %s", result.BaseName)
	}
	if result.IsScript {
		t.Logf("Detected as script with interpreter: %s", result.Interpreter)
	}
}
