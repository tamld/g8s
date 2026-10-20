package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var gateCheckTimeout = 25 * time.Second

// HookDecision represents the decision returned by agent hooks.
type HookDecision struct {
	Decision string `json:"decision"` // "allow", "deny", "force_ask", "continue"
	Reason   string `json:"reason,omitempty"`
}

// PreToolInput captures the flexible schema for PreToolUse events.
type PreToolInput struct {
	ToolCall struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Args      map[string]any `json:"args"`
	} `json:"toolCall"`
	ToolCallSnake struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
		Args      map[string]any `json:"args"`
	} `json:"tool_call"`
	ToolName            string         `json:"tool_name"`
	ToolNameCamel       string         `json:"toolName"`
	ToolInput           map[string]any `json:"tool_input"`
	ToolInputCamel      map[string]any `json:"toolInput"`
	CommandLine         string         `json:"commandLine"`
	Command             string         `json:"command"`
	WorkspacePaths      []string       `json:"workspacePaths"`
	WorkspacePathsSnake []string       `json:"workspace_paths"`
	Cwd                 string         `json:"cwd"`
}

// StopInput captures the flexible schema for Stop events.
type StopInput struct {
	TranscriptPath            string   `json:"transcriptPath"`
	TranscriptPathSnake       string   `json:"transcript_path"`
	WorkspacePaths            []string `json:"workspacePaths"`
	WorkspacePathsSnake       []string `json:"workspace_paths"`
	TurnID                    string   `json:"turnId"`
	TurnIDSnake               string   `json:"turn_id"`
	SessionID                 string   `json:"sessionId"`
	SessionIDSnake            string   `json:"session_id"`
	LastAssistantMessage      string   `json:"lastAssistantMessage"`
	LastAssistantMessageSnake string   `json:"last_assistant_message"`
	Message                   string   `json:"message"`
}

func hookCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: f0 hook <pre-tool|stop>\n")
		return 1
	}

	switch args[0] {
	case "pre-tool":
		return runHookPreTool(args[1:])
	case "stop":
		return runHookStop(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "f0 hook: unknown subcommand %q\n", args[0])
		return 1
	}
}

func writeDecision(dec HookDecision) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(dec)
	return 0
}

func getMapString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if val, ok := m[k]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func runHookPreTool(args []string) int {
	var input PreToolInput
	data, err := io.ReadAll(os.Stdin)
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &input)
	}

	dec := evaluatePreToolHook(input)
	return writeDecision(dec)
}

func evaluatePreToolHook(input PreToolInput) HookDecision {
	// Extract tool name
	toolName := input.ToolCall.Name
	if toolName == "" {
		toolName = input.ToolCallSnake.Name
	}
	if toolName == "" {
		toolName = input.ToolName
	}
	if toolName == "" {
		toolName = input.ToolNameCamel
	}

	// Axiom 1: exploration is free. Only intercept command execution tools.
	if toolName != "" && toolName != "run_command" && toolName != "bash" && toolName != "shell" {
		return HookDecision{Decision: "allow"}
	}

	// Extract command line
	cmdLine := input.CommandLine
	if cmdLine == "" {
		cmdLine = input.Command
	}
	if cmdLine == "" && input.ToolCall.Arguments != nil {
		cmdLine = getMapString(input.ToolCall.Arguments, "CommandLine", "command", "cmd")
	}
	if cmdLine == "" && input.ToolCall.Args != nil {
		cmdLine = getMapString(input.ToolCall.Args, "CommandLine", "command", "cmd")
	}
	if cmdLine == "" && input.ToolCallSnake.Arguments != nil {
		cmdLine = getMapString(input.ToolCallSnake.Arguments, "CommandLine", "command", "cmd")
	}
	if cmdLine == "" && input.ToolCallSnake.Args != nil {
		cmdLine = getMapString(input.ToolCallSnake.Args, "CommandLine", "command", "cmd")
	}
	if cmdLine == "" && input.ToolInput != nil {
		cmdLine = getMapString(input.ToolInput, "CommandLine", "command", "cmd")
	}
	if cmdLine == "" && input.ToolInputCamel != nil {
		cmdLine = getMapString(input.ToolInputCamel, "CommandLine", "command", "cmd")
	}

	if strings.TrimSpace(cmdLine) == "" {
		return HookDecision{Decision: "allow"}
	}

	// Extract target workspace / repo directory
	repoDir := "."
	if len(input.WorkspacePaths) > 0 && input.WorkspacePaths[0] != "" {
		repoDir = input.WorkspacePaths[0]
	} else if len(input.WorkspacePathsSnake) > 0 && input.WorkspacePathsSnake[0] != "" {
		repoDir = input.WorkspacePathsSnake[0]
	} else if input.Cwd != "" {
		repoDir = input.Cwd
	}

	return classifyAndEvaluateCommand(cmdLine, repoDir)
}

