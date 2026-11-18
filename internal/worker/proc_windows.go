//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/tamld/g8s/internal/sandbox"
	"golang.org/x/sys/windows"
)

const (
	syscallSIGTERM = syscall.SIGTERM
	syscallSIGKILL = syscall.SIGKILL
)

// configureSysProcAttr configures process creation attributes on Windows.
// CREATE_NEW_PROCESS_GROUP = 0x00000200 groups the immediate children for
// CTRL signals, while CREATE_SUSPENDED keeps instruction 0 frozen until
// spawnJobHandle binds the process to its KILL_ON_JOB_CLOSE Job Object and
// resumes threads, eliminating post-Start pre-job TOCTOU escapes (#920).
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED,
	}
}

func killUncontainedChild(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
		fmt.Fprintf(os.Stderr, "[warn] worker: kill uncontained pid %d: %v\n", cmd.Process.Pid, kerr)
	}
}

func closeJobAndKill(job windows.Handle, cmd *exec.Cmd) {
	if cerr := windows.CloseHandle(job); cerr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: close job handle: %v\n", cerr)
	}
	killUncontainedChild(cmd)
}

// spawnJobHandle creates a Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
// binds the suspended child to it, and resumes the child's primary thread (#415
// PR-2, #920). Failing closed here prevents a fast child from spawning
// descendants or completing before kernel tree containment is armed.
func spawnJobHandle(cmd *exec.Cmd) (uintptr, error) {
	if cmd == nil || cmd.Process == nil {
		return 0, errors.New("assign job: child process not started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		killUncontainedChild(cmd)
		return 0, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	// #nosec G103 -- Windows SetInformationJobObject requires pointer to JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	_, err = windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)))
	if err != nil {
		closeJobAndKill(job, cmd)
		return 0, fmt.Errorf("set job limits: %w", err)
	}
	// os.Process does not expose its raw handle — open one by PID with
	// enough rights to assign the process into the job.
	child, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false,
		uint32(cmd.Process.Pid))
	if err != nil {
		closeJobAndKill(job, cmd)
		return 0, fmt.Errorf("open child process %d: %w", cmd.Process.Pid, err)
	}
	assignErr := windows.AssignProcessToJobObject(job, child)
	if cerr := windows.CloseHandle(child); cerr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: close child process handle %d: %v\n", cmd.Process.Pid, cerr)
	}
	if assignErr != nil {
		closeJobAndKill(job, cmd)
		return 0, fmt.Errorf("assign child %d to job: %w", cmd.Process.Pid, assignErr)
	}
	if rerr := sandbox.ResumeProcessThreads(cmd.Process.Pid); rerr != nil {
		closeJobAndKill(job, cmd)
		return 0, fmt.Errorf("resume child %d after job attach: %w", cmd.Process.Pid, rerr)
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
