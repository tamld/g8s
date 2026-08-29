package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

var fixedClock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestAgyWorkerName(t *testing.T) {
	w := &AgyWorker{}
	if got := w.Name(); got != "agy" {
		t.Errorf("Name() = %v, want %v", got, "agy")
	}
}

func TestAgyWorkerAvailableNoBinary(t *testing.T) {
	w := &AgyWorker{binary: ""}
	err := w.Available(context.Background())
	if err == nil {
		t.Error("Available() expected error for empty binary")
	}
}

func TestAgyWorkerAvailableWithBinary(t *testing.T) {
	w := &AgyWorker{binary: "/fake/agy"}
	err := w.Available(context.Background())
	if err != nil {
		t.Errorf("Available() unexpected error: %v", err)
	}
}

func TestAgyWorkerSpawnNoBinary(t *testing.T) {
	w := &AgyWorker{binary: ""}
	_, err := w.Spawn(context.Background(), Task{})
	if !errors.Is(err, ErrWorkerUnavailable) {
		t.Errorf("Spawn() err = %v, want %v", err, ErrWorkerUnavailable)
	}
}

func TestAgyHandlePIDNilCmd(t *testing.T) {
	h := &agyHandle{cmd: nil}
	if got := h.PID(); got != 0 {
		t.Errorf("PID() = %v, want 0", got)
	}
}

func TestAgyHandlePIDNilProcess(t *testing.T) {
	cmd := exec.Command("echo") // un-started cmd has nil Process
	h := &agyHandle{cmd: cmd}
	if got := h.PID(); got != 0 {
		t.Errorf("PID() = %v, want 0", got)
	}
}

func TestAgyHandleStdoutStream(t *testing.T) {
	h := &agyHandle{}
	if got := h.StdoutStream(); got != nil {
		t.Errorf("StdoutStream() = %v, want nil", got)
	}
}

func TestAgyHandleSynthesizeSuccess(t *testing.T) {
	h := &agyHandle{
		stdout:    bytes.NewBufferString("out"),
		stderr:    bytes.NewBufferString("err"),
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	r := h.synthesize(nil)
	if !r.OK {
		t.Error("synthesize(nil) OK = false, want true")
	}
	if r.HarnessCode != 0 {
		t.Errorf("synthesize(nil) HarnessCode = %v, want 0", r.HarnessCode)
	}
}

func TestAgyHandleSynthesizeCancelled(t *testing.T) {
	h := &agyHandle{
		stdout:    bytes.NewBufferString(""),
		stderr:    bytes.NewBufferString(""),
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	r := h.synthesize(ErrWorkerCancelled)
	if r.OK {
		t.Error("synthesize(ErrWorkerCancelled) OK = true, want false")
	}
	if r.HarnessCode != 130 {
		t.Errorf("synthesize(ErrWorkerCancelled) HarnessCode = %v, want 130", r.HarnessCode)
	}
}

func TestAgyHandleSynthesizeTimeout(t *testing.T) {
	h := &agyHandle{
		stdout:    bytes.NewBufferString(""),
		stderr:    bytes.NewBufferString(""),
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	r := h.synthesize(ErrWorkerTimeout)
	if r.OK {
		t.Error("synthesize(ErrWorkerTimeout) OK = true, want false")
	}
	if r.HarnessCode != 124 {
		t.Errorf("synthesize(ErrWorkerTimeout) HarnessCode = %v, want 124", r.HarnessCode)
	}
}

func TestAgyHandleSynthesizeExitError(t *testing.T) {
	h := &agyHandle{
		stdout:    bytes.NewBufferString(""),
		stderr:    bytes.NewBufferString(""),
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	cmd := exec.Command("false")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %v", err)
	}
	r := h.synthesize(exitErr)
	if r.OK {
		t.Error("synthesize(ExitError) OK = true, want false")
	}
	if r.ReturnCode != exitErr.ExitCode() {
		t.Errorf("ReturnCode = %v, want %v", r.ReturnCode, exitErr.ExitCode())
	}
}

func TestAgyHandleCancelAlreadyDone(t *testing.T) {
	h := &agyHandle{done: true}
	err := h.Cancel(context.Background())
	if err != nil {
		t.Errorf("Cancel() err = %v, want nil", err)
	}
}

func TestAgyHandleCancelNilCmd(t *testing.T) {
	h := &agyHandle{cmd: nil}
	err := h.Cancel(context.Background())
	if err != nil {
		t.Errorf("Cancel() err = %v, want nil", err)
	}
}

func TestAgyHandleWaitAlreadyDone(t *testing.T) {
	h := &agyHandle{
		done:      true,
		waitErr:   ErrWorkerTimeout,
		stdout:    bytes.NewBufferString(""),
		stderr:    bytes.NewBufferString(""),
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	receipt, err := h.Wait(context.Background())
	if !errors.Is(err, ErrWorkerTimeout) {
		t.Errorf("Wait() err = %v, want %v", err, ErrWorkerTimeout)
	}
	if receipt.HarnessCode != 124 {
		t.Errorf("receipt.HarnessCode = %v, want 124", receipt.HarnessCode)
	}
}

func TestAgyHandleWaitRealProcess(t *testing.T) {
	cmd := exec.Command("true")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() failed: %v", err)
	}
	h := &agyHandle{
		cmd:       cmd,
		stdout:    &stdout,
		stderr:    &stderr,
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	receipt, err := h.Wait(context.Background())
	if err != nil {
		t.Errorf("Wait() err = %v, want nil", err)
	}
	if !receipt.OK {
		t.Error("Wait() receipt OK = false, want true")
	}
}

func TestAgyHandleWaitContextCancel(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() failed: %v", err)
	}
	h := &agyHandle{
		cmd:       cmd,
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		startedAt: fixedClock(),
		clock:     fixedClock,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	receipt, err := h.Wait(ctx)
	if !errors.Is(err, ErrWorkerCancelled) && !errors.Is(err, ErrWorkerTimeout) {
		t.Errorf("Wait() err = %v, want ErrWorkerCancelled or ErrWorkerTimeout", err)
	}
	if receipt.HarnessCode != 130 && receipt.HarnessCode != 124 {
		t.Errorf("receipt.HarnessCode = %v, want 130 or 124", receipt.HarnessCode)
	}
}

func TestAgyWorkerSpawnDefaultsFilledIn(t *testing.T) {
	w := &AgyWorker{
		binary: "true",
		clock:  fixedClock,
	}
	handle, err := w.Spawn(context.Background(), Task{})
	if err != nil {
		t.Fatalf("Spawn() err = %v, want nil", err)
	}
	h, ok := handle.(*agyHandle)
	if !ok {
		t.Fatalf("Spawn() handle is not *agyHandle")
	}
	if h.timeout != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", h.timeout)
	}
	// Wait for process to finish
	_, _ = h.Wait(context.Background())
}
