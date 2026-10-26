// Package conformance implements the DELTA-43 provider conformance probe
// suite: evidence-backed PASS/FAIL/UNSUPPORTED/UNVERIFIED verdicts per
// provider (CLI agents and HTTP LLM gateways), persisted as a versioned
// ground-truth matrix that dispatch gates can read.
package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the versioned schema identifier written to
// .g8s/provider-conformance.yml.
const SchemaVersion = "g8s.provider-conformance/v1"

// DefaultMatrixPath is the repo-relative ground-truth file location.
const DefaultMatrixPath = ".g8s/provider-conformance.yml"

// DefaultDocPath is the repo-relative rendered markdown matrix location.
const DefaultDocPath = "docs/user-guide/provider-matrix.md"

// Status is the verdict of a single probe.
type Status string

const (
	// StatusPass means the probe ran and the observed behavior met the contract.
	StatusPass Status = "PASS"
	// StatusFail means the probe ran and the observed behavior violated the contract.
	StatusFail Status = "FAIL"
	// StatusUnsupported means the provider does not offer the capability
	// (static evidence: flag absent from --help, or class has no such surface).
	StatusUnsupported Status = "UNSUPPORTED"
	// StatusUnverified means no runtime evidence exists (static mode, policy
	// refusal, provider not installed, or inconclusive observation).
	StatusUnverified Status = "UNVERIFIED"
)

// Class distinguishes CLI agent subprocess providers from HTTP LLM gateways.
type Class string

const (
	// ClassCLIAgent is a local coding-agent binary spawned as a subprocess.
	ClassCLIAgent Class = "cli_agent"
	// ClassHTTPLLM is an OpenAI-compatible HTTP LLM endpoint.
	ClassHTTPLLM Class = "http_llm"
)

// ProbeID identifies a conformance probe.
type ProbeID string

// Probe identifiers (DELTA-43 R2).
const (
	ProbeP0 ProbeID = "P0" // binary/endpoint presence + version
	ProbeP1 ProbeID = "P1" // non-interactive one-shot exits
	ProbeP2 ProbeID = "P2" // structured/JSON output parseable
	ProbeP3 ProbeID = "P3" // exit code / error semantics on failure
	ProbeP4 ProbeID = "P4" // timeout + process-group kill honored
	ProbeP5 ProbeID = "P5" // cwd/worktree confinement
	ProbeP6 ProbeID = "P6" // file edit lands in worktree
	ProbeP7 ProbeID = "P7" // respects output schema
	ProbeP8 ProbeID = "P8" // usage / token accounting reported
	ProbeH1 ProbeID = "H1" // HTTP model listing
	ProbeH2 ProbeID = "H2" // HTTP tool-calling support
	ProbeH3 ProbeID = "H3" // HTTP max context claim vs observed
)

// probeNames maps probe IDs to stable snake_case names.
var probeNames = map[ProbeID]string{
	ProbeP0: "presence_and_version",
	ProbeP1: "non_interactive_one_shot",
	ProbeP2: "structured_json_output",
	ProbeP3: "failure_exit_semantics",
	ProbeP4: "timeout_process_group_kill",
	ProbeP5: "worktree_confinement",
	ProbeP6: "file_edit_lands",
	ProbeP7: "output_schema_respected",
	ProbeP8: "usage_token_accounting",
	ProbeH1: "model_listing",
	ProbeH2: "tool_calling",
	ProbeH3: "context_window_claim_vs_observed",
}

// ProbeName returns the stable name of a probe ID.
func ProbeName(id ProbeID) string { return probeNames[id] }

// CLIProbeOrder is the canonical probe order for CLI agents.
var CLIProbeOrder = []ProbeID{ProbeP0, ProbeP1, ProbeP2, ProbeP3, ProbeP4, ProbeP5, ProbeP6, ProbeP7, ProbeP8}

// HTTPProbeOrder is the canonical probe order for HTTP LLM providers.
var HTTPProbeOrder = []ProbeID{ProbeP0, ProbeH1, ProbeP1, ProbeP2, ProbeP3, ProbeP4, ProbeP5, ProbeP6, ProbeP7, ProbeP8, ProbeH2, ProbeH3}

// requiredCLI lists probes whose FAIL makes a CLI agent unsafe to dispatch.
var requiredCLI = map[ProbeID]bool{ProbeP0: true, ProbeP1: true, ProbeP3: true, ProbeP4: true, ProbeP5: true, ProbeP6: true}

// requiredHTTP lists probes whose FAIL makes an HTTP provider unsafe to dispatch.
var requiredHTTP = map[ProbeID]bool{ProbeP0: true, ProbeP1: true, ProbeP3: true}

// IsRequired reports whether a probe is dispatch-required for a class.
func IsRequired(class Class, id ProbeID) bool {
	if class == ClassHTTPLLM {
		return requiredHTTP[id]
	}
	return requiredCLI[id]
}

// Evidence is the mechanical record backing a probe verdict.
type Evidence struct {
	Command         string `yaml:"command" json:"command"`
	ExitCode        int    `yaml:"exit_code" json:"exit_code"`
	HTTPStatus      int    `yaml:"http_status,omitempty" json:"http_status,omitempty"`
	StdoutSHA256    string `yaml:"stdout_sha256" json:"stdout_sha256"`
	DurationMs      int64  `yaml:"duration_ms" json:"duration_ms"`
	ProviderVersion string `yaml:"provider_version" json:"provider_version"`
	ObservedAt      string `yaml:"observed_at" json:"observed_at"`
}

