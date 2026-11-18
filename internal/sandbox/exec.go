package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// RunContained prepares and executes cfg inside OS-enforced containment, waits
// for completion or context cancellation, runs post-exit orphan cleanup, and
// returns the child exit code, containment mode, and backend identifier.
func RunContained(ctx context.Context, cfg Config, stdout, stderr io.Writer) (exitCode int, mode ContainmentMode, backend string, err error) {
	prep, err := Prepare(cfg)
	if err != nil {
		return -1, "", "", err
	}
	defer func() {
		if prep.Cleanup != nil {
			prep.Cleanup()
		}
	}()

	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	if cfg.WorktreeDir != "" {
		cmd.Dir = CanonicalPath(cfg.WorktreeDir)
	}
	cmd.Env = prep.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureContainedProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return -1, prep.Mode, prep.Backend, fmt.Errorf("sandbox: start command: %w", err)
	}

	releaseHandle, attachErr := attachContainedHandle(cmd)
	if attachErr != nil {
		if werr := cmd.Wait(); werr != nil {
			fmt.Fprintf(os.Stderr, "[warn] sandbox: wait uncontained child: %v\n", werr)
		}
		return -1, prep.Mode, prep.Backend, fmt.Errorf("sandbox: attach containment handle: %w", attachErr)
	}
	defer func() {
		if releaseHandle != nil {
			releaseHandle()
		}
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		terminateContainedProc(cmd, releaseHandle)
		waitErr = <-waitCh
		return exitCodeFromWait(waitErr, ctx.Err()), prep.Mode, prep.Backend, ctx.Err()
	}

	return exitCodeFromWait(waitErr, nil), prep.Mode, prep.Backend, nil
}

func exitCodeFromWait(waitErr, ctxErr error) int {
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode()
	}
	if ctxErr != nil {
		return -1
	}
	return 1
}
