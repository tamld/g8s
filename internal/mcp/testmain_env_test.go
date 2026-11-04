package mcp

import (
	"fmt"
	"os"
	"testing"
)

// ambientDelegationEnvVars lists harness-injected delegation variables that F1 workers
// inherit from g8s worker hydration. Package tests assume a clean ambient env; TestMain
// clears these once before m.Run(). Tests that need a value use t.Setenv. G8S_STATE_DIR is
// not listed — tests manage it themselves.
var ambientDelegationEnvVars = []string{
	"G8S_TIER",
	"G8S_STRATUM",
	"G8S_SCOPE_ROOT",
	"G8S_RECEIPT_ID",
	"G8S_PERMISSION",
	"G8S_TASK_ID",
	"G8S_ROLE",
	"AGY_MCP_ALLOW_WORKSPACE_WRITE",
}

func TestMain(m *testing.M) {
	for _, key := range ambientDelegationEnvVars {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "TestMain: os.Unsetenv(%q): %v\n", key, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestMain_ScrubsAmbientDelegationEnv(t *testing.T) {
	for _, key := range ambientDelegationEnvVars {
		if got := os.Getenv(key); got != "" {
			t.Errorf("ambient delegation env %q = %q, want empty", key, got)
		}
	}
}
