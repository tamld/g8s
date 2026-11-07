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
// The optional claim carries literal paths, intersected with receipt globs.
// Ledger-owning callers must also check actor/revision before this backstop.
func VerifyWorktreeDiffScope(worktreeDir, permission string, allowedPaths []string, claims ...*receipt.ClaimBinding) ([]string, error) {
	var claim *receipt.ClaimBinding
	if len(claims) > 0 {
		claim = claims[0]
	}
	if claim != nil {
		if err := claim.Validate(time.Now()); err != nil {
			return nil, err
		}
		if len(allowedPaths) == 0 {
			return nil, receipt.ErrClaimFenceOutOfScope
		}
	}
	dir := strings.TrimSpace(worktreeDir)
	if dir == "" {
		if claim != nil {
			return nil, fmt.Errorf("%w: empty worktree", receipt.ErrClaimFenceStale)
		}
		if isDiffFirewallReadPermission(permission) {
			return nil, nil
		}
		return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: no worktree dir provided; cannot attest diff scope")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if claim != nil {
			return nil, fmt.Errorf("%w: worktree unavailable: %v", receipt.ErrClaimFenceStale, err)
		}
		if isDiffFirewallReadPermission(permission) {
			return nil, nil
		}
		return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: worktree dir missing or not a git worktree (%s): %w", dir, err)
	}

	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all")
	if claim != nil {
		cmd.Args = append(cmd.Args, "-z")
	}
	out, err := cmd.Output()
	if err != nil {
		if claim != nil {
			return nil, fmt.Errorf("%w: git status: %v", receipt.ErrClaimFenceStale, err)
		}
		return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: git status failed: %w", err)
	}

	var touched []string
	seen := make(map[string]bool)
	if claim != nil {
		// NUL records preserve quoted/newline paths and both rename sides.
		// Tool-owned directories receive no exemption from claim scope.
		entries := strings.Split(string(out), "\x00")
		for i := 0; i < len(entries); i++ {
			entry := entries[i]
			if entry == "" {
				continue
			}
			if len(entry) < 4 {
				return nil, receipt.ErrClaimFenceStale
			}
			touched = append(touched, filepath.ToSlash(entry[3:]))
			if strings.ContainsAny(entry[:2], "RC") {
				i++
				if i >= len(entries) || entries[i] == "" {
					return nil, receipt.ErrClaimFenceStale
				}
				touched = append(touched, filepath.ToSlash(entries[i]))
			}
		}
	} else {
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
	}
	if claim != nil {
		var violations []string
		for _, p := range touched {
			if !claim.Contains(p) {
				violations = append(violations, p)
			}
		}
		if len(violations) > 0 {
			return violations, fmt.Errorf("%w: %s", receipt.ErrClaimFenceOutOfScope, strings.Join(violations, ", "))
		}
	}

	if len(touched) == 0 {
		return nil, nil
	}

	if isDiffFirewallReadPermission(permission) {
		return touched, fmt.Errorf("E_DIFF_FIREWALL_READ_ONLY_MUTATION: %d files modified under %s permission: %s",
			len(touched), permission, strings.Join(touched, ", "))
	}

	if len(allowedPaths) == 0 {
		return touched, fmt.Errorf("E_DIFF_FIREWALL_UNSCOPED: %d files modified but no allowed-paths scope available", len(touched))
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

func isDiffFirewallReadPermission(permission string) bool {
	switch strings.ToLower(strings.TrimSpace(permission)) {
	case "read_only", "automation_read", "supplied_text":
		return true
	default:
		return false
	}
}

func resolveReceiptAllowedPaths(receiptID string) ([]string, error) {
	if strings.TrimSpace(receiptID) == "" {
		return nil, fmt.Errorf("E_DIFF_FIREWALL_RECEIPT_LOOKUP: no receipt ID provided")
	}
	dbPath := filepath.Join(pathutil.DefaultStateDir(), "receipts.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("E_DIFF_FIREWALL_RECEIPT_LOOKUP: stat receipts database: %w", err)
	}
	mgr, err := receipt.NewReceiptManager(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("E_DIFF_FIREWALL_RECEIPT_LOOKUP: open receipts database: %w", err)
	}
	defer mgr.Close()
	rc, verifyErr := mgr.VerifyReceipt(receiptID)
	if verifyErr == nil && rc != nil {
		return rc.AllowedPaths, nil
	}
	// Consumed receipts still attest the scope of the completed attempt.
	list, listErr := mgr.ListReceipts("", time.Time{}, "")
	if listErr == nil {
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].ReceiptID == receiptID {
				return list[i].AllowedPaths, nil
			}
		}
	}
	return nil, fmt.Errorf("E_DIFF_FIREWALL_RECEIPT_LOOKUP: receipt %q unavailable (verify: %v; list: %v)", receiptID, verifyErr, listErr)
}
