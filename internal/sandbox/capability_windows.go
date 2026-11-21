//go:build windows

package sandbox

// probeSandboxCapability reports Windows JobObject semantics honestly: the
// platform baseline is mediated containment (process-tree termination via
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE plus environment scrubbing), not kernel
// filesystem isolation. Per issue #971 this must never falsely lock out
// workspace_write on Windows — it proceeds with sandbox_degraded recorded.
func probeSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Mode:    CapabilityMediated,
		Backend: "windows_job_object",
		Reason:  "Windows JobObject semantics: process-tree termination (KILL_ON_JOB_CLOSE) and environment scrubbing; no kernel filesystem ACLs or outbound socket isolation — workspace_write proceeds with sandbox_degraded recorded",
	}
}
