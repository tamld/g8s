package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Uninstall item kinds, in apply order.
const (
	KindService  = "service-unit"
	KindMCPEntry = "mcp-entry"
	KindStateDir = "state-dir"
	KindBackup   = "binary-backup"
	KindBinary   = "binary"
)

// Uninstall actions reported per item.
const (
	ActionWouldRemove = "would_remove"
	ActionRemoved     = "removed"
	ActionSkipped     = "skipped"
	ActionFailed      = "failed"
)

// MCPConfig is one IDE MCP config file that `g8s init` may have written.
type MCPConfig struct {
	IDE  string
	Path string
	// Dedicated marks files owned entirely by g8s (e.g. Antigravity's
	// mcp/g8s.json); they are deleted when no other content remains.
	Dedicated bool
}

// ServiceHook detects and removes the background service unit installed by
// `g8s service install`. Production wires the platform service manager; tests
// inject fakes so no init system is touched.
type ServiceHook interface {
	// Detect reports whether a unit is installed and a human-readable locator
	// (unit file path or service name).
	Detect() (bool, string)
	// Uninstall stops and removes the unit.
	Uninstall() error
}

// UninstallOptions carries every root the planner inspects. All paths are
// explicit so tests drive the planner entirely inside t.TempDir().
type UninstallOptions struct {
	// BinaryPath is the installed g8s executable (resolved running exe).
	BinaryPath string
	GOOS       string
	// MCPConfigs are the IDE config files written by `g8s init`.
	MCPConfigs []MCPConfig
	// Service is optional; nil skips service detection.
	Service ServiceHook
	// StateDirs are g8s-owned state/data/config/cache/log roots; they are only
	// planned for removal when PurgeState is true.
	StateDirs  []string
	PurgeState bool
	// Home is the user's home directory; it and its ancestors are never removed.
	Home string
}

// UninstallItem is one planned or applied removal.
type UninstallItem struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

// UninstallReport is the JSON payload emitted by `g8s uninstall`.
type UninstallReport struct {
	DryRun     bool            `json:"dry_run"`
	PurgeState bool            `json:"purge_state"`
	Items      []UninstallItem `json:"items"`
	Kept       []UninstallItem `json:"kept,omitempty"`
}

// packageManagedPrefixes hold binaries owned by a system package manager;
// uninstall refuses to remove them and points at the package manager.
var packageManagedPrefixes = []string{
	"/opt/homebrew/Cellar/",
	"/usr/local/Cellar/",
	"/" + "home/linuxbrew/.linuxbrew/Cellar/",
	"/usr/bin/",
	"/snap/",
}

// insideGitWorkTree reports whether p sits inside a git working tree (any
// ancestor holds a .git entry). Such binaries are developer builds (./bin/g8s)
// and the repository is never touched by uninstall.
func insideGitWorkTree(p string) bool {
	dir := filepath.Dir(p)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// SafeStateDir reports whether dir may be purged: it must be absolute, its
// base name must carry "g8s", it must not be the filesystem root, the home
// directory or any ancestor of home, and it must not be a git repository.
func SafeStateDir(dir, home string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("refusing non-absolute path %q", dir)
	}
	clean := filepath.Clean(dir)
	if !strings.Contains(strings.ToLower(filepath.Base(clean)), "g8s") {
		return fmt.Errorf("refusing %s: base name does not identify a g8s-owned directory", clean)
	}
	if filepath.Dir(clean) == clean {
		return fmt.Errorf("refusing filesystem root %s", clean)
	}
	if home != "" {
		h := filepath.Clean(home)
		if clean == h {
			return fmt.Errorf("refusing home directory %s", clean)
		}
		if rel, err := filepath.Rel(clean, h); err == nil && !strings.HasPrefix(rel, "..") {
			return fmt.Errorf("refusing %s: it contains the home directory", clean)
		}
	}
	if _, err := os.Lstat(filepath.Join(clean, ".git")); err == nil {
		return fmt.Errorf("refusing %s: it is a git repository", clean)
	}
	return nil
}

// mcpHasG8s reports whether a config file carries an mcpServers.g8s entry.
func mcpHasG8s(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		return false
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return false
	}
	_, exists := servers["g8s"]
	return exists
}

