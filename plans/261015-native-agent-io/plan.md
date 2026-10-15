---
title: "Native Cross-Platform Agent I/O (Read, Edit, Select) Across CLI, MCP, and HTTP API"
issue: 593
status: pending
created: 2026-10-15
---

# Plan: Native Cross-Platform Agent I/O & JSON Selector (`#593`)

- **Session type**: T1 (strategy)
- **Issue**: `tamld/g8s#593`
- **Brainstorm Report**: [`plans/reports/brainstorm-261015-native-crossplatform-agent-io.md`](../reports/brainstorm-261015-native-crossplatform-agent-io.md)
- **Status**: Pending Implementation

---

## 1. Executive Summary

Following the delivery of `g8s search` (`#591`), this plan implements the remaining top-frequency cross-platform primitives identified in the empirical audit (**Cat 1–4**, covering **94%+** of external OS tool invocations across `tools/`, `offer/`, and F0/F1 task workflows) using a **1 Zero-Trust Core -> 3 Thin Adapters (`CLI + MCP + HTTP API`)** architecture:

1. **Cat 3 — Bounded File Read (`internal/fsops.Read`)**: Line-range slicing (`--lines M:N`), 512-byte NUL binary guard, `MaxCaptureBytes` cap, full-file `sha256` digest, and central `dispatch.SanitizeOutput` redaction.
2. **Cat 2 — Atomic File Edit (`internal/fsops.Edit`)**: Exact block replacement (`--old` / `--new` / `--all`), optimistic concurrency check (`--if-sha256`), native newline (`LF`/`CRLF`) and permission preservation, and atomic temp-file + `os.Rename` writes.
3. **Cat 4 — Native JSON Dot-Path Selector (`internal/cli.SelectPath`)**: Zero-`jq` field extraction (`--select .data.receipt_id`, `.data.matches[0].file`, `.data.matches[].file`) integrated into `cli.WriteResponse` and exposed via `g8s select <expr>`.
4. **Cat 1 — Shell Gate Migration (`tools/ai_lint.sh` & `tools/brief_lint.sh`)**: Migrate remaining `find | xargs grep` and `jq` pipelines to prioritize `./bin/g8s search` and `./bin/g8s select`.

---

## 2. Phase Breakdown & Wave Routing (`F0 -> F1`)

| Phase | Spec File | Scope (`AllowedPaths`) | Effort Class | Dependencies |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1 (WF1)** | [`phase-01-core-fsops-and-json-selector.md`](phase-01-core-fsops-and-json-selector.md) | `internal/fsops/**`, `internal/cli/**` | `feature` (`high`) | None |
| **Phase 2 (WF2)** | [`phase-02-cli-mcp-and-http-api-surfaces.md`](phase-02-cli-mcp-and-http-api-surfaces.md) | `cmd/g8s/**`, `internal/mcp/**`, `internal/server/**` | `feature` (`high`) | Phase 1 |
| **Phase 3 (WF3)** | [`phase-03-ci-gates-migration-and-verification.md`](phase-03-ci-gates-migration-and-verification.md) | `tools/**`, `offer/**`, `README.md` | `feature` (`medium`) | Phase 2 |

---

## 3. Definition of Done (DoD)

- [ ] `internal/fsops` implements `Read` and `Edit` with Smart Zero-Trust Receipt Resolution (`--receipt-id` -> `G8S_RECEIPT_ID` -> workspace traversal guard) and `>= 85%` statement coverage.
- [ ] `internal/cli` implements `SelectPath` supporting nested object fields (`.a.b`), array indices (`.items[0]`), and array projections (`.items[].field`).
- [ ] CLI exposes `g8s read`, `g8s edit`, `g8s select`, and `--select <path>` on key commands (`receipt`, `submit`, `get`, `search`, `read`, `edit`).
- [ ] MCP server (`internal/mcp/server.go`) registers `g8s_search`, `g8s_read`, and `g8s_edit`.
- [ ] HTTP REST server (`internal/server/server.go`) registers `/api/v1/fs/search`, `/api/v1/fs/read`, and `/api/v1/fs/edit` behind Bearer auth and Receipt enforcement.
- [ ] `tools/ai_lint.sh` and `tools/brief_lint.sh` prioritize `./bin/g8s search` and `./bin/g8s select` before falling back to `rg`/`grep`/`jq`.
- [ ] Passes 3-stage `ck:code-review`, `golangci-lint` (`0` issues), `go test -race`, and all 12 `pre-push` gates.
