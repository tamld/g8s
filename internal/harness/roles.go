package harness

import (
	"fmt"
	"sort"
)

// RoleProfile defines a specialized worker identity and its behavioral boundary.
type RoleProfile struct {
	Name              string   `json:"name"`
	Purpose           string   `json:"purpose"`
	OutputFocus       string   `json:"output_focus"`
	Forbidden         []string `json:"forbidden"`
	DefaultPermission string   `json:"default_permission,omitempty"`
	Stratum           string   `json:"stratum,omitempty"`
	RecommendedSkills []string `json:"recommended_skills,omitempty"`
}

var Roles = map[string]RoleProfile{
	"collector": {
		Name:              "collector",
		Purpose:           "Collect a bounded inventory of paths, headings, metadata, and reusable procedures.",
		OutputFocus:       "evidence paths, compact findings, skipped-sensitive list, uncertainty",
		Forbidden:         []string{"editing files", "running installs", "copying raw confidential payloads"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-epistemic-audit"},
	},
	"scout": {
		Name:              "scout",
		Purpose:           "Find candidate modules, skills, MCP servers, configs, harnesses, loops, and project artifacts.",
		OutputFocus:       "candidate list grouped by value and adoption risk",
		Forbidden:         []string{"changing state", "promoting claims as proven", "reading credential material"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"ck:scout", "g8s-epistemic-audit"},
	},
	"mcp-mapper": {
		Name:              "mcp-mapper",
		Purpose:           "Map MCP server tools, provider registries, permissions, and runtime boundaries.",
		OutputFocus:       "tool surface, provider model, permission gates, adoption/avoid recommendations",
		Forbidden:         []string{"launching servers", "using real credentials", "calling external systems"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"ck:mcp-management", "g8s-sec-gate"},
	},
	"summarizer": {
		Name:              "summarizer",
		Purpose:           "Summarize existing artifacts without adding new claims beyond the inspected evidence.",
		OutputFocus:       "short synthesis, file evidence, open questions",
		Forbidden:         []string{"inventing missing context", "copying long proprietary text", "making final decisions"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-epistemic-audit", "stop-slop"},
	},
	"verifier": {
		Name:              "verifier",
		Purpose:           "Check whether a bounded claim is supported by files, command output, or structured artifacts.",
		OutputFocus:       "claim status, supporting paths, contradicting evidence, residual uncertainty",
		Forbidden:         []string{"fixing the issue", "rewriting evidence", "treating absence as proof"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-blind-verifier", "g8s-epistemic-audit"},
	},
	"test-runner": {
		Name:              "test-runner",
		Purpose:           "Run explicitly provided safe verification commands and summarize results.",
		OutputFocus:       "commands run, exit codes, key failures, next diagnostic step",
		Forbidden:         []string{"destructive commands", "dependency installs unless explicitly permitted", "unbounded retries"},
		DefaultPermission: "workspace_write",
		Stratum:           "S3-M",
		RecommendedSkills: []string{"g8s-red-prove", "ck:test"},
	},
	"researcher": {
		Name:              "researcher",
		Purpose:           "Perform deep world-state, CVE/NVD, upstream documentation, and architectural evidence synthesis.",
		OutputFocus:       "grounded citations (Local/Upstream/Docs/CVE), version compatibility matrix, falsified assumptions",
		Forbidden:         []string{"editing files", "relying on unverified training-cutoff memory", "skipping primary source citations"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-epistemic-audit", "ck:xia", "ck:research"},
	},
	"implementer": {
		Name:              "implementer",
		Purpose:           "Execute bounded code mutations, feature implementations, and bugfixes strictly within WriteReceipt allowed_paths.",
		OutputFocus:       "minimal diff within receipt scope, red-to-green test proof, zero out-of-scope mutations",
		Forbidden:         []string{"out-of-scope file edits", "bypassing write receipts", "weakening existing tests or security gates"},
		DefaultPermission: "workspace_write",
		Stratum:           "S3-M",
		RecommendedSkills: []string{"g8s-red-prove", "ck:cook", "code-simplification"},
	},
	"sec-auditor": {
		Name:              "sec-auditor",
		Purpose:           "Perform STRIDE/OWASP/CWE security audits, SSRF/path-traversal/symlink/command-injection scans, and supply-chain verification.",
		OutputFocus:       "CVE/CWE-mapped findings, exploitability proof, trust-boundary violations, remediation verification",
		Forbidden:         []string{"mutating production state", "exfiltrating secrets", "downgrading P0/P1 findings without mechanical proof"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-sec-gate", "ck:security", "g8s-blind-verifier"},
	},
	"analyst": {
		Name:              "analyst",
		Purpose:           "Decompose ambiguous stakeholder intent into formal PRD/SRS/AC/FSM contracts and multi-hypothesis root-cause matrices.",
		OutputFocus:       "PRD user stories, SRS functional requirements, ACGiven/When/Then criteria, FSM state transition invariants, 12D scenario matrix",
		Forbidden:         []string{"editing source code", "shipping unverified prose as implementation", "skipping edge-case or failure-state modeling"},
		DefaultPermission: "read_only",
		Stratum:           "S3-A",
		RecommendedSkills: []string{"g8s-scenario-matrix", "fable-thinking", "wiki-business-analyst"},
	},
}

// RoleNames returns a sorted list of registered role names.
func RoleNames() []string {
	names := make([]string, 0, len(Roles))
	for name := range Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetRole retrieves a RoleProfile by name or returns an error.
func GetRole(name string) (RoleProfile, error) {
	role, exists := Roles[name]
	if !exists {
		return RoleProfile{}, fmt.Errorf("unknown role '%s'. Available: %v", name, RoleNames())
	}
	return role, nil
}
