//go:build linux

package sandbox

import "fmt"

// probeSandboxCapability probes Linux kernel containment backends in the same
// precedence prepareOS uses: runnable bubblewrap first, then Landlock ABI >= 1.
// Restricted Linux/Docker CI hosts with neither report CapabilityUnavailable so
// the degraded-sandbox gate can fail closed (or record honest degradation when
// the operator opts in) instead of silently bypassing (#971 WP1).
func probeSandboxCapability() SandboxCapability {
	if bin, ok := probeBwrap(); ok {
		return SandboxCapability{
			Mode:    CapabilityKernelEnforced,
			Backend: "bwrap",
			Reason:  fmt.Sprintf("Linux bubblewrap runnable at %s; workspace_write auto-enforces kernel filesystem containment", bin),
		}
	}
	if abi := probeLandlockABI(); abi >= 1 {
		return SandboxCapability{
			Mode:    CapabilityKernelEnforced,
			Backend: "landlock",
			Reason:  fmt.Sprintf("Linux Landlock ABI %d available (no runnable bubblewrap); workspace_write auto-enforces kernel filesystem containment", abi),
		}
	}
	return SandboxCapability{
		Mode:    CapabilityUnavailable,
		Backend: "linux_policy_only",
		Reason:  "restricted Linux host: no runnable bubblewrap and Landlock ABI < 1 (typical of unprivileged Docker CI); only bwrap/Landlock backends are supported, unshare-based containment is not implemented — workspace_write is refused unless --allow-degraded-sandbox is set",
	}
}
