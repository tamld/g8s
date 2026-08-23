package harness

import (
	"fmt"
	"sort"
)

// RoleProfile defines a specialized worker identity and its behavioral boundary.
type RoleProfile struct {
	Name        string   `json:"name"`
	Purpose     string   `json:"purpose"`
	OutputFocus string   `json:"output_focus"`
	Forbidden   []string `json:"forbidden"`
}

var Roles = map[string]RoleProfile{
	"collector": {
		Name:        "collector",
		Purpose:     "Collect a bounded inventory of paths, headings, metadata, and reusable procedures.",
		OutputFocus: "evidence paths, compact findings, skipped-sensitive list, uncertainty",
		Forbidden:   []string{"editing files", "running installs", "copying raw confidential payloads"},
	},
	"scout": {
		Name:        "scout",
		Purpose:     "Find candidate modules, skills, MCP servers, configs, harnesses, loops, and project artifacts.",
		OutputFocus: "candidate list grouped by value and adoption risk",
		Forbidden:   []string{"changing state", "promoting claims as proven", "reading credential material"},
	},
	"mcp-mapper": {
		Name:        "mcp-mapper",
		Purpose:     "Map MCP server tools, provider registries, permissions, and runtime boundaries.",
		OutputFocus: "tool surface, provider model, permission gates, adoption/avoid recommendations",
		Forbidden:   []string{"launching servers", "using real credentials", "calling external systems"},
	},
	"summarizer": {
		Name:        "summarizer",
		Purpose:     "Summarize existing artifacts without adding new claims beyond the inspected evidence.",
		OutputFocus: "short synthesis, file evidence, open questions",
		Forbidden:   []string{"inventing missing context", "copying long proprietary text", "making final decisions"},
	},
	"verifier": {
		Name:        "verifier",
		Purpose:     "Check whether a bounded claim is supported by files, command output, or structured artifacts.",
		OutputFocus: "claim status, supporting paths, contradicting evidence, residual uncertainty",
		Forbidden:   []string{"fixing the issue", "rewriting evidence", "treating absence as proof"},
	},
	"test-runner": {
		Name:        "test-runner",
		Purpose:     "Run explicitly provided safe verification commands and summarize results.",
		OutputFocus: "commands run, exit codes, key failures, next diagnostic step",
		Forbidden:   []string{"destructive commands", "dependency installs unless explicitly permitted", "unbounded retries"},
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
