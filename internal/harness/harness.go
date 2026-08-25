package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var BlockedTaskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-rf\b`),
	regexp.MustCompile(`(?i)\bdel\s+/[sS]\b`),
	regexp.MustCompile(`(?i)\brmdir\s+/[sS]\b`),
	regexp.MustCompile(`(?i)\bfdisk\b`),
	regexp.MustCompile(`(?i)\bmkfs\b`),
	regexp.MustCompile(`(?i)\bdd\s+if=`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
	regexp.MustCompile(`(?i)\bdrop\s+table\b`),
	regexp.MustCompile(`(?i)\btruncate\s+table\b`),
	regexp.MustCompile(`(?i)\bshutdown\b`),
	regexp.MustCompile(`(?i)\breboot\b`),
	regexp.MustCompile(`(?i)\bhalt\b`),
	regexp.MustCompile(`(?i)\binit\s+0\b`),
	regexp.MustCompile(`(?i)\bcopy\s+private\s+key\b`),
	regexp.MustCompile(`(?i)\bexfiltrate\s+private\s+key\b`),
	regexp.MustCompile(`(?i)\bcat\s+\.env\b`),
	regexp.MustCompile(`(?i)\bopen\s+\.env\b`),
	regexp.MustCompile(`(?i)\bcopy\s+token\s+store\b`),
}

var DeniedPathFragments = []string{
	"/.env",
	"/.ssh",
	"/.gnupg",
	"/.aws",
	"/.config/gh",
	"/.npmrc",
	"/.pypirc",
	"master.key",
	"id_rsa",
	"id_ed25519",
}

// ValidateRequest checks if a task request passes all safety gates.
func ValidateRequest(
	prompt string,
	roleName string,
	permissionName string,
	addDirs []string,
	skipPermissions bool,
	receiptID string,
) error {
	_, err := GetRole(roleName)
	if err != nil {
		return err
	}

	permission, err := GetPermission(permissionName)
	if err != nil {
		return err
	}

	if len(prompt) > permission.MaxPromptChars {
		return fmt.Errorf("prompt too long for permission=%s: %d > %d", permission.Name, len(prompt), permission.MaxPromptChars)
	}

	if skipPermissions && !permission.SkipPermissionsAllowed {
		return fmt.Errorf("--skip-permissions is not allowed for permission=%s", permission.Name)
	}

	if permission.MutationAllowed && receiptID == "" {
		return fmt.Errorf("permission=%s requires --receipt-id from Brain orchestrator", permission.Name)
	}

	// Check blocked patterns in prompt
	for _, pattern := range BlockedTaskPatterns {
		if pattern.MatchString(prompt) {
			return fmt.Errorf("blocked task pattern detected: %s", pattern.String())
		}
	}

	// Check denied directory paths
	homeDir, _ := os.UserHomeDir()
	for _, rawDir := range addDirs {
		cleanDir := filepath.Clean(rawDir)
		if strings.HasPrefix(cleanDir, "~") && homeDir != "" {
			cleanDir = filepath.Join(homeDir, cleanDir[1:])
		}
		absDir, err := filepath.Abs(cleanDir)
		if err != nil {
			absDir = cleanDir
		}
		if realDir, err := filepath.EvalSymlinks(absDir); err == nil {
			absDir = realDir
		}
		normalized := strings.ToLower(filepath.ToSlash(absDir))

		for _, fragment := range DeniedPathFragments {
			if strings.Contains(normalized, strings.ToLower(fragment)) {
				return fmt.Errorf("denied path fragment detected in add-dir: %s", rawDir)
			}
		}
	}

	return nil
}

// ReceiptRef carries the minimal receipt identity injected into delegated-write
// prompts (spec 01: BuildContractPrompt injects exact allowed paths when a
// receipt is present).
type ReceiptRef struct {
	ReceiptID string
	Issuer    string
}

// BuildContractPrompt constructs the enforced boundary prompt sent to the LLM worker.
func BuildContractPrompt(
	prompt string,
	roleName string,
	permissionName string,
	allowedPaths []string,
) (string, error) {
	return BuildContractPromptWithReceipt(prompt, roleName, permissionName, allowedPaths, nil)
}

// BuildContractPromptWithReceipt builds the contract prompt with optional
// receipt identity. When the permission allows mutation and a receipt ref is
// supplied, the delegated-write block always renders (even with empty path
// patterns) and carries the receipt ID plus issuer so workers can trace their
// write authorization back to a single-use Brain grant.
func BuildContractPromptWithReceipt(
	prompt string,
	roleName string,
	permissionName string,
	allowedPaths []string,
	receipt *ReceiptRef,
) (string, error) {
	role, err := GetRole(roleName)
	if err != nil {
		return "", err
	}

	permission, err := GetPermission(permissionName)
	if err != nil {
		return "", err
	}

	var mutationLine string
	switch {
	case permission.MutationAllowed && receipt != nil:
		mutationLine = buildDelegatedWriteBlock(allowedPaths, receipt)
	case permission.MutationAllowed && len(allowedPaths) > 0:
		mutationLine = buildDelegatedWriteBlock(allowedPaths, nil)
	case permission.MutationAllowed:
		mutationLine = "This task may mutate files only inside the explicit workspace scope."
	default:
		mutationLine = "This task is read-only: do not edit, delete, move, install, commit, or write files."
	}

	wikiBlock := ""
	if !permission.MutationAllowed {
		wikiBlock = "\n\nWiki engine policy (MANDATORY):\n" +
			"- ALLOWED: wiki.py query, wiki.py search, wiki.py read, wiki.py classify\n" +
			"- FORBIDDEN: wiki.py write, wiki.py reflect, wiki.py orient, wiki.py claim, wiki.py bypass\n" +
			"  These commands mutate shared session state and are reserved for the Brain orchestrator."
	}

	forbiddenItems := make([]string, len(role.Forbidden))
	for i, f := range role.Forbidden {
		forbiddenItems[i] = fmt.Sprintf("- %s", f)
	}

	contract := fmt.Sprintf(`You are a subdispatch worker running behind the security harness.

