package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfig_LoadDefaults(t *testing.T) {
	dir := t.TempDir()

	// 1. Inferred verify_commands for go.mod
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0644)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	expectedVerify := [][]string{{"go", "test", "./..."}}
	if !reflect.DeepEqual(cfg.VerifyCommands, expectedVerify) {
		t.Errorf("expected %v, got %v", expectedVerify, cfg.VerifyCommands)
	}
	if cfg.CI != "none" {
		t.Errorf("expected CI=none without git remote, got %s", cfg.CI)
	}
	if len(cfg.ManualFree) != 0 {
		t.Errorf("expected empty manual_free, got %v", cfg.ManualFree)
	}

	// 2. Inferred verify_commands for Cargo.toml
	cargoDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(cargoDir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0644)
	cfgCargo, err := LoadConfig(cargoDir)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	expectedCargo := [][]string{{"cargo", "test"}}
	if !reflect.DeepEqual(cfgCargo.VerifyCommands, expectedCargo) {
		t.Errorf("expected %v, got %v", expectedCargo, cfgCargo.VerifyCommands)
	}

	// 3. Inferred verify_commands for package.json
	nodeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte("{\"name\": \"test\"}\n"), 0644)
	cfgNode, err := LoadConfig(nodeDir)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	expectedNode := [][]string{{"npm", "test"}, {"pnpm", "test"}, {"node", "--test"}}
	if !reflect.DeepEqual(cfgNode.VerifyCommands, expectedNode) {
		t.Errorf("expected %v, got %v", expectedNode, cfgNode.VerifyCommands)
	}
}

func TestConfig_LoadExplicitPolicy(t *testing.T) {
	dir := t.TempDir()
	g8sDir := filepath.Join(dir, ".g8s")
	_ = os.MkdirAll(g8sDir, 0755)

	content := `
ci: github
verify_commands:
  - ["make", "check"]
  - ["python3", "-m", "unittest"]
manual_free:
  - "plans/**"
  - "docs/notes.md"
`
	_ = os.WriteFile(filepath.Join(g8sDir, "f0.yml"), []byte(content), 0644)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	if cfg.CI != "github" {
		t.Errorf("expected CI=github, got %s", cfg.CI)
	}

	expectedCmds := [][]string{
		{"make", "check"},
		{"python3", "-m", "unittest"},
	}
	if !reflect.DeepEqual(cfg.VerifyCommands, expectedCmds) {
		t.Errorf("expected %v, got %v", expectedCmds, cfg.VerifyCommands)
	}

	expectedFree := []string{"plans/**", "docs/notes.md"}
	if !reflect.DeepEqual(cfg.ManualFree, expectedFree) {
		t.Errorf("expected %v, got %v", expectedFree, cfg.ManualFree)
	}
}

func TestConfig_DefaultCIGitHub(t *testing.T) {
	dir := t.TempDir()
	_ = exec.Command("git", "-C", dir, "init").Run()
	_ = exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/tamld/g8s.git").Run()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.CI != "github" {
		t.Errorf("expected CI=github with github remote, got %s", cfg.CI)
	}
}
