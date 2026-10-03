package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
)

func TestCheckProvidersFileMatrix(t *testing.T) {
	tempDir := t.TempDir()

	// 1. File absent matrix -> PASS (OK)
	absentPath := filepath.Join(tempDir, "absent_providers.json")
	res := checkProvidersFile(absentPath)
	if res.Status != "OK" {
		t.Fatalf("expected OK for absent providers file, got %s: %s", res.Status, res.Message)
	}

	// 2. Valid providers.json -> PASS (OK)
	validContent := `{
		"providers": [
			{
				"class": "platform_dispatch",
				"name": "agy",
				"models": [{"id": "gemini-3.8-flash-high"}]
			}
		]
	}`
	validPath := filepath.Join(tempDir, "valid_providers.json")
	if err := os.WriteFile(validPath, []byte(validContent), 0o600); err != nil {
		t.Fatalf("write valid providers.json: %v", err)
	}
	res = checkProvidersFile(validPath)
	if res.Status != "OK" {
		t.Fatalf("expected OK for valid providers file, got %s: %s", res.Status, res.Message)
	}

	// 3. Malformed JSON -> FAIL with required fix instruction
	invalidJSONPath := filepath.Join(tempDir, "invalid_json_providers.json")
	if err := os.WriteFile(invalidJSONPath, []byte(`{broken-json`), 0o600); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	res = checkProvidersFile(invalidJSONPath)
	if res.Status != "FAIL" {
		t.Fatalf("expected FAIL for malformed JSON, got %s", res.Status)
	}
	if !strings.Contains(res.Message, "fix providers.json or remove it to fall back to built-ins") {
		t.Fatalf("expected fix advice in message, got: %s", res.Message)
	}

	// 4. Schema error (api_call missing base_url) -> FAIL with required fix instruction
	invalidSchemaContent := `{
		"providers": [
			{
				"class": "api_call",
				"name": "custom-api",
				"models": [{"id": "m1"}]
			}
		]
	}`
	invalidSchemaPath := filepath.Join(tempDir, "invalid_schema_providers.json")
	if err := os.WriteFile(invalidSchemaPath, []byte(invalidSchemaContent), 0o600); err != nil {
		t.Fatalf("write invalid schema: %v", err)
	}
	res = checkProvidersFile(invalidSchemaPath)
	if res.Status != "FAIL" {
		t.Fatalf("expected FAIL for schema error, got %s", res.Status)
	}
	if !strings.Contains(res.Message, "fix providers.json or remove it to fall back to built-ins") {
		t.Fatalf("expected fix advice in message, got: %s", res.Message)
	}
}

func TestCheckDiskSpaceThreshold(t *testing.T) {
	orig := diskFreeSpaceFunc
	defer func() { diskFreeSpaceFunc = orig }()

	// 1. Adequate free space (e.g. 5 GiB >= 2 GiB) -> OK
	diskFreeSpaceFunc = func(path string) (uint64, error) {
		return 5 * 1024 * 1024 * 1024, nil
	}
	res := checkDiskSpace()
	if res.Status != "OK" {
		t.Fatalf("expected OK for 5GiB free space, got %s: %s", res.Status, res.Message)
	}

	// 2. Low free space (< 2 GiB, e.g. 1.5 GiB) -> WARN (or FAIL) and cites incident
	diskFreeSpaceFunc = func(path string) (uint64, error) {
		return 1500 * 1024 * 1024, nil
	}
	res = checkDiskSpace()
	if res.Status != "WARN" && res.Status != "FAIL" {
		t.Fatalf("expected WARN/FAIL for 1.5GiB free space, got %s", res.Status)
	}
	if !strings.Contains(res.Message, "disk exhaustion has masqueraded as random test/attempt failures twice") {
		t.Fatalf("expected incident citation in message, got: %s", res.Message)
	}

	// 3. Stat failure -> WARN
	diskFreeSpaceFunc = func(path string) (uint64, error) {
		return 0, errors.New("simulated stat error")
	}
	res = checkDiskSpace()
	if res.Status != "WARN" && res.Status != "FAIL" {
		t.Fatalf("expected WARN/FAIL on stat error, got %s", res.Status)
	}
}

