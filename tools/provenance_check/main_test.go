package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/dogfood"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read boom")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write boom")
}

func TestRunValidMessages(t *testing.T) {
	t.Run("receipt via -message text", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"-message", "feat(core): harden fence\n\nReceipt: task-20261119-001"}, strings.NewReader(""), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		if !strings.Contains(out.String(), "OK: kind=receipt ref=task-20261119-001") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("receipt via -message json", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"-json", "-message", "feat(core): harden fence\n\nReceipt: task-20261119-001"}, strings.NewReader(""), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		var res checkResult
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal json: %v (raw=%q)", err, out.String())
		}
		if !res.OK || res.Kind != "receipt" || res.Ref != "task-20261119-001" || res.Hint != "" {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("f0_direct via stdin default", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		msg := "chore: tighten scripts\n\nProvenance: F0-direct — bootstrap tooling\n"
		rc := run(nil, strings.NewReader(msg), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		if !strings.Contains(out.String(), "OK: kind=f0_direct ref=bootstrap tooling") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("f0_direct via explicit -file -", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		msg := "chore: tighten scripts\n\nProvenance: F0-direct — explicit stdin dash\n"
		rc := run([]string{"-file", "-"}, strings.NewReader(msg), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		if !strings.Contains(out.String(), "OK: kind=f0_direct ref=explicit stdin dash") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("blank line separates Closes from Receipt trailer", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		msg := "fix(harness): enforce boundary\n\nCloses #943\n\nReceipt: task-20261119-001\n"
		rc := run([]string{"-message", msg}, strings.NewReader(""), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		if !strings.Contains(out.String(), "OK: kind=receipt ref=task-20261119-001") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("valid -file temp file", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "msg.txt")
		if err := os.WriteFile(p, []byte("chore: docs\n\nProvenance: F0-direct — file read check\n"), 0o600); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		var out, errBuf bytes.Buffer
		rc := run([]string{"-file", p}, strings.NewReader(""), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0, got %d (stderr=%q)", rc, errBuf.String())
		}
		if !strings.Contains(out.String(), "OK: kind=f0_direct ref=file read check") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})
}

func TestRunInvalidMessages(t *testing.T) {
	cases := []struct {
		name     string
		msg      string
		wantKind string
		wantRef  string
	}{
		{
			name:     "Closes merged into final paragraph without blank line",
			msg:      "fix(harness): enforce boundary\n\nCloses #943\nProvenance: F0-direct — reason",
			wantKind: "unclassified",
		},
		{
			name:     "bare F0-direct without em-dash and reason",
			msg:      "chore: quick fix\n\nProvenance: F0-direct",
			wantKind: "unclassified",
		},
		{
			name:     "F0-direct with empty reason after em-dash",
			msg:      "chore: quick fix\n\nProvenance: F0-direct —   ",
			wantKind: "classification_error",
			wantRef:  dogfood.E_PROVENANCE_TAG_EMPTY_REASON,
		},
		{
			name:     "invalid short receipt ID",
			msg:      "feat: add feature\n\nReceipt: short",
			wantKind: "unclassified",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			rc := run([]string{"-message", tc.msg}, strings.NewReader(""), &out, &errBuf)
			if rc != 1 {
				t.Fatalf("expected rc=1, got %d (stdout=%q stderr=%q)", rc, out.String(), errBuf.String())
			}
			if !strings.Contains(errBuf.String(), "FAIL: kind="+tc.wantKind) {
				t.Fatalf("unexpected stderr: %q", errBuf.String())
			}

			out.Reset()
			errBuf.Reset()
			rc = run([]string{"-json", "-message", tc.msg}, strings.NewReader(""), &out, &errBuf)
			if rc != 1 {
				t.Fatalf("expected json rc=1, got %d (stdout=%q stderr=%q)", rc, out.String(), errBuf.String())
			}
			var res checkResult
			if err := json.Unmarshal(out.Bytes(), &res); err != nil {
				t.Fatalf("unmarshal json: %v (raw=%q)", err, out.String())
			}
			if res.OK || res.Kind != tc.wantKind || res.Ref != tc.wantRef || res.Hint == "" {
				t.Fatalf("unexpected json result: %+v", res)
			}
		})
	}
}

func TestRunErrorsAndUsage(t *testing.T) {
	t.Run("missing file returns exit 2", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		missing := filepath.Join(t.TempDir(), "does-not-exist.txt")
		rc := run([]string{"-file", missing}, strings.NewReader(""), &out, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2, got %d", rc)
		}
		if !strings.Contains(errBuf.String(), "read message file") {
			t.Fatalf("unexpected stderr: %q", errBuf.String())
		}
	})

	t.Run("mutually exclusive -file and -message returns exit 2", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"-file", "msg.txt", "-message", "hi"}, strings.NewReader(""), &out, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2, got %d", rc)
		}
	})

	t.Run("unexpected positional argument returns exit 2", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"extra"}, strings.NewReader(""), &out, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2, got %d", rc)
		}
	})

	t.Run("unknown flag returns exit 2", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"-bogus"}, strings.NewReader(""), &out, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2, got %d", rc)
		}
	})

	t.Run("help flag returns exit 0", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run([]string{"-h"}, strings.NewReader(""), &out, &errBuf)
		if rc != 0 {
			t.Fatalf("expected rc=0 for -h, got %d", rc)
		}
	})

	t.Run("stdin read error returns exit 2", func(t *testing.T) {
		var out, errBuf bytes.Buffer
		rc := run(nil, failingReader{}, &out, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2 on stdin read error, got %d", rc)
		}
	})

	t.Run("stdout write error returns exit 2", func(t *testing.T) {
		var errBuf bytes.Buffer
		rc := run([]string{"-message", "feat: x\n\nReceipt: task-20261119-001"}, strings.NewReader(""), failingWriter{}, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2 on text stdout write error, got %d", rc)
		}
		rc = run([]string{"-json", "-message", "feat: x\n\nReceipt: task-20261119-001"}, strings.NewReader(""), failingWriter{}, &errBuf)
		if rc != 2 {
			t.Fatalf("expected rc=2 on json stdout write error, got %d", rc)
		}
	})
}
