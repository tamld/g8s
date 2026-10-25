package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeSecretsPreservingLines(t *testing.T) {
	raw := strings.Join([]string{
		"package auth",
		`const openAIKey = "sk-proj-1234567890abcdefghijklmnop"`,
		`const ghToken = "ghp_1234567890abcdefghijklmnopqrstuv"`,
		"-----BEGIN RSA PRIVATE KEY-----",
		"MIIEowIBAAKCAQEA0123456789abcdef",
		"0123456789abcdef0123456789abcdef",
		"-----END RSA PRIVATE KEY-----",
		"func Verify() bool { return true }",
	}, "\n")

	sanitized, count := SanitizeSecretsPreservingLines(raw)
	if count < 3 {
		t.Fatalf("expected at least 3 redactions, got %d", count)
	}
	origLines := strings.Count(raw, "\n")
	newLines := strings.Count(sanitized, "\n")
	if origLines != newLines {
		t.Fatalf("expected exact line count preservation (%d), got %d", origLines, newLines)
	}
	if strings.Contains(sanitized, "sk-proj-") || strings.Contains(sanitized, "ghp_") || strings.Contains(sanitized, "MIIEowIBAAKCAQEA") {
		t.Fatalf("leaked secret in sanitized output:\n%s", sanitized)
	}
}

func TestElideFileGoAndLexicalTiers(t *testing.T) {
	goSrc := `package engine

import "fmt"

// ComputeScore calculates the rank score for a node.
func ComputeScore(items []int, weight float64) float64 {
	total := 0.0
	for _, v := range items {
		if v > 0 {
			total += float64(v) * weight
		}
	}
	fmt.Println("computed", total)
	return total
}
`
	skel := ElideFile("engine/score.go", goSrc, TierSkel)
	if !strings.Contains(skel, "func ComputeScore(items []int, weight float64) float64") {
		t.Fatalf("expected SKEL to preserve function signature, got:\n%s", skel)
	}
	if !strings.Contains(skel, "[SKEL: ComputeScore body lines") {
		t.Fatalf("expected SKEL elision marker, got:\n%s", skel)
	}
	if strings.Contains(skel, `fmt.Println("computed"`) {
		t.Fatalf("expected SKEL to elide interior body statements, got:\n%s", skel)
	}

	sigs := ElideFile("engine/score.go", goSrc, TierSigs)
	if !strings.Contains(sigs, "func ComputeScore(items []int, weight float64) float64") {
		t.Fatalf("expected SIGS to include function signature, got:\n%s", sigs)
	}

	tree := ElideFile("engine/score.go", goSrc, TierTree)
	if !strings.Contains(tree, "ComputeScore") {
		t.Fatalf("expected TREE to list symbol ComputeScore, got:\n%s", tree)
	}

	jsSrc := strings.Join([]string{
		"import { db } from './db';",
		"export function processQueue(items) {",
		"  const out = [];",
		"  for (const item of items) {",
		"    out.push(item * 2);",
		"  }",
		"  for (const x of out) {",
		"    console.log(x);",
		"  }",
		"  for (let i = 0; i < 15; i++) {",
		"    out.push(i);",
		"  }",
		"  for (let j = 0; j < 15; j++) {",
		"    out.push(j);",
		"  }",
		"  return out;",
		"}",
	}, "\n")
	jsSkel := ElideFile("queue.js", jsSrc, TierSkel)
	if !strings.Contains(jsSkel, "export function processQueue") {
		t.Fatalf("expected lexical SKEL to keep JS function header, got:\n%s", jsSkel)
	}
	jsSigs := ElideFile("queue.js", jsSrc, TierSigs)
	if !strings.Contains(jsSigs, "export function processQueue") {
		t.Fatalf("expected lexical SIGS to keep JS function signature, got:\n%s", jsSigs)
	}
}