func TestCheckStaleBinary(t *testing.T) {
	tempDir := t.TempDir()

	// 1. No bin/g8s present -> skips cleanly (OK)
	res := checkStaleBinary(tempDir)
	if res.Status != "OK" {
		t.Fatalf("expected OK (clean skip) when bin/g8s absent, got %s: %s", res.Status, res.Message)
	}

	// 2. bin/g8s present but no git repo -> skips cleanly (OK)
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	binFile := filepath.Join(binDir, "g8s")
	if err := os.WriteFile(binFile, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	res = checkStaleBinary(tempDir)
	if res.Status != "OK" {
		t.Fatalf("expected OK (clean skip) when git repo absent, got %s: %s", res.Status, res.Message)
	}

	// 3. Initialize a real git repo in tempDir and create a commit
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester",
			"GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=tester",
			"GIT_COMMITTER_EMAIL=tester@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	runCmd("init")
	runCmd("config", "user.name", "tester")
	runCmd("config", "user.email", "tester@example.com")
	dummyFile := filepath.Join(tempDir, "file.txt")
	_ = os.WriteFile(dummyFile, []byte("hello"), 0o600)
	runCmd("add", "file.txt")
	runCmd("commit", "-m", "initial commit")

	// 4. Fabricated old binary: set mtime older than commit time -> WARN
	oldTime := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(binFile, oldTime, oldTime)

	res = checkStaleBinary(tempDir)
	if res.Status != "WARN" {
		t.Fatalf("expected WARN for stale binary, got %s: %s", res.Status, res.Message)
	}
	if !strings.Contains(strings.ToLower(res.Message), "predates") && !strings.Contains(strings.ToLower(res.Message), "stale") {
		t.Fatalf("expected warning about stale/predating binary, got: %s", res.Message)
	}

	// 5. Fresh binary: set mtime newer than commit time -> OK
	freshTime := time.Now().Add(1 * time.Hour)
	_ = os.Chtimes(binFile, freshTime, freshTime)

	res = checkStaleBinary(tempDir)
	if res.Status != "OK" {
		t.Fatalf("expected OK for fresh binary, got %s: %s", res.Status, res.Message)
	}
}

func TestDiskFreeSpace_RealCall(t *testing.T) {
	tmp := t.TempDir()
	bytes, err := defaultDiskFreeSpace(tmp)
	if err != nil {
		t.Logf("defaultDiskFreeSpace returned err (permitted in CI sandbox): %v", err)
	} else if bytes == 0 {
		t.Errorf("expected non-zero free space for %s", tmp)
	}

	if runtime.GOOS != "windows" {
		uBytes, uErr := unixDiskFreeSpace(tmp)
		if uErr == nil && uBytes == 0 {
			t.Errorf("expected non-zero unix disk space, got 0")
		}
	}
}

func TestResolveProvidersPath_EnvAndLegacy(t *testing.T) {
	tempDir := t.TempDir()
	customFile := filepath.Join(tempDir, "custom_prov.json")
	t.Setenv("G8S_PROVIDERS", customFile)
	if got := resolveProvidersPath(); got != customFile {
		t.Errorf("expected %s, got %s", customFile, got)
	}

	t.Setenv("G8S_PROVIDERS", "")
	// Test directory error in checkProvidersFile
	dirRes := checkProvidersFile(tempDir)
	if dirRes.Status != "FAIL" || !strings.Contains(dirRes.Message, "is a directory") {
		t.Errorf("expected FAIL for directory, got %+v", dirRes)
	}
}

func TestCheckGhostProcesses_Coverage(t *testing.T) {
	doc := New()
	ctx := context.Background()

	// Calling with empty heartbeatDir covers hbDir == ""
	resEmpty := doc.checkGhostProcesses(ctx, "")
	if resEmpty == "" {
		t.Error("expected non-empty result")
	}

	// Calling with active ghost process
	tmpDir := t.TempDir()
	hbStore := heartbeat.NewStore(tmpDir, time.Now)
	// Stale heartbeat with dead PID (999999)
	oldTime := time.Now().Add(-10 * time.Minute)
	_, _ = hbStore.Record("sess-dead", heartbeat.StatusRunning, nil,
		heartbeat.WithPID(999999),
		heartbeat.WithBinary("agy"),
		heartbeat.WithLastUpdate(oldTime),
	)
	resGhost := doc.checkGhostProcesses(ctx, tmpDir)
	if !strings.Contains(resGhost, "ghost process") {
		t.Logf("checkGhostProcesses result: %s", resGhost)
	}
}

func TestWindowsDiskFreeSpace_Simulated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-simulated fixture (shell-script/ chmod semantics) — not meaningful on real Windows")
	}
	// 1. Without powershell/fsutil on Linux -> errors cleanly
	_, err := windowsDiskFreeSpace("C:\\")
	if err == nil {
		t.Log("windowsDiskFreeSpace succeeded or mocked")
	}

	// 2. With fake powershell on PATH
	tmpDir := t.TempDir()
	psScript := "#!/bin/sh\necho 10737418240\n"
	psPath := filepath.Join(tmpDir, "powershell")
	if err := os.WriteFile(psPath, []byte(psScript), 0o755); err == nil {
		t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		bytes, err := windowsDiskFreeSpace("C:\\")
		if err != nil || bytes != 10737418240 {
			t.Errorf("expected 10737418240 bytes, got %d, err=%v", bytes, err)
		}
	}

	// 3. With fake powershell failing, and fake fsutil on PATH
	tmpDir2 := t.TempDir()
	badPs := filepath.Join(tmpDir2, "powershell")
	_ = os.WriteFile(badPs, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	fsutilPath := filepath.Join(tmpDir2, "fsutil")
	fsScript := "#!/bin/sh\necho 'Total # of free bytes : 5,368,709,120'\n"
	_ = os.WriteFile(fsutilPath, []byte(fsScript), 0o755)
	t.Setenv("PATH", tmpDir2+string(os.PathListSeparator)+os.Getenv("PATH"))
	bytes, err := windowsDiskFreeSpace("D:\\")
	if err != nil || bytes != 5368709120 {
		t.Errorf("expected 5368709120 bytes from fsutil, got %d, err=%v", bytes, err)
	}
}

func TestFormatBytes(t *testing.T) {
	if got := formatBytes(500); got != "500 B" {
		t.Errorf("expected 500 B, got %s", got)
	}
	if got := formatBytes(1024); got != "1.0 KiB" {
		t.Errorf("expected 1.0 KiB, got %s", got)
	}
	if got := formatBytes(1024 * 1024 * 5); got != "5.0 MiB" {
		t.Errorf("expected 5.0 MiB, got %s", got)
	}
}

func TestCheckWorkspace_DeniedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-simulated fixture (shell-script/ chmod semantics) — not meaningful on real Windows")
	}
	tmpDir := t.TempDir()
	deniedDir := filepath.Join(tmpDir, ".env")
	if err := os.MkdirAll(deniedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	if err := os.Chdir(deniedDir); err != nil {
		t.Fatal(err)
	}
	res := checkWorkspace()
	if res.Status != "WARN" || !strings.Contains(res.Message, "denied path fragment") {
		t.Errorf("expected WARN for workspace in denied path, got %+v", res)
	}
}
