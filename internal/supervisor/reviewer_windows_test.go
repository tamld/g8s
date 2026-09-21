//go:build windows

package supervisor

import (
	"path/filepath"
	"testing"
)

func TestRealReviewerCollectPackagesWindows(t *testing.T) {
	r := NewRealReviewer()
	files := []string{
		"internal\\a\\a.go",
		"internal\\a\\b.go",
		"internal\\b\\c.go",
		"README.md",
		"cmd\\main.go",
	}
	pkgs := r.collectPackages(files)
	if len(pkgs) != 3 {
		t.Errorf("expected 3 packages, got %d: %v", len(pkgs), pkgs)
	}
	expected := map[string]bool{
		filepath.Dir("internal\\a\\a.go"): true,
		filepath.Dir("internal\\b\\c.go"): true,
		filepath.Dir("cmd\\main.go"):      true,
	}
	for _, p := range pkgs {
		if !expected[p] {
			t.Errorf("unexpected package: %s", p)
		}
	}
}
