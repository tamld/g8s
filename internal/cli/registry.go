package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// CodeUnboundCLIClaim is emitted when a documentation or spec file invokes a
// g8s subcommand that is not registered in the canonical CLI surface contract.
const CodeUnboundCLIClaim = "E_UNBOUND_CLI_CLAIM"

// CommandContract defines the machine-readable surface contract for a top-level
// g8s CLI command (#677 Generative Surface Contract v2).
type CommandContract struct {
	Name          string   `json:"name"`
	Stratum       string   `json:"stratum"`
	EnvelopeKinds []string `json:"envelope_kinds,omitempty"`
	Description   string   `json:"description"`
	Hidden        bool     `json:"hidden,omitempty"`
}

// CanonicalCommandContracts returns the authoritative Generative Surface Contract v2
// for all 66 top-level g8s CLI commands (#677).
func CanonicalCommandContracts() []CommandContract {
	return []CommandContract{
		{Name: "submit", Stratum: "S2", EnvelopeKinds: []string{"task"}, Description: "Queue an asynchronous durable task with harness safety checks"},
		{Name: "get", Stratum: "S2/S3", EnvelopeKinds: []string{"task"}, Description: "Show the durable state of one queued task (g8s get <task-id>)"},
		{Name: "deliver", Stratum: "S2", EnvelopeKinds: []string{"deliver"}, Description: "Apply worktree deliverable to current checkout (g8s deliver <task-id> [--dry-run])"},
		{Name: "resume", Stratum: "S2", EnvelopeKinds: []string{"task"}, Description: "Resume a NEEDS_INFO/BLOCKED task (g8s resume <task-id> [--prompt <text>])"},
		{Name: "tasks", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"tasks"}, Description: "List durable tasks optionally filtered by state (--state, --limit)"},
		{Name: "cancel", Stratum: "S2", EnvelopeKinds: []string{"task"}, Description: "Cancel an active or queued task (g8s cancel <task-id> [--reason <text>])"},
		{Name: "resubmit", Stratum: "S2", EnvelopeKinds: []string{"resubmit"}, Description: "Resubmit a FAILED task with supervisor receipt boundary (g8s resubmit --task <id>)"},
		{Name: "lineage", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"lineage"}, Description: "Show ancestry tree for a task up to root (g8s lineage <task-id>)"},
		{Name: "children", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"children"}, Description: "List direct child subtasks for a task (g8s children <parent-id>)"},
		{Name: "receipt", Stratum: "S2", EnvelopeKinds: []string{"receipt", "receipts", "receipt_verification"}, Description: "Issue write delegation receipts (g8s receipt issue --path <glob>)"},
		{Name: "receipt-export", Stratum: "S2", EnvelopeKinds: []string{"receipt"}, Description: "Recover or inspect durable write receipt for a task (g8s receipt-export --task <id>)"},
		{Name: "doctor", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"doctor"}, Description: "Run diagnostic health and environment sanity checks (g8s doctor [--fix])"},
		{Name: "init", Stratum: "S0/S2", EnvelopeKinds: []string{"init"}, Description: "Initialize g8s and configure IDE MCP integration (g8s init)"},
		{Name: "update", Stratum: "S0/S2", EnvelopeKinds: []string{"update_check", "update_result"}, Description: "Self-update from GitHub releases with SHA-256 verification (g8s update [--check] [--version vX.Y.Z])"},
		{Name: "uninstall", Stratum: "S0/S2", EnvelopeKinds: []string{"uninstall_plan", "uninstall_result"}, Description: "Remove binary, MCP entries and service unit; dry-run unless --yes (g8s uninstall [--purge-state] [--yes])"},
		{Name: "config", Stratum: "S2", EnvelopeKinds: []string{"config"}, Description: "Manage persistent configuration key-values (g8s config get|set|list)"},
		{Name: "completion", Stratum: "S0/S2", Description: "Generate shell autocompletion scripts (g8s completion bash|zsh|fish)"},
		{Name: "service", Stratum: "S0/S2", EnvelopeKinds: []string{"service", "service_status"}, Description: "Manage background daemon lifecycle (g8s service install|start|status)"},
		{Name: "worker", Stratum: "S3-M/S3-A", EnvelopeKinds: []string{"worker_task", "worker_status"}, Description: "Run local background supervisor loop (g8s worker [--once])"},
		{Name: "memory", Stratum: "S2", EnvelopeKinds: []string{"memory"}, Description: "Manage memory entries and revocations (g8s memory list|revoke)"},
		{Name: "offer", Stratum: "S1/S2", EnvelopeKinds: []string{"offer"}, Description: "Scaffold and check repository offer profiles (g8s offer init|check|version)"},
		{Name: "analyze", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"analyze"}, Description: "Quantify code blast radius and recommend write scopes (g8s analyze ...)"},
		{Name: "vault", Stratum: "S2/S3", EnvelopeKinds: []string{"vault_record", "vault_records", "vault_delete"}, Description: "Manage decoupled Tri-Anchor knowledge records (store/query/list)"},
		{Name: "orchestrate", Stratum: "S2", EnvelopeKinds: []string{"orchestrate_result"}, Description: "Run the supervisor self-test loop against the real agy worker"},
		{Name: "orchestrate-aic", Stratum: "S2", EnvelopeKinds: []string{"review"}, Description: "Run AIC automated PR review orchestrator (g8s orchestrate-aic --pr <num> --intent <text>)"},
		{Name: "supervisor-metrics", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"supervisor_metrics", "supervisor_metrics_item"}, Description: "Print supervisor telemetry (--task-id | --aggregate)"},
		{Name: "supervisor-metrics-update-false", Stratum: "S2", EnvelopeKinds: []string{"supervisor_metrics_update_false"}, Description: "Mark escalation as false (g8s supervisor-metrics-update-false --task-id <id> --false)"},
		{Name: "brief-issue", Stratum: "S2", EnvelopeKinds: []string{"brief"}, Description: "Issue a structured task brief with DoD and TTL (g8s brief-issue ...)"},
		{Name: "brief-consume", Stratum: "S2/S3", EnvelopeKinds: []string{"brief"}, Description: "Consume an active brief by ID (g8s brief-consume --id <id>)"},
		{Name: "converge", Stratum: "S2", EnvelopeKinds: []string{"converge"}, Description: "Synthesize N independent solution.md proposals into a converged design"},
		{Name: "sleep", Stratum: "S0/S2", EnvelopeKinds: []string{"sleep_state"}, Description: "Mark operator away and defer non-critical notifications (g8s sleep [--until <time>])"},
		{Name: "wake", Stratum: "S0/S2", EnvelopeKinds: []string{"wake_summary"}, Description: "End sleep cycle and output voice/json summary (g8s wake [--format voice|json])"},
		{Name: "cleanup-worktrees", Stratum: "S2", EnvelopeKinds: []string{"cleanup_report"}, Description: "Clean up stale agy subagent git worktrees (g8s cleanup-worktrees --older-than 1h [--dry-run])"},
		{Name: "cleanup", Stratum: "S2", EnvelopeKinds: []string{"cleanup_report"}, Description: "Run lifecycle sweep for ghost processes, orphan worktrees, branches, tags, receipts"},
		{Name: "migrate", Stratum: "S2", EnvelopeKinds: []string{"migrate"}, Description: "Migrate legacy cwd-relative g8s data to canonical paths (g8s migrate --from ./ [--dry-run])"},
		{Name: "status", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"status_report"}, Description: "Display worker heartbeat and lifecycle observability status (--worker)"},
		{Name: "state", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"state"}, Description: "Show state and replay event logs (g8s state show|replay <id>)"},
		{Name: "mcp", Stratum: "S2/S3", Description: "Serve the Stdio JSON-RPC MCP surface on stdin/stdout"},
		{Name: "reflex", Stratum: "S2", EnvelopeKinds: []string{"reflex_verdict"}, Description: "Run the System-1 reflex gate on a planned mutation (g8s reflex triage --summary ...)"},
		{Name: "search", Stratum: "S2/S3", EnvelopeKinds: []string{"search"}, Description: "High-performance safe code search bounded by delegation context (g8s search <pattern>)"},
		{Name: "read", Stratum: "S2/S3", EnvelopeKinds: []string{"read"}, Description: "Safe bounded file read with line slicing and DLP (g8s read <path>)"},
		{Name: "edit", Stratum: "S3-M", EnvelopeKinds: []string{"edit"}, Description: "Atomic single/all string replacement with SHA-256 verification (g8s edit <path>)"},
		{Name: "select", Stratum: "S2/S3", Description: "Extract values from JSON using dot/index path selector (g8s select <expr>)"},
		{Name: "watch", Stratum: "S2", EnvelopeKinds: []string{"watch_verdict"}, Description: "Block until a watched condition is terminal (g8s watch --pr N / --task ID)"},
		{Name: "eval", Stratum: "S2/S3-A", EnvelopeKinds: []string{"eval"}, Description: "Adversarial probe harness: list or run the suite against a provider (#254)"},
		{Name: "roles", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"roles"}, Description: "List registered worker roles"},
		{Name: "permissions", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"permissions"}, Description: "List registered permission profiles"},
		{Name: "providers", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"providers", "provider_conformance", "models_scorecard", "models_discovery"}, Description: "List detected agent providers and availability status"},
		{Name: "llm-call", Stratum: "S2/S3", EnvelopeKinds: []string{"llm_call"}, Description: "Direct synchronous LLM call consumer against configured providers"},
		{Name: "autopilot", Stratum: "S2", EnvelopeKinds: []string{"autopilot_schedule", "autopilot_config"}, Description: "Manage cron-based supervisor trigger (g8s autopilot start|status|trigger)"},
		{Name: "serve", Stratum: "S2", EnvelopeKinds: []string{"serve"}, Description: "Run daemon mode with HTTP API server (g8s serve [--address :8080])"},
		{Name: "verify", Stratum: "S2/S3-A", EnvelopeKinds: []string{"verdict"}, Description: "Run verifier-class acceptance checks on a task (g8s verify --task <id>)"},
		{Name: "feature", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"feature"}, Description: "Declarative feature map validation and blast radius routing (g8s feature verify|inspect|list)"},
		{Name: "lesson", Stratum: "S2", EnvelopeKinds: []string{"lesson"}, Description: "Record and query operational lessons (g8s lesson record|list)"},
		{Name: "feedback", Stratum: "S2", EnvelopeKinds: []string{"feedback"}, Description: "Record supervisor feedback and escalation outcomes (g8s feedback ...)"},
		{Name: "ladder", Stratum: "S2", EnvelopeKinds: []string{"ladder"}, Description: "Quality-ladder failure classification and escalation (g8s ladder status|advance|gauges)"},
		{Name: "fleet", Stratum: "S1/S2/O-Plane", EnvelopeKinds: []string{"fleet"}, Description: "Cross-repository git & release hygiene scanner (g8s fleet status [--dir <workspace>])"},
		{Name: "charter", Stratum: "S1/S2/O-Plane", EnvelopeKinds: []string{"charter", "portfolio"}, Description: "Orthogonal Stratum Federation charter lifecycle & O-Plane portfolio (g8s charter issue|verify|revoke|complete|list|portfolio)"},
		{Name: "context-pack", Stratum: "S2/S3", EnvelopeKinds: []string{"context_pack"}, Description: "Build bounded pre-mutation context packets (g8s context-pack --paths <p1,p2>)"},
		{Name: "qa-report", Stratum: "S2/S3-A", EnvelopeKinds: []string{"qa_report"}, Description: "Generate mechanical QA verification reports (g8s qa-report --task <id>)"},
		{Name: "claim", Stratum: "S2", EnvelopeKinds: []string{"claim_check"}, Description: "Verify documentation claims against executable anchors (g8s claim verify)"},
		{Name: "f0", Stratum: "S2", EnvelopeKinds: []string{"f0_gate_check", "f0_status"}, Description: "F0 supervisor commitment-point gate and status checks (g8s f0 gate check|status)"},
		{Name: "coordinator", Stratum: "S2", EnvelopeKinds: []string{"coordinator_intake", "coordinator_mastery"}, Description: "Coordinator Cognitive Intake, Root-Core Deconstruction & Domain Mastery Scorecard (g8s coordinator intake|mastery)"},
		{Name: "scorecard", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"scorecard_5axis"}, Description: "DELTA-45 5-Axis Cross-Evaluation & Kaizen Engine (g8s scorecard status|audit|kaizen)"},
		{Name: "audit", Stratum: "S2/O-Plane", EnvelopeKinds: []string{"audit_log"}, Description: "Query transactional enterprise access-audit log (#441: g8s audit list [--actor] [--action])"},
		{Name: "version", Stratum: "S0/S1/S2/S3", EnvelopeKinds: []string{"version"}, Description: "Show application version"},
		{Name: "help", Stratum: "S0/S1/S2/S3", Description: "Show this message"},
		{Name: "internal", Stratum: "S3-M", Description: "Internal supervisor process wrapper hook", Hidden: true},
	}
}

// CanonicalCommands returns the authoritative list of top-level g8s CLI commands.
// Derived directly from CanonicalCommandContracts().
func CanonicalCommands() []string {
	contracts := CanonicalCommandContracts()
	out := make([]string, 0, len(contracts))
	for _, c := range contracts {
		out = append(out, c.Name)
	}
	return out
}

// IsRegisteredCommand reports whether cmd is a registered top-level g8s command
// or a standard version/help flag alias.
func IsRegisteredCommand(cmd string) bool {
	switch cmd {
	case "-v", "--version", "-h", "--help":
		return true
	}
	for _, c := range CanonicalCommandContracts() {
		if c.Name == cmd {
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
