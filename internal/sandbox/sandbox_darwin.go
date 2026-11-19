//go:build darwin

package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func prepareOS(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
	sbBin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		const fallback = "/usr/bin/sandbox-exec"
		if st, statErr := os.Stat(fallback); statErr != nil || st.IsDir() {
			if ownTmp {
				removeAllLogged(tmpDir)
			}
			return nil, fmt.Errorf("sandbox: locate sandbox-exec: %w", err)
		}
		sbBin = fallback
	}

	writableDirs := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
	denyDirs, denyFiles := SensitiveDenySpec(resolveHomeDir(cfg), cfg.StateDir, resolveSSHAuthSock(cfg))
	denyDirs = MergeDenyDirs(denyDirs, cfg.DenyDirs)
	profile, err := BuildSeatbeltProfile(writableDirs, denyDirs, denyFiles)
	if err != nil {
		if ownTmp {
			removeAllLogged(tmpDir)
		}
		return nil, err
	}

	wrappedArgv := make([]string, 0, len(cfg.Argv)+4)
	wrappedArgv = append(wrappedArgv, sbBin, "-p", profile, "--")
	wrappedArgv = append(wrappedArgv, cfg.Argv...)

	return &PreparedLaunch{
		Argv:      wrappedArgv,
		Env:       env,
		Mode:      ModeKernelEnforced,
		Backend:   "seatbelt",
		TempDir:   tmpDir,
		SandboxID: sandboxID,
		Cleanup:   makeDarwinCleanup(sandboxID, tmpDir, ownTmp),
	}, nil
}

func makeDarwinCleanup(sandboxID, tmpDir string, ownTmp bool) func() {
	return func() {
		if sandboxID != "" {
			sweepDarwinSandboxProcesses(sandboxID)
		}
		if ownTmp {
			removeAllLogged(tmpDir)
		}
	}
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func hasExactEnvToken(out []byte, token []byte) bool {
	idx := 0
	for {
		j := bytes.Index(out[idx:], token)
		if j < 0 {
			return false
		}
		matchIdx := idx + j
		startOK := matchIdx == 0 || isSpaceByte(out[matchIdx-1])
		endIdx := matchIdx + len(token)
		endOK := endIdx == len(out) || isSpaceByte(out[endIdx])
		if startOK && endOK {
			return true
		}
		idx = matchIdx + 1
	}
}

func findSandboxProcessesOS(sandboxID string) []int {
	selfPID := os.Getpid()
	out, err := exec.Command("ps", "-x", "-o", "pid=").Output()
	if err != nil {
		return nil
	}
	target := []byte("G8S_SANDBOX_ID=" + sandboxID)
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, perr := strconv.Atoi(strings.TrimSpace(line))
		if perr != nil || pid <= 1 || pid == selfPID {
			continue
		}
		envOut, psErr := exec.Command("ps", "-E", "-ww", "-p", strconv.Itoa(pid)).Output()
		if psErr != nil {
			continue
		}
		if hasExactEnvToken(envOut, target) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func sweepDarwinSandboxProcesses(sandboxID string) {
	for _, pid := range findSandboxProcessesOS(sandboxID) {
		if kerr := syscall.Kill(pid, syscall.SIGKILL); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: kill survivor pid %d: %v\n", pid, kerr)
		}
	}
}
