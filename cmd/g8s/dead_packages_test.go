package main

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestNoDeadInternalPackages(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	allCmd := exec.Command("go", "list", "./internal/...")
	allCmd.Dir = repoRoot
	allOut, err := allCmd.Output()
	if err != nil {
		t.Fatalf("go list ./internal/... failed: %v", err)
	}

	depsCmd := exec.Command("go", "list", "-deps", "./cmd/...", "./tools/...")
	depsCmd.Dir = repoRoot
	depsOut, err := depsCmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/... ./tools/... failed: %v", err)
	}

	reachable := make(map[string]bool)
	for _, line := range strings.Split(string(depsOut), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg != "" {
			reachable[pkg] = true
		}
	}

	exempt := map[string]bool{
		"github.com/tamld/g8s/internal/testenv": true,
	}

	var dead []string
	for _, line := range strings.Split(string(allOut), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" || exempt[pkg] {
			continue
		}
		if !reachable[pkg] {
			dead = append(dead, pkg)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("found %d unwired internal package(s) not transitively imported by ./cmd/... or ./tools/...: %v", len(dead), dead)
	}
}
