//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureDetachedHelper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

func isTestProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() {
		if cerr := windows.CloseHandle(h); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox test: close handle %d: %v\n", pid, cerr)
		}
	}()
	const windowsStillActive uint32 = 259
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == windowsStillActive
}

func forceKillTestPID(pid int) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer func() {
		if cerr := windows.CloseHandle(h); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox test: close terminate handle %d: %v\n", pid, cerr)
		}
	}()
	if terr := windows.TerminateProcess(h, 1); terr != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox test: terminate pid %d: %v\n", pid, terr)
	}
}
