# Cursor IDE Integration Guide

Use `g8s` inside **Cursor IDE** via Cursor's native Model Context Protocol (MCP) server support.

---

## 1. Open Cursor MCP Settings

1. Open Cursor Settings (`Cmd + ,` on macOS, `Ctrl + ,` on Linux/Windows).
2. Navigate to **Features** $\rightarrow$ **MCP Servers**.
3. Click **+ Add New MCP Server**.

---

## 2. Configure g8s Server

Fill in the server details:

* **Name**: `g8s`
* **Type**: `command`
* **Command**: `g8s mcp` (or full path `~/.local/bin/g8s mcp`)

Alternatively, configure your project's `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "g8s": {
      "command": "g8s",
      "args": ["mcp"]
    }
  }
}
```

---

## 3. Usage with Cursor Composer / Chat

In Cursor Chat (`Cmd + L`) or Composer (`Cmd + I`):

```text
Use g8s_dispatch to execute a unit test synthesis task with role "test-runner" on ./internal/receipt/...
```

Cursor will interact with the local `g8s` SQLite control plane to orchestrate workers without locking up the editor.