// ProbeResult is one probe verdict with its evidence.
type ProbeResult struct {
	ID       ProbeID   `yaml:"id" json:"id"`
	Name     string    `yaml:"name" json:"name"`
	Required bool      `yaml:"required" json:"required"`
	Mode     string    `yaml:"mode" json:"mode"` // "static" or "live"
	Status   Status    `yaml:"status" json:"status"`
	Reason   string    `yaml:"reason" json:"reason"`
	Evidence *Evidence `yaml:"evidence,omitempty" json:"evidence,omitempty"`
}

// ProviderReport aggregates all probe verdicts for one provider.
type ProviderReport struct {
	Name            string        `yaml:"name" json:"name"`
	Class           Class         `yaml:"class" json:"class"`
	Available       bool          `yaml:"available" json:"available"`
	Binary          string        `yaml:"binary,omitempty" json:"binary,omitempty"`
	Version         string        `yaml:"version,omitempty" json:"version,omitempty"`
	Mode            string        `yaml:"mode" json:"mode"`
	LivePolicy      string        `yaml:"live_policy" json:"live_policy"`
	Model           string        `yaml:"model,omitempty" json:"model,omitempty"`
	RolesEligible   []string      `yaml:"roles_eligible,omitempty" json:"roles_eligible,omitempty"`
	RecommendedFor  []string      `yaml:"recommended_for,omitempty" json:"recommended_for,omitempty"`
	Note            string        `yaml:"note,omitempty" json:"note,omitempty"`
	FlagsAdvertised []string      `yaml:"flags_advertised,omitempty" json:"flags_advertised,omitempty"`
	Probes          []ProbeResult `yaml:"probes" json:"probes"`
}

// ApplyRoleProfile populates RolesEligible, RecommendedFor, and Note per
// operator role-tier strategy (Brain vs Worker vs Scout vs Compatibility-Only).
func ApplyRoleProfile(r *ProviderReport) {
	if r == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(r.Name)) {
	case "claude", "codex":
		r.RolesEligible = []string{"brain", "worker", "scout"}
		r.RecommendedFor = []string{"f0-brain", "t3-t4-architecture", "complex-decomposition"}
	case "9router", "opencode":
		r.RolesEligible = []string{"worker", "scout"}
		r.RecommendedFor = []string{"f1-worker", "f1-scout", "bounded-swe"}
	case "gemini":
		r.RolesEligible = []string{"worker", "scout"}
		r.RecommendedFor = []string{"f1-scout", "long-context-audit", "multimodal"}
	case "agy":
		r.RolesEligible = []string{"brain"}
		r.RecommendedFor = []string{"f0-interactive-supervisor"}
		r.Note = "F1 worker spawning forbidden by Lesson #23 (F0 Brain quota protection)"
	case "ollama":
		r.RolesEligible = []string{"compatibility-only"}
		r.RecommendedFor = []string{"tts", "voice", "light-offline"}
		r.Note = "supported for compatibility only; not recommended for coding due to local SSD storage footprint and capability ceiling"
	default:
		r.RolesEligible = []string{"worker", "scout"}
	}
}

// Probe returns the result for a probe ID, if present.
func (r ProviderReport) Probe(id ProbeID) (ProbeResult, bool) {
	for _, p := range r.Probes {
		if p.ID == id {
			return p, true
		}
	}
	return ProbeResult{}, false
}

// FailedRequired lists required probes that FAILed.
func (r ProviderReport) FailedRequired() []ProbeID {
	var out []ProbeID
	for _, p := range r.Probes {
		if p.Required && p.Status == StatusFail {
			out = append(out, p.ID)
		}
	}
	return out
}

// Matrix is the versioned ground-truth document.
type Matrix struct {
	SchemaVersion string           `yaml:"schema_version" json:"schema_version"`
	GeneratedAt   string           `yaml:"generated_at" json:"generated_at"`
	Generator     string           `yaml:"generator" json:"generator"`
	Providers     []ProviderReport `yaml:"providers" json:"providers"`
}

// Find returns the report for a provider name (case-insensitive).
func (m *Matrix) Find(name string) (ProviderReport, bool) {
	if m == nil {
		return ProviderReport{}, false
	}
	for _, p := range m.Providers {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p, true
		}
	}
	return ProviderReport{}, false
}

// Merge overlays the reports in update onto m (replacing same-named
// providers) and returns the merged matrix sorted by provider name.
func (m *Matrix) Merge(update Matrix) Matrix {
	byName := map[string]ProviderReport{}
	if m != nil {
		for _, p := range m.Providers {
			byName[strings.ToLower(p.Name)] = p
		}
	}
	for _, p := range update.Providers {
		byName[strings.ToLower(p.Name)] = p
	}
	out := Matrix{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   update.GeneratedAt,
		Generator:     update.Generator,
	}
	for _, p := range byName {
		out.Providers = append(out.Providers, p)
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].Name < out.Providers[j].Name })
	return out
}

// sha256Hex returns the lowercase hex sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// nowRFC3339 is overridable for deterministic tests.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

func newResult(class Class, id ProbeID, mode string, status Status, reason string, ev *Evidence) ProbeResult {
	if ev != nil {
		cp := *ev
		cp.Command = Sanitize(cp.Command)
		cp.ProviderVersion = Sanitize(cp.ProviderVersion)
		ev = &cp
	}
	return ProbeResult{
		ID:       id,
		Name:     ProbeName(id),
		Required: IsRequired(class, id),
		Mode:     mode,
		Status:   status,
		Reason:   Sanitize(reason),
		Evidence: ev,
	}
}
