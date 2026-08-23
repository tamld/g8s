# OpenSpec DELTA-04: Stdio JSON-RPC 2.0 Model Context Protocol (MCP) Server

**Status**: `PROPOSED`  
**Milestone**: M2 (Capabilities)  
**Package**: `internal/mcp`  

---

## 1. Goal & Context
Expose `g8s` capabilities as a standard Model Context Protocol (MCP) server over Unix Stdio (JSON-RPC 2.0). This allows Claude Desktop, Cursor, Codex, and Windsurf to seamlessly dispatch worker tasks, query resource pools, and issue write receipts.

---

## 2. Supported MCP Tools

1. **`g8s_run`**: Synchronously execute an isolated task with progress notifications.
2. **`g8s_submit`**: Asynchronously queue a durable background task.
3. **`g8s_get`**: Fetch task status and sanitized execution output.
4. **`g8s_receipt_issue`**: Issue a path-scoped, time-limited single-use Write Receipt.
5. **`g8s_self_awareness`**: Query active providers, model availability, and concurrency slots.
6. **`g8s_blast_radius`**: Query LSP call hierarchy and AST impact score for a target symbol.

---

## 3. Go Interface Definition

```go
package mcp

import "context"

type MCPServer interface {
    ServeStdio(ctx context.Context) error
    RegisterTools() error
}
```
