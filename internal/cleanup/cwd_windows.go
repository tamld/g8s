//go:build windows

package cleanup

import (
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processBasicInformation struct {
	ExitStatus                   uintptr
	PebBaseAddress               uintptr
	AffinityMask                 uintptr
	BasePriority                 uintptr
	UniqueProcessId              uintptr
	InheritedFromUniqueProcessId uintptr
}

type unicodeString struct {
	Length        uint16
	MaximumLength uint16
	_             uint32 // 4-byte padding on 64-bit Windows
	Buffer        uintptr
}

var (
	ntdll                         = windows.NewLazyDLL("ntdll.dll")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
)

// resolveProcessCWD resolves the current working directory of a process by PID on Windows
// using the Process Environment Block (PEB). Returns empty string if the process cannot
// be accessed or CWD cannot be determined.
func resolveProcessCWD(pid int) string {
	if pid <= 0 {
		return ""
	}

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	var pbi processBasicInformation
	var retLen uint32
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(h),
		0, // ProcessBasicInformation
		uintptr(unsafe.Pointer(&pbi)),
		uintptr(unsafe.Sizeof(pbi)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if status != 0 || pbi.PebBaseAddress == 0 {
		return ""
	}

	// In 64-bit PEB, offset to ProcessParameters is 0x20
	var procParamsAddr uintptr
	var bytesRead uintptr
	err = windows.ReadProcessMemory(h, pbi.PebBaseAddress+0x20, (*byte)(unsafe.Pointer(&procParamsAddr)), unsafe.Sizeof(procParamsAddr), &bytesRead)
	if err != nil || procParamsAddr == 0 {
		return ""
	}

	// In 64-bit RTL_USER_PROCESS_PARAMETERS, offset to CurrentDirectory (CURDIR) is 0x38
	var curDir unicodeString
	err = windows.ReadProcessMemory(h, procParamsAddr+0x38, (*byte)(unsafe.Pointer(&curDir)), unsafe.Sizeof(curDir), &bytesRead)
	if err != nil || curDir.Length == 0 || curDir.Buffer == 0 {
		return ""
	}

	buf := make([]uint16, curDir.Length/2)
	err = windows.ReadProcessMemory(h, curDir.Buffer, (*byte)(unsafe.Pointer(&buf[0])), uintptr(curDir.Length), &bytesRead)
	if err != nil {
		return ""
	}

	raw := syscall.UTF16ToString(buf)
	if raw == "" {
		return ""
	}
	return filepath.Clean(raw)
}
