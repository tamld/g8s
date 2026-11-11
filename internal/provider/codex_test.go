package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Observed live on 2026-11-08 when codex hit its usage ceiling (printed twice on stderr).
const fixtureCodexUsageLimit20261108 = "You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\n" +
	"You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\n"

func TestClassifyProviderStderrRefusal(t *testing.T) {
	tests := []struct {
		name       string
		stderr     string
		want       bool
		wantCode   string
		wantRetry  bool
		wantSubstr string
	}{
		{
			name:       "usage limit live fixture",
			stderr:     fixtureCodexUsageLimit20261108,
			want:       true,
			wantCode:   ErrCodeProviderQuotaExhausted,
			wantRetry:  true,
			wantSubstr: "usage limit",
		},
		{
			name:     "invalid api key",
			stderr:   "Error: Invalid API key provided\n",
			want:     true,
			wantCode: ErrCodeProviderAuth,
		},
		{
			name:     "unauthorized",
			stderr:   "request failed: unauthorized\n",
			want:     true,
			wantCode: ErrCodeProviderAuth,
		},
		{
			name:     "insufficient credits",
			stderr:   "billing: Insufficient credits to complete request\n",
			want:     true,
			wantCode: ErrCodeProviderAuth,
		},
		{
			name:     "model not found",
			stderr:   "The model not found in your account\n",
			want:     true,
			wantCode: ErrCodeProviderModelUnavailable,
		},
		{
			name:     "model does not exist",
			stderr:   "model gpt-foo does not exist\n",
			want:     true,
			wantCode: ErrCodeProviderModelUnavailable,
		},
		{
			name:   "empty stderr",
			stderr: "",
			want:   false,
		},
		{
			name:   "empty stdout companion stderr without markers",
			stderr: "warning: deprecated flag --foo\n",
			want:   false,
		},
		{
			name:   "does not exist without model context",
			stderr: "file does not exist\n",
			want:   false,
		},
		{
			name:   "unified diff line with model not found fixture ignored",
			stderr: "diff --git a/a.go b/a.go\n+\t\t\terrMsg: \"model not found\",\n",
			want:   false,
		},
		{
			name:   "unified diff context line with model not found ignored",
			stderr: "diff --git a/a.go b/a.go\n@@ -1 +1,2 @@\n \tmessage := \"model not found\"\n+\t_ = message\n",
			want:   false,
		},
		{
			name:   "codex echoed assistant block mentioning refusal phrase ignored",
			stderr: "OpenAI Codex v0.166.0\ncodex\n{\"counterexamples\":[\"model not found\"]}\nhook: Stop\ntokens used\n100\n",
			want:   false,
		},
		{
			name:   "codex exec tool block echoing refusal fixture ignored",
			stderr: "OpenAI Codex v0.166.0\nexec\n/bin/zsh -lc 'cat test.go'\n succeeded in 0ms:\nThe model not found in your account\nhook: PostToolUse\ncodex\n{\"verdict\":\"ACCEPTED\"}\n",
			want:   false,
		},
		{
			name:   "codex exec tool block echoing ERROR refusal line ignored",
			stderr: "OpenAI Codex v0.166.0\nexec\ncat fixture.txt\n succeeded in 0ms:\nERROR: model not found\nhook: PostToolUse\ncodex\nDone\n",
			want:   false,
		},
		{
			name:   "codex transcript without hook markers ignores echoed user exec and assistant fixtures",
			stderr: "OpenAI Codex\n--------\nuser\ninspect model not found fixture\nexec\ncat fixture.txt\n succeeded in 0ms:\nmodel not found\ncodex\nDone\n",
			want:   false,
		},
		{
			name:     "codex transcript without hook markers still catches real ERROR line",
			stderr:   "OpenAI Codex v0.166.0\n--------\nuser\nhello\nERROR: model gpt-foo does not exist\n",
			want:     true,
			wantCode: ErrCodeProviderModelUnavailable,
		},
		{
			name:   "codex exec tool block echoing ERROR refusal line without trailing marker ignored",
			stderr: "OpenAI Codex v0.166.0\nexec\ncat fixture.txt\n succeeded in 0ms:\nERROR: model not found\n",
			want:   false,
		},
		{
			name:     "codex transcript with tokens used footer still catches real ERROR line",
			stderr:   "OpenAI Codex v0.166.0\n--------\nuser\nhello\nERROR: model gpt-foo does not exist\ntokens used\n0\n",
			want:     true,
			wantCode: ErrCodeProviderModelUnavailable,
		},
		{
			name:   "codex exec tool block echoing usage limit fixture without trailing marker ignored",
			stderr: "OpenAI Codex v0.166.0\nexec\ncat fixture.txt\n succeeded in 0ms:\nYou've hit your usage limit.\n",
			want:   false,
		},
		{
			name:       "codex exec without hook markers followed by next turn thinking usage limit caught",
			stderr:     "OpenAI Codex v0.166.0\nexec\nls\n succeeded in 0ms:\nok\nthinking\nYou've hit your usage limit.\n",
			want:       true,
			wantCode:   ErrCodeProviderQuotaExhausted,
			wantSubstr: "usage limit",
		},
		{
			name:     "codex ERROR line containing stderr substring still caught",
			stderr:   "OpenAI Codex\nuser\nhello\nERROR: model not found; stderr: upstream rejected request\n",
			want:     true,
			wantCode: ErrCodeProviderModelUnavailable,
		},
		{
			name:       "codex exec tool block followed by real usage limit refusal caught",
			stderr:     "OpenAI Codex v0.166.0\nexec\n/bin/zsh -lc 'ls'\n succeeded in 0ms:\nok\nhook: PostToolUse\nYou've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\n",
			want:       true,
			wantCode:   ErrCodeProviderQuotaExhausted,
			wantRetry:  true,
			wantSubstr: "usage limit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ClassifyProviderStderrRefusal(tc.stderr)
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v (refusal=%+v)", ok, tc.want, got)
			}
			if !tc.want {
				return
			}
			if got.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tc.wantCode)
			}
			reason := got.Reason()
			if !strings.HasPrefix(reason, tc.wantCode) {
				t.Fatalf("reason %q missing code prefix %q", reason, tc.wantCode)
			}
			if tc.wantSubstr != "" && !strings.Contains(strings.ToLower(reason), tc.wantSubstr) {
				t.Fatalf("reason %q missing %q", reason, tc.wantSubstr)
			}
			if tc.wantRetry && got.RetryAt == "" {
				t.Fatalf("expected RetryAt parsed, got empty (reason=%q)", reason)
			}
			if tc.wantRetry && !strings.Contains(reason, "retry_at=") {
				t.Fatalf("reason %q missing retry_at=", reason)
			}
		})
	}
}

