package worker

import (
	"testing"
	"time"
)

func TestFunctionalOptions(t *testing.T) {
	clk := func() time.Time { return time.Unix(1234567890, 0) }
	s := NewSupervisor(nil, t.TempDir(),
		WithClock(clk),
		WithCaptureMaxBytes(1024),
		WithBinaryPath("/usr/bin/true"),
		WithEvidenceDir(t.TempDir()),
		WithPollInterval(250*time.Millisecond),
	)
	if s == nil {
		t.Fatal("NewSupervisor returned nil")
	}
	if s.binaryPath != "/usr/bin/true" {
		t.Errorf("binaryPath not applied: %q", s.binaryPath)
	}
	if s.captureMaxBytes != 1024 {
		t.Errorf("captureMaxBytes not applied: %d", s.captureMaxBytes)
	}
	if s.pollInterval != 250*time.Millisecond {
		t.Errorf("pollInterval not applied: %v", s.pollInterval)
	}
}

func TestSubstituteTemplate(t *testing.T) {
	tests := []struct {
		tmpl          []string
		prompt, model string
		timeout       string
		wantJoined    string
	}{
		{[]string{"echo", "{prompt}"}, "hello", "m1", "10s", "echo hello"},
		{[]string{"run", "{model}"}, "hello", "m1", "10s", "run m1"},
		{[]string{"timeout", "{timeout}"}, "hello", "m1", "10s", "timeout 10s"},
		{[]string{"literal"}, "hello", "m1", "10s", "literal"},
		{[]string{}, "hello", "m1", "10s", ""},
		{[]string{"{prompt}", "{model}", "{timeout}"}, "x", "y", "z", "x y z"},
	}
	for _, tt := range tests {
		got := substituteTemplate(tt.tmpl, tt.prompt, tt.model, tt.timeout)
		joined := ""
		for i, s := range got {
			if i > 0 {
				joined += " "
			}
			joined += s
		}
		if joined != tt.wantJoined {
			t.Errorf("substituteTemplate(%v) joined=%q, want %q", tt.tmpl, joined, tt.wantJoined)
		}
	}
}

func TestExitCodeOf(t *testing.T) {
	if exitCodeOf(nil) != 0 {
		t.Error("nil error should yield 0")
	}
}

type fakeErrExit int

func (e fakeErrExit) Error() string { return "fake err" }

func TestExitCodeOfNonNil(t *testing.T) {
	if got := exitCodeOf(assertErr("plain error")); got != -1 {
		t.Errorf("expected -1 for non-exit-error, got %d", got)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