func classifyAndEvaluateCommand(cmdLine, defaultRepoDir string) HookDecision {
	allCommands := extractAllCommands(cmdLine)

	var hasTamper bool
	var tamperReason string

	var hasBypass bool
	var bypassReason string

	var hasIrreversible bool
	var irreversibleReason string

	var hasMerge bool
	var mergePR int
	var mergeRepo string

	var hasIssueClose bool

	// Check if any command specifies a directory change
	for _, sub := range allCommands {
		if dir := extractCdDir(sub); dir != "" {
			mergeRepo = dir
		}
	}

	for _, sub := range allCommands {
		if ok, reason := checkTamper(sub); ok {
			hasTamper = true
			tamperReason = reason
			break
		}
		if ok, reason := checkBypass(sub); ok {
			hasBypass = true
			bypassReason = reason
		}
		if ok, reason := checkIrreversible(sub); ok {
			hasIrreversible = true
			irreversibleReason = reason
		}
		if ok, pr, repo := checkMerge(sub); ok {
			hasMerge = true
			if pr > 0 {
				mergePR = pr
			}
			if repo != "" {
				mergeRepo = repo
			}
		}
		if checkIssueClose(sub) {
			hasIssueClose = true
		}
	}

	// Priority 1: T4 tamper -> deny
	if hasTamper {
		return HookDecision{
			Decision: "deny",
			Reason:   tamperReason,
		}
	}

	// Priority 2: Bypass flags -> deny
	if hasBypass {
		return HookDecision{
			Decision: "deny",
			Reason:   bypassReason,
		}
	}

	// Priority 3: T4 irreversible -> force_ask
	if hasIrreversible {
		return HookDecision{
			Decision: "force_ask",
			Reason:   irreversibleReason,
		}
	}

	// Priority 4: Merge commitment point -> gate check
	if hasMerge {
		repoToUse := defaultRepoDir
		if mergeRepo != "" {
			repoToUse = mergeRepo
		}
		return runMergeGateCheck(repoToUse, mergePR)
	}

	// Priority 5: Issue close -> allow with whisper
	if hasIssueClose {
		return HookDecision{
			Decision: "allow",
			Reason:   "[jit_whisper] Ensure every acceptance criterion on the issue has verified evidence before closing (G7 in Wave B).",
		}
	}

	// All other commands: allow
	return HookDecision{Decision: "allow"}
}

