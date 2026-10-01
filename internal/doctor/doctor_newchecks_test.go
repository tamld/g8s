package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
