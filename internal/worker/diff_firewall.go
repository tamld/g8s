package worker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/search"
)

// DefaultCLIProviderTemplates returns built-in non-interactive CLI invocation templates
// for external coding agents (DELTA-39: opencode, commandcode, qwen, codex).
func DefaultCLIProviderTemplates() map[string][]string {
	return map[string][]string{
		"opencode":    {"opencode", "run", "--model", "{model}", "{prompt}"},
		"commandcode": {"commandcode", "--no-session", "--skip-onboarding", "--no-auto-update", "--yolo", "--model", "{model}", "-p", "{prompt}"},
		"qwen":        {"qwen", "--yolo", "--model", "{model}", "-p", "{prompt}"},
		"codex":       {"codex", "exec", "--model", "{model}", "{prompt}"},
		"cursor":      {"cursor-agent", "--print", "--trust", "--yolo", "--model", "{model}", "{prompt}"},
	}
}

func resolveDefaultCLITemplate(provider, prompt, model, timeout string) ([]string, bool) {
	tmpls := DefaultCLIProviderTemplates()
	tmpl, ok := tmpls[strings.ToLower(strings.TrimSpace(provider))]
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(model) == "" {
		filtered := make([]string, 0, len(tmpl))
		for i := 0; i < len(tmpl); i++ {
			if tmpl[i] == "--model" && i+1 < len(tmpl) && tmpl[i+1] == "{model}" {
				i++
				continue
			}
			filtered = append(filtered, tmpl[i])
		}
		tmpl = filtered
	}
	return SubstituteTemplate(tmpl, prompt, model, timeout), true
}

// VerifyWorktreeDiffScope inspects git status --porcelain inside worktreeDir after a worker attempt
// and enforces that any modified or untracked files comply with the task permission and WriteReceipt
// allowed_paths (DELTA-39 Post-Run Worktree Diff Firewall).
func VerifyWorktreeDiffScope(worktreeDir, permission string, allowedPaths []string) ([]string, error) {
	dir := strings.TrimSpace(worktreeDir)
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, nil
	}

	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	var touched []string
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(string(out), "\n") {
		if len(rawLine) < 4 {
			continue
		}
		entry := strings.TrimSpace(rawLine[3:])
		if idx := strings.Index(entry, " -> "); idx >= 0 {
			entry = strings.TrimSpace(entry[idx+4:])
		}
		entry = strings.Trim(entry, `"`)
		relSlash := filepath.ToSlash(filepath.Clean(entry))
		if relSlash == "." || relSlash == "" ||
			strings.HasPrefix(relSlash, ".g8s") ||
			strings.HasPrefix(relSlash, ".opencode") ||
			strings.HasPrefix(relSlash, ".omo") ||
			strings.HasPrefix(relSlash, ".commandcode") ||
			strings.HasPrefix(relSlash, ".qwen") ||
			strings.HasPrefix(relSlash, ".cursor") {
			continue
		}
		if !seen[relSlash] {
			seen[relSlash] = true
			touched = append(touched, relSlash)
		}
	}

	if len(touched) == 0 {
		return nil, nil
	}

	perm := strings.ToLower(strings.TrimSpace(permission))
	if perm == "read_only" || perm == "automation_read" || perm == "supplied_text" {
		return touched, fmt.Errorf("E_DIFF_FIREWALL_READ_ONLY_MUTATION: %d files modified under %s permission: %s",
			len(touched), permission, strings.Join(touched, ", "))
	}

	if len(allowedPaths) == 0 {
		return nil, nil
	}

	var violations []string
	for _, relSlash := range touched {
		if !search.MatchesAllowedPaths(relSlash, allowedPaths) {
			violations = append(violations, relSlash)
		}
	}
	if len(violations) > 0 {
		return violations, fmt.Errorf("E_DIFF_FIREWALL_OUT_OF_SCOPE: %d files modified outside write receipt scope (%v): %s",
			len(violations), allowedPaths, strings.Join(violations, ", "))
	}
	return nil, nil
}

func resolveReceiptAllowedPaths(receiptID string) []string {
	if strings.TrimSpace(receiptID) == "" {
		return nil
	}
	dbPath := filepath.Join(pathutil.DefaultStateDir(), "receipts.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return nil
	}
	defer mgr.Close()
	if rc, err := mgr.VerifyReceipt(receiptID); err == nil && rc != nil {
		return rc.AllowedPaths
	}
	if list, err := mgr.ListReceipts("", time.Time{}, ""); err == nil {
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].ReceiptID == receiptID {
				return list[i].AllowedPaths
			}
		}
	}
	return nil
}
