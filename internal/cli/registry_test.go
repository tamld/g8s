package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalCommandsAndAliases(t *testing.T) {
	cmds := CanonicalCommands()
	if len(cmds) < 55 {
		t.Fatalf("expected at least 55 canonical commands, got %d", len(cmds))
	}
	required := []string{"submit", "worker", "verify", "deliver", "reflex", "f0", "scorecard", "receipt-export", "charter", "fleet"}
	for _, req := range required {
		if !IsRegisteredCommand(req) {
			t.Errorf("expected %q to be registered in CanonicalCommands()", req)
		}
	}
	aliases := CanonicalFlagAliases()
	for _, family := range []string{"task", "paths", "receipt", "glob"} {
		if len(aliases[family]) < 2 {
			t.Errorf("expected canonical flag family %q to have at least 2 aliases, got %v", family, aliases[family])
		}
	}
}

func TestScanMarkdownCLIInvocationsDetectsUnbound(t *testing.T) {
	md := `# Example
Run ` + "`./bin/g8s f0 gate check --json`" + ` and ` + "`./bin/g8s scorecard status --json`" + `.
Bad invocation: ` + "`./bin/g8s nonexistent-cmd --json`" + `
Also bad: ` + "`g8s phantom-subcmd --flag`" + `
Allowed post-MVP: ` + "`g8s run --help`" + `
`
	claims := ScanMarkdownCLIInvocations("EXAMPLE.md", md)
	if len(claims) != 2 {
		t.Fatalf("expected 2 unbound claims, got %d: %+v", len(claims), claims)
	}
	if claims[0].Subcommand != "nonexistent-cmd" || claims[0].Code != CodeUnboundCLIClaim {
		t.Errorf("unexpected first claim: %+v", claims[0])
	}
	if claims[1].Subcommand != "phantom-subcmd" {
		t.Errorf("unexpected second claim: %+v", claims[1])
	}
}

func TestScanRepoCoreMarkdownFilesBound(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	targets := []string{
		"AGENTS.md",
		"README.md",
		"spec/constitution.md",
		"skills/g8s-supervisor/SKILL.md",
	}
	for _, rel := range targets {
		p := filepath.Join(repoRoot, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		claims := ScanMarkdownCLIInvocations(rel, string(data))
		if len(claims) > 0 {
			t.Errorf("%s has unbound CLI claims: %+v", rel, claims)
		}
	}
}
