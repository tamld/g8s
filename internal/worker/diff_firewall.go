package worker

import (
	"crypto/sha256"
	"fmt"
	"io"
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
	return DefaultCLIProviderTemplatesForPermission("workspace_write")
}

// DefaultCLIProviderTemplatesForPermission returns built-in non-interactive CLI
// invocation templates tailored to permission (suppressing --yolo on read_only
// permissions per DELTA-49).
func DefaultCLIProviderTemplatesForPermission(permission string) map[string][]string {
	if isDiffFirewallReadPermission(permission) {
		return map[string][]string{
			"opencode":    {"opencode", "run", "--model", "{model}", "{prompt}"},
			"commandcode": {"commandcode", "--no-session", "--skip-onboarding", "--no-auto-update", "--model", "{model}", "-p", "{prompt}"},
			"qwen":        {"qwen", "--approval-mode", "plan", "--model", "{model}", "-p", "{prompt}"},
			"codex":       {"codex", "exec", "--sandbox", "read-only", "--model", "{model}", "{prompt}"},
			"cursor":      {"cursor-agent", "--print", "--trust", "--mode", "plan", "--model", "{model}", "{prompt}"},
		}
	}
	return map[string][]string{
		"opencode":    {"opencode", "run", "--model", "{model}", "{prompt}"},
		"commandcode": {"commandcode", "--no-session", "--skip-onboarding", "--no-auto-update", "--yolo", "--model", "{model}", "-p", "{prompt}"},
		"qwen":        {"qwen", "--yolo", "--model", "{model}", "-p", "{prompt}"},
		"codex":       {"codex", "exec", "--model", "{model}", "{prompt}"},
		"cursor":      {"cursor-agent", "--print", "--trust", "--yolo", "--model", "{model}", "{prompt}"},
	}
}

func argvHasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

func sanitizeReadOnlyCLIArgv(argv []string, providerName, permission string) []string {
	if len(argv) == 0 || !isDiffFirewallReadPermission(permission) {
		return argv
	}
	out := make([]string, 0, len(argv)+2)
	for _, arg := range argv {
		if arg == "--yolo" || arg == "--dangerously-skip-permissions" {
			continue
		}
		out = append(out, arg)
	}
	if len(out) == 0 {
		return out
	}
	prov := strings.ToLower(strings.TrimSpace(providerName))
	switch prov {
	case "qwen":
		if !argvHasFlag(out, "--approval-mode") {
			withMode := make([]string, 0, len(out)+2)
			withMode = append(withMode, out[0], "--approval-mode", "plan")
			withMode = append(withMode, out[1:]...)
			out = withMode
		}
	case "cursor":
		if !argvHasFlag(out, "--mode") {
			withMode := make([]string, 0, len(out)+2)
			withMode = append(withMode, out[0], "--mode", "plan")
			withMode = append(withMode, out[1:]...)
			out = withMode
		}
	case "codex":
		if !argvHasFlag(out, "--sandbox") {
			insertAt := 1
			if len(out) > 1 && out[1] == "exec" {
				insertAt = 2
			}
			withSandbox := make([]string, 0, len(out)+2)
			withSandbox = append(withSandbox, out[:insertAt]...)
			withSandbox = append(withSandbox, "--sandbox", "read-only")
			withSandbox = append(withSandbox, out[insertAt:]...)
			out = withSandbox
		}
	}
	return out
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

const maxIgnoredHashBytes = 1 << 20 // 1 MiB threshold for exact pre/post hash comparison

// IgnoredFileStamp records file metadata for pre-run gitignored paths so
// post-run checks only flag newly created or modified ignored files (F13).
type IgnoredFileStamp struct {
	Size        int64
	ModTimeNano int64
	HasHash     bool
	SHA256      [32]byte
}

// DiffFirewallOptions configures post-run worktree diff scope verification.
type DiffFirewallOptions struct {
	Claim           *receipt.ClaimBinding
	BaselineIgnored map[string]IgnoredFileStamp
	CheckIgnored    bool
}

var exemptCLIRuntimeDirs = []string{
	".opencode",
	".omo",
	".commandcode",
	".qwen",
	".cursor",
}

func isExemptEphemeralRuntimePath(relSlash, permission string) bool {
	matchesDir := func(dir string) bool {
		return relSlash == dir || strings.HasPrefix(relSlash, dir+"/")
	}
	for _, dir := range exemptCLIRuntimeDirs {
		if matchesDir(dir) {
			return true
		}
	}
	if !isDiffFirewallReadPermission(permission) && matchesDir(gitCommitRequestsDir) {
		return true
	}
	return false
}

func stampIgnoredFile(absPath string) (IgnoredFileStamp, bool) {
	fi, err := os.Lstat(absPath)
	if err != nil || fi.IsDir() {
		return IgnoredFileStamp{}, false
	}
	st := IgnoredFileStamp{
		Size:        fi.Size(),
		ModTimeNano: fi.ModTime().UnixNano(),
	}
	if fi.Mode().IsRegular() && fi.Size() <= maxIgnoredHashBytes {
		f, oerr := os.Open(absPath)
		if oerr == nil {
			defer f.Close()
			h := sha256.New()
			if _, cerr := io.Copy(h, f); cerr == nil {
				copy(st.SHA256[:], h.Sum(nil))
				st.HasHash = true
			}
		}
	}
	return st, true
}

func ignoredFileUnchanged(worktreeDir, relSlash string, baseline map[string]IgnoredFileStamp) bool {
	prev, existed := baseline[relSlash]
	if !existed {
		return false
	}
	cur, ok := stampIgnoredFile(filepath.Join(worktreeDir, filepath.FromSlash(relSlash)))
	if !ok {
		return false
	}
	if prev.Size != cur.Size {
		return false
	}
	if prev.HasHash && cur.HasHash {
		return prev.SHA256 == cur.SHA256
	}
	return prev.ModTimeNano == cur.ModTimeNano
}

// SnapshotIgnoredFiles captures pre-existing gitignored files in worktreeDir so
// post-run diff verification only flags newly introduced or modified ignored paths.
func SnapshotIgnoredFiles(worktreeDir string) (map[string]IgnoredFileStamp, error) {
	dir := strings.TrimSpace(worktreeDir)
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, nil
	}
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=all", "--ignored=traditional", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	stamps := make(map[string]IgnoredFileStamp)
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		xy := entry[:2]
		if xy == "!!" {
			relSlash := filepath.ToSlash(entry[3:])
			if stamp, ok := stampIgnoredFile(filepath.Join(dir, filepath.FromSlash(relSlash))); ok {
				stamps[relSlash] = stamp
			}
		}
		if strings.ContainsAny(xy, "RC") && i+1 < len(entries) {
			i++
		}
	}
	return stamps, nil
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
	return VerifyWorktreeDiffScopeWithOptions(worktreeDir, permission, allowedPaths, DiffFirewallOptions{
		Claim:        claim,
		CheckIgnored: true,
	})
}

