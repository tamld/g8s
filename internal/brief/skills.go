package brief

// #398: L4 advisory skill routing — deterministic keyword/manifest match
// over the operator-local skill bank. Display-only: nothing enforces the
// suggestion, consumption semantics are unchanged, and an unreadable bank
// degrades to no suggestions.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillManifest is the minimal routing surface of one installed skill.
type SkillManifest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Score       int    `json:"score,omitempty"`
}

// ScanSkillBank reads the operator skill bank directory (one subdirectory
// per skill, each optionally carrying a SKILL.md with a
// "name: <slug>" / "description: <text>" frontmatter). Missing directory
// yields no manifests and no error.
func ScanSkillBank(bankDir string) []SkillManifest {
	entries, err := os.ReadDir(bankDir)
	if err != nil {
		return nil
	}
	var out []SkillManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := SkillManifest{Name: e.Name()}
		raw, err := os.ReadFile(filepath.Join(bankDir, e.Name(), "SKILL.md"))
		if err == nil {
			m.Description = extractDescription(string(raw))
		}
		out = append(out, m)
	}
	return out
}

// extractDescription pulls the description line from a skill frontmatter
// block (--- delimited at the top).
func extractDescription(md string) string {
	lines := strings.SplitN(md, "\n", 40)
	inFrontmatter := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				break
			}
			if strings.HasPrefix(trimmed, "description:") {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			}
		}
	}
	return ""
}

// SuggestSkills ranks manifests by keyword overlap with the brief's
// title+payload text and returns the top n. Zero enforcement: the caller
// only renders the result.
func SuggestSkills(title, payload string, bank []SkillManifest, n int) []SkillManifest {
	text := strings.ToLower(title + " " + payload)
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-' || r == '_')
	}) {
		if len(w) >= 4 { // skip stopword-length noise
			words[w] = true
		}
	}
	scored := make([]SkillManifest, len(bank))
	for i, m := range bank {
		hay := strings.ToLower(m.Name + " " + m.Description)
		score := 0
		for w := range words {
			if strings.Contains(hay, w) {
				score++
			}
		}
		m.Score = score
		scored[i] = m
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	var out []SkillManifest
	for _, m := range scored {
		if len(out) >= n {
			break
		}
		if m.Score == 0 {
			break // no keyword overlap — better silent than noisy
		}
		out = append(out, m)
	}
	return out
}
