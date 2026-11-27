//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

const darwinSandboxExecFallback = "/usr/bin/sandbox-exec"

// probeSandboxCapability probes macOS Seatbelt (sandbox-exec) presence.
// Seatbelt is the kernel-enforced backend Prepare wraps every sandboxed launch in.
func probeSandboxCapability() SandboxCapability {
	sbBin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		if st, statErr := os.Stat(darwinSandboxExecFallback); statErr != nil || st.IsDir() {
			return SandboxCapability{
				Mode:    CapabilityUnavailable,
				Backend: "",
				Reason:  "macOS Seatbelt unavailable: sandbox-exec not found in PATH or /usr/bin; workspace_write would be refused unless --allow-degraded-sandbox is set",
			}
		}
		sbBin = darwinSandboxExecFallback
	}
	return SandboxCapability{
		Mode:    CapabilityKernelEnforced,
		Backend: "seatbelt",
		Reason:  fmt.Sprintf("macOS Seatbelt present at %s; workspace_write auto-enforces kernel containment", sbBin),
	}
}
