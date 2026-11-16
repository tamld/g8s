package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePromptSpecVariants(t *testing.T) {
	spec, err := parsePromptSpec(`{"mode":"clean-write","target":"a.txt","content":"ok"}`)
	if err != nil || spec.Mode != "clean-write" || spec.Target != "a.txt" {
		t.Fatalf("raw json parse failed: spec=%+v err=%v", spec, err)
	}

	wrapped := "System preamble {ignored}\nUser prompt:\nFAULTWORKER_SPEC:{\"mode\":\"stdout-error-rc0\",\"message\":\"boom\"}\nTrailing instructions after spec."
	specWrapped, err := parsePromptSpec(wrapped)
	if err != nil || specWrapped.Mode != "stdout-error-rc0" || specWrapped.Message != "boom" {
		t.Fatalf("wrapped prompt parse failed: spec=%+v err=%v", specWrapped, err)
	}

	fallbackWrapped := "preamble\n{\"mode\":\"clean-write\",\"target\":\"b.txt\",\"content\":\"hi\"}\nfooter"
	specFallback, err := parsePromptSpec(fallbackWrapped)
	if err != nil || specFallback.Target != "b.txt" {
		t.Fatalf("fallback parse failed: spec=%+v err=%v", specFallback, err)
	}

	if _, err := parsePromptSpec("   "); err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if _, err := parsePromptSpec("plain text with no json"); err == nil {
		t.Fatal("expected error for non-json prompt")
	}
	if _, err := parsePromptSpec("{bad json"); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestRunCleanWriteAndStdoutError(t *testing.T) {
	dir := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"--mode", "clean-write",
		"--target", "pkg/hello.txt",
		"--content", "hello world",
	}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("clean-write rc=%d stderr=%s", rc, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "pkg", "hello.txt"))
	if err != nil || string(got) != "hello world" {
		t.Fatalf("file content=%q err=%v", string(got), err)
	}

	// Reject traversal target
	stdout.Reset()
	stderr.Reset()
	if rc := run([]string{"--mode", "clean-write", "--target", "../escape.txt"}, &stdout, &stderr); rc == 0 {
		t.Fatal("expected non-zero rc for parent path traversal")
	}

	// Stdout error envelope with rc=0
	stdout.Reset()
	stderr.Reset()
	rc = run([]string{"--prompt", `FAULTWORKER_SPEC:{"mode":"stdout-error-rc0","message":"rate limited"}`}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("expected rc=0 for stdout-error-rc0, got %d", rc)
	}
	if !strings.Contains(stdout.String(), `"kind":"error"`) || !strings.Contains(stdout.String(), `"rate limited"`) {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}

	// Default message
	stdout.Reset()
	if rc := run([]string{"--mode", "stdout-error-rc0"}, &stdout, &stderr); rc != 0 || !strings.Contains(stdout.String(), "simulated upstream") {
		t.Fatalf("unexpected default error envelope: rc=%d out=%s", rc, stdout.String())
	}
}

func TestRunInvalidArgsAndModes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if rc := run([]string{"--unknown-flag"}, &stdout, &stderr); rc != 2 {
		t.Fatalf("unknown flag rc=%d, want 2", rc)
	}
	if rc := run([]string{"--mode", "does-not-exist"}, &stdout, &stderr); rc != 2 {
		t.Fatalf("unsupported mode rc=%d, want 2", rc)
	}
	if rc := run([]string{"--prompt", "not-json"}, &stdout, &stderr); rc != 2 {
		t.Fatalf("bad prompt rc=%d, want 2", rc)
	}
	if rc := run([]string{"--mode", "notify-controlled"}, &stdout, &stderr); rc != 2 {
		t.Fatalf("missing notify-url rc=%d, want 2", rc)
	}
}

