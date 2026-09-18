// Package runtime provides runtime verification utilities for executable identity
// and process management.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrExecutableNotFound indicates the executable was not found or is invalid.
var ErrExecutableNotFound = errors.New("executable not found or invalid")

// ErrExecutableMismatch indicates the executable identity doesn't match expected.
var ErrExecutableMismatch = errors.New("executable identity mismatch")

// ErrCommandTimeout indicates a command execution timed out.
var ErrCommandTimeout = errors.New("command execution timeout")

// VerifyOptions configures executable verification behavior.
type VerifyOptions struct {
	// ExpectedNames is a list of acceptable executable basenames (e.g., ["python3", "python"]).
	// If empty, only path existence and executability are checked.
	ExpectedNames []string

	// AllowedDirs restricts executable to these directories.
	// If empty, any directory is allowed.
	AllowedDirs []string

	// RequireAbsolutePath requires the executable to be an absolute path.
	RequireAbsolutePath bool

	// CheckShebang interprets shebang for script files and verifies interpreter.
	CheckShebang bool
}

// VerifyResult contains the result of executable verification.
type VerifyResult struct {
	ResolvedPath  string
	BaseName      string
	Dir           string
	IsScript      bool
	Interpreter   string
	Verified      bool
	VerifiedNames []string
}

// VerifyExecutable verifies an executable's identity and returns detailed info.
func VerifyExecutable(path string, opts VerifyOptions) (VerifyResult, error) {
	if path == "" {
		return VerifyResult{}, ErrExecutableNotFound
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return VerifyResult{}, ErrExecutableNotFound
	}

	// Check if file exists and is executable
	info, err := os.Stat(absPath)
	if err != nil {
		return VerifyResult{}, ErrExecutableNotFound
	}

	if info.Mode()&0o111 == 0 {
		return VerifyResult{}, ErrExecutableNotFound
	}

	// Check allowed directories
	if len(opts.AllowedDirs) > 0 {
		allowed := false
		for _, dir := range opts.AllowedDirs {
			if strings.HasPrefix(absPath, filepath.Clean(dir)+string(filepath.Separator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return VerifyResult{}, ErrExecutableMismatch
		}
	}

	// Verify basename if expected names provided
	verifiedNames := make([]string, 0)
	if len(opts.ExpectedNames) > 0 {
		base := filepath.Base(absPath)
		matched := false
		for _, expected := range opts.ExpectedNames {
			// ⚡ Bolt Optimization: Use EqualFold on a sliced substring for zero-allocation case-insensitive prefix checking
			// instead of strings.HasPrefix(strings.ToLower()) which allocates two new strings on the heap.
			if strings.EqualFold(base, expected) || (len(base) >= len(expected) && strings.EqualFold(base[:len(expected)], expected)) {
				matched = true
				verifiedNames = append(verifiedNames, expected)
			}
		}
		if !matched {
			return VerifyResult{}, ErrExecutableMismatch
		}
	}

	// Check shebang if requested
	isScript := false
	var interpreter string
	if opts.CheckShebang {
		isScript, interpreter = checkShebang(absPath)
	}

	return VerifyResult{
		ResolvedPath:  absPath,
		BaseName:      filepath.Base(absPath),
		Dir:           filepath.Dir(absPath),
		IsScript:      isScript,
		Interpreter:   interpreter,
		Verified:      true,
		VerifiedNames: verifiedNames,
	}, nil
}

// checkShebang reads the first line of a file to detect shebang.
func checkShebang(path string) (bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	content := string(buf[:n])

	if !strings.HasPrefix(content, "#!") {
		return false, ""
	}

	// Find the interpreter path
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return false, ""
	}

	shebang := strings.TrimSpace(lines[0][2:]) // Remove "#! "
	parts := strings.Fields(shebang)
	if len(parts) == 0 {
		return true, ""
	}

	return true, parts[0]
}

// VerifyCommandIdentity verifies the actual executable that would be run by exec.Command.
func VerifyCommandIdentity(name string, args []string) (string, VerifyResult, error) {
	// Use exec.LookPath to find the actual executable
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", VerifyResult{}, ErrExecutableNotFound
	}

	result, err := VerifyExecutable(resolved, VerifyOptions{
		ExpectedNames: []string{filepath.Base(name)},
		CheckShebang:  true,
	})
	if err != nil {
		return "", result, err
	}

	return resolved, result, nil
}

// RunWithTimeout executes a command with a timeout and returns the result.
func RunWithTimeout(timeout time.Duration, command string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), stderr.Bytes(), ErrCommandTimeout
	}

	return stdout.Bytes(), stderr.Bytes(), err
}

// RunWithTimeoutAndVerify combines executable verification with timed execution.
func RunWithTimeoutAndVerify(timeout time.Duration, command string, args []string, opts VerifyOptions) (VerifyResult, []byte, []byte, error) {
	// First verify the executable
	resolved, verifyResult, err := VerifyCommandIdentity(command, args)
	if err != nil {
		return verifyResult, nil, nil, err
	}

	// Run with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, resolved, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return verifyResult, stdout.Bytes(), stderr.Bytes(), ErrCommandTimeout
	}

	return verifyResult, stdout.Bytes(), stderr.Bytes(), err
}
