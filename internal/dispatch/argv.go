package dispatch

import (
	"os"
)

// BuildWorkerArgvOptions describes parameters for constructing worker invocation arguments.
type BuildWorkerArgvOptions struct {
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

// WorkerArgvOptions is an alias for BuildWorkerArgvOptions.
type WorkerArgvOptions = BuildWorkerArgvOptions

// BuildWorkerArgv constructs the worker command argv according to standard CLI argument structure.
func BuildWorkerArgv(opts BuildWorkerArgvOptions) []string {
	binary := opts.Binary
	if binary == "" {
		binary = "agy"
	}

	argv := []string{binary}

	if opts.PromptFile != "" {
		if content, err := os.ReadFile(opts.PromptFile); err == nil {
			argv = append(argv, "--prompt", string(content))
		} else if opts.Prompt != "" {
			argv = append(argv, "--prompt", opts.Prompt)
		}
	} else if opts.Prompt != "" {
		argv = append(argv, "--prompt", opts.Prompt)
	}

	if opts.Model != "" {
		argv = append(argv, "--model", opts.Model)
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

	if opts.Permission == "read_only" || (!opts.NoSandbox && opts.Permission != "workspace_write") {
		argv = append(argv, "--sandbox")
	}

	argv = append(argv, "--output-format", "stream-json")
	argv = append(argv, "--print-timeout", "30m")

	return argv
}
