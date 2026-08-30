//go:build windows

package cleanup

import (
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// resolveProcessCWD resolves the working directory or executable image directory of a process by PID on Windows.
func resolveProcessCWD(pid int) string {
	if pid <= 0 {
		return ""
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
	if err != nil {
		return ""
	}
	imgPath := syscall.UTF16ToString(buf[:size])
	return filepath.Dir(imgPath)
}
