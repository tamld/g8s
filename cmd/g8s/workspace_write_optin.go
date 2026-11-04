package main

import "os"

func withWorkspaceWriteOptIn(allow bool) (restore func(), err error) {
	if !allow {
		return func() {}, nil
	}

	const key = "AGY_MCP_ALLOW_WORKSPACE_WRITE"
	oldValue, wasSet := os.LookupEnv(key)
	restore = func() {
		if wasSet {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	}
	if err := os.Setenv(key, "1"); err != nil {
		restore()
		return func() {}, err
	}
	return restore, nil
}
