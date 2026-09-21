package worker

import (
	"testing"
	"path/filepath"
	"runtime"
	"os"
)

func TestVerifyExecutableIdentityWorker(t *testing.T) {
	// Create a fake python3 that's actually a Node script
	tmpDir := t.TempDir()
	fakePython := filepath.Join(tmpDir, "python3")
	if runtime.GOOS == "windows" {
		fakePython += ".cmd"
	}

	content := []byte("#!/usr/bin/env node\nconsole.log('node v18.0.0');\n")
	if runtime.GOOS == "windows" {
		content = []byte("@echo off\necho node v18.0.0\n")
	}

	err := os.WriteFile(fakePython, content, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// This should detect the mismatch
	err = verifyExecutableIdentity(fakePython)
	if err == nil {
		t.Error("Expected error for fake python3 (Node.js)")
	} else {
		t.Logf("Correctly detected fake: %v", err)
	}

	// Just cover all branches for the test
	tests := []struct {
		name     string
		baseName string
		output   string
		wantErr  bool
	}{
		{"valid_python", "python3", "Python 3.10.0", false},
		{"fake_node", "node", "Python 3.10.0", true},
		{"valid_go", "go", "go version go1.20 linux/amd64", false},
		{"fake_go", "go", "ruby 3.0.0", true},
		{"valid_ruby", "ruby", "ruby 3.0.0", false},
		{"fake_ruby", "ruby", "Python 3.10.0", true},
		{"valid_java", "java", "openjdk 17.0.2", false},
		{"fake_java", "java", "Python 3.10.0", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.baseName)
			if runtime.GOOS == "windows" {
				path += ".cmd"
			}

			var scriptContent []byte
			if runtime.GOOS == "windows" {
				scriptContent = []byte("@echo off\necho " + tc.output + "\n")
			} else {
				scriptContent = []byte("#!/bin/sh\necho '" + tc.output + "'\n")
			}

			_ = os.WriteFile(path, scriptContent, 0755)
			err := verifyExecutableIdentity(path)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error for %s with output %s, got nil", tc.baseName, tc.output)
			} else if !tc.wantErr && err != nil {
				t.Errorf("Expected no error for %s with output %s, got %v", tc.baseName, tc.output, err)
			}
		})
	}
}
