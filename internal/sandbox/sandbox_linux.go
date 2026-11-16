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

func probeBwrap() (string, bool) {
	bwrapOnce.Do(func() {
		bin, err := exec.LookPath("bwrap")
		if err != nil {
			return
		}
		trueBin := "/bin/true"
		if resolved, tErr := exec.LookPath("true"); tErr == nil {
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
		if runErr := cmd.Run(); runErr == nil {
			bwrapPath = bin
			bwrapOK = true
		}
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

func prepareOS(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
	writableDirs := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
	denyDirs, denyFiles := SensitiveDenySpec(resolveHomeDir(cfg), cfg.StateDir, resolveSSHAuthSock(cfg))
	cleanup := makeLinuxCleanup(sandboxID, tmpDir, ownTmp)

	if bwrapBin, ok := probeBwrap(); ok {
		args := BuildBwrapArgs(bwrapBin, cfg.Argv, cfg.WorktreeDir, writableDirs, denyDirs, denyFiles, nil)
		return &PreparedLaunch{
			Argv:      args,
			Env:       env,
			Mode:      ModeKernelEnforced,
			Backend:   "bwrap",
			TempDir:   tmpDir,
			SandboxID: sandboxID,
			Cleanup:   cleanup,
		}, nil
	}

	if abi := probeLandlockABI(); abi >= 1 {
		selfExe, err := os.Executable()
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("sandbox: resolve executable for landlock trampoline: %w", err)
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
			cleanup()
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

	return &PreparedLaunch{
		Argv:      append([]string{}, cfg.Argv...),
		Env:       env,
		Mode:      ModePolicyOnly,
		Backend:   "linux_policy_only",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup:   cleanup,
	}, nil
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

func sweepLinuxSandboxProcesses(sandboxID string) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	selfPID := os.Getpid()
	target := []byte("G8S_SANDBOX_ID=" + sandboxID)
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
			if kerr := syscall.Kill(pid, syscall.SIGKILL); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
				fmt.Fprintf(os.Stderr, "[warn] sandbox: kill survivor pid %d: %v\n", pid, kerr)
			}
		}
	}
}
