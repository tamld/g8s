package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// runSelfAudit serves as a thin CLI wrapper around tools/dogfood_report.sh,
// streaming stdout and stderr directly so CI gates can inspect diagnostic output.
func runSelfAudit(args []string) {
	scriptPath, err := findDogfoodScript()
	failIf(err)

	exitCode, err := executeSelfAudit(os.Stdout, os.Stderr, scriptPath, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", AppName, err)
	}
	os.Exit(exitCode)
}

func executeSelfAudit(stdout, stderr io.Writer, scriptPath string, args []string) (int, error) {
	cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}

	return 1, fmt.Errorf("execute dogfood report: %w", err)
}

func findDogfoodScript() (string, error) {
	// Candidate locations in priority order:
	// 1. tools/dogfood_report.sh relative to cwd
	// 2. ../tools/dogfood_report.sh (running inside cmd/g8s)
	// 3. Relative to executable path
	// 4. Git repository root via git rev-parse
	candidates := []string{
		filepath.Join("tools", "dogfood_report.sh"),
		filepath.Join("..", "tools", "dogfood_report.sh"),
		filepath.Join("..", "..", "tools", "dogfood_report.sh"),
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "tools", "dogfood_report.sh"),
			filepath.Join(exeDir, "..", "tools", "dogfood_report.sh"),
			filepath.Join(exeDir, "..", "..", "tools", "dogfood_report.sh"),
		)
	}

	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return abs, nil
			}
		}
	}

	// Try git rev-parse --show-toplevel
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		gitRoot := string(filepath.Clean(string(out)))
		gitCand := filepath.Join(gitRoot, "tools", "dogfood_report.sh")
		if info, err := os.Stat(gitCand); err == nil && !info.IsDir() {
			return gitCand, nil
		}
	}

	return "", fmt.Errorf("tools/dogfood_report.sh not found (searched candidates: %v)", candidates)
}
