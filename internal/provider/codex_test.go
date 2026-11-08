package provider

import (
	"context"
	"os"
	"path/filepath"
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

func TestCodexSpawn_UsageLimitExitZeroFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "codex-quota")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'codex 1.0'; exit 0; fi\n" +
		"printf '%s\\n' \"You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\" \"You've hit your usage limit. Upgrade for more usage, or try again at 9:57 PM.\" >&2\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
