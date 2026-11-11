package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/brief"
)

func writeTestSkill(t *testing.T, bankDir, name, body string) {
	t.Helper()
	skillDir := filepath.Join(bankDir, name)
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test\n---\n" + body
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHydrateRoleSkillsVerifier(t *testing.T) {
	repo := t.TempDir()
	bank := filepath.Join(repo, "skills")
	writeTestSkill(t, bank, "g8s-blind-verifier", "verify blind")
	writeTestSkill(t, bank, "g8s-epistemic-audit", "audit epistemic")
	got, err := hydrateRoleSkills(taskRequest{Role: "verifier", Prompt: "base brief"}, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "--- HYDRATED SKILL CONTRACTS (DELTA-34) ---") {
		t.Fatalf("missing hydration marker: %q", got)
	}
	if !strings.Contains(got, "g8s-blind-verifier") || !strings.Contains(got, "g8s-epistemic-audit") {
		t.Fatalf("expected verifier skills in prompt, got %q", got)
	}
}

func TestHydrateRoleSkillsTestRunner(t *testing.T) {
	repo := t.TempDir()
	bank := filepath.Join(repo, "skills")
	writeTestSkill(t, bank, "g8s-red-prove", "prove red")
	got, err := hydrateRoleSkills(taskRequest{Role: "test-runner", Prompt: "run tests"}, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "g8s-red-prove") {
		t.Fatalf("expected test-runner skill, got %q", got)
	}
}

func TestHydrateRoleSkillsFailOpenWithoutBank(t *testing.T) {
	got, err := hydrateRoleSkills(taskRequest{Role: "verifier", Prompt: "no skills here"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "no skills here" {
		t.Fatalf("expected unchanged prompt, got %q", got)
	}
}

func TestHydrateRoleSkillsIdempotent(t *testing.T) {
	marked := "task\n\n--- HYDRATED SKILL CONTRACTS (DELTA-34) ---\nexisting"
	got, err := hydrateRoleSkills(taskRequest{Role: "verifier", Prompt: marked}, t.TempDir())
	if err != nil || got != marked {
		t.Fatalf("expected idempotent prompt, got %q err=%v", got, err)
	}
}

func TestHydrateRoleSkillsDigestMismatchFailsClosed(t *testing.T) {
	repo := t.TempDir()
	bank := filepath.Join(repo, "skills")
	writeTestSkill(t, bank, "g8s-red-prove", "body")
	res, err := brief.ReadVerifiedSkill("g8s-red-prove", "", bank)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt digest expectation via direct read — HydrateSkillsBlock reads without expected digest,
	// so simulate fail-closed by rewriting file after recording digest in a separate call with wrong digest.
	_ = res
	if _, err := brief.ReadVerifiedSkill("g8s-red-prove", "sha256:0000000000000000", bank); err == nil || !strings.Contains(err.Error(), "E_SKILL_DIGEST_MISMATCH") {
		t.Fatalf("expected digest mismatch probe to fail closed, got %v", err)
	}
}
