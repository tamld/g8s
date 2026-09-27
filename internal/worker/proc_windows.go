//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	syscallSIGTERM = syscall.SIGTERM
	syscallSIGKILL = syscall.SIGKILL
)

// configureSysProcAttr configures process creation attributes on Windows.
// CREATE_NEW_PROCESS_GROUP = 0x00000200 groups the immediate children for
// CTRL signals; hard containment comes from the Job Object (#415 PR-2) —
// CREATE_NEW_PROCESS_GROUP alone does not kill grandchildren.
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000200,
	}
}

// spawnJobHandle creates a Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// and assigns the freshly started child to it (#415 PR-2). While the handle
// is open the tree runs normally; the moment the handle closes, the kernel
// terminates every process in the job — including grandchildren that
// taskkill /T cannot reach (detached wrappers, platform subagents). Returns
// 0 when containment could not be established (the attempt still runs; the
// failure is surfaced by the caller).
func spawnJobHandle(cmd *exec.Cmd) (uintptr, error) {
	if cmd.Process == nil {
		return 0, errors.New("assign job: child process not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(windows.Handle(job),
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)))
	if err != nil {
		_ = windows.CloseHandle(windows.Handle(job))
		return 0, fmt.Errorf("set job limits: %w", err)
	}
	// os.Process does not expose its raw handle — open one by PID with
	// enough rights to assign the process into the job.
	child, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false,
		uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(windows.Handle(job))
		return 0, fmt.Errorf("open child process %d: %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(child)
	if err := windows.AssignProcessToJobObject(windows.Handle(job), child); err != nil {
		_ = windows.CloseHandle(windows.Handle(job))
		return 0, fmt.Errorf("assign child %d to job: %w", cmd.Process.Pid, err)
	}
	return uintptr(job), nil
}

// closeJobHandle closes the job handle. With KILL_ON_JOB_CLOSE this is the
// kernel-guaranteed tree kill: every process still in the job — including
// detached grandchildren — is terminated by the OS. Idempotent in effect
// (double close is a benign no-op).
func closeJobHandle(h uintptr) {
	if h == 0 {
		return
	}
	_ = windows.CloseHandle(windows.Handle(h))
}

// killProcessGroup terminates the entire process tree on Windows,
// mirroring the POSIX two-stage contract (#336):
//
//   - SIGTERM stage: taskkill /T without /F requests graceful termination —
//     GUI/windowed children receive WM_CLOSE and console children attached
//     to a control handler get a cleanup window before being terminated.
//   - SIGKILL stage (or any explicit force path): taskkill /T /F hard-terminates.
//
// If the process has already exited, taskkill exit codes 128 (not found)
// and 1 (already terminated) are treated as benign.
func killProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if sig == syscallSIGKILL {
		return forceKillProcessTree(pid)
	}
	// Graceful stage: taskkill without /F posts WM_CLOSE to top-level
	// windows of the tree and signals console process groups. Processes
	// that ignore the request are cleaned up by the SIGKILL stage after
	// the supervisor's grace period (processChild.Terminate).
	graceful := exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid))
	_ = graceful.Run() // best effort; survivors are force-killed later
	return nil
}

// forceKillProcessTree hard-terminates the process tree (taskkill /T /F).
func forceKillProcessTree(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Exit code 128 (process not found) or 1 (already terminated) is benign.
		if exitErr.ExitCode() == 128 || exitErr.ExitCode() == 1 {
			return nil
		}
	}
	return err
}

// groupAlive is a Windows stub for the POSIX group probe: Windows
// containment is the Job Object (spawnJobHandle/closeJobHandle), not a
// process-group signal target.
func groupAlive(pid int) bool {
	return false
}

// closeJob closes the child's Job Object handle (KILL_ON_JOB_CLOSE kills
// the whole tree). Idempotent via sync.Once.
func (c *processChild) closeJob() {
	c.jobClose.Do(func() { closeJobHandle(c.job) })
}
