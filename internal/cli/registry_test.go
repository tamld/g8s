package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalCommandsAndAliases(t *testing.T) {
	contracts := CanonicalCommandContracts()
	cmds := CanonicalCommands()
	if len(cmds) != len(contracts) || len(cmds) < 60 {
		t.Fatalf("expected >=60 canonical commands matching contracts, got cmds=%d contracts=%d", len(cmds), len(contracts))
	}
	for _, c := range contracts {
		if c.Name == "" || c.Stratum == "" || c.Description == "" {
			t.Errorf("incomplete CommandContract: %+v", c)
		}
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
	// Also walk spec/domains and docs/user-guide (#677 R3).
	for _, dir := range []string{"spec/domains", "docs/user-guide"} {
		_ = filepath.WalkDir(filepath.Join(repoRoot, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			if rel, rErr := filepath.Rel(repoRoot, p); rErr == nil {
				targets = append(targets, filepath.ToSlash(rel))
			}
			return nil
		})
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