// splitShellCommands splits on top-level &&, ||, ;, \n, |, & honoring quotes.
func splitShellCommands(cmd string) []string {
	var results []string
	var cur strings.Builder
	var inSingle, inDouble, inBacktick, escape bool

	runes := []rune(cmd)
	n := len(runes)

	for i := 0; i < n; i++ {
		ch := runes[i]
		if escape {
			cur.WriteRune(ch)
			escape = false
			continue
		}
		if ch == '\\' && !inSingle {
			escape = true
			cur.WriteRune(ch)
			continue
		}
		if ch == '\'' && !inDouble && !inBacktick {
			inSingle = !inSingle
			cur.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingle && !inBacktick {
			inDouble = !inDouble
			cur.WriteRune(ch)
			continue
		}
		if ch == '`' && !inSingle {
			inBacktick = !inBacktick
			cur.WriteRune(ch)
			continue
		}

		if !inSingle && !inDouble && !inBacktick {
			if ch == '\n' || ch == ';' {
				part := strings.TrimSpace(cur.String())
				if part != "" {
					results = append(results, part)
				}
				cur.Reset()
				continue
			}
			if ch == '&' {
				if i+1 < n && runes[i+1] == '&' {
					i++
					part := strings.TrimSpace(cur.String())
					if part != "" {
						results = append(results, part)
					}
					cur.Reset()
					continue
				}
				part := strings.TrimSpace(cur.String())
				if part != "" {
					results = append(results, part)
				}
				cur.Reset()
				continue
			}
			if ch == '|' {
				if i+1 < n && runes[i+1] == '|' {
					i++
					part := strings.TrimSpace(cur.String())
					if part != "" {
						results = append(results, part)
					}
					cur.Reset()
					continue
				}
				part := strings.TrimSpace(cur.String())
				if part != "" {
					results = append(results, part)
				}
				cur.Reset()
				continue
			}
		}
		cur.WriteRune(ch)
	}

	part := strings.TrimSpace(cur.String())
	if part != "" {
		results = append(results, part)
	}
	return results
}

var shellCRegex = regexp.MustCompile(`(?i)(?:^|/|\s)(?:bash|sh|zsh)\b[^\n;&]*\s-[a-zA-Z]*c[a-zA-Z]*\s+`)

func extractShellCPayload(s string) (string, bool) {
	loc := shellCRegex.FindStringIndex(s)
	if loc == nil {
		return "", false
	}

	rest := strings.TrimSpace(s[loc[1]:])
	if rest == "" {
		return "", false
	}

	if rest[0] == '\'' {
		end := strings.Index(rest[1:], "'")
		if end != -1 {
			return rest[1 : 1+end], true
		}
		return rest[1:], true
	}

	if rest[0] == '"' {
		var buf strings.Builder
		escape := false
		for i := 1; i < len(rest); i++ {
			c := rest[i]
			if escape {
				buf.WriteByte(c)
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				return buf.String(), true
			}
			buf.WriteByte(c)
		}
		return buf.String(), true
	}

	// Unquoted token
	fields := strings.Fields(rest)
	if len(fields) > 0 {
		return fields[0], true
	}
	return "", false
}

var (
	subshellDollarRegex = regexp.MustCompile(`\$\(([^)]+)\)`)
	subshellBacktickRegex = regexp.MustCompile("`([^`]+)`")
)

func extractSubshells(s string) []string {
	var subs []string
	for _, m := range subshellDollarRegex.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
			subs = append(subs, strings.TrimSpace(m[1]))
		}
	}
	for _, m := range subshellBacktickRegex.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
			subs = append(subs, strings.TrimSpace(m[1]))
		}
	}
	return subs
}

func extractAllCommands(cmd string) []string {
	var all []string
	visited := make(map[string]bool)

	var walk func(s string)
	walk = func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || visited[s] {
			return
		}
		visited[s] = true
		all = append(all, s)

		if payload, ok := extractShellCPayload(s); ok {
			walk(payload)
		}

		for _, sub := range extractSubshells(s) {
			walk(sub)
		}

		parts := splitShellCommands(s)
		if len(parts) > 1 {
			for _, p := range parts {
				walk(p)
			}
		}
	}

	walk(cmd)
	return all
}

func targetsProtectedPath(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "hooks.json") ||
		strings.Contains(lower, "agy-hooks.json") ||
		strings.Contains(lower, ".local/state/g8s/f0") ||
		strings.Contains(lower, "global-ledger.jsonl") ||
		strings.Contains(lower, ".git/f0") ||
		strings.Contains(lower, "f0/ledger.jsonl")
}

var mutatingVerbRegex = regexp.MustCompile(`(?i)(?:^|[\s|;&])(?:rm|unlink|shred|mv|cp|touch|truncate|tee|chmod|chown)\b`)
var sedInPlaceRegex = regexp.MustCompile(`(?i)\bsed\b[^\n;&]*(?:-i|--in-place)`)
var gitMutatingRegex = regexp.MustCompile(`(?i)\bgit\s+(?:rm|restore|checkout\s+--)\b`)

