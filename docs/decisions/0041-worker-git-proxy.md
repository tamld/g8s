# ADR-0041: Harness-side git commit proxy for platform_dispatch workers

- **Status**: Accepted (2026-11-08) — operator ratified Option 3 ("anh chọn phương án này")
- **Depends on**: ADR-0032 (containment tiers), ADR-0038 (fence enforcement points), ADR-0039 (provenance trailers), #822
- **Evidence**: the codex lane live-observations (2026-11-07/08) — linked worktrees keep `.git/worktrees/wt-*/` outside the platform sandbox write-jail, so codex workers can never fetch/commit; the workaround (codex = file-work dirty-tree, F0 harvest-commits) produced the stale-tree harvest artifact class (#829's three rounds)

## Context

Platform-dispatch workers (codex) run inside a narrow sandbox whose write-jail covers `[workdir, /tmp, $TMPDIR]`. g8s isolation uses linked worktrees, so the git metadata they need to commit lives in the main checkout's `.git/worktrees/wt-*/` — outside the jail. The result is a lane that can produce file changes but cannot record them; every landing requires manual F0 harvest-commits, which is where the stale-tree artifact class was born (deleted files, reverted hardening, hallucinated flags committed wholesale).

## Decision

The **harness** (the unsandboxed `g8s worker` parent process) becomes a narrow git-commit proxy:

1. **Transport = workdir request markers.** Workers (inside the jail) write `.g8s-git-requests/<uuid>.json` requests; the harness polls (2s, Windows-safe loop) while the provider runs, plus a final sweep after exit.
2. **Validation fail-closed in the harness**: workspace_write permission, per-task opt-in (`allow_worker_commit` in request_json — set by a future submit flag), receipt `AllowedPaths` union via the same `resolveReceiptAllowedPaths` as the fence, path-escape rejection. The jail itself never widens — ADR-0038's enforcement points are untouched; the proxy is a new controlled op THROUGH them.
3. **Commit-only**: fetch/push remain F0-owned (harvest flow unchanged). Committer/author = `g8s-worker <task-id> <actor>` with a mandatory `Receipt: <task-id>` trailer — ADR-0039 provenance is built-in, not bolted on.
4. **Result markers** (`<uuid>.result.json`) answer the worker in its own jail: ok+sha or typed `E_*` reason. Requests persist as the audit trail.
5. **Windows**: `E_COMMIT_REQUEST_UNSUPPORTED_OS` — fail-loud, dirty-tree doctrine unchanged there.

## Consequences

- The codex lane can land its own commits (receipt-backed) without widening any sandbox.
- F0 harvest remains for: fetch, push, PR assembly, artifact audit — but no longer as the *only* path to a recorded commit.
- The request dir is worker-writable by design: validation happens at execution time in the harness, so a malicious request is rejected loudly, not silently honored.
- Unknown ops are rejected with a typed result marker (extensible later, e.g. a future `tag` op, without schema break).
