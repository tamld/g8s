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

	"github.com/tamld/g8s/internal/vault"
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

		// Narrower flag scope inside env scope ceiling succeeds
		rcpt, scope, err := ResolveDelegationContext("rc-worker", "worker/dir/sub", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rcpt != "rc-worker" || scope != "worker/dir/sub" {
			t.Fatalf("unexpected values: rcpt=%s scope=%s", rcpt, scope)
		}

		// Sibling/broader flag scope outside env scope ceiling is rejected (#598)
		if _, _, err := ResolveDelegationContext("rc-worker", "flag/dir", true); err == nil || !strings.Contains(err.Error(), "scope_root mismatch") {
			t.Fatalf("expected scope_root mismatch for broader flag/dir, got %v", err)
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

func TestJITWhisperOnReadAndEdit(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	vdbPath := filepath.Join(tempDir, "vault.db")
	v, err := vault.NewVault(vdbPath, nil)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	_, err = v.Store(ctx, vault.DistillationRecord{
		ID:        "JIT-RULE-01",
		Title:     "Avoid raw sleep in worker loop",
		Status:    "APPLIED",
		Milestone: "v0.17.0",
		Causality: vault.CausalityAnchor{
			Problem:  "Flaky tests on slow CI runners",
			TradeOff: "Use injectable clock instead of time.Sleep",
		},
		SpatialCoordinates: vault.SpatialAnchor{
			Package:         "pkg/worker",
			File:            "pkg/worker/loop.go",
			DeniedFragments: []string{"time.Sleep("},
		},
	})
	v.Close()
	if err != nil {
		t.Fatalf("Store vault record: %v", err)
	}

	t.Setenv("G8S_VAULT_DB", vdbPath)

	pkgDir := filepath.Join(tempDir, "pkg", "worker")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(pkgDir, "loop.go")
	if err := os.WriteFile(targetFile, []byte("package worker\nfunc run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readRes, err := Read(ctx, ReadOptions{
		Path:      targetFile,
		ScopeRoot: tempDir,
	})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(readRes.JITWhisper) != 1 || !strings.Contains(readRes.JITWhisper[0], "[jit:JIT-RULE-01]") {
		t.Fatalf("expected non-blocking JITWhisper on Read, got %+v", readRes.JITWhisper)
	}

	// Edit must NOT block even if the edit touches the file or introduces denied fragments;
	// instead it attaches the non-blocking JITWhisper so LLM intelligence is never strangled in-flight.
	editRes, err := Edit(ctx, EditOptions{
		Path:      targetFile,
		ScopeRoot: tempDir,
		OldString: "func run() {}",
		NewString: "func run() { /* time.Sleep(1) */ }",
	})
	if err != nil {
		t.Fatalf("Edit should never block in-flight on JITWhisper: %v", err)
	}
	if len(editRes.JITWhisper) != 1 || !strings.Contains(editRes.JITWhisper[0], "avoid: time.Sleep(") {
		t.Fatalf("expected non-blocking JITWhisper on Edit, got %+v", editRes.JITWhisper)
	}
}

func TestDelegatedScopeContainment_Issue598(t *testing.T) {
	t.Run("ResolveDelegationContext_CannotWidenScopeRoot", func(t *testing.T) {
		tmpDir := t.TempDir()
		subScope := filepath.Join(tmpDir, "sub")
		if err := os.MkdirAll(subScope, 0o755); err != nil {
			t.Fatal(err)
		}

		t.Setenv("G8S_TIER", "F1")
		t.Setenv("G8S_SCOPE_ROOT", subScope)

		// Scope widening to parent tmpDir must be rejected
		_, _, err := ResolveDelegationContext("", tmpDir, false)
		if err == nil || (!strings.Contains(err.Error(), "escapes delegated worker environment scope root") &&
			!strings.Contains(err.Error(), "outside delegated G8S_SCOPE_ROOT") &&
			!strings.Contains(err.Error(), "scope_root mismatch")) {
			t.Fatalf("expected scope widening rejection for tmpDir, got %v", err)
		}

		// Scope widening to /etc must be rejected
		_, _, err = ResolveDelegationContext("", "/etc", false)
		if err == nil || (!strings.Contains(err.Error(), "escapes delegated worker environment scope root") &&
			!strings.Contains(err.Error(), "outside delegated G8S_SCOPE_ROOT") &&
			!strings.Contains(err.Error(), "scope_root mismatch")) {
			t.Fatalf("expected scope widening rejection for /etc, got %v", err)
		}

		// Exact subScope matches the ceiling and must succeed
		_, scope, err := ResolveDelegationContext("", subScope, false)
		if err != nil {
			t.Fatalf("unexpected error for subScope: %v", err)
		}
		if scope != subScope {
			t.Fatalf("expected scope %q, got %q", subScope, scope)
		}

		// Narrower child directory must succeed
		innerDir := filepath.Join(subScope, "inner")
		if err := os.MkdirAll(innerDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, scope, err = ResolveDelegationContext("", innerDir, false)
		if err != nil {
			t.Fatalf("unexpected error for innerDir: %v", err)
		}
		if scope != innerDir {
			t.Fatalf("expected scope %q, got %q", innerDir, scope)
		}
	})

	t.Run("ResolveDelegationContext_F1MutationWithoutReceiptBlocked", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("G8S_TIER", "F1")
		t.Setenv("G8S_SCOPE_ROOT", tmpDir)
		t.Setenv("G8S_RECEIPT_ID", "")

		_, _, err := ResolveDelegationContext("", "", true)
		if err == nil || (!strings.Contains(err.Error(), "requires a write receipt") &&
			!strings.Contains(err.Error(), "requires a valid WriteReceipt")) {
			t.Fatalf("expected write receipt requirement error, got %v", err)
		}
	})

	t.Run("SymlinkEscapeBlockedInReadEditAndVerifyScope", func(t *testing.T) {
		tmpDir := t.TempDir()
		scopeDir := filepath.Join(tmpDir, "scope")
		if err := os.MkdirAll(scopeDir, 0o755); err != nil {
			t.Fatal(err)
		}

		outsideSecret := filepath.Join(tmpDir, "outside_secret.txt")
		if err := os.WriteFile(outsideSecret, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}

		escapeLink := filepath.Join(scopeDir, "escape.txt")
		if err := os.Symlink(outsideSecret, escapeLink); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}

		// VerifyScope returns error containing symlink escape
		_, err := VerifyScope(escapeLink, scopeDir, []string{"*"})
		if err == nil || (!strings.Contains(err.Error(), "escapes scope root") &&
			!strings.Contains(err.Error(), "symlink target escapes scope root")) ||
			!strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink escape error in VerifyScope, got %v", err)
		}

		// Read returns an error
		_, err = Read(context.Background(), ReadOptions{
			Path:         escapeLink,
			ScopeRoot:    scopeDir,
			AllowedPaths: []string{"*"},
		})
		if err == nil {
			t.Fatalf("expected Read on escaping symlink to fail, got nil")
		}

		// Edit returns an error and does NOT modify outsideSecret
		_, err = Edit(context.Background(), EditOptions{
			Path:         escapeLink,
			OldString:    "secret",
			NewString:    "hacked",
			ScopeRoot:    scopeDir,
			AllowedPaths: []string{"*"},
		})
		if err == nil {
			t.Fatalf("expected Edit on escaping symlink to fail, got nil")
		}

		gotContent, err := os.ReadFile(outsideSecret)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotContent) != "secret" {
			t.Fatalf("outsideSecret was modified! got %q, want %q", string(gotContent), "secret")
		}

		// Dangling symlink pointing outside scope must also be rejected by VerifyScope
		danglingLink := filepath.Join(scopeDir, "dangling_escape.txt")
		if err := os.Symlink(filepath.Join(tmpDir, "nonexistent_outside.txt"), danglingLink); err == nil {
			if _, err := VerifyScope(danglingLink, scopeDir, nil); err == nil {
				t.Fatalf("expected dangling symlink escaping scope to fail VerifyScope")
			}
		}

		// Symlink depth > 32 must be rejected
		if _, err := evalCanonicalPathDepth(scopeDir, 33); err == nil || !strings.Contains(err.Error(), "too many symlink levels") {
			t.Fatalf("expected too many symlink levels error, got %v", err)
		}

		// NUL byte in path or scope root must be rejected
		if _, err := VerifyScope("bad\x00path.txt", scopeDir, nil); err == nil || !strings.Contains(err.Error(), "NUL") {
			t.Fatalf("expected NUL byte rejection in VerifyScope, got %v", err)
		}
	})

	t.Run("ResolveDelegationContext_F1WithoutEnvScopeUsesCwdCeiling", func(t *testing.T) {
		t.Setenv("G8S_TIER", "F1")
		t.Setenv("G8S_SCOPE_ROOT", "")
		t.Setenv("G8S_RECEIPT_ID", "rc-cwd")

		if _, scope, err := ResolveDelegationContext("", "", false); err != nil || scope != "." {
			t.Fatalf("expected default scope '.' for F1 without G8S_SCOPE_ROOT, got scope=%q err=%v", scope, err)
		}
		if _, _, err := ResolveDelegationContext("", "..", false); err == nil || !strings.Contains(err.Error(), "scope_root mismatch") {
			t.Fatalf("expected F1 cwd ceiling to reject '..', got %v", err)
		}
	})
}