func checkTamper(cmd string) (bool, string) {
	if !targetsProtectedPath(cmd) {
		return false, ""
	}

	// Check redirection > or >>
	if strings.Contains(cmd, ">") {
		// Verify > is not just 2>&1
		clean := strings.ReplaceAll(cmd, "2>&1", "")
		clean = strings.ReplaceAll(clean, "2>&-", "")
		if strings.Contains(clean, ">") {
			return true, "T4 tamper: modification of protected f0 state or hooks configuration is denied"
		}
	}

	if mutatingVerbRegex.MatchString(cmd) {
		return true, "T4 tamper: modification of protected f0 state or hooks configuration is denied"
	}
	if sedInPlaceRegex.MatchString(cmd) {
		return true, "T4 tamper: modification of protected f0 state or hooks configuration is denied"
	}
	if gitMutatingRegex.MatchString(cmd) {
		return true, "T4 tamper: modification of protected f0 state or hooks configuration is denied"
	}

	return false, ""
}

var (
	gitCommitFlagNRegex = regexp.MustCompile(`(?i)\bgit\s+commit\b[^\n;&]*\s-[a-zA-Z]*n[a-zA-Z]*\b`)
	huskyRegex          = regexp.MustCompile(`(?i)\bHUSKY=(?:0|false|"0"|'0'|""|'')\b`)
	lefthookRegex       = regexp.MustCompile(`(?i)\bLEFTHOOK=(?:0|false|"0"|'0'|""|'')\b`)
	skipRegex           = regexp.MustCompile(`(?i)\bSKIP=\S+`)
)

func checkBypass(cmd string) (bool, string) {
	if strings.Contains(cmd, "--no-verify") {
		return true, "bypass flags (--no-verify, -n, HUSKY=0, LEFTHOOK=0, SKIP=) are denied: all commits and merges must run verifiers"
	}
	if gitCommitFlagNRegex.MatchString(cmd) {
		return true, "bypass flags (--no-verify, -n, HUSKY=0, LEFTHOOK=0, SKIP=) are denied: all commits and merges must run verifiers"
	}
	if huskyRegex.MatchString(cmd) {
		return true, "bypass flags (--no-verify, -n, HUSKY=0, LEFTHOOK=0, SKIP=) are denied: all commits and merges must run verifiers"
	}
	if lefthookRegex.MatchString(cmd) {
		return true, "bypass flags (--no-verify, -n, HUSKY=0, LEFTHOOK=0, SKIP=) are denied: all commits and merges must run verifiers"
	}
	if skipRegex.MatchString(cmd) {
		return true, "bypass flags (--no-verify, -n, HUSKY=0, LEFTHOOK=0, SKIP=) are denied: all commits and merges must run verifiers"
	}
	return false, ""
}

var (
	ghReleaseCreateRegex = regexp.MustCompile(`(?i)\bgh\s+release\s+create\b`)
	ghRepoEditRegex      = regexp.MustCompile(`(?i)\bgh\s+repo\s+edit\b`)
	ghRepoDeleteRegex    = regexp.MustCompile(`(?i)\bgh\s+repo\s+delete\b`)
	ghSecretRegex        = regexp.MustCompile(`(?i)\bgh\s+secret\b`)
	ghAuthRegex          = regexp.MustCompile(`(?i)\bgh\s+auth\b`)
	gitPushRegex         = regexp.MustCompile(`(?i)\bgit\s+push\b`)
	gitPushForceRegex    = regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&]*(?:--force\b|--force-with-lease\b|--force-if-includes\b|\s-[a-zA-Z]*f[a-zA-Z]*\b)`)
	gitPushTagsRegex     = regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&]*--tags\b`)
	gitPushDeleteRegex   = regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&]*(?:--delete\b|\s-[a-zA-Z]*d[a-zA-Z]*\b)`)
	gitPushColonRegex    = regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&]*\s:(?:[a-zA-Z0-9_\-\.\/]+|\s*$)`)
)

