package verifier

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanary_InjectSyntheticMutant(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		input        string
		expectedType string
		expectedDiff string
	}{
		{
			name:         "js strict equality flip",
			filename:     "app.js",
			input:        "// comment with ==\nconst ok = (a === b);",
			expectedType: "js_strict_eq_flip",
			expectedDiff: "const ok = (a !== b);",
		},
		{
			name:         "operator flip eq to neq",
			filename:     "service.go",
			input:        "// comment\n# bash comment\nif a == 10 {",
			expectedType: "operator_flip_eq_to_neq",
			expectedDiff: "if a != 10 {",
		},
		{
			name:         "operator flip neq to eq",
			filename:     "check.py",
			input:        "# comment\nif user_id != 'admin':",
			expectedType: "operator_flip_neq_to_eq",
			expectedDiff: "if user_id == 'admin':",
		},
		{
			name:         "bool flip true to false",
			filename:     "config.go",
			input:        "// comment\nvar IsActive = true",
			expectedType: "bool_flip_true_to_false",
			expectedDiff: "var IsActive = false",
		},
		{
			name:         "bool flip false to true",
			filename:     "config.go",
			input:        "// comment\nvar IsDebug = false",
			expectedType: "bool_flip_false_to_true",
			expectedDiff: "var IsDebug = true",
		},
		{
			name:         "status code 206 inversion",
			filename:     "stream.go",
			input:        "w.WriteHeader(206)",
			expectedType: "status_code_inversion",
			expectedDiff: "w.WriteHeader(500)",
		},
		{
			name:         "status code 200 inversion",
			filename:     "server.js",
			input:        "res.status(200).send()",
			expectedType: "status_code_inversion",
			expectedDiff: "res.status(500).send()",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated, mutType, ok := InjectSyntheticMutant([]byte(tc.input), tc.filename)
			if !ok {
				t.Fatalf("expected mutation to succeed, got ok=false")
			}
			if mutType != tc.expectedType {
				t.Errorf("expected mutationType %q, got %q", tc.expectedType, mutType)
			}
			if !strings.Contains(string(mutated), tc.expectedDiff) {
				t.Errorf("expected mutated output to contain %q, got:\n%s", tc.expectedDiff, string(mutated))
			}
		})
	}
}

func TestCanary_InjectSyntheticMutant_NoMutation(t *testing.T) {
	input := "// only comments\n# no mutable tokens\nvar x = 42\n"
	mutated, mutType, ok := InjectSyntheticMutant([]byte(input), "test.go")
	if ok {
		t.Errorf("expected ok=false for non-mutable input, got ok=true, mutType=%q", mutType)
	}
	if !bytes.Equal(mutated, []byte(input)) {
		t.Errorf("expected original bytes preserved when ok=false")
	}
}

func TestCanary_RunCheck_MutantCaught(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "auth.go")
	origContent := []byte("package auth\n\nfunc IsAdmin(role string) bool {\n\treturn role == \"admin\"\n}\n")
	if err := os.WriteFile(srcFile, origContent, 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ctx := context.Background()

	// ProbeRunner verifies that mutant is caught (fails test when mutated)
	probeRan := false
	probeRunner := func(ctx context.Context, targetDir string) (bool, string, error) {
		probeRan = true
		content, err := os.ReadFile(srcFile)
		if err != nil {
			return false, err.Error(), err
		}
		// In the probe runner, the file must be currently mutated!
		if !strings.Contains(string(content), "role != \"admin\"") {
			t.Errorf("expected probeRunner to see mutated file, saw:\n%s", string(content))
		}
		// Return passed=false -> verification suite caught the mutant
		return false, "test failed on mutated operator", nil
	}

	result := RunCanaryCheck(ctx, tempDir, []string{"auth.go"}, probeRunner)

	if !probeRan {
		t.Errorf("expected probeRunner to have executed")
	}
	if !result.Executed {
		t.Errorf("expected Executed to be true")
	}
	if !result.CanaryCaught {
		t.Errorf("expected CanaryCaught to be true")
	}
	if result.VetoStatus != "" {
		t.Errorf("expected empty VetoStatus when mutant caught, got %q", result.VetoStatus)
	}
	if result.MutationType != "operator_flip_eq_to_neq" {
		t.Errorf("expected mutationType 'operator_flip_eq_to_neq', got %q", result.MutationType)
	}
	if result.Detail != "seeded canary mutant caught by verification suite" {
		t.Errorf("unexpected detail: %q", result.Detail)
	}

	// Verify file is restored to original state on disk!
	restoredContent, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read restored file: %v", err)
	}
	if !bytes.Equal(restoredContent, origContent) {
		t.Errorf("file was not cleanly restored on disk! Got:\n%s", string(restoredContent))
	}
}

func TestCanary_RunCheck_MutantSurvived_Veto(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "handler.go")
	origContent := []byte("package handler\n\nfunc Handle() bool {\n\treturn true\n}\n")
	if err := os.WriteFile(srcFile, origContent, 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ctx := context.Background()

	// ProbeRunner returns passed=true (blind verification failed to catch mutant!)
	probeRunner := func(ctx context.Context, targetDir string) (bool, string, error) {
		return true, "all tests passed undetected", nil
	}

	result := RunCanaryCheck(ctx, tempDir, []string{"handler.go"}, probeRunner)

	if !result.Executed {
		t.Errorf("expected Executed to be true")
	}
	if result.CanaryCaught {
		t.Errorf("expected CanaryCaught to be false")
	}
	if result.VetoStatus != VetoCanarySurvived {
		t.Errorf("expected VetoStatus %q, got %q", VetoCanarySurvived, result.VetoStatus)
	}
	if result.Detail != "verification blindness veto: seeded canary mutant survived undetected" {
		t.Errorf("unexpected detail: %q", result.Detail)
	}

	// Verify disk restoration
	restoredContent, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read restored file: %v", err)
	}
	if !bytes.Equal(restoredContent, origContent) {
		t.Errorf("file was not cleanly restored on disk!")
	}
}

func TestCanary_RunCheck_GlobResolutionAndSkipTests(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "pkg", "auth")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	// Create a test file (which must be skipped) and a source file
	testFile := filepath.Join(subDir, "auth_test.go")
	_ = os.WriteFile(testFile, []byte("package auth\nfunc TestAuth() { if 1 == 1 {} }"), 0o644)

	srcFile := filepath.Join(subDir, "auth.go")
	origContent := []byte("package auth\nfunc Valid() bool {\n\treturn true\n}\n")
	_ = os.WriteFile(srcFile, origContent, 0o644)

	ctx := context.Background()
	result := RunCanaryCheck(ctx, tempDir, []string{"pkg/auth/**"}, func(ctx context.Context, targetDir string) (bool, string, error) {
		return false, "failed", nil
	})

	if !result.Executed {
		t.Fatalf("expected canary check to execute on pkg/auth/** glob")
	}
	if result.MutatedFile != filepath.Join("pkg", "auth", "auth.go") {
		t.Errorf("expected mutated file to be auth.go (not auth_test.go), got: %s", result.MutatedFile)
	}
}

func TestCanary_RunCheck_NoMutableCandidate(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	result := RunCanaryCheck(ctx, tempDir, []string{"nonexistent.go"}, func(ctx context.Context, targetDir string) (bool, string, error) {
		return true, "", nil
	})

	if result.Executed {
		t.Errorf("expected Executed=false when no candidate files found")
	}
	if result.Detail != "no mutable candidate file found" {
		t.Errorf("expected detail 'no mutable candidate file found', got %q", result.Detail)
	}
}
