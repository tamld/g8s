package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// CodeUnboundCLIClaim is emitted when a documentation or spec file invokes a
// g8s subcommand that is not registered in the canonical CLI surface contract.
const CodeUnboundCLIClaim = "E_UNBOUND_CLI_CLAIM"

// CanonicalCommands returns the authoritative list of top-level g8s CLI commands.
// Every command here must have a corresponding case in cmd/g8s/main.go and an
// entry in printUsage() (except "internal", which is an internal supervisor plumbing hook).
func CanonicalCommands() []string {
	return []string{
		"submit",
		"get",
		"deliver",
		"resume",
		"tasks",
		"cancel",
		"resubmit",
		"lineage",
		"children",
		"receipt",
		"receipt-export",
		"doctor",
		"init",
		"update",
		"uninstall",
		"config",
		"completion",
		"service",
		"worker",
		"memory",
		"offer",
		"analyze",
		"vault",
		"orchestrate",
		"orchestrate-aic",
		"supervisor-metrics",
		"supervisor-metrics-update-false",
		"brief-issue",
		"brief-consume",
		"converge",
		"sleep",
		"wake",
		"cleanup-worktrees",
		"cleanup",
		"migrate",
		"status",
		"state",
		"mcp",
		"reflex",
		"search",
		"read",
		"edit",
		"select",
		"watch",
		"eval",
		"roles",
		"permissions",
		"providers",
		"llm-call",
		"autopilot",
		"serve",
		"verify",
		"feature",
		"lesson",
		"feedback",
		"ladder",
		"fleet",
		"charter",
		"context-pack",
		"qa-report",
		"claim",
		"f0",
		"scorecard",
		"version",
		"help",
		"internal",
	}
}

// IsRegisteredCommand reports whether cmd is a registered top-level g8s command
// or a standard version/help flag alias.
func IsRegisteredCommand(cmd string) bool {
	switch cmd {
	case "-v", "--version", "-h", "--help":
		return true
	}
	for _, c := range CanonicalCommands() {
		if c == cmd {
			return true
		}
	}
	return false
}

// CanonicalFlagAliases returns the canonical flag family dictionary enforced
// across g8s CLI subcommands (#677).
func CanonicalFlagAliases() map[string][]string {
	return map[string][]string{
		"task":    {"--task", "--task-id"},
		"paths":   {"--paths", "--path", "--files"},
		"receipt": {"--receipt", "--receipt-id"},
		"glob":    {"--glob", "--globs", "-g"},
	}
}

// UnboundCLIClaim represents an unregistered g8s CLI invocation discovered in Markdown.
type UnboundCLIClaim struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Subcommand string `json:"subcommand"`
	Snippet    string `json:"snippet"`
	Code       string `json:"code"`
}

func (u UnboundCLIClaim) Error() string {
	return fmt.Sprintf("%s: %s:%d: unbound CLI subcommand %q in %q", u.Code, u.File, u.Line, u.Subcommand, u.Snippet)
}

var (
	// Matches `./bin/g8s <subcmd>` anywhere in markdown.
	reBinG8S = regexp.MustCompile(`(?:\./)?bin/g8s\s+([a-z0-9][a-z0-9-]*)`)
	// Matches inline code or shell lines invoking `g8s <subcmd> ... --<flag>`.
	reFlaggedG8S = regexp.MustCompile("(?:`|\\$\\s+|^\\s*)g8s\\s+([a-z0-9][a-z0-9-]*)(?:\\s+[a-z0-9_-]+){0,2}\\s+--[a-z0-9-]+")
)

// ignoredDocSubcommandTokens are documented post-MVP or meta-syntax placeholders.
var ignoredDocSubcommandTokens = map[string]bool{
	"run":     true, // Explicitly documented as Planned (post-MVP): run (sync dispatch)
	"subcmd":  true,
	"command": true,
	"cmd":     true,
}

// ScanMarkdownCLIInvocations scans markdown content for `./bin/g8s <subcmd>` and
// flagged `g8s <subcmd> ... --flag` invocations and returns any claims referencing
// a subcommand not in CanonicalCommands().
func ScanMarkdownCLIInvocations(filePath, content string) []UnboundCLIClaim {
	var claims []UnboundCLIClaim
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		lineNo := idx + 1
		// Skip lines that explicitly talk about unbound/invalid commands in tests or examples.
		if strings.Contains(line, CodeUnboundCLIClaim) || strings.Contains(line, "Unknown command") {
			continue
		}
		seenOnLine := map[string]bool{}
		for _, m := range reBinG8S.FindAllStringSubmatch(line, -1) {
			sub := m[1]
			if ignoredDocSubcommandTokens[sub] || IsRegisteredCommand(sub) || seenOnLine[sub] {
				continue
			}
			seenOnLine[sub] = true
			claims = append(claims, UnboundCLIClaim{
				File:       filePath,
				Line:       lineNo,
				Subcommand: sub,
				Snippet:    strings.TrimSpace(m[0]),
				Code:       CodeUnboundCLIClaim,
			})
		}
		for _, m := range reFlaggedG8S.FindAllStringSubmatch(line, -1) {
			sub := m[1]
			if ignoredDocSubcommandTokens[sub] || IsRegisteredCommand(sub) || seenOnLine[sub] {
				continue
			}
			seenOnLine[sub] = true
			claims = append(claims, UnboundCLIClaim{
				File:       filePath,
				Line:       lineNo,
				Subcommand: sub,
				Snippet:    strings.TrimSpace(m[0]),
				Code:       CodeUnboundCLIClaim,
			})
		}
	}
	return claims
}