func parsePorcelainZRecords(out []byte, worktreeDir, permission string, opts DiffFirewallOptions) ([]string, error) {
	claim := opts.Claim
	entries := strings.Split(string(out), "\x00")
	var touched []string
	seen := make(map[string]bool)
	addTouched := func(p string) {
		if p == "." || p == "" || seen[p] {
			return
		}
		seen[p] = true
		touched = append(touched, p)
	}

	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 {
			if claim != nil {
				return nil, receipt.ErrClaimFenceStale
			}
			return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: malformed git status -z record %q", entry)
		}
		xy := entry[:2]
		relSlash := filepath.ToSlash(entry[3:])
		isUntrackedOrIgnored := xy == "??" || xy == "!!"

		var origSlash string
		if strings.ContainsAny(xy, "RC") {
			i++
			if i >= len(entries) || entries[i] == "" {
				if claim != nil {
					return nil, receipt.ErrClaimFenceStale
				}
				return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: missing rename/copy source in git status -z")
			}
			origSlash = filepath.ToSlash(entries[i])
		}

		if xy == "!!" && opts.BaselineIgnored != nil && ignoredFileUnchanged(worktreeDir, relSlash, opts.BaselineIgnored) {
			continue
		}

		// Tool-owned ephemeral directories get no exemption when ClaimBinding is
		// active, on tracked/renamed entries, or for .g8s governance paths (F01).
		if claim == nil && isUntrackedOrIgnored && isExemptEphemeralRuntimePath(relSlash, permission) {
			continue
		}

		addTouched(relSlash)
		if origSlash != "" {
			addTouched(origSlash)
		}
	}
	return touched, nil
}

// VerifyWorktreeDiffScopeWithOptions inspects worktreeDir with unified NUL-delimited
// git status parsing, fail-closed missing-.git handling, and optional .gitignore delta tracking.
func VerifyWorktreeDiffScopeWithOptions(worktreeDir, permission string, allowedPaths []string, opts DiffFirewallOptions) ([]string, error) {
	claim := opts.Claim
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
		return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: worktree dir missing or not a git worktree (%s): %w", dir, err)
	}

	args := []string{"-C", dir, "status", "--porcelain", "--untracked-files=all"}
	if opts.CheckIgnored {
		args = append(args, "--ignored=traditional")
	}
	args = append(args, "-z")
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		if claim != nil {
			return nil, fmt.Errorf("%w: git status: %v", receipt.ErrClaimFenceStale, err)
		}
		return nil, fmt.Errorf("E_DIFF_FIREWALL_UNVERIFIABLE: git status failed: %w", err)
	}

	touched, perr := parsePorcelainZRecords(out, dir, permission, opts)
	if perr != nil {
		return nil, perr
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
