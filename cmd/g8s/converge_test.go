package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvergeCLI(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	sol1Path := filepath.Join(tempDir, "sol-1.md")
	sol2Path := filepath.Join(tempDir, "sol-2.md")
	sol3Path := filepath.Join(tempDir, "sol-3.md")
	outPath := filepath.Join(tempDir, "converged-cli.md")

	_ = os.WriteFile(sol1Path, []byte("# Design Alpha\n\n## Architecture\n- Pure-Go zero-CGO\n- POSIX file containment\n\n## Decisions\n- Use modernc sqlite\n"), 0o644)
	_ = os.WriteFile(sol2Path, []byte("# Design Beta\n\n## Architecture\n- Pure-Go zero-CGO\n- Process isolation Setpgid\n\n## Decisions\n- Use modernc sqlite\n"), 0o644)
	_ = os.WriteFile(sol3Path, []byte("# Design Gamma\n\n## Architecture\n- Pure-Go zero-CGO\n- Write receipt gating\n\n## Decisions\n- Use modernc sqlite\n"), 0o644)

	// 1. Test plaintext CLI converge
	cmd := exec.Command(binPath, "converge", sol1Path, sol2Path, sol3Path, "--out", outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("converge CLI failed: %v\nOutput: %s", err, string(out))
	}

	if !strings.Contains(string(out), "Synthesized 3 solutions into") {
		t.Errorf("unexpected output: %s", string(out))
	}

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("converged file %s was not written", outPath)
	}

	content, _ := os.ReadFile(outPath)
	if !strings.Contains(string(content), "Pure-Go zero-CGO") {
		t.Errorf("converged output missing consensus item: %s", string(content))
	}

	// 2. Test JSON envelope output
	jsonCmd := exec.Command(binPath, "converge", sol1Path, sol2Path, sol3Path, "--out", outPath, "--json")
	jsonOut, err := jsonCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("converge --json failed: %v\nOutput: %s", err, string(jsonOut))
	}

	var env testEnvelope
	if err := json.Unmarshal(jsonOut, &env); err != nil {
		t.Fatalf("failed to parse JSON envelope: %v\nRaw: %s", err, string(jsonOut))
	}
	if env.Command != "converge" {
		t.Errorf("expected cmd='converge', got %q", env.Command)
	}
}

func TestOrchestrateBlindConvergeCLI(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "blind-test.db")
	briefPath := filepath.Join(tempDir, "sample-brief.md")

	_ = os.WriteFile(briefPath, []byte("# Brief: Dual Blind Architecture\n\nDesign a pure Go convergence harness.\n\n## DoD\n- [ ] 3 solutions synthesized\n"), 0o644)

	cmd := exec.Command(binPath, "orchestrate", "--blind-converge", "2", "--brief-file", briefPath, "--timeout", "2s", "--json")
	cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrate --blind-converge failed: %v\nOutput: %s", err, string(out))
	}

	var env testEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("failed to parse JSON envelope: %v\nRaw: %s", err, string(out))
	}
	if env.Command != "orchestrate" || env.Subcommand != "blind-converge" {
		t.Errorf("expected cmd='orchestrate' sub='blind-converge', got %q/%q", env.Command, env.Subcommand)
	}
}
