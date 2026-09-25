//go:build windows

package process

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// TasklistLister is the Windows implementation of ProcessLister using CIM, tasklist, and taskkill.
type TasklistLister struct{}

func newLister() ProcessLister {
	return &TasklistLister{}
}

type cimProcess struct {
	ProcessID       int     `json:"ProcessId"`
	ParentProcessID int     `json:"ParentProcessId"`
	Name            string  `json:"Name"`
	CommandLine     *string `json:"CommandLine"`
}

// List inspects host processes using CIM/PowerShell to get real command lines,
// falling back to tasklist if PowerShell or CIM is unavailable.
func (l *TasklistLister) List() ([]ProcessInfo, error) {
	// Attempt CIM-based listing to capture full CommandLine and ParentProcessId
	procs, err := listViaCIM()
	if err == nil && len(procs) > 0 {
		return procs, nil
	}

	// Fallback to tasklist
	cmd := exec.Command("tasklist", "/FO", "CSV", "/V", "/NH")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to basic tasklist if /V is unsupported
		cmd = exec.Command("tasklist", "/FO", "CSV", "/NH")
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("list processes via tasklist: %w", err)
		}
	}

	return parseTasklistCSV(out), nil
}

func listViaCIM() ([]ProcessInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		`@(Get-CimInstance Win32_Process | Select-Object ProcessId, ParentProcessId, Name, CommandLine) | ConvertTo-Json -Compress`)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseCimJSON(out)
}

func parseCimJSON(data []byte) ([]ProcessInfo, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	var rawList []cimProcess
	if data[0] == '[' {
		if err := json.Unmarshal(data, &rawList); err != nil {
			return nil, err
		}
	} else if data[0] == '{' {
		var single cimProcess
		if err := json.Unmarshal(data, &single); err != nil {
			return nil, err
		}
		rawList = append(rawList, single)
	} else {
		return nil, fmt.Errorf("unexpected JSON format from CIM")
	}

	var processes []ProcessInfo
	for _, p := range rawList {
		if p.ProcessID <= 0 {
			continue
		}

		cleanBin := filepath.Base(p.Name)
		cleanBin = strings.TrimSuffix(cleanBin, ".exe")

		cmdLine := p.Name
		if p.CommandLine != nil && strings.TrimSpace(*p.CommandLine) != "" {
			cmdLine = *p.CommandLine
		}

		processes = append(processes, ProcessInfo{
			PID:         p.ProcessID,
			PPID:        p.ParentProcessID,
			Binary:      cleanBin,
			CommandLine: cmdLine,
		})
	}

	return processes, nil
}

// parseTasklistCSV parses tasklist CSV output.
// Expected /V columns: "Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"
// Expected basic columns: "Image Name","PID","Session Name","Session#","Mem Usage"
func parseTasklistCSV(data []byte) []ProcessInfo {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil
	}

	var processes []ProcessInfo
	for _, row := range records {
		if len(row) < 2 {
			continue
		}

		binName := strings.TrimSpace(row[0])
		pidStr := strings.TrimSpace(row[1])
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}

		user := ""
		if len(row) >= 7 {
			user = strings.TrimSpace(row[6])
		}

		cleanBin := filepath.Base(binName)
		cleanBin = strings.TrimSuffix(cleanBin, ".exe")

		processes = append(processes, ProcessInfo{
			PID:         pid,
			Binary:      cleanBin,
			CommandLine: binName,
			User:        user,
		})
	}

	return processes
}

// ResolveCWD on Windows resolves the current working directory of a process by PID
// using the Process Environment Block (PEB).
func (l *TasklistLister) ResolveCWD(pid int) string {
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

// Kill terminates a Windows process using taskkill.
func (l *TasklistLister) Kill(pid int) error {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill pid %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// KillForce forcefully terminates a Windows process using taskkill /F.
func (l *TasklistLister) KillForce(pid int) error {
	cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill /F pid %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsAlive checks whether the process is alive on Windows.
func (l *TasklistLister) IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