func checkIrreversible(cmd string) (bool, string) {
	if ghReleaseCreateRegex.MatchString(cmd) {
		return true, "T4 irreversible operation (gh release create): requires operator (PiC) confirmation"
	}
	if ghRepoEditRegex.MatchString(cmd) {
		return true, "T4 irreversible operation (gh repo edit): requires operator (PiC) confirmation"
	}
	if ghRepoDeleteRegex.MatchString(cmd) {
		return true, "T4 irreversible operation (gh repo delete): requires operator (PiC) confirmation"
	}
	if ghSecretRegex.MatchString(cmd) {
		return true, "T4 irreversible operation (gh secret): requires operator (PiC) confirmation"
	}
	if ghAuthRegex.MatchString(cmd) {
		return true, "T4 irreversible operation (gh auth): requires operator (PiC) confirmation"
	}
	if gitPushRegex.MatchString(cmd) {
		if gitPushTagsRegex.MatchString(cmd) {
			return true, "T4 irreversible operation (git push --tags): requires operator (PiC) confirmation"
		}
		if gitPushForceRegex.MatchString(cmd) {
			return true, "T4 irreversible operation (git push --force): requires operator (PiC) confirmation"
		}
		if gitPushDeleteRegex.MatchString(cmd) {
			return true, "T4 irreversible operation (git push --delete): requires operator (PiC) confirmation"
		}
		if gitPushColonRegex.MatchString(cmd) {
			return true, "T4 irreversible operation (git push delete refspec): requires operator (PiC) confirmation"
		}
	}
	return false, ""
}

var (
	ghPrMergeRegex   = regexp.MustCompile(`(?i)\bgh\s+pr\s+merge\b`)
	ghApiMergeRegex  = regexp.MustCompile(`(?i)\bgh\s+api\b[^\n;&]*pulls/(\d+)/merge`)
	gitPushMainRegex = regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&]*(?:\s+(?:[^\s;&]+[/])?(?:main|master)\b|:(?:main|master)\b)`)
	prUrlRegex       = regexp.MustCompile(`pull/(\d+)`)
	prFlagRegex      = regexp.MustCompile(`--pr(?:=|\s+)(\d+)`)
	cdDirRegex       = regexp.MustCompile(`(?i)(?:^|[\s;&])cd\s+([^\s;&]+)`)
	repoFlagRegex    = regexp.MustCompile(`(?:-R|--repo)(?:=|\s+)(\S+)`)
)

func extractCdDir(cmd string) string {
	m := cdDirRegex.FindStringSubmatch(cmd)
	if len(m) > 1 {
		return strings.Trim(m[1], `"'`)
	}
	return ""
}

func extractPRNumber(cmd string) int {
	if m := ghApiMergeRegex.FindStringSubmatch(cmd); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	if m := prUrlRegex.FindStringSubmatch(cmd); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	if m := prFlagRegex.FindStringSubmatch(cmd); len(m) > 1 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	if ghPrMergeRegex.MatchString(cmd) {
		tokens := strings.Fields(cmd)
		for i, tok := range tokens {
			if strings.EqualFold(tok, "merge") && i+1 < len(tokens) {
				for j := i + 1; j < len(tokens); j++ {
					t := tokens[j]
					if strings.HasPrefix(t, "-") {
						if t == "-R" || t == "--repo" {
							j++ // skip repo arg
						}
						continue
					}
					if n, err := strconv.Atoi(t); err == nil {
						return n
					}
				}
			}
		}
	}
	return 0
}

