// Package testenv provides helpers imported only by test files.
// Go does not make a dependency's _test.go files available to importing tests;
// TestProductionPackagesDoNotImportTestenv enforces the test-only boundary.
package testenv

import (
	"fmt"
	"os"
)

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

// AmbientDelegationEnvVars returns a copy of the harness-injected variables.
// G8S_STATE_DIR is excluded because tests manage it themselves.
func AmbientDelegationEnvVars() []string {
	return append([]string(nil), ambientDelegationEnvVars...)
}

// ScrubAmbientDelegationEnv clears worker hydration before TestMain runs tests.
// Tests requiring delegation values should set them explicitly with t.Setenv.
func ScrubAmbientDelegationEnv() error {
	for _, key := range ambientDelegationEnvVars {
		if err := os.Unsetenv(key); err != nil {
			return fmt.Errorf("os.Unsetenv(%q): %w", key, err)
		}
	}
	return nil
}