func TestCodexFinalizeReceipt_ExitZeroRefusal(t *testing.T) {
	code, status, err := codexFinalizeReceipt(0, "COMPLETED", nil, fixtureCodexUsageLimit20261108)
	if code != 1 || status != "FAILED" || err == nil {
		t.Fatalf("got code=%d status=%q err=%v", code, status, err)
	}
	if !strings.Contains(err.Error(), ErrCodeProviderQuotaExhausted) {
		t.Fatalf("err %q missing quota code", err.Error())
	}
}

// writeCodexQuotaFixture writes a platform-executable fake codex that answers
// --version then prints the 2026-11-08 usage-limit refusal to stderr (twice,
// as observed live) and exits 0 — the fail-closed classification input.
func writeCodexQuotaFixture(t *testing.T, dir string) (string, func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		bin := filepath.Join(dir, "codex-quota.cmd")
		script := "@echo off\r\n" +
			"if \"%~1\" == \"--version\" (echo codex 1.0\r\nexit /b 0)\r\n" +
			"echo You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM. 1>&2\r\n" +
			"echo You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM. 1>&2\r\n" +
			"exit /b 0\r\n"
		if err := os.WriteFile(bin, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		return bin, func() {}
	}
	bin := filepath.Join(dir, "codex-quota")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'codex 1.0'; exit 0; fi\n" +
		"printf '%s\\n' \"You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\" \"You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\" >&2\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, func() {}
}

func TestCodexSpawn_UsageLimitExitZeroFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	bin, cleanup := writeCodexQuotaFixture(t, tmpDir)
	defer cleanup()
	t.Setenv("CODEX_BIN", bin)
	p := NewCodexProvider()
	handle, err := p.Spawn(context.Background(), Spec{Model: "gpt-test", Brief: "task"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rc, werr := handle.Wait(context.Background())
	if werr == nil {
		t.Fatal("expected refusal error on Wait")
	}
	if rc.Status != "FAILED" || rc.ExitCode != 1 {
		t.Fatalf("status=%q exit=%d, want FAILED/1", rc.Status, rc.ExitCode)
	}
	if !strings.Contains(werr.Error(), ErrCodeProviderQuotaExhausted) {
		t.Fatalf("wait err %q missing typed code", werr.Error())
	}
}
