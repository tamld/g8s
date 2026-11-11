package dispatch

import "strings"

// externalCLIWorktreeFirewallProviders lists platform_dispatch CLIs that execute
// with a working directory and can be contained via ephemeral git worktrees plus
// VerifyWorktreeDiffScope (DELTA-39 / issue #880).
var externalCLIWorktreeFirewallProviders = map[string]struct{}{
	"codex":       {},
	"cursor":      {},
	"opencode":    {},
	"commandcode": {},
	"qwen":        {},
}

// SupportsWorktreeReadOnlyFirewall reports whether read_only for the named provider
// can be mechanically enforced by spawning inside an isolated git worktree and
// running the post-exit diff firewall.
func SupportsWorktreeReadOnlyFirewall(providerName, defaultProvider string) bool {
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	_, ok := externalCLIWorktreeFirewallProviders[name]
	return ok
}

// SupportsReadOnlyPolicy reports whether read_only can be mechanically enforced
// for a provider without a full registry entry. Built-in AGY enforces permissions
// in-process; the external CLI set listed above uses the worktree diff firewall.
func SupportsReadOnlyPolicy(providerName, defaultProvider string) bool {
	name := strings.ToLower(strings.TrimSpace(providerName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	if name == "" || name == "agy" {
		return true
	}
	return SupportsWorktreeReadOnlyFirewall(providerName, defaultProvider)
}
