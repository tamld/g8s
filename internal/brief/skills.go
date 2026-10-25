package brief

// #398 + DELTA-34: L4 advisory skill routing + digest-pinned skill resource
// hydration (skill://g8s/<tier>/<name>) across F0/F1/F2 containment tiers.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillManifest is the routing and digest-pinned metadata surface of one installed skill.
type SkillManifest struct {
	Name        string `json:"name"`
	URI         string `json:"uri,omitempty"`
	Description string `json:"description,omitempty"`
	Digest      string `json:"digest,omitempty"`
	Tier        string `json:"tier,omitempty"`
	Score       int    `json:"score,omitempty"`
}

// SkillResource represents a verified, digest-pinned SKILL.md payload (DELTA-34).
type SkillResource struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Tier        string `json:"tier"`
	Description string `json:"description,omitempty"`
	Digest      string `json:"digest"`
	Content     string `json:"content"`
}

func skillTierAndURI(name string) (string, string) {
	if name == "g8s-supervisor" {
		return "F0", "skill://g8s/f0/" + name
	}
	return "shared", "skill://g8s/shared/" + name
}

func computeSkillDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DefaultSkillDirs returns the ordered candidate skill directories for the current process.
func DefaultSkillDirs(explicitBank string) []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		dirs = append(dirs, p)
	}
	add(explicitBank)
	add(os.Getenv("G8S_SKILL_BANK"))
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		add(filepath.Join(cwd, "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".agents", "skills"))
		add(filepath.Join(home, ".gemini", "skills"))
	}
	return dirs
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
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		tier, uri := skillTierAndURI(e.Name())
		m := SkillManifest{
			Name: e.Name(),
			URI:  uri,
			Tier: tier,
		}
		raw, err := os.ReadFile(filepath.Join(bankDir, e.Name(), "SKILL.md"))
		if err == nil {
			m.Description = extractDescription(string(raw))
			m.Digest = computeSkillDigest(raw)
		}
		out = append(out, m)
	}
	return out
}

// ListVerifiedSkills scans one or more skill directories, deduplicating by name
// and filtering out F0-exclusive skills when called from an F1 or F2 worker tier.
func ListVerifiedSkills(bankDirs ...string) []SkillManifest {
	if len(bankDirs) == 0 {
		bankDirs = DefaultSkillDirs("")
	}
	callerTier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER")))
	seen := make(map[string]bool)
	var out []SkillManifest
	for _, dir := range bankDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, m := range ScanSkillBank(dir) {
			if seen[m.Name] || m.Digest == "" {
				continue
			}
			if (callerTier == "F1" || callerTier == "F2") && m.Tier == "F0" {
				continue
			}
			seen[m.Name] = true
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ReadVerifiedSkill loads a skill by name (or skill://g8s/<tier>/<name> URI),
// enforces F0/F1/F2 containment boundaries, and verifies expectedDigest (if non-empty)
// against the SHA-256 digest of SKILL.md to prevent TOCTOU tampering (DELTA-34).
func ReadVerifiedSkill(nameOrURI, expectedDigest string, bankDirs ...string) (*SkillResource, error) {
	clean := strings.TrimSpace(nameOrURI)
	if strings.HasPrefix(clean, "skill://g8s/") {
		parts := strings.Split(strings.TrimPrefix(clean, "skill://g8s/"), "/")
		clean = parts[len(parts)-1]
	}
	if idx := strings.LastIndex(clean, ":"); idx >= 0 {
		// Support aliases like "ck:cook" -> "cook"
		clean = clean[idx+1:]
	}
	if clean == "" || strings.Contains(clean, "..") || strings.ContainsAny(clean, `/\`) {
		return nil, fmt.Errorf("E_SKILL_INVALID_NAME: invalid skill identifier %q", nameOrURI)
	}

	tier, uri := skillTierAndURI(clean)
	callerTier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER")))
	if (callerTier == "F1" || callerTier == "F2") && tier == "F0" {
		return nil, fmt.Errorf("E_SKILL_TIER_FORBIDDEN: skill %q is reserved for tier F0 (caller tier: %s)", clean, callerTier)
	}

	if len(bankDirs) == 0 {
		bankDirs = DefaultSkillDirs("")
	}
	for _, dir := range bankDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		p := filepath.Join(dir, clean, "SKILL.md")
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		actualDigest := computeSkillDigest(raw)
		if strings.TrimSpace(expectedDigest) != "" && !strings.EqualFold(strings.TrimSpace(expectedDigest), actualDigest) {
			return nil, fmt.Errorf("E_SKILL_DIGEST_MISMATCH: skill %q digest mismatch: expected %s, got %s", clean, expectedDigest, actualDigest)
		}
		return &SkillResource{
			Name:        clean,
			URI:         uri,
			Tier:        tier,
			Description: extractDescription(string(raw)),
			Digest:      actualDigest,
			Content:     string(raw),
		}, nil
	}
	return nil, fmt.Errorf("E_SKILL_NOT_FOUND: skill %q not found in configured skill banks", clean)
}

// HydrateSkillsBlock loads and formats a bounded prompt injection block for the
// specified skills, returning the formatted text and the list of verified SkillManifests.
func HydrateSkillsBlock(skillNames []string, maxBytesPerSkill int, bankDirs ...string) (string, []SkillManifest, error) {
	if maxBytesPerSkill <= 0 {
		maxBytesPerSkill = 4096
	}
	var sb strings.Builder
	var manifests []SkillManifest
	seen := make(map[string]bool)
	for _, rawName := range skillNames {
		name := strings.TrimSpace(rawName)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		res, err := ReadVerifiedSkill(name, "", bankDirs...)
		if err != nil {
			return "", nil, err
		}
		body := res.Content
		if len(body) > maxBytesPerSkill {
			body = body[:maxBytesPerSkill] + "\n...[skill truncated]"
		}
		if sb.Len() == 0 {
			sb.WriteString("--- HYDRATED SKILL CONTRACTS (DELTA-34) ---\n")
		}
		_, _ = fmt.Fprintf(&sb, "### Skill: %s (%s, digest: %s)\n%s\n\n", res.Name, res.URI, res.Digest, body)
		manifests = append(manifests, SkillManifest{
			Name:        res.Name,
			URI:         res.URI,
			Tier:        res.Tier,
			Description: res.Description,
			Digest:      res.Digest,
		})
	}
	return sb.String(), manifests, nil
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
		isWordByte := 'a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '-' || r == '_'
		return !isWordByte
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
