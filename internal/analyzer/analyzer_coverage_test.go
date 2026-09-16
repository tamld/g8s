package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeGenericFileCoverage(t *testing.T) {
	tempDir := t.TempDir()
	analyzer, err := NewAnalyzer(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	targetFile := filepath.Join(tempDir, "config.yaml")
	_ = os.WriteFile(targetFile, []byte("key: value"), 0644)

	otherFile := filepath.Join(tempDir, "main.go")
	_ = os.WriteFile(otherFile, []byte("import \"config.yaml\""), 0644)

	report, err := analyzer.analyzeGenericFile("config.yaml", targetFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TargetFile != "config.yaml" {
		t.Errorf("Expected primary target config.yaml, got %s", report.TargetFile)
	}
}
