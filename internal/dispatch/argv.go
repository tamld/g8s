package dispatch

import (
	"os"
)

// WorkerArgvOptions describes parameters for constructing worker invocation arguments.
type WorkerArgvOptions struct {
	Binary          string
	Prompt          string
	PromptFile      string
	Model           string
	Role            string
	Permission      string
	Timeout         string
	ResultPath      string
	AddDirs         []string
	SkipPermissions bool
	NoSandbox       bool
	Home            string
}

// BuildWorkerArgv constructs the worker command argv according to standard CLI argument structure.
func BuildWorkerArgv(opts WorkerArgvOptions) []string {
	binary := opts.Binary
	if binary == "" {
		binary = "agy"
	}

	argv := []string{binary}

	if opts.PromptFile != "" {
		argv = append(argv, "--prompt-file", opts.PromptFile)
	} else if opts.Prompt != "" {
		argv = append(argv, "--prompt", opts.Prompt)
	}

	if opts.Model != "" {
		argv = append(argv, "--model", opts.Model)
	}
	if opts.Role != "" {
		argv = append(argv, "--role", opts.Role)
	}
	if opts.Permission != "" {
		argv = append(argv, "--permission", opts.Permission)
	}
	if opts.Timeout != "" {
		argv = append(argv, "--timeout", opts.Timeout)
	}
	if opts.ResultPath != "" {
		argv = append(argv, "--out", opts.ResultPath)
	}

	home := opts.Home
	if home == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			home = homeDir
		}
	}

	for _, dir := range opts.AddDirs {
		argv = append(argv, "--add-dir", expandUser(dir, home))
	}
	if opts.SkipPermissions {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	return argv
}