// PlanUninstall enumerates what an uninstall would remove without mutating
// anything. Items are returned in apply order.
func PlanUninstall(opts UninstallOptions) *UninstallReport {
	rep := &UninstallReport{DryRun: true, PurgeState: opts.PurgeState, Items: []UninstallItem{}}
	keep := func(kind, p, why string) {
		rep.Kept = append(rep.Kept, UninstallItem{Kind: kind, Path: p, Detail: why, Action: ActionSkipped})
	}
	add := func(kind, p, detail string) {
		rep.Items = append(rep.Items, UninstallItem{Kind: kind, Path: p, Detail: detail, Action: ActionWouldRemove})
	}

	if opts.Service != nil {
		if present, where := opts.Service.Detect(); present {
			add(KindService, where, "stop and remove background service unit (g8s service install)")
		}
	}

	for _, cfg := range opts.MCPConfigs {
		if cfg.Path == "" || !mcpHasG8s(cfg.Path) {
			continue
		}
		add(KindMCPEntry, cfg.Path, fmt.Sprintf("remove mcpServers.g8s entry (%s, written by g8s init)", cfg.IDE))
	}

	for _, clean := range dedupeNested(opts.StateDirs) {
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		if !opts.PurgeState {
			keep(KindStateDir, clean, "kept: pass --purge-state to remove g8s state/config/cache")
			continue
		}
		if err := SafeStateDir(clean, opts.Home); err != nil {
			keep(KindStateDir, clean, err.Error())
			continue
		}
		add(KindStateDir, clean, "remove g8s-owned directory (includes g8s-managed worktrees under it)")
	}

	if opts.BinaryPath != "" {
		bin := filepath.Clean(opts.BinaryPath)
		base := strings.ToLower(filepath.Base(bin))
		switch {
		case base != "g8s" && base != "g8s.exe":
			keep(KindBinary, bin, "kept: executable name is not g8s")
		case isPackageManaged(bin):
			keep(KindBinary, bin, "kept: package-managed binary; remove it with your package manager (brew/apt/dnf/apk)")
		case insideGitWorkTree(bin):
			keep(KindBinary, bin, "kept: binary lives inside a git working tree (developer build)")
		default:
			if _, err := os.Stat(BackupPath(bin)); err == nil {
				add(KindBackup, BackupPath(bin), "remove rollback copy left by g8s update")
			}
			detail := "remove g8s executable"
			if opts.GOOS == "windows" {
				detail = "rename running g8s.exe aside to .uninstall-pending (Windows locks running executables); delete it after exit"
			}
			add(KindBinary, bin, detail)
		}
	}
	return rep
}

// dedupeNested cleans, de-duplicates and sorts dirs, dropping any entry that
// lives under another entry (removing the parent already covers it).
func dedupeNested(dirs []string) []string {
	var cleaned []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		c := filepath.Clean(d)
		if !seen[c] {
			seen[c] = true
			cleaned = append(cleaned, c)
		}
	}
	sort.Strings(cleaned)
	var out []string
	for _, c := range cleaned {
		nested := false
		for _, parent := range cleaned {
			if parent == c {
				continue
			}
			if rel, err := filepath.Rel(parent, c); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, c)
		}
	}
	return out
}

func isPackageManaged(p string) bool {
	slash := filepath.ToSlash(p)
	for _, prefix := range packageManagedPrefixes {
		if strings.HasPrefix(slash, prefix) {
			return true
		}
	}
	return false
}

// removeMCPEntry deletes mcpServers.g8s from a config file, preserving every
// other key. Dedicated files left with no content are removed entirely.
func removeMCPEntry(cfg MCPConfig) error {
	data, err := os.ReadFile(cfg.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.Path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", cfg.Path, err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}
	delete(servers, "g8s")
	if cfg.Dedicated && len(servers) == 0 && len(root) == 1 {
		if err := os.Remove(cfg.Path); err != nil {
			return fmt.Errorf("remove %s: %w", cfg.Path, err)
		}
		return nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", cfg.Path, err)
	}
	perm := os.FileMode(0o600)
	if info, err := os.Stat(cfg.Path); err == nil {
		perm = info.Mode().Perm() & 0o600
		if perm == 0 {
			perm = 0o600
		}
	}
	tmp := cfg.Path + ".g8s-uninstall.tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), perm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, cfg.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", cfg.Path, err)
	}
	return nil
}

// ApplyUninstall executes a plan produced by PlanUninstall. Each item is
// attempted independently; failures are recorded per item and returned as a
// joined error so callers exit non-zero.
func ApplyUninstall(opts UninstallOptions, plan *UninstallReport) (*UninstallReport, error) {
	out := &UninstallReport{DryRun: false, PurgeState: plan.PurgeState, Items: []UninstallItem{}, Kept: plan.Kept}
	mcpByPath := map[string]MCPConfig{}
	for _, c := range opts.MCPConfigs {
		mcpByPath[filepath.Clean(c.Path)] = c
	}
	var errs []error
	for _, it := range plan.Items {
		res := it
		var err error
		switch it.Kind {
		case KindService:
			if opts.Service == nil {
				err = errors.New("no service hook configured")
			} else {
				err = opts.Service.Uninstall()
			}
		case KindMCPEntry:
			cfg, ok := mcpByPath[filepath.Clean(it.Path)]
			if !ok {
				cfg = MCPConfig{Path: it.Path}
			}
			err = removeMCPEntry(cfg)
		case KindStateDir:
			if err = SafeStateDir(it.Path, opts.Home); err == nil {
				err = os.RemoveAll(it.Path)
			}
		case KindBackup:
			err = os.Remove(it.Path)
		case KindBinary:
			if opts.GOOS == "windows" {
				pending := it.Path + ".uninstall-pending"
				if rmErr := os.Remove(pending); rmErr != nil && !os.IsNotExist(rmErr) {
					err = rmErr
				} else {
					err = os.Rename(it.Path, pending)
				}
			} else {
				err = os.Remove(it.Path)
			}
		default:
			err = fmt.Errorf("unknown item kind %q", it.Kind)
		}
		if err != nil && !os.IsNotExist(err) {
			res.Action = ActionFailed
			res.Error = err.Error()
			errs = append(errs, fmt.Errorf("%s %s: %w", it.Kind, it.Path, err))
		} else {
			res.Action = ActionRemoved
		}
		out.Items = append(out.Items, res)
	}
	return out, errors.Join(errs...)
}
