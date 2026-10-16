package fsops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveDelegationContext(t *testing.T) {
	// 1. read_only block for mutation
	t.Run("mutation forbidden under read_only", func(t *testing.T) {
		t.Setenv("G8S_PERMISSION", "read_only")
		t.Setenv("G8S_RECEIPT_ID", "rc-123")
		t.Setenv("G8S_SCOPE_ROOT", "/some/root")

		_, _, err := ResolveDelegationContext("", "", true)
		if err == nil || !strings.Contains(err.Error(), "read_only environment forbids file mutation") {
			t.Fatalf("expected read_only mutation error, got %v", err)
		}

		// Non-mutation (read) should succeed
		rcpt, scope, err := ResolveDelegationContext("", "", false)
		if err != nil {
			t.Fatalf("unexpected error for read operation: %v", err)
		}
		if rcpt != "rc-123" || scope != "/some/root" {
			t.Fatalf("expected rc-123 and /some/root, got rcpt=%q scope=%q", rcpt, scope)
		}
	})

	t.Run("mutation forbidden under automation_read", func(t *testing.T) {
		t.Setenv("G8S_PERMISSION", "automation_read")
		_, _, err := ResolveDelegationContext("", "", true)
		if err == nil || !strings.Contains(err.Error(), "automation_read environment forbids file mutation") {
			t.Fatalf("expected automation_read mutation error, got %v", err)
		}
	})

	t.Run("receipt mismatch error", func(t *testing.T) {
		t.Setenv("G8S_PERMISSION", "workspace_write")
		t.Setenv("G8S_RECEIPT_ID", "rc-legit")

		_, _, err := ResolveDelegationContext("rc-spoofed", "", true)
		if err == nil || !strings.Contains(err.Error(), "receipt mismatch") {
			t.Fatalf("expected receipt mismatch error, got %v", err)
		}
	})

	t.Run("delegation defaults and overrides", func(t *testing.T) {
		t.Setenv("G8S_PERMISSION", "workspace_write")
		t.Setenv("G8S_RECEIPT_ID", "rc-worker")
		t.Setenv("G8S_SCOPE_ROOT", "worker/dir")

		// Flag matches env receipt
		rcpt, scope, err := ResolveDelegationContext("rc-worker", "flag/dir", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rcpt != "rc-worker" || scope != "flag/dir" {
			t.Fatalf("unexpected values: rcpt=%s scope=%s", rcpt, scope)
		}

		// Empty flags inherit from env
		rcpt2, scope2, err := ResolveDelegationContext("", "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rcpt2 != "rc-worker" || scope2 != "worker/dir" {
			t.Fatalf("unexpected values: rcpt=%s scope=%s", rcpt2, scope2)
		}
	})

	t.Run("defaults to dot when no scope root", func(t *testing.T) {
		t.Setenv("G8S_PERMISSION", "")
		t.Setenv("G8S_RECEIPT_ID", "")
		t.Setenv("G8S_SCOPE_ROOT", "")

		rcpt, scope, err := ResolveDelegationContext("rc-direct", "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rcpt != "rc-direct" || scope != "." {
			t.Fatalf("unexpected values: rcpt=%s scope=%s", rcpt, scope)
		}
	})
}

func TestVerifyScope(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	validFile := filepath.Join(subDir, "file.txt")
	if err := os.WriteFile(validFile, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	outsideFile := filepath.Join(tempDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("empty path rejected", func(t *testing.T) {
		_, err := VerifyScope("", tempDir, nil)
		if err == nil {
			t.Fatal("expected empty path error")
		}
	})

	t.Run("inside scope root passes", func(t *testing.T) {
		rel, err := VerifyScope(validFile, tempDir, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel != "sub/file.txt" {
			t.Fatalf("expected sub/file.txt, got %s", rel)
		}
	})

	t.Run("escaping scope root rejected", func(t *testing.T) {
		// Scoped to subDir, accessing outsideFile
		_, err := VerifyScope(outsideFile, subDir, nil)
		if err == nil || !strings.Contains(err.Error(), "escapes scope root") {
			t.Fatalf("expected escapes scope root error, got %v", err)
		}

		// Direct dot-dot traversal
		dotDotPath := filepath.Join(subDir, "..", "outside.txt")
		_, err = VerifyScope(dotDotPath, subDir, nil)
		if err == nil || !strings.Contains(err.Error(), "escapes scope root") {
			t.Fatalf("expected traversal escape error, got %v", err)
		}
	})

	t.Run("allowed paths enforced", func(t *testing.T) {
		allowed := []string{"sub/*.txt"}
		rel, err := VerifyScope(validFile, tempDir, allowed)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel != "sub/file.txt" {
			t.Fatalf("expected sub/file.txt, got %s", rel)
		}

		disallowedFile := filepath.Join(tempDir, "sub", "file.go")
		_, err = VerifyScope(disallowedFile, tempDir, allowed)
		if err == nil || !strings.Contains(err.Error(), "outside receipt allowed_paths") {
			t.Fatalf("expected outside allowed_paths error, got %v", err)
		}
	})
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	content := "Line 1: public info\nLine 2: pass: postgresql://admin:secret123@db.internal/prod\nLine 3: third line\nLine 4: fourth line\n"
	samplePath := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(samplePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(content))
	expectedSHA := hex.EncodeToString(sum[:])

	t.Run("read full file with DLP sanitization", func(t *testing.T) {
		res, err := Read(ctx, ReadOptions{
			Path:      samplePath,
			ScopeRoot: tempDir,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if res.TotalLines != 4 {
			t.Fatalf("expected 4 total lines, got %d", res.TotalLines)
		}
		if res.StartLine != 1 || res.EndLine != 4 {
			t.Fatalf("expected start=1 end=4, got start=%d end=%d", res.StartLine, res.EndLine)
		}
		if res.SHA256 != expectedSHA {
			t.Fatalf("expected sha %s, got %s", expectedSHA, res.SHA256)
		}
		if res.Truncated {
			t.Fatal("expected truncated=false")
		}
		if len(res.Lines) != 4 {
			t.Fatalf("expected 4 lines, got %d", len(res.Lines))
		}
		// Check DLP sanitization on line 2
		if strings.Contains(res.Lines[1].Text, "secret123") {
			t.Fatalf("credentials not sanitized in line text: %s", res.Lines[1].Text)
		}
		if !strings.Contains(res.Lines[1].Text, "<REDACTED>") {
			t.Fatalf("redacted marker missing in line text: %s", res.Lines[1].Text)
		}
		if strings.Contains(res.Content, "secret123") {
			t.Fatalf("credentials not sanitized in content: %s", res.Content)
		}
	})

	t.Run("read line range", func(t *testing.T) {
		res, err := Read(ctx, ReadOptions{
			Path:      samplePath,
			ScopeRoot: tempDir,
			StartLine: 2,
			EndLine:   3,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if len(res.Lines) != 2 {
			t.Fatalf("expected 2 lines, got %d", len(res.Lines))
		}
		if res.Lines[0].Line != 2 || res.Lines[1].Line != 3 {
			t.Fatalf("unexpected line numbers: %d, %d", res.Lines[0].Line, res.Lines[1].Line)
		}
	})

	t.Run("start line greater than end line returns empty lines", func(t *testing.T) {
		res, err := Read(ctx, ReadOptions{
			Path:      samplePath,
			ScopeRoot: tempDir,
			StartLine: 3,
			EndLine:   2,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if len(res.Lines) != 0 {
			t.Fatalf("expected 0 lines, got %d", len(res.Lines))
		}
		if res.Content != "" {
			t.Fatalf("expected empty content, got %q", res.Content)
		}
	})

	t.Run("binary rejection", func(t *testing.T) {
		binPath := filepath.Join(tempDir, "binary.bin")
		binData := []byte{0x48, 0x65, 0x6C, 0x00, 0x6F} // contains NUL
		if err := os.WriteFile(binPath, binData, 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Read(ctx, ReadOptions{
			Path:      binPath,
			ScopeRoot: tempDir,
		})
		if err == nil || !strings.Contains(err.Error(), "binary file cannot be read as text") {
			t.Fatalf("expected binary file rejection, got %v", err)
		}
	})

	t.Run("max bytes truncation", func(t *testing.T) {
		res, err := Read(ctx, ReadOptions{
			Path:      samplePath,
			ScopeRoot: tempDir,
			MaxBytes:  30,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if !res.Truncated {
			t.Fatal("expected truncated=true")
		}
		if len(res.Lines) > 2 {
			t.Fatalf("expected at most 2 lines within 30 bytes, got %d", len(res.Lines))
		}
	})

	t.Run("empty file handling", func(t *testing.T) {
		emptyPath := filepath.Join(tempDir, "empty.txt")
		if err := os.WriteFile(emptyPath, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := Read(ctx, ReadOptions{
			Path:      emptyPath,
			ScopeRoot: tempDir,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if res.TotalLines != 0 {
			t.Fatalf("expected 0 total lines, got %d", res.TotalLines)
		}
		if len(res.Lines) != 0 {
			t.Fatalf("expected empty lines slice, got %d", len(res.Lines))
		}
		if res.Content != "" {
			t.Fatalf("expected empty content, got %q", res.Content)
		}
	})

	t.Run("crlf file handling", func(t *testing.T) {
		crlfPath := filepath.Join(tempDir, "crlf.txt")
		if err := os.WriteFile(crlfPath, []byte("Line 1\r\nLine 2\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := Read(ctx, ReadOptions{
			Path:      crlfPath,
			ScopeRoot: tempDir,
		})
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if res.TotalLines != 2 {
			t.Fatalf("expected 2 lines, got %d", res.TotalLines)
		}
		if res.Lines[0].Text != "Line 1" || res.Lines[1].Text != "Line 2" {
			t.Fatalf("trailing \\r not stripped cleanly: %q, %q", res.Lines[0].Text, res.Lines[1].Text)
		}
	})
}

func TestEdit(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	t.Run("single replace success", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "single.txt")
		if err := os.WriteFile(filePath, []byte("hello old world"), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "old",
			NewString: "new",
		})
		if err != nil {
			t.Fatalf("unexpected edit error: %v", err)
		}
		if res.Replacements != 1 {
			t.Fatalf("expected 1 replacement, got %d", res.Replacements)
		}
		content, _ := os.ReadFile(filePath)
		if string(content) != "hello new world" {
			t.Fatalf("unexpected content after edit: %s", string(content))
		}
	})

	t.Run("empty old_string rejected", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "empty_old.txt")
		if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "",
			NewString: "bar",
		})
		if err == nil || !strings.Contains(err.Error(), "old_string must not be empty") {
			t.Fatalf("expected empty old_string error, got %v", err)
		}
	})

	t.Run("multi replace error without ReplaceAll", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "multi.txt")
		if err := os.WriteFile(filePath, []byte("foo bar foo baz foo"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:       filePath,
			ScopeRoot:  tempDir,
			OldString:  "foo",
			NewString:  "qux",
			ReplaceAll: false,
		})
		if err == nil || !strings.Contains(err.Error(), "set --all to replace all occurrences") {
			t.Fatalf("expected multi-occurrence error, got %v", err)
		}

		// With ReplaceAll
		res, err := Edit(ctx, EditOptions{
			Path:       filePath,
			ScopeRoot:  tempDir,
			OldString:  "foo",
			NewString:  "qux",
			ReplaceAll: true,
		})
		if err != nil {
			t.Fatalf("unexpected error with ReplaceAll: %v", err)
		}
		if res.Replacements != 3 {
			t.Fatalf("expected 3 replacements, got %d", res.Replacements)
		}
		content, _ := os.ReadFile(filePath)
		if string(content) != "qux bar qux baz qux" {
			t.Fatalf("unexpected content: %s", string(content))
		}
	})

	t.Run("not found error", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "not_found.txt")
		if err := os.WriteFile(filePath, []byte("some text"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "absent",
			NewString: "present",
		})
		if err == nil || !strings.Contains(err.Error(), "old_string not found in file") {
			t.Fatalf("expected not found error, got %v", err)
		}
	})

	t.Run("sha256 conflict vs match", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "sha.txt")
		original := []byte("version 1")
		if err := os.WriteFile(filePath, original, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(original)
		correctSHA := hex.EncodeToString(sum[:])

		// Conflict
		_, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "version 1",
			NewString: "version 2",
			IfSHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		})
		if err == nil || !strings.Contains(err.Error(), "sha256 conflict") {
			t.Fatalf("expected sha256 conflict error, got %v", err)
		}

		// Match
		res, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "version 1",
			NewString: "version 2",
			IfSHA256:  correctSHA,
		})
		if err != nil {
			t.Fatalf("unexpected error with matching sha256: %v", err)
		}
		if res.OldSHA256 != correctSHA {
			t.Fatalf("expected old sha %s, got %s", correctSHA, res.OldSHA256)
		}
	})

	t.Run("crlf preservation", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "crlf_edit.txt")
		if err := os.WriteFile(filePath, []byte("line 1\r\nline 2\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "line 2",
			NewString: "modified line 2",
		})
		if err != nil {
			t.Fatalf("unexpected edit error: %v", err)
		}
		raw, _ := os.ReadFile(filePath)
		if !strings.Contains(string(raw), "\r\n") {
			t.Fatalf("CRLF line endings were not preserved: %q", string(raw))
		}
		if string(raw) != "line 1\r\nmodified line 2\r\n" {
			t.Fatalf("unexpected CRLF content: %q", string(raw))
		}
	})

	t.Run("binary file rejection in edit", func(t *testing.T) {
		binPath := filepath.Join(tempDir, "binary_edit.bin")
		if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:      binPath,
			ScopeRoot: tempDir,
			OldString: "\x01",
			NewString: "\x05",
		})
		if err == nil || !strings.Contains(err.Error(), "binary file cannot be edited as text") {
			t.Fatalf("expected binary edit rejection, got %v", err)
		}
	})

	t.Run("atomic write preserves file permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("windows stat modes do not carry POSIX 0755 execute bits")
		}
		filePath := filepath.Join(tempDir, "executable.sh")
		if err := os.WriteFile(filePath, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		_, err := Edit(ctx, EditOptions{
			Path:      filePath,
			ScopeRoot: tempDir,
			OldString: "echo hello",
			NewString: "echo world",
		})
		if err != nil {
			t.Fatalf("unexpected edit error: %v", err)
		}

		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("expected permissions 0755, got %v", info.Mode().Perm())
		}
	})
}
