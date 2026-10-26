//go:build windows

package conformance

import (
	"errors"
	"os/exec"
)

// pgidSupported reports whether process-group probes (P4) can run here.
const pgidSupported = false

func configurePgid(_ *exec.Cmd) {}

func killGroup(_ int) error { return errors.New("process groups unsupported on windows") }

func groupAlive(_ int) bool { return false }