func extractRepoFlag(cmd string) string {
	if m := repoFlagRegex.FindStringSubmatch(cmd); len(m) > 1 {
		p := strings.Trim(m[1], `"'`)
		// Check if it's a directory on disk
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

func checkMerge(cmd string) (bool, int, string) {
	isMerge := false
	if ghPrMergeRegex.MatchString(cmd) || ghApiMergeRegex.MatchString(cmd) || gitPushMainRegex.MatchString(cmd) {
		isMerge = true
	}
	if !isMerge {
		return false, 0, ""
	}
	pr := extractPRNumber(cmd)
	repo := extractRepoFlag(cmd)
	return true, pr, repo
}

func checkIssueClose(cmd string) bool {
	return regexp.MustCompile(`(?i)\bgh\s+issue\s+close\b`).MatchString(cmd)
}

func runMergeGateCheck(repoDir string, pr int) HookDecision {
	absRepo, err := filepath.Abs(repoDir)
	if err == nil {
		repoDir = absRepo
	}

	type gateRes struct {
		out *GateOutput
		err error
	}

	ctx, cancel := context.WithTimeout(context.Background(), gateCheckTimeout)
	defer cancel()

	ch := make(chan gateRes, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- gateRes{err: fmt.Errorf("panic in gate check: %v", r)}
			}
		}()
		out, err := EvaluateGate(GateOptions{
			RepoDir: repoDir,
			Action:  "merge",
			PR:      pr,
		})
		ch <- gateRes{out: out, err: err}
	}()

	select {
	case <-ctx.Done():
		return HookDecision{
			Decision: "deny",
			Reason:   "f0 gate check timed out after 25s (fail-safe deny)",
		}
	case res := <-ch:
		if res.err != nil {
			return HookDecision{
				Decision: "deny",
				Reason:   fmt.Sprintf("f0 gate check failed with error: %v (fail-safe deny)", res.err),
			}
		}
		if res.out.Overall == "PASS" {
			return HookDecision{
				Decision: "allow",
			}
		}

		var failing []string
		for _, c := range res.out.Checks {
			if c.Status == "FAIL" {
				failing = append(failing, fmt.Sprintf("%s (%s)", c.ID, c.Detail))
			}
		}
		failingSummary := strings.Join(failing, ", ")

		prFlag := ""
		if pr > 0 {
			prFlag = fmt.Sprintf(" --pr %d", pr)
		}
		return HookDecision{
			Decision: "deny",
			Reason:   fmt.Sprintf("f0 gate check FAILED: %s. Run: f0 status --repo %s%s to inspect ground truth.", failingSummary, repoDir, prFlag),
		}
	}
}

func runHookStop(args []string) int {
	var input StopInput
	data, err := io.ReadAll(os.Stdin)
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &input)
	}

	dec := evaluateStopHook(input)
	return writeDecision(dec)
}

func evaluateStopHook(input StopInput) HookDecision {
	transcriptPath := input.TranscriptPath
	if transcriptPath == "" {
		transcriptPath = input.TranscriptPathSnake
	}

	repoDir := "."
	if len(input.WorkspacePaths) > 0 && input.WorkspacePaths[0] != "" {
		repoDir = input.WorkspacePaths[0]
	} else if len(input.WorkspacePathsSnake) > 0 && input.WorkspacePathsSnake[0] != "" {
		repoDir = input.WorkspacePathsSnake[0]
	}

	turnID := input.TurnID
	if turnID == "" {
		turnID = input.TurnIDSnake
	}
	if turnID == "" {
		turnID = input.SessionID
	}
	if turnID == "" {
		turnID = input.SessionIDSnake
	}

	lastMsg := input.LastAssistantMessage
	if lastMsg == "" {
		lastMsg = input.LastAssistantMessageSnake
	}
	if lastMsg == "" {
		lastMsg = input.Message
	}

	var assistantText string
	var lastUserStep any

	if transcriptPath != "" {
		txt, uStep, err := extractAssistantMessageFromTranscript(transcriptPath)
		if err == nil && txt != "" {
			assistantText = txt
			lastUserStep = uStep
		}
	}
	if assistantText == "" && lastMsg != "" {
		assistantText = lastMsg
	}

	if strings.TrimSpace(assistantText) == "" {
		return HookDecision{Decision: "allow"}
	}

	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		absRepo = repoDir
	}

	cfg, err := LoadConfig(absRepo)
	if err != nil {
		cfg = &Config{CI: "none", VerifyCommands: defaultVerifyCommands(absRepo)}
	}

	repoLedger, _ := OpenRepoLedger(absRepo)
	forge, _ := GetForgeAdapter(absRepo)

	violations, err := LintClaims(assistantText, "report", absRepo, "", cfg.CI, repoLedger, forge)
	if err != nil || len(violations) == 0 {
		return HookDecision{Decision: "allow"}
	}

	// Determine turn key for continuation cap
	turnKey := turnID
	if turnKey == "" && lastUserStep != nil {
		turnKey = fmt.Sprintf("%s:user-%v", transcriptPath, lastUserStep)
	} else if turnKey == "" && transcriptPath != "" {
		turnKey = transcriptPath
	} else if turnKey == "" {
		turnKey = "default"
	}

	globalLedger, lErr := OpenGlobalLedger()
	if lErr != nil || globalLedger == nil {
		return HookDecision{
			Decision: "allow",
			Reason:   "global ledger unavailable for stop-hook cap check",
		}
	}

	entries, _ := globalLedger.ReadEntries()
	stopHookCount := 0
	for _, e := range entries {
		if e.Kind != "stop-hook" {
			continue
		}
		if e.Extra != nil {
			if tk, ok := e.Extra["turn_key"].(string); ok && tk == turnKey {
				stopHookCount++
				continue
			}
			if turnID == "" && lastUserStep == nil {
				if tp, ok := e.Extra["transcript_path"].(string); ok && tp == transcriptPath {
					stopHookCount++
					continue
				}
			}
		}
	}

	if stopHookCount < 2 {
		entry := &Entry{
			ID:           newEvidenceID(),
			Ts:           time.Now().UTC().Format(time.RFC3339),
			Kind:         "stop-hook",
			Repo:         absRepo,
			Status:       "CONTINUE",
			Cmd:          []string{"hook", "stop"},
			ItemsChecked: len(violations),
			Extra: map[string]any{
				"turn_key":        turnKey,
				"transcript_path": transcriptPath,
				"violations":      len(violations),
			},
		}
		_ = globalLedger.Append(entry)

		var vMsgs []string
		for _, v := range violations {
			vMsgs = append(vMsgs, fmt.Sprintf("%s (%s)", v.Line, v.Reason))
		}
		reason := fmt.Sprintf("Uncited status claims: %s. Cite ev-xxxxxxxx or actions/runs/<id> or remove the claim.", strings.Join(vMsgs, "; "))
		return HookDecision{
			Decision: "continue",
			Reason:   reason,
		}
	}

	return HookDecision{
		Decision: "allow",
		Reason:   "stop-hook continuation cap (2) reached",
	}
}

