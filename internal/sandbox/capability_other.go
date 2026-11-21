//go:build !darwin && !linux && !windows

package sandbox

// probeSandboxCapability reports honestly that no sandbox backend exists for
// this GOOS; only the policy_only fallback remains (#971 WP1).
func probeSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Mode:    CapabilityUnavailable,
		Backend: "",
		Reason:  "no sandbox backend implemented for this GOOS; only the policy-only fallback remains — workspace_write is refused unless --allow-degraded-sandbox is set",
	}
}
