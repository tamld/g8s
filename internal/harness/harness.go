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
		normalized := strings.ToLower(filepath.ToSlash(absDir))

		for _, fragment := range DeniedPathFragments {
			if strings.Contains(normalized, strings.ToLower(fragment)) {
				return fmt.Errorf("denied path fragment detected in add-dir: %s", rawDir)
			}
		}
	}

	return nil
}

// BuildContractPrompt constructs the enforced boundary prompt sent to the LLM worker.
func BuildContractPrompt(
	prompt string,
	roleName string,
	permissionName string,
	allowedPaths []string,
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
	if permission.MutationAllowed && len(allowedPaths) > 0 {
		var pathList []string
		for _, p := range allowedPaths {
			pathList = append(pathList, fmt.Sprintf("  - %s", p))
		}
		mutationLine = fmt.Sprintf("This task has DELEGATED WRITE permission via receipt.\nYou may ONLY write to files matching these path patterns:\n%s\nWriting outside this scope is a policy violation.", strings.Join(pathList, "\n"))
	} else if permission.MutationAllowed {
		mutationLine = "This task may mutate files only inside the explicit workspace scope."
	} else {
		mutationLine = "This task is read-only: do not edit, delete, move, install, commit, or write files."
	}

	forbiddenItems := make([]string, len(role.Forbidden))
	for i, f := range role.Forbidden {
		forbiddenItems[i] = fmt.Sprintf("- %s", f)
	}

	contract := fmt.Sprintf(`You are a subdispatch worker running behind the security harness.

Role: %s
Purpose: %s
Permission profile: %s — %s
Mutation policy: %s

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
%s`, role.Name, role.Purpose, permission.Name, permission.Description, mutationLine, strings.Join(forbiddenItems, "\n"), prompt)

	return contract, nil
}
