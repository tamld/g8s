package sandbox

import "runtime"

// SandboxCapabilityLevel distinguishes the strongest containment level a host can
// provide for worker subprocesses (issue #971 WP1 capability-probed preflight).
type SandboxCapabilityLevel string

const (
	// CapabilityKernelEnforced: the OS kernel isolates the worker filesystem
	// (macOS Seatbelt, Linux bubblewrap/Landlock). workspace_write auto-enforces.
	CapabilityKernelEnforced SandboxCapabilityLevel = "kernel_enforced"
	// CapabilityMediated: containment is userspace-mediated rather than kernel
	// filesystem isolation (Windows JobObject process-tree + env scrubbing).
	CapabilityMediated SandboxCapabilityLevel = "mediated"
	// CapabilityUnavailable: no OS containment backend exists on this host
	// (restricted Linux/Docker CI); only the policy_only fallback remains.
	CapabilityUnavailable SandboxCapabilityLevel = "unavailable"
)

// SandboxCapability is the machine-readable result of a host sandbox-capability
// probe (issue #971 WP1): which enforcement level the platform provides, via
// which backend, and an honest human/machine-readable reason.
type SandboxCapability struct {
	Mode    SandboxCapabilityLevel `json:"mode"`
	Backend string                 `json:"backend"`
	Reason  string                 `json:"reason"`
}

// capabilityProbe is the injectable per-OS seam; tests swap it to stub the
// host probe (red-first mockable per-OS probe, #971 WP1).
var capabilityProbe = probeSandboxCapability

// ProbeCapability reports whether the host can provide kernel-enforced process
// containment, mediated containment, or none at all:
//   - macOS: Seatbelt (sandbox-exec) present?
//   - Linux: bubblewrap runnable? Landlock ABI >= 1?
//   - Windows: JobObject process-tree + env-scrubbing semantics (mediated).
//
// The probe is a preflight check: it never spawns a worker attempt and never
// mutates state. It powers g8s doctor --json sandbox_capability and the
// compliance_vs_quality operating_modes telemetry (#971 WP1/WP2).
func ProbeCapability() SandboxCapability {
	return capabilityProbe()
}

// probeGOOS reports the GOOS the probe targets (kept indirect so tests can
// stub the whole probe without touching runtime).
func probeGOOS() string { return runtime.GOOS }
