package reflection

import "embed"

// PromptFiles exposes the versioned prompt bytes used for manifest audits.
//
//go:embed prompts/*.md
var PromptFiles embed.FS

// PromptManifest returns a fresh copy of the reviewed SHA-256 manifest. Hashes
// are pinned, not generated at runtime: prompt edits require manifest review.
func PromptManifest() map[string]string {
	return map[string]string{
		"prompts/judge-v1.md": "e266405bec8098170ac0c3b178b8a45429773d1b0ee91fe67718d3b2c5093d54",
	}
}
