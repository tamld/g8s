package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWorkspaceWriteOptIn_Matrix(t *testing.T) {
	const key = "AGY_MCP_ALLOW_WORKSPACE_WRITE"
	origVal, origSet := os.LookupEnv(key)
	t.Cleanup(func() {
		if origSet {
			_ = os.Setenv(key, origVal)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	tests := []struct {
		name          string
		allow         bool
		priorSet      bool
		priorVal      string
		wantDuringVal string
	}{
		{"allow false, prior absent", false, false, "", ""},
		{"allow false, prior empty", false, true, "", ""},
		{"allow false, prior 0", false, true, "0", "0"},
		{"allow false, prior 1", false, true, "1", "1"},
		{"allow true, prior absent", true, false, "", "1"},
		{"allow true, prior empty", true, true, "", "1"},
		{"allow true, prior 0", true, true, "0", "1"},
		{"allow true, prior 1", true, true, "1", "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.priorSet {
				t.Setenv(key, tt.priorVal)
			} else {
				_ = os.Unsetenv(key)
			}

			restore, err := withWorkspaceWriteOptIn(tt.allow)
			if err != nil {
				t.Fatalf("withWorkspaceWriteOptIn error: %v", err)
			}

			val, set := os.LookupEnv(key)
			if tt.wantDuringVal != "" {
				if !set || val != tt.wantDuringVal {
					t.Errorf("during: got (%q, %v), want (%q, true)", val, set, tt.wantDuringVal)
				}
			} else if tt.priorSet {
				if !set || val != tt.priorVal {
					t.Errorf("during: got (%q, %v), want (%q, true)", val, set, tt.priorVal)
				}
			} else {
				if set {
					t.Errorf("during: got %q (set), want unset", val)
				}
			}

			restore()

			afterVal, afterSet := os.LookupEnv(key)
			if afterSet != tt.priorSet || (tt.priorSet && afterVal != tt.priorVal) {
				t.Errorf("after restore: got (%q, %v), want (%q, %v)", afterVal, afterSet, tt.priorVal, tt.priorSet)
			}
		})
	}
}

func TestWorkspaceWriteOptIn_FlagHelp(t *testing.T) {
	bin := buildG8sBinary(t)
	for _, sub := range []string{"submit", "worker"} {
		out, err := exec.Command(bin, sub, "--help").CombinedOutput()
		if err != nil {
			t.Fatalf("%s --help: %v\n%s", sub, err, string(out))
		}
		if !strings.Contains(string(out), "allow-workspace-write") {
			t.Errorf("%s --help output missing allow-workspace-write flag:\n%s", sub, string(out))
		}
	}
}
