# OpenSpec DELTA-13: DX/AX Experience, Interactive Onboarding Wizard & Self-Healing Auto-Remediation

## 1. Context & Objectives
- **Zero-Friction Agent & Developer Onboarding**: Provide an interactive setup wizard (`g8s init`) that auto-detects installed AI IDEs (Claude Desktop, Cursor, Windsurf, Google Antigravity) and safely injects the Stdio MCP server configuration block without data loss.
- **Headless Programmatic Setup**: Provide non-interactive flag mode (`g8s init --agent --ide=<ide> --json`) for automated Docker and CI container environments.
- **Autonomous Self-Healing Diagnostics**: Upgrade `g8s doctor` with `--fix` to automatically repair permission violations (POSIX mode 0600 on SQLite DBs) and reconstruct missing evidence and state directories.
- **Atomic Configuration Store**: Provide `g8s config get|set|list|unset` for safe runtime setting manipulation.
- **Shell Autocompletion**: Provide `g8s completion <bash|zsh|fish>` for full command-line DX completion.

## 2. Architecture & Components

### 2.1. `internal/initwiz`
- Discovers IDE configuration paths across macOS, Linux, and Windows.
- Safely parses and injects `mcpServers.g8s` JSON block via atomic temporary file swap.
- Prepares `~/.local/state/g8s` and `~/.local/state/g8s/evidence` directories (mode `0700`).
- Writes initial default `~/.config/g8s/providers.json` template if absent.

### 2.2. `internal/doctor` (`--fix`)
- Enforces POSIX `0600` permissions on SQLite DB, WAL, and SHM files.
- Creates state and evidence directories with mode `0700`.
- Emits structured `applied_fixes` in text table and JSON outputs.

### 2.3. `internal/settings`
- Atomically loads and mutates `~/.config/g8s/config.json`.
- Enforces strict key validation against `AllowedConfigKeys`.

### 2.4. `internal/completion`
- Generates native autocompletion scripts for `bash`, `zsh`, and `fish`.

## 3. Verification & Compliance
- Pass 1: `CGO_ENABLED=0 go test ./...`
- Pass 2: `CGO_ENABLED=1 go test -race ./internal/...`
- Multi-OS cross-compilation verified.
