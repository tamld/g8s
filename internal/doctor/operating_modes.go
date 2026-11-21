package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/sandbox"
)

// Operating-mode readiness (issue #971 WP1).
//
// The issue defines two operating modes; readiness is reported honestly —
// every requirement carries met/unmet plus probe detail, and nothing is
// claimed "ready" that the host cannot actually do:
//
//   - Mode 1 — Single-Provider Safe Wrapper: one usable provider behind the
//     durable queue with OS sandbox containment auto-enforced (kernel_enforced
//     on capable hosts), mediated on Windows, or explicitly recorded-degraded
//     via the operator opt-in when the host has no sandbox backend.
//
//   - Mode 2 — Multi-Provider Fleet: two or more usable providers with
//     per-attempt worktree isolation (git available) for concurrent fan-out,
//     plus the Mode 1 containment requirements.
//
// The degraded opt-in (--allow-degraded-sandbox / G8S_ALLOW_DEGRADED_SANDBOX=1)
// is surfaced in the sandbox requirement detail when the capability probe is
// unavailable — never a silent bypass, never a false lockout.

// OperatingModeRequirement is one machine-checkable readiness requirement.
type OperatingModeRequirement struct {
	ID     string `json:"id"`
	Met    bool   `json:"met"`
	Detail string `json:"detail,omitempty"`
}

// OperatingModeReadiness is the honest readiness assessment for one mode.
type OperatingModeReadiness struct {
	Mode         string                     `json:"mode"` // "mode1" | "mode2"
	Name         string                     `json:"name"`
	Ready        bool                       `json:"ready"`
	Reason       string                     `json:"reason"`
	Requirements []OperatingModeRequirement `json:"requirements"`
}

// OperatingModesStatus is the DoctorReport.OperatingModes additive field (#971 WP1).
type OperatingModesStatus struct {
	Mode1 OperatingModeReadiness `json:"mode1"`
	Mode2 OperatingModeReadiness `json:"mode2"`
}

// OperatingModeProbe carries the honest inputs the readiness evaluation needs.
// Doctor fills it from its executed checks; scorecard fills it from
// lightweight probes; tests inject fixtures directly.
type OperatingModeProbe struct {
	Sandbox        sandbox.SandboxCapability
	DatabaseOK     bool
	DatabaseDetail string
	// UsableProviders are distinct provider identifiers actually usable on
	// this host: worker CLI binaries present on PATH (or AGY_BIN) plus
	// providers.json provider entries.
	UsableProviders []string
	GitAvailable    bool
}

const (
	operatingMode1ID   = "mode1"
	operatingMode2ID   = "mode2"
	operatingMode1Name = "Single-Provider Safe Wrapper"
	operatingMode2Name = "Multi-Provider Fleet"
)

// EvaluateOperatingModes derives honest Mode 1 / Mode 2 readiness from the probe.
func EvaluateOperatingModes(p OperatingModeProbe) *OperatingModesStatus {
	sandboxMet := p.Sandbox.Mode == sandbox.CapabilityKernelEnforced || p.Sandbox.Mode == sandbox.CapabilityMediated
	sandboxDetail := fmt.Sprintf("capability probe mode=%s backend=%s: %s", p.Sandbox.Mode, p.Sandbox.Backend, p.Sandbox.Reason)
	if p.Sandbox.Mode == sandbox.CapabilityUnavailable {
		sandboxDetail += "; explicit degraded opt-in: --allow-degraded-sandbox (submit) or G8S_ALLOW_DEGRADED_SANDBOX=1 (worker) records sandbox_degraded in envelopes"
	}

	databaseDetail := strings.TrimSpace(p.DatabaseDetail)
	if databaseDetail == "" {
		databaseDetail = fmt.Sprintf("database_ok=%v", p.DatabaseOK)
	}

	mode1 := OperatingModeReadiness{
		Mode: operatingMode1ID,
		Name: operatingMode1Name,
		Requirements: []OperatingModeRequirement{
			{ID: "durable_queue_database", Met: p.DatabaseOK, Detail: databaseDetail},
			{ID: "provider_configured", Met: len(p.UsableProviders) >= 1, Detail: usableProvidersDetail(p.UsableProviders)},
			{ID: "sandbox_capability", Met: sandboxMet, Detail: sandboxDetail},
		},
	}
	finalizeModeReadiness(&mode1)

	mode2 := OperatingModeReadiness{
		Mode: operatingMode2ID,
		Name: operatingMode2Name,
		Requirements: []OperatingModeRequirement{
			{ID: "durable_queue_database", Met: p.DatabaseOK, Detail: databaseDetail},
			{ID: "multi_provider_configured", Met: len(p.UsableProviders) >= 2, Detail: usableProvidersDetail(p.UsableProviders)},
			{ID: "git_worktree_isolation", Met: p.GitAvailable, Detail: gitIsolationDetailText(p.GitAvailable)},
			{ID: "sandbox_capability", Met: sandboxMet, Detail: sandboxDetail},
		},
	}
	finalizeModeReadiness(&mode2)

	return &OperatingModesStatus{Mode1: mode1, Mode2: mode2}
}

