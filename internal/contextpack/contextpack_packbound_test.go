package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackSkipsOversizeFile is the #981 guard test: a candidate that grows past the
// per-file budget between collection and read must be skipped instead of buffered
// whole; normal-size siblings still pack.
func TestPackSkipsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	oversize := filepath.Join(dir, "oversize.go")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", int(maxPackFileBytes)+1)), 0o600); err != nil {
		t.Fatalf("seed oversize file: %v", err)
	}
	normal := filepath.Join(dir, "normal.go")
	if err := os.WriteFile(normal, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("seed normal file: %v", err)
	}

	res, err := Pack(PackRequest{RootDir: dir, BudgetTokens: 20000, MaxFiles: 10})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	for _, fe := range res.Files {
		if fe.Path == "oversize.go" {
			t.Fatalf("oversize file was packed (tokens=%d) — must be skipped (#981)", fe.OriginalTokens)
		}
	}
	found := false
	for _, fe := range res.Files {
		if fe.Path == "normal.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("normal.go missing from pack result files: %+v", res.Files)
	}
}
