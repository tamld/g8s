//go:build windows

package worker

import (
	"errors"
	"os/exec"
	"syscall"
)

const (
	syscallSIGTERM = syscall.SIGTERM
	syscallSIGKILL = syscall.SIGKILL
)

// configureSysProcAttr is a no-op on windows: Job Objects would be required
// for tree-wide signaling and are out of scope for the MVP supervisor.
func configureSysProcAttr(cmd *exec.Cmd) {}

func killProcessGroup(_ int, _ syscall.Signal) error {
	return errors.New("process groups are unsupported on this platform")
}
