package worker

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyExecutableIdentity_Disguise(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Fake Python script that outputs Node.js version
	fakePython := filepath.Join(tmpDir, "python3")
	content := "#!/bin/sh\necho 'node v20.0.0'\n"
	if runtime.GOOS == "windows" {
		fakePython = filepath.Join(tmpDir, "python.cmd")
		content = "@echo off\r\necho node v20.0.0\r\n"
	}

	if err := os.WriteFile(fakePython, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write fake python: %v", err)
	}

	err := verifyExecutableIdentity(fakePython)
	if err == nil {
		t.Errorf("expected identity error for fake python disguised as node, got nil")
	}

	// 2. Fake Node script that outputs Python version
	fakeNode := filepath.Join(tmpDir, "node")
	contentNode := "#!/bin/sh\necho 'Python 3.11.0'\n"
	if runtime.GOOS == "windows" {
		fakeNode = filepath.Join(tmpDir, "node.cmd")
		contentNode = "@echo off\r\necho Python 3.11.0\r\n"
	}

	if err := os.WriteFile(fakeNode, []byte(contentNode), 0o755); err != nil {
		t.Fatalf("failed to write fake node: %v", err)
	}

	err = verifyExecutableIdentity(fakeNode)
	if err == nil {
		t.Errorf("expected identity error for fake node disguised as python, got nil")
	}
}