func extractAssistantMessageFromTranscript(transcriptPath string) (string, any, error) {
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return "", nil, err
	}

	var lastAssistantText string
	var lastUserStep any

	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		var list []map[string]any
		if err := json.Unmarshal(trimmed, &list); err == nil {
			for idx, item := range list {
				stepType, _ := item["type"].(string)
				role, _ := item["role"].(string)
				source, _ := item["source"].(string)

				if stepType == "USER_INPUT" || role == "user" || source == "USER_EXPLICIT" || source == "USER" {
					if sIdx := item["step_index"]; sIdx != nil {
						lastUserStep = sIdx
					} else if sIdx := item["stepIdx"]; sIdx != nil {
						lastUserStep = sIdx
					} else {
						lastUserStep = idx + 1
					}
				}

				if stepType == "PLANNER_RESPONSE" || role == "assistant" || source == "MODEL" {
					txt := extractContentString(item["content"])
					if txt != "" {
						lastAssistantText = txt
					}
				}
			}
			return lastAssistantText, lastUserStep, nil
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		stepType, _ := item["type"].(string)
		role, _ := item["role"].(string)
		source, _ := item["source"].(string)

		if stepType == "USER_INPUT" || role == "user" || source == "USER_EXPLICIT" || source == "USER" {
			if sIdx := item["step_index"]; sIdx != nil {
				lastUserStep = sIdx
			} else if sIdx := item["stepIdx"]; sIdx != nil {
				lastUserStep = sIdx
			} else {
				lastUserStep = lineNum
			}
		}

		if stepType == "PLANNER_RESPONSE" || role == "assistant" || source == "MODEL" {
			txt := extractContentString(item["content"])
			if txt != "" {
				lastAssistantText = txt
			}
		}
	}

	return lastAssistantText, lastUserStep, scanner.Err()
}

func extractContentString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]any); ok {
		var parts []string
		for _, elem := range arr {
			if m, ok := elem.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			} else if s, ok := elem.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return fmt.Sprint(v)
}
