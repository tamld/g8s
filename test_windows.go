package main

import (
	"fmt"
	"path/filepath"
	"runtime"
)

func main() {
	files := []string{
		"internal/a/a.go",
		"internal/a/b.go",
		"internal/b/c.go",
		"cmd/main.go",
	}

	for _, f := range files {
		dir := filepath.Dir(f)
		fmt.Printf("File: %s -> Dir: %s\n", f, dir)
	}
	fmt.Println("OS:", runtime.GOOS)
}
