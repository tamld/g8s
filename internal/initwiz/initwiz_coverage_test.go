package initwiz

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectIDEsCoverage(t *testing.T) {
	tempDir := t.TempDir()

	cursorDir := filepath.Join(tempDir, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)

	mcpPath := filepath.Join(cursorDir, "mcp.json")
	_ = os.WriteFile(mcpPath, []byte(`{"mcpServers": {"g8s": {}}}`), 0o644)

	ides, err := DetectIDEs(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ides) != 4 {
		t.Errorf("Expected 4 ides, got %d", len(ides))
	}
}

func TestCheckIDECoverage(t *testing.T) {
	tempDir := t.TempDir()

	// Create invalid json
	mcpPath := filepath.Join(tempDir, "mcp.json")
	_ = os.WriteFile(mcpPath, []byte(`{invalid`), 0o644)

	checkIDE(IDECursor, "Cursor IDE", mcpPath)
}
