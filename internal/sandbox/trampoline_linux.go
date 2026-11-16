//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	internalExecEnvKey = "G8S_INTERNAL_SANDBOX_EXEC"
	internalCfgEnvKey  = "G8S_INTERNAL_SANDBOX_CFG"
)

type landlockPayload struct {
	Argv         []string `json:"argv"`
	WorktreeDir  string   `json:"worktree_dir"`
	WritableDirs []string `json:"writable_dirs"`
	DenyDirs     []string `json:"deny_dirs"`
	DenyFiles    []string `json:"deny_files"`
	SandboxID    string   `json:"sandbox_id"`
	ABI          int      `json:"abi"`
}

func init() {
	if os.Getenv(internalExecEnvKey) != "1" {
		return
	}
	rawCfg := os.Getenv(internalCfgEnvKey)
	if err := os.Unsetenv(internalExecEnvKey); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: unset %s: %v\n", internalExecEnvKey, err)
	}
	if err := os.Unsetenv(internalCfgEnvKey); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: unset %s: %v\n", internalCfgEnvKey, err)
	}
	os.Exit(runLandlockShim(rawCfg))
}

func runLandlockShim(rawCfg string) int {
	var payload landlockPayload
	if err := json.Unmarshal([]byte(rawCfg), &payload); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: invalid landlock payload: %v\n", err)
		return 126
	}
	if len(payload.Argv) == 0 || strings.TrimSpace(payload.Argv[0]) == "" {
		fmt.Fprintln(os.Stderr, "sandbox: empty target argv")
		return 126
	}

	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: set PR_SET_CHILD_SUBREAPER: %v\n", err)
	}
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] sandbox: set PR_SET_PDEATHSIG: %v\n", err)
	}

	if err := applyLandlockPolicy(payload); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: apply landlock policy: %v\n", err)
		return 126
	}

	cmd := exec.Command(payload.Argv[0], payload.Argv[1:]...)
	if payload.WorktreeDir != "" {
		cmd.Dir = payload.WorktreeDir
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: start target %s: %v\n", payload.Argv[0], err)
		return 127
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	done := false
	for !done {
		select {
		case waitErr = <-waitCh:
			done = true
		case sig := <-sigCh:
			if cmd.Process != nil {
				if serr := cmd.Process.Signal(sig); serr != nil && !errors.Is(serr, os.ErrProcessDone) {
					fmt.Fprintf(os.Stderr, "[warn] sandbox: forward signal %v: %v\n", sig, serr)
				}
			}
		}
	}

	reapSubreaperChildren(payload.SandboxID)
	return exitCodeFromWait(waitErr, nil)
}

func reapSubreaperChildren(sandboxID string) {
	selfPID := os.Getpid()
	taskDir := filepath.Join("/proc", strconv.Itoa(selfPID), "task")
	tids, err := os.ReadDir(taskDir)
	if err == nil {
		for _, tid := range tids {
			data, rerr := os.ReadFile(filepath.Join(taskDir, tid.Name(), "children"))
			if rerr != nil {
				continue
			}
			for _, field := range strings.Fields(string(data)) {
				cpid, perr := strconv.Atoi(field)
				if perr != nil || cpid <= 1 || cpid == selfPID {
					continue
				}
				if kerr := syscall.Kill(cpid, syscall.SIGKILL); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
					fmt.Fprintf(os.Stderr, "[warn] sandbox: kill reparented child %d: %v\n", cpid, kerr)
				}
			}
		}
	}
	if sandboxID != "" {
		sweepLinuxSandboxProcesses(sandboxID)
	}
}

func applyLandlockPolicy(payload landlockPayload) error {
	abiPtr, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("query landlock abi: %w", errno)
	}
	abi := int(abiPtr)
	if abi < 1 {
		return fmt.Errorf("unsupported landlock abi %d", abi)
	}

	handledFS := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 3 {
		handledFS |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}

	type rulesetAttr struct {
		HandledAccessFS uint64
	}
	attr := rulesetAttr{HandledAccessFS: handledFS}
	// #nosec G103 -- Linux landlock_create_ruleset expects pointer to 8-byte v1 attr struct.
	r1, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	rulesetFD := int(r1)
	defer func() {
		if cerr := unix.Close(rulesetFD); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: close landlock ruleset fd: %v\n", cerr)
		}
	}()

	dirRO := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	fileRO := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE)
	dirRW := handledFS
	fileRW := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE|unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_WRITE_FILE|unix.LANDLOCK_ACCESS_FS_TRUNCATE) & handledFS

	allowPaths, err := ComputeLandlockReadAllowPaths("/", payload.DenyDirs, payload.DenyFiles, os.ReadDir)
	if err != nil {
		return fmt.Errorf("compute read allow paths: %w", err)
	}
	for _, p := range allowPaths {
		if rerr := addLandlockPathRule(rulesetFD, p, handledFS, dirRO, fileRO); rerr != nil {
			return fmt.Errorf("add ro rule %s: %w", p, rerr)
		}
	}
	for _, dev := range []string{"/dev/null", "/dev/zero", "/dev/urandom", "/dev/random", "/dev/tty"} {
		if rerr := addLandlockPathRule(rulesetFD, dev, handledFS, dirRW, fileRW); rerr != nil {
			return fmt.Errorf("add dev rule %s: %w", dev, rerr)
		}
	}
	for _, w := range payload.WritableDirs {
		if rerr := addLandlockPathRule(rulesetFD, w, handledFS, dirRW, fileRW); rerr != nil {
			return fmt.Errorf("add rw rule %s: %w", w, rerr)
		}
	}

	if _, _, eno := syscall.AllThreadsSyscall(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0); eno != 0 {
		return fmt.Errorf("prctl PR_SET_NO_NEW_PRIVS: %w", eno)
	}
	if _, _, eno := syscall.AllThreadsSyscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0); eno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", eno)
	}
	return nil
}

func addLandlockPathRule(rulesetFD int, path string, handledFS, dirAccess, fileAccess uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.ELOOP) {
			return nil
		}
		return err
	}
	defer func() {
		if cerr := unix.Close(fd); cerr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: close path fd %s: %v\n", path, cerr)
		}
	}()

	var st unix.Stat_t
	if ferr := unix.Fstat(fd, &st); ferr != nil {
		return nil
	}
	access := fileAccess
	if (st.Mode & unix.S_IFMT) == unix.S_IFDIR {
		access = dirAccess
	}
	access &= handledFS
	if access == 0 {
		return nil
	}

	pathAttr := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}
	// #nosec G103 -- Linux landlock_add_rule expects pointer to LandlockPathBeneathAttr.
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&pathAttr)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