func TestSelectJSON(t *testing.T) {
	jsonDoc := []byte(`{
		"cmd": "test",
		"data": {
			"receipt_id": "rc-42",
			"count": 100,
			"active": true,
			"empty_val": null,
			"items": [
				{"id": "item-1", "name": "alpha"},
				{"id": "item-2", "name": "beta"}
			]
		},
		"matches": [
			{"file": "foo.go", "line": 10},
			{"file": "bar.go", "line": 25}
		],
		"tags": ["go", "security"]
	}`)

	t.Run("nested fields and dot indexing", func(t *testing.T) {
		val, err := SelectJSON(jsonDoc, ".data.receipt_id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != `"rc-42"` {
			t.Fatalf("expected %q, got %q", `"rc-42"`, val)
		}

		val2, err := SelectJSON(jsonDoc, "data.items.0.name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val2 != `"alpha"` {
			t.Fatalf("expected %q, got %q", `"alpha"`, val2)
		}

		val3, err := SelectJSON(jsonDoc, "data.items[1].id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val3 != `"item-2"` {
			t.Fatalf("expected %q, got %q", `"item-2"`, val3)
		}
	})

	t.Run("primitives and null", func(t *testing.T) {
		valNum, err := SelectJSON(jsonDoc, "data.count")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valNum != "100" {
			t.Fatalf("expected 100, got %s", valNum)
		}

		valBool, err := SelectJSON(jsonDoc, "data.active")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valBool != "true" {
			t.Fatalf("expected true, got %s", valBool)
		}

		valNull, err := SelectJSON(jsonDoc, "data.empty_val")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valNull != "null" {
			t.Fatalf("expected null, got %s", valNull)
		}
	})

	t.Run("array projection", func(t *testing.T) {
		valProj, err := SelectJSON(jsonDoc, "matches[].file")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valProj != `["foo.go","bar.go"]` {
			t.Fatalf("expected [\"foo.go\",\"bar.go\"], got %s", valProj)
		}
	})

	t.Run("root selector", func(t *testing.T) {
		valRoot, err := SelectJSON(jsonDoc, ".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(valRoot, `"rc-42"`) {
			t.Fatalf("expected root json containing rc-42, got %s", valRoot)
		}
	})

	t.Run("errors on empty input or invalid selector", func(t *testing.T) {
		if _, err := SelectJSON([]byte(""), "data"); err == nil {
			t.Fatal("expected error on empty json")
		}
		if _, err := SelectJSON([]byte("{bad-json"), "data"); err == nil {
			t.Fatal("expected error on malformed json")
		}
		if _, err := SelectJSON(jsonDoc, "data.nonexistent"); err == nil {
			t.Fatal("expected error on missing field")
		}
	})
}
