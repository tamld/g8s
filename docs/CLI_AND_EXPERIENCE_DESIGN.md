# g8s CLI Ergonomics & Dual-Audience Experience Design (Human DX + Agent AX)

> **Design Axiom**: *"Tools are abandoned when configuration is painful. Make it 30-second delightful for humans, and 100% deterministic for AI agents."*  
> **Target Personas**: (1) Human Software Engineers (Human DX), (2) Autonomous AI Agents (Agent AX).

---

## 1. Dual-Audience Configuration Matrix

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          DUAL-AUDIENCE ONBOARDING                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│   👨‍💻 PERSONA 1: HUMAN DEVELOPER (DX)    🤖 PERSONA 2: AI AGENT (AX)     │
│   • Interactive Terminal Wizard (`g8s init`) • Non-Interactive Flags (`--agent`)│
│   • Colorized TUI & Progress Spinners     • Pure JSON Output (`--json`) │
│   • Actionable Error Hints ("Did you mean")• Remediation Shell Commands │
│   • Auto Shell Completion (Tab/Fish)      • Atomic CLI Setters (`config set`)│
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Persona 1: Human Developer Experience (Human DX)

### A. The 30-Second Interactive Wizard: `g8s init`
Running `g8s init` launches an interactive terminal wizard that configures the local workstation without reading documentation:

```text
$ g8s init

🛡️ Welcome to g8s (The Gatekeepers) Setup Wizard!

[1/3] Probing installed AI worker backends...
  ✓ Found Antigravity CLI: /usr/local/bin/agy (Gemini 3.7 Flash)
  ✓ Found Claude CLI:      /usr/local/bin/claude (Claude 3.5 Haiku)
  ✗ Ollama Local Backend:  Not found (Optional)

[2/3] Select your AI Development Environment:
  ❯ [●] Claude Desktop (~/Library/Application Support/Claude/claude_desktop_config.json)
    [ ] Cursor (~/.cursor/mcp.json)
    [ ] Windsurf (~/.codeium/windsurf/mcp_config.json)
    [ ] Standalone Terminal CLI only

[3/3] Testing Worker Sandbox & Health...
  ✓ Spawning test worker (Flash Low)... OK (180ms)
  ✓ SQLite state initialized at ~/.local/state/g8s/control-plane.sqlite3
  ✓ MCP server registered successfully!

🎉 You're all set! Restart Claude Desktop or run:
   g8s run --provider agy --role scout --prompt "Scan ./src"
```

---

### B. Self-Diagnostic Health Inspector: `g8s doctor`
Whenever something goes wrong, `g8s doctor` pinpoints the exact failure and offers a one-command fix (`g8s doctor --fix`):

```text
$ g8s doctor

🏥 Running g8s System Diagnostics...

[✓] Pure-Go Binary Integrity (v0.1.0-alpha, darwin/arm64)
[✓] State Directory & Database Permissions (0700/0600)
[✓] Worker Provider: agy (Ready, 10 concurrency slots)
[!] Background Daemon Service: NOT RUNNING
    👉 Fix: Run `g8s service install && g8s service start`
[✓] MCP JSON-RPC Server Interface (Healthy)

Status: 1 Warning found. Run `g8s doctor --fix` to auto-repair.
```

---

### C. Rich Contextual Error Messages (Feynman Principle)
`g8s` never prints raw stack traces or cryptic errors. Every error answers: **What happened**, **Why**, and **How to fix it**:

```text
❌ Error: Write Receipt '7e2b19cf' has EXPIRED (TTL 600s elapsed).

💡 Why this happened:
   Worker #3 attempted to write to 'src/auth/jwt.go' after its 10-minute authorization window closed.

👉 Recommended Actions:
   1. Ask your Supervisor to issue a fresh receipt:
      g8s receipt issue --issuer opus --allow "src/auth/*.go" --ttl 600
   2. Audit remaining active receipts:
      g8s receipt list
```

---

## 3. Persona 2: Autonomous AI Agent Experience (Agent AX)

AI coding agents (Cursor, Claude Code, Codex, Antigravity) tasked with configuring `g8s` for a user require **zero interactivity, strict JSON schemas, and atomic CLI mutations**:

### A. Machine-Readable Diagnostic: `g8s doctor --json`
Returns structured JSON so the agent can self-heal without parsing terminal colors:

```json
{
  "healthy": false,
  "summary": "1 issue detected",
  "checks": [
    {
      "id": "state_permissions",
      "status": "PASS",
      "details": "Mode 0700 on ~/.local/state/g8s"
    },
    {
      "id": "daemon_service",
      "status": "FAIL",
      "remediation_cmd": "g8s service install && g8s service start",
      "auto_fixable": true
    }
  ]
}
```

### B. Silent Auto-Configuration: `g8s init --agent`
Allows an agent to configure `g8s` programmatically in one shot:

```bash
g8s init --agent --ide=cursor --ide=claude-desktop --provider=all --json
```

Output:
```json
{
  "ok": true,
  "status": "CONFIGURED",
  "mcp_configs_updated": [
    "/Users/tamld/.cursor/mcp.json",
    "/Users/tamld/Library/Application Support/Claude/claude_desktop_config.json"
  ],
  "discovered_providers": ["agy", "claude"]
}
```

### C. Atomic Key-Value Setters: `g8s config set`
Prevents agents from corrupting YAML syntax with regex file edits:

```bash
g8s config set providers.gemini.max_concurrency 8
g8s config set security.default_ttl_seconds 1200
```

---

## 4. Complete CLI Command & Helper Tree

```text
g8s
├── init                  # Interactive onboarding wizard (--agent for AI)
├── doctor                # Self-diagnostic health inspector (--fix, --json)
├── run                   # Synchronous worker execution with TUI spinner
│   ├── --provider        # agy | claude | gemini | ollama
│   ├── --role            # collector | scout | mcp-mapper | summarizer | verifier | test-runner
│   ├── --permission      # read_only | automation_read | workspace_write
│   ├── --receipt-id      # Write receipt token from Brain
│   ├── --add-dir         # Scoped directory path (repeatable)
│   └── --timeout         # Execution deadline (e.g. 5m0s)
├── submit                # Queue durable asynchronous task (SQLite WAL)
├── get                   # Retrieve task state & sanitized result (--json)
├── list                  # List active and historical tasks (--state, --limit)
├── cancel                # Terminate running task & kill process tree
├── receipt               # Capability delegation manager
│   ├── issue             # Issue time-limited write receipt (--allow, --ttl)
│   ├── list              # List active unconsumed receipts
│   └── revoke            # Revoke an active receipt before use
├── config                # Configuration manager
│   ├── get               # Read a configuration key
│   └── set               # Atomically update a configuration key
├── service               # Background OS Daemon lifecycle manager
│   ├── install           # Install user-level LaunchAgent/systemd/Windows Svc
│   ├── start             # Start the background worker daemon
│   ├── stop              # Stop the background worker daemon
│   ├── status            # Inspect daemon status and private logs
│   └── uninstall         # Uninstall daemon (preserves SQLite database)
├── mcp                   # Start Stdio JSON-RPC 2.0 MCP server
├── completion            # Generate auto shell completion (bash/zsh/fish)
└── version               # Print version, platform, and license info
```