func finalizeModeReadiness(mode *OperatingModeReadiness) {
	var unmet []string
	for _, req := range mode.Requirements {
		if !req.Met {
			unmet = append(unmet, req.ID)
		}
	}
	mode.Ready = len(unmet) == 0
	if mode.Ready {
		mode.Reason = "all requirements met"
		return
	}
	mode.Reason = "unmet requirements: " + strings.Join(unmet, ", ")
}

func usableProvidersDetail(providers []string) string {
	if len(providers) == 0 {
		return "0 usable providers (no worker CLI binary on PATH / AGY_BIN, no providers.json entries)"
	}
	shown := providers
	if len(shown) > 5 {
		shown = shown[:5]
	}
	detail := fmt.Sprintf("%d usable provider(s): %s", len(providers), strings.Join(shown, ", "))
	if len(providers) > 5 {
		detail += fmt.Sprintf(" (+%d more)", len(providers)-5)
	}
	return detail
}

// gitIsolationDetailText explains the worktree-isolation requirement honestly.
func gitIsolationDetailText(available bool) string {
	if available {
		return "git available: per-attempt worktree isolation for concurrent fan-out"
	}
	return "git not found on PATH: concurrent fan-out lacks per-attempt worktree isolation"
}

// UsableProviderNames returns the distinct provider identifiers considered
// usable on this host for Mode 1/2 readiness: worker CLI binaries found on
// PATH (agy, claude, gemini, ollama), AGY_BIN, and providers.json provider
// entries (api_call providers need no local binary). Shared by doctor and the
// scorecard compliance_vs_quality telemetry (#971 WP1/WP2).
func UsableProviderNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, bin := range []string{"agy", "claude", "gemini", "ollama"} {
		if _, err := exec.LookPath(bin); err == nil {
			add(bin)
		}
	}
	if agyBin := strings.TrimSpace(os.Getenv("AGY_BIN")); agyBin != "" {
		if _, err := os.Stat(agyBin); err == nil {
			add("agy")
		}
	}
	if path := resolveProvidersPath(); path != "" {
		if cfg, err := config.Load(path); err == nil {
			for _, p := range cfg.Providers {
				add(p.Name)
			}
		}
	}
	return out
}

// evaluateOperatingModesFromChecks derives the OperatingModeProbe from the
// executed doctor checks and evaluates Mode 1 / Mode 2 readiness (#971 WP1).
func evaluateOperatingModesFromChecks(report *DoctorReport, sbxCap *sandbox.SandboxCapability) *OperatingModesStatus {
	probe := OperatingModeProbe{}
	if _, gitErr := exec.LookPath("git"); gitErr == nil {
		probe.GitAvailable = true
	}
	if sbxCap != nil {
		probe.Sandbox = *sbxCap
	} else {
		probe.Sandbox = sandbox.SandboxCapability{Mode: sandbox.CapabilityUnavailable, Reason: "sandbox capability not probed"}
	}
	for _, chk := range report.Checks {
		switch {
		case chk.Name == "Database State" && chk.Status == "OK":
			probe.DatabaseOK = true
			probe.DatabaseDetail = chk.Message
		case chk.Name == "Database Connectivity" && chk.Status == "OK":
			probe.DatabaseOK = true
			probe.DatabaseDetail = chk.Message
		}
	}
	probe.UsableProviders = UsableProviderNames()
	return EvaluateOperatingModes(probe)
}
