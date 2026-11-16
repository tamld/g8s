//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func prepareOS(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
	return &PreparedLaunch{
		Argv:      append([]string{}, cfg.Argv...),
		Env:       env,
		Mode:      ModeJobObjectEnvScrubbed,
		Backend:   "windows_job_object",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup: func() {
			if ownTmp {
				removeAllLogged(tmpDir)
			}
		},
	}, nil
}

// AssignJobObjectKillOnClose binds pid to a new Windows Job Object configured
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and returns an idempotent close hook
// that terminates the entire process tree when invoked.
func AssignJobObjectKillOnClose(pid int) (func(), error) {
	if pid <= 0 {
		return nil, fmt.Errorf("sandbox: invalid windows pid %d", pid)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	// #nosec G103 -- Windows SetInformationJobObject requires pointer to JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	if _, serr := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); serr != nil {
		if cerr := windows.CloseHandle(job); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: close job handle: %v\n", cerr)
		}
		return nil, fmt.Errorf("sandbox: set job limits: %w", serr)
	}

	child, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if cerr := windows.CloseHandle(job); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: close job handle: %v\n", cerr)
		}
		return nil, fmt.Errorf("sandbox: open process %d: %w", pid, err)
	}
	assignErr := windows.AssignProcessToJobObject(job, child)
	if cerr := windows.CloseHandle(child); cerr != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: close process handle %d: %v\n", pid, cerr)
	}
	if assignErr != nil {
		if cerr := windows.CloseHandle(job); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: close job handle: %v\n", cerr)
		}
		return nil, fmt.Errorf("sandbox: assign process %d to job: %w", pid, assignErr)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if cerr := windows.CloseHandle(job); cerr != nil {
				fmt.Fprintf(os.Stderr, "[warn] sandbox: close job handle for pid %d: %v\n", pid, cerr)
			}
		})
	}, nil
}

func configureContainedProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func attachContainedHandle(cmd *exec.Cmd) (func(), error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("sandbox: child process not started")
	}
	return AssignJobObjectKillOnClose(cmd.Process.Pid)
}

func terminateContainedProc(cmd *exec.Cmd, releaseHandle func()) {
	if cmd != nil && cmd.Process != nil && cmd.Process.Pid > 0 {
		pid := cmd.Process.Pid
		if terr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); terr != nil {
			if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
				fmt.Fprintf(os.Stderr, "[warn] sandbox: kill windows pid %d: %v\n", pid, kerr)
			}
		}
	}
	if releaseHandle != nil {
		releaseHandle()
	}
}
