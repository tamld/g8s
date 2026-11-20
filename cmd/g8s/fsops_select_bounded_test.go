package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loopReader yields 'a' bytes forever without allocating the payload up front.
type loopReader struct{}

func (loopReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// TestSelectStdinOversizeTypedReject is the #981 red-test: g8s select must wrap
// io.ReadAll(os.Stdin) in a 64 MiB budget and fail with a typed error past it,
// never buffer unbounded worker-directed stdin.
func TestSelectStdinOversizeTypedReject(t *testing.T) {
	payload := io.LimitReader(loopReader{}, maxSelectInputBytes+1)

	raw, err := readSelectInput(payload, maxSelectInputBytes)
	if err == nil {
		t.Fatalf("readSelectInput succeeded on %d-byte stdin payload, want typed oversize reject (#981)", len(raw))
	}
	if !errors.Is(err, errInputTooLarge) {
		t.Fatalf("error = %v, want errors.Is(err, errInputTooLarge)", err)
	}
}

// TestSelectStdinWithinBudgetPasses pins the boundary: input of exactly the budget
// must flow through unharmed.
func TestSelectStdinWithinBudgetPasses(t *testing.T) {
	payload := io.LimitReader(loopReader{}, maxSelectInputBytes)

	raw, err := readSelectInput(payload, maxSelectInputBytes)
	if err != nil {
		t.Fatalf("readSelectInput rejected exactly-at-budget input: %v", err)
	}
	if int64(len(raw)) != maxSelectInputBytes {
		t.Fatalf("raw len = %d, want %d", len(raw), maxSelectInputBytes)
	}
}

// TestSelectFileOversizeTypedReject covers the --file sibling path: readFileBounded
// stats first and typed-rejects files past the limit (exercised here with a small
// file and an explicit small limit so no 64 MiB artifact is written).
func TestSelectFileOversizeTypedReject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 2048)), 0o600); err != nil {
		t.Fatalf("seed input file: %v", err)
	}

	_, err := readFileBounded(path, 1024)
	if !errors.Is(err, errInputTooLarge) {
		t.Fatalf("error = %v, want errors.Is(err, errInputTooLarge)", err)
	}

	raw, err := readFileBounded(path, 2048)
	if err != nil {
		t.Fatalf("exactly-at-limit file rejected: %v", err)
	}
	if len(raw) != 2048 {
		t.Fatalf("raw len = %d, want 2048", len(raw))
	}
}
