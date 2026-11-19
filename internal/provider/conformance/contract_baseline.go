package conformance

import "strings"

// ContractSpec describes the CI-proven offline contract verdict for a (provider, probe) pair.
type ContractSpec struct {
	Status Status
	Reason string
}

const (
	reasonCLIOfflineContract = "verified offline via deterministic compiled adapter contract suite in CI (no paid vendor calls)"
	reasonCLINoSchemaWired   = "no output-schema flag wired in CLIProfile"
	reasonHTTPOfflineOpenAI  = "verified offline against deterministic OpenAI-compatible HTTP mock in CI (timeout/cancel/streaming/tools/tokens)"
	reasonHTTPAnthropicMock  = "verified offline against deterministic Anthropic Messages HTTP mock in CI (effort/SSE/tools/tokens)"
	reasonHTTPAnthropicNoRF  = "Anthropic Messages API adapter uses system/tools rather than OpenAI response_format JSON schema or /models context claims"
	reasonHTTPNoSubprocess   = "HTTP request transport has no OS subprocess; client-side request cancel/timeout verified in adapter contract suite"
	reasonHTTPNoFilesystem   = "HTTP request transport has no direct worktree filesystem surface; g8s applies edits under WriteReceipt and claim fence"
)

var cliFullContract = map[ProbeID]ContractSpec{
	ProbeP0: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP1: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP2: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP3: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP4: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP5: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP6: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP7: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP8: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP9: {Status: StatusPass, Reason: reasonCLIOfflineContract},
}

var cliNoSchemaContract = map[ProbeID]ContractSpec{
	ProbeP0: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP1: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP2: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP3: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP4: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP5: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP6: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP7: {Status: StatusUnsupported, Reason: reasonCLINoSchemaWired},
	ProbeP8: {Status: StatusPass, Reason: reasonCLIOfflineContract},
	ProbeP9: {Status: StatusPass, Reason: reasonCLIOfflineContract},
}

var httpOpenAIContract = map[ProbeID]ContractSpec{
	ProbeP0: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeH1: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP1: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP2: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP3: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP4: {Status: StatusUnsupported, Reason: reasonHTTPNoSubprocess},
	ProbeP5: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeP6: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeP7: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP8: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeP9: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeH2: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
	ProbeH3: {Status: StatusPass, Reason: reasonHTTPOfflineOpenAI},
}

var httpAnthropicContract = map[ProbeID]ContractSpec{
	ProbeP0: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeH1: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeP1: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeP2: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeP3: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeP4: {Status: StatusUnsupported, Reason: reasonHTTPNoSubprocess},
	ProbeP5: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeP6: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeP7: {Status: StatusUnsupported, Reason: reasonHTTPAnthropicNoRF},
	ProbeP8: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeP9: {Status: StatusUnsupported, Reason: reasonHTTPNoFilesystem},
	ProbeH2: {Status: StatusPass, Reason: reasonHTTPAnthropicMock},
	ProbeH3: {Status: StatusUnsupported, Reason: reasonHTTPAnthropicNoRF},
}

var contractBaselineByProvider = map[string]map[ProbeID]ContractSpec{
	"agy":         cliFullContract,
	"claude":      cliFullContract,
	"codex":       cliFullContract,
	"cursor":      cliFullContract,
	"qwen":        cliFullContract,
	"commandcode": cliNoSchemaContract,
	"opencode":    cliNoSchemaContract,
	"9router":     httpOpenAIContract,
	"openai":      httpOpenAIContract,
	"ollama":      httpOpenAIContract,
	"anthropic":   httpAnthropicContract,
}

// ContractBaselineProviders returns the canonical list of 11 providers covered by
// the deterministic offline CI adapter contract suite.
func ContractBaselineProviders() []string {
	return []string{
		"9router",
		"agy",
		"anthropic",
		"claude",
		"codex",
		"commandcode",
		"cursor",
		"ollama",
		"openai",
		"opencode",
		"qwen",
	}
}

// LookupContractBaseline returns the CI-verified offline contract verdict for (providerName, id).
func LookupContractBaseline(providerName string, id ProbeID) (ContractSpec, bool) {
	specs, ok := contractBaselineByProvider[strings.ToLower(strings.TrimSpace(providerName))]
	if !ok {
		return ContractSpec{}, false
	}
	spec, ok := specs[id]
	return spec, ok
}

// ApplyOfflineContractBaseline populates ContractStatus and ContractReason on
// every known (provider, probe) in m without overwriting LiveStatus, Status,
// or operator live Evidence.
func ApplyOfflineContractBaseline(m *Matrix) {
	if m == nil {
		return
	}
	for i := range m.Providers {
		rep := &m.Providers[i]
		for j := range rep.Probes {
			pr := &rep.Probes[j]
			if pr.LiveStatus == "" && pr.Status != "" {
				pr.LiveStatus = pr.Status
			}
			if spec, ok := LookupContractBaseline(rep.Name, pr.ID); ok {
				pr.ContractStatus = spec.Status
				pr.ContractReason = spec.Reason
			}
		}
	}
}