func TestPackKnapsackRespectsBudgetAndPrioritizesPinned(t *testing.T) {
	tmp := t.TempDir()

	coreGo := `package core

// SolveKnapsack optimizes token budget allocation across AST tiers.
func SolveKnapsack(budget int, weights []int, values []float64) int {
	best := 0
	for i := range weights {
		if weights[i] <= budget && int(values[i]) > best {
			best = int(values[i])
		}
	}
	return best
}
`
	var satelliteBuilder strings.Builder
	satelliteBuilder.WriteString("package satellite\n\nimport \"example/core\"\n\n")
	for i := 0; i < 20; i++ {
		satelliteBuilder.WriteString("func HelperFunc" + string(rune('A'+i)) + "() int {\n")
		for j := 0; j < 12; j++ {
			satelliteBuilder.WriteString("\tx" + string(rune('a'+j)) + " := core.SolveKnapsack(100, nil, nil)\n")
			satelliteBuilder.WriteString("\t_ = x" + string(rune('a'+j)) + "\n")
		}
		satelliteBuilder.WriteString("\treturn 42\n}\n\n")
	}

	if err := os.MkdirAll(filepath.Join(tmp, "core"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "satellite"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "core", "knapsack.go"), []byte(coreGo), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "satellite", "helpers.go"), []byte(satelliteBuilder.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Pack(PackRequest{
		RootDir:      tmp,
		AllowedPaths: []string{"core/**"},
		FocusQuery:   "SolveKnapsack token budget",
		BudgetTokens: 350,
	})
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	if res.TotalTokens > res.BudgetTokens {
		t.Fatalf("expected TotalTokens (%d) <= BudgetTokens (%d)", res.TotalTokens, res.BudgetTokens)
	}
	if len(res.Files) != 2 {
		t.Fatalf("expected 2 packed files, got %d", len(res.Files))
	}

	var coreEntry, satEntry *FileEntry
	for i := range res.Files {
		if strings.HasPrefix(res.Files[i].Path, "core/") {
			coreEntry = &res.Files[i]
		} else {
			satEntry = &res.Files[i]
		}
	}
	if coreEntry == nil || satEntry == nil {
		t.Fatalf("expected both core and satellite entries in result")
	}
	if !coreEntry.Pinned {
		t.Fatalf("expected core/knapsack.go to be marked Pinned")
	}
	if coreEntry.SelectedTier != TierFull && coreEntry.SelectedTier != TierSkel {
		t.Fatalf("expected pinned focus file to get FULL or SKEL, got %s", coreEntry.SelectedTier)
	}
	if satEntry.SelectedTier == TierFull {
		t.Fatalf("expected large satellite file to be elided under tight 350-token budget, got FULL")
	}
}

func TestPackExplicitPathsAndEmptyCases(t *testing.T) {
	if s, c := SanitizeSecretsPreservingLines(""); s != "" || c != 0 {
		t.Fatalf("expected empty sanitize result")
	}
	if EstimateTokens("") != 0 {
		t.Fatalf("expected 0 tokens for empty string")
	}

	tmp := t.TempDir()
	emptyRes, err := Pack(PackRequest{RootDir: tmp, BudgetTokens: 0})
	if err != nil || len(emptyRes.Files) != 0 {
		t.Fatalf("expected empty result for empty dir, got err=%v files=%d", err, len(emptyRes.Files))
	}

	typesGo := `package models

const DefaultLimit = 50

type UserRecord struct {
	ID   string
	Name string
}
`
	p := filepath.Join(tmp, "models.go")
	if err := os.WriteFile(p, []byte(typesGo), 0o600); err != nil {
		t.Fatal(err)
	}
	sigs := ElideFile("models.go", typesGo, TierSigs)
	if !strings.Contains(sigs, "type UserRecord struct") || !strings.Contains(sigs, "const DefaultLimit") {
		t.Fatalf("expected SIGS to include type and const declarations, got:\n%s", sigs)
	}

	res, err := Pack(PackRequest{
		RootDir:      tmp,
		Paths:        []string{"models.go"},
		BudgetTokens: 2000,
	})
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("expected 1 file from explicit path, got err=%v len=%d", err, len(res.Files))
	}
	if res.Files[0].SelectedTier != TierFull {
		t.Fatalf("expected FULL tier under generous budget, got %s", res.Files[0].SelectedTier)
	}
}