Role: %s
Purpose: %s
Permission profile: %s — %s
Mutation policy: %s%s

Forbidden for this role:
%s

Output contract:
- Return compact JSON or Markdown with exact paths inspected.
- If required information is missing, return JSON with status NEEDS_INFO and required_inputs.
- If policy or environment prevents safe work, return JSON with status BLOCKED and a reason.
- Separate findings from uncertainty.
- Put skipped sensitive paths under sensitive_flags.
- Do not copy secrets, credentials, private keys, identity documents, or raw confidential payloads.
- Do not claim completion beyond the evidence you inspected.

Original task:
%s`, role.Name, role.Purpose, permission.Name, permission.Description, mutationLine, wikiBlock, strings.Join(forbiddenItems, "\n"), prompt)

	return contract, nil
}

// buildDelegatedWriteBlock renders the delegated-write section. With a receipt
// ref the block always renders its header lines even when no path patterns are
// attached; without one it keeps the legacy path-only rendering.
func buildDelegatedWriteBlock(allowedPaths []string, receipt *ReceiptRef) string {
	var sb strings.Builder

	if receipt != nil {
		// Calculate capacity
		cap := 150 + len(receipt.ReceiptID) + len(receipt.Issuer)
		for _, p := range allowedPaths {
			cap += len(p) + 5
		}
		sb.Grow(cap)

		sb.WriteString("This task has DELEGATED WRITE permission via receipt.\n")
		if len(allowedPaths) > 0 {
			sb.WriteString("You may ONLY write to files matching these path patterns:\n")
			for _, p := range allowedPaths {
				sb.WriteString("  - ")
				sb.WriteString(p)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("Writing to ANY path outside this scope is a policy violation.\n")
		sb.WriteString("Receipt ID: ")
		sb.WriteString(receipt.ReceiptID)
		sb.WriteString("\nIssuer: ")
		sb.WriteString(receipt.Issuer)
		return sb.String()
	}

	cap := 165
	for _, p := range allowedPaths {
		cap += len(p) + 5
	}
	sb.Grow(cap)

	sb.WriteString("This task has DELEGATED WRITE permission via receipt.\nYou may ONLY write to files matching these path patterns:\n")
	for _, p := range allowedPaths {
		sb.WriteString("  - ")
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	if len(allowedPaths) == 0 {
		sb.WriteString("\n")
	}
	sb.WriteString("Writing outside this scope is a policy violation.")
	return sb.String()
}