func TestRunSleepChildAndHangWithGrandchild(t *testing.T) {
	origSleep := sleepFunc
	origSpawn := spawnChildFunc
	origExe := executablePathFunc
	t.Cleanup(func() {
		sleepFunc = origSleep
		spawnChildFunc = origSpawn
		executablePathFunc = origExe
	})

	var slept []time.Duration
	sleepFunc = func(d time.Duration) {
		slept = append(slept, d)
	}

	var stdout, stderr bytes.Buffer
	if rc := run([]string{"--sleep-child", "250ms"}, &stdout, &stderr); rc != 0 {
		t.Fatalf("sleep-child rc=%d", rc)
	}
	if rc := run([]string{"--sleep-child", "bad"}, &stdout, &stderr); rc != 0 {
		t.Fatalf("sleep-child fallback rc=%d", rc)
	}
	if len(slept) != 2 || slept[0] != 250*time.Millisecond || slept[1] != 5*time.Second {
		t.Fatalf("unexpected slept durations: %v", slept)
	}

	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		var req notifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.GrandchildPID != 4242 || req.Mode != "hang-with-grandchild" {
			http.Error(w, "unexpected payload", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	executablePathFunc = func() (string, error) { return "/fake/faultworker", nil }
	spawnChildFunc = func(exe string, d time.Duration) (int, error) {
		return 4242, nil
	}

	rc := run([]string{
		"--mode", "hang-with-grandchild",
		"--notify-url", srv.URL,
		"--sleep", "3s",
	}, &stdout, &stderr)
	if rc != 0 || received.Load() != 1 {
		t.Fatalf("hang-with-grandchild rc=%d received=%d stderr=%s", rc, received.Load(), stderr.String())
	}

	spawnChildFunc = func(string, time.Duration) (int, error) {
		return 0, errors.New("spawn boom")
	}
	if rc := run([]string{"--mode", "hang-with-grandchild"}, &stdout, &stderr); rc == 0 {
		t.Fatal("expected non-zero rc on spawn error")
	}
}

func TestRunNotifyControlledDirtyHangAndCleanWrite(t *testing.T) {
	dir := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	origSleep := sleepFunc
	origSpawn := spawnChildFunc
	origExe := executablePathFunc
	t.Cleanup(func() {
		sleepFunc = origSleep
		spawnChildFunc = origSpawn
		executablePathFunc = origExe
	})

	sleepFunc = func(time.Duration) {}
	executablePathFunc = func() (string, error) { return "/fake/faultworker", nil }
	spawnChildFunc = func(string, time.Duration) (int, error) { return 7777, nil }

	var attempt atomic.Int32
	var readySeen atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("phase") == "ready" {
			readySeen.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		n := attempt.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			_ = json.NewEncoder(w).Encode(notifyResponse{
				Action:       "dirty_hang",
				DirtyTarget:  "dirty.txt",
				DirtyContent: "partial",
				Sleep:        "20ms",
			})
		case 2:
			_ = json.NewEncoder(w).Encode(notifyResponse{
				Action:  "clean_write",
				Target:  "clean.txt",
				Content: "recovered",
			})
		case 3:
			_ = json.NewEncoder(w).Encode(notifyResponse{Action: "bogus"})
		default:
			http.Error(w, "fail", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if rc := run([]string{"--mode", "notify-controlled", "--notify-url", srv.URL}, &stdout, &stderr); rc != 0 || !readySeen.Load() {
		t.Fatalf("dirty_hang rc=%d readySeen=%v stderr=%s", rc, readySeen.Load(), stderr.String())
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "dirty.txt")); err != nil || string(raw) != "partial" {
		t.Fatalf("dirty file=%q err=%v", string(raw), err)
	}

	stdout.Reset()
	if rc := run([]string{"--mode", "notify-controlled", "--notify-url", srv.URL}, &stdout, &stderr); rc != 0 {
		t.Fatalf("clean_write rc=%d stderr=%s", rc, stderr.String())
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "clean.txt")); err != nil || string(raw) != "recovered" {
		t.Fatalf("clean file=%q err=%v", string(raw), err)
	}

	if rc := run([]string{"--mode", "notify-controlled", "--notify-url", srv.URL}, &stdout, &stderr); rc != 2 {
		t.Fatalf("bogus action rc=%d, want 2", rc)
	}
	if rc := run([]string{"--mode", "notify-controlled", "--notify-url", srv.URL}, &stdout, &stderr); rc != 1 {
		t.Fatalf("500 notify rc=%d, want 1", rc)
	}
}

func TestSpawnDetachedSleepChildReal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real child spawn in short mode")
	}
	// Invoking compiled test binary with -test.run=^$ runs a no-op quickly; wait 20ms.
	pid, err := spawnDetachedSleepChild(os.Args[0], 20*time.Millisecond)
	if err != nil {
		t.Fatalf("spawnDetachedSleepChild: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("invalid child pid %d", pid)
	}
	time.Sleep(80 * time.Millisecond)
}
