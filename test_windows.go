package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"os"
)

func main() {
	tmpDir := os.TempDir()
	exePath := filepath.Join(tmpDir, "verify-tool.cmd")

	content := `@echo off
echo verify-tool version 1.0
`

	err := os.WriteFile(exePath, []byte(content), 0o755)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer os.Remove(exePath)

	cmd := exec.Command("cmd", "/c", exePath)
	out, err := cmd.CombinedOutput()
	fmt.Println("Error:", err)
	fmt.Println("Out:", string(out))
}
