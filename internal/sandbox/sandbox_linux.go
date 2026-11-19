//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var (
	bwrapOnce sync.Once
	bwrapPath string
	bwrapOK   bool

	landlockOnce sync.Once
	landlockABI  int
)

func probeBwrapWith(lookPath func(string) (string, error)) (string, bool) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("bwrap")
	if err != nil {
		return "", false
	}
	trueBin := "/bin/true"
	if resolved, tErr := lookPath("true"); tErr == nil {
		trueBin = resolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", trueBin,
	)
	if runErr := cmd.Run(); runErr != nil {
		return "", false
	}
	return bin, true
}

func probeBwrap() (string, bool) {
	bwrapOnce.Do(func() {
		bwrapPath, bwrapOK = probeBwrapWith(exec.LookPath)
	})
	return bwrapPath, bwrapOK
}

func probeLandlockABI() int {
	landlockOnce.Do(func() {
		abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
		if errno == 0 && int(abi) >= 1 {
			landlockABI = int(abi)
		}
	})
	return landlockABI
}

func buildBwrapLaunch(
	bwrapBin string,
	cfg Config,
	env []string,
	tmpDir string,
	writableDirs, denyDirs, denyFiles []string,
	sandboxID string,
	cleanup func(),
) *PreparedLaunch {
	args := BuildBwrapArgs(bwrapBin, cfg.Argv, cfg.WorktreeDir, writableDirs, denyDirs, denyFiles, nil)
	return &PreparedLaunch{
		Argv:      args,
		Env:       env,
		Mode:      ModeKernelEnforced,
		Backend:   "bwrap",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup:   cleanup,
	}
}

func buildLandlockLaunch(
	selfExe string,
	abi int,
	cfg Config,
	env []string,
	tmpDir string,
	writableDirs, denyDirs, denyFiles []string,
	sandboxID string,
	cleanup func(),
) (*PreparedLaunch, error) {
	if selfExe == "" {
		var err error
		selfExe, err = os.Executable()
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return nil, fmt.Errorf("sandbox: resolve executable for landlock trampoline: %w", err)
		}
	}
	payload := landlockPayload{
		Argv:         append([]string{}, cfg.Argv...),
		WorktreeDir:  CanonicalPath(cfg.WorktreeDir),
		WritableDirs: writableDirs,
		DenyDirs:     denyDirs,
		DenyFiles:    denyFiles,
		SandboxID:    sandboxID,
		ABI:          abi,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("sandbox: encode landlock payload: %w", err)
	}
	envWithCfg := append([]string{}, env...)
	envWithCfg = append(envWithCfg,
		internalExecEnvKey+"=1",
		internalCfgEnvKey+"="+string(raw),
	)
	return &PreparedLaunch{
		Argv:      []string{selfExe},
		Env:       envWithCfg,
		Mode:      ModeKernelEnforced,
		Backend:   "landlock",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup:   cleanup,
	}, nil
}

func buildLinuxPolicyOnlyLaunch(cfg Config, env []string, tmpDir, sandboxID string, cleanup func()) *PreparedLaunch {
	return &PreparedLaunch{
		Argv:      append([]string{}, cfg.Argv...),
		Env:       env,
		Mode:      ModePolicyOnly,
		Backend:   "linux_policy_only",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup:   cleanup,
	}
}

func prepareOS(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
	writableDirs := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
	denyDirs, denyFiles := SensitiveDenySpec(resolveHomeDir(cfg), cfg.StateDir, resolveSSHAuthSock(cfg))
	cleanup := makeLinuxCleanup(sandboxID, tmpDir, ownTmp)

	if bwrapBin, ok := probeBwrap(); ok {
		return buildBwrapLaunch(bwrapBin, cfg, env, tmpDir, writableDirs, denyDirs, denyFiles, sandboxID, cleanup), nil
	}

	if abi := probeLandlockABI(); abi >= 1 {
		return buildLandlockLaunch("", abi, cfg, env, tmpDir, writableDirs, denyDirs, denyFiles, sandboxID, cleanup)
	}

	return buildLinuxPolicyOnlyLaunch(cfg, env, tmpDir, sandboxID, cleanup), nil
}

func makeLinuxCleanup(sandboxID, tmpDir string, ownTmp bool) func() {
	return func() {
		if sandboxID != "" {
			sweepLinuxSandboxProcesses(sandboxID)
		}
		if ownTmp {
			removeAllLogged(tmpDir)
		}
	}
}

func hasExactNulEnvToken(data, token []byte) bool {
	idx := 0
	for {
		j := bytes.Index(data[idx:], token)
		if j < 0 {
			return false
		}
		matchIdx := idx + j
		startOK := matchIdx == 0 || data[matchIdx-1] == 0
		endIdx := matchIdx + len(token)
		endOK := endIdx == len(data) || data[endIdx] == 0
		if startOK && endOK {
			return true
		}
		idx = matchIdx + 1
	}
}

func findSandboxProcessesOS(sandboxID string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	selfPID := os.Getpid()
	target := []byte("G8S_SANDBOX_ID=" + sandboxID)
	var pids []int
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		pid, perr := strconv.Atoi(ent.Name())
		if perr != nil || pid <= 1 || pid == selfPID {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join("/proc", ent.Name(), "environ"))
		if rerr != nil {
			continue
		}
		if hasExactNulEnvToken(data, target) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func sweepLinuxSandboxProcesses(sandboxID string) {
	for _, pid := range findSandboxProcessesOS(sandboxID) {
		if kerr := syscall.Kill(pid, syscall.SIGKILL); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: kill survivor pid %d: %v\n", pid, kerr)
		}
	}
}
