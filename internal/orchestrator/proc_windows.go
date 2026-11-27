//go:build windows

package orchestrator

import (
	"errors"
	"os/exec"
	"strconv"
	"syscall"
)

// configureSysProcAttr places each agy child in a new process group
// (CREATE_NEW_PROCESS_GROUP) so tree containment has a group boundary to
// signal, mirroring internal/provider (#986 A-14).
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}

// killProcessTree force-terminates the whole process tree rooted at pid
// (taskkill /T /F), mirroring internal/provider's killProcessGroup. Exit
// codes 128 (not found) and 1 (already terminated) are benign.
func killProcessTree(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 128 || exitErr.ExitCode() == 1) {
		return nil
	}
	return err
}
