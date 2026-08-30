package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
)

// runWrapExec executes a child command and records its outcome as a g8s
// result envelope (DELTA-10 R4), letting the supervisor supervise CLIs
// that do not natively emit result.json. The wrapper itself always exits
// 0 on its own success; the envelope carries the child's outcome.
func runWrapExec(argv []string) error {
	if len(argv) == 0 || argv[0] != "wrap-exec" {
		return errors.New("usage: g8s internal wrap-exec --out <path> -- <child argv>")
	}
	rest := argv[1:]
	idx := -1
	for i, a := range rest {
		if a == "--" {
			idx = i
			break
		}
	}
	if idx <= 0 || idx == len(rest)-1 {
		return errors.New("usage: g8s internal wrap-exec --out <path> -- <child argv>")
	}
	outPath := ""
	fs := flag.NewFlagSet("wrap-exec", flag.ExitOnError)
	fs.StringVar(&outPath, "out", "", "path to write the result envelope")
	if err := fs.Parse(rest[:idx]); err != nil {
		return err
	}
	if outPath == "" || fs.NArg() != 0 {
		return errors.New("usage: g8s internal wrap-exec --out <path> -- <child argv>")
	}
	child := rest[idx+1:]

	cmd := exec.Command(child[0], child[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code = ee.ExitCode()
		} else {
			return runErr
		}
	}
	envelope := map[string]any{"ok": code == 0, "exit_code": code}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, raw, 0o600)
}
