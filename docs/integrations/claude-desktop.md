# Claude Desktop Integration Guide

Connect **Claude 3.7 Sonnet / Opus** in Claude Desktop to `g8s` as an out-of-process Model Context Protocol (MCP) execution and delegation harness.

---

## 1. Locate Configuration File

Open your Claude Desktop configuration file:

* **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`
* **Linux**: `~/.config/Claude/claude_desktop_config.json`

---

## 2. Add g8s MCP Server Configuration

Add `g8s` under the `mcpServers` section:

```json
{
  "mcpServers": {
    "g8s": {
      "command": "g8s",
      "args": ["mcp"],
      "env": {
        "G8S_DB": "/Users/YOUR_USERNAME/.local/state/g8s/g8s.db"
      }
    }
  }
}
```

> **Tip**: If `g8s` is installed in `~/.local/bin/g8s`, specify the full path: `"/Users/YOUR_USERNAME/.local/bin/g8s"`.

---

## 3. Restart Claude Desktop

Restart Claude Desktop. You will see a hammer icon indicating that `g8s` tools are available:

* `g8s_dispatch`: Delegate tasks to subagent roles (`collector`, `scout`, `mcp-mapper`, `test-runner`, `verifier`).
* `g8s_get_task`: Inspect worker execution status.
* `g8s_list_tasks`: Monitor queue throughput.
* `g8s_cancel_task`: Cancel runaway worker tasks.

---

## 4. Prompt Example

Ask Claude Desktop:
```text
Delegate a read-only repository scan to g8s:
Use role "scout" with permission "read_only" on the current workspace to inventory all Go files.
```
Claude Desktop will dispatch the subtask through `g8s`, where the harness enforces POSIX process group sandboxing and role constraints.
