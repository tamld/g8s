# Windsurf / Cascade Integration Guide

Connect **Codeium Windsurf** / Cascade to `g8s` via standard Model Context Protocol (MCP).

---

## 1. Configure Windsurf MCP

Open `~/.codeium/windsurf/mcp_config.json` (or click on Cascade MCP configuration in settings):

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

## 2. Interacting from Cascade

Ask Cascade:
```text
Run a background verification task through g8s using role "verifier" to check if all Go tests pass.
```

Cascade communicates through `g8s`'s Stdio JSON-RPC 2.0 interface to trigger async execution.
