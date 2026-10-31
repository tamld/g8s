# Equal-peer coordinator experiment — continuation contract

**Session type**: T2 (execution)

Local experiment continuation; no release, push, or merge is authorized. Status: INCOMPLETE. This file contains local runtime paths and is not a public release document.

## Mission and current status

The human wants the actual Codex, AGY, and Zcode platforms to solve one problem as equal coordinators, synchronize accepted knowledge, avoid conflicting writes, preserve quota, and rely on a g8s-owned protocol rather than platform computer use. Each platform must detect its own quota and publish it to the shared ledger. One debate round is complete; do not reopen it. The full goal remains active.

The native g8s mesh core, CLI, and MCP adapter exist in the isolated experiment checkout. Eleven real SQLite core tests pass. Codex has self-published its native quota through the built CLI. Actual AGY ledger participation and actual Zcode native participation/self-quota remain to be proved. This is not a completed three-platform drill.

## Scope and guardrails

- Equal authority among peers; temporary task/integration ownership only. No permanent Codex integrator.
- Work only in the experiment checkout below. Keep the primary main checkout clean.
- F1 code, repair, and log-analysis tasks must use g8s submit/worker, path-scoped receipts, isolated worktrees, and separate state directories. Do not use native host subagents or raw coding CLI as a substitute.
- Existing direct peer communication is human-authorized. Orca terminal CLI is an attention/bootstrap adapter only. The g8s ledger is the protocol authority. Do not silently count GUI or Orca activity as native g8s success.
- No credential/token/config-content reads, credential decryption, new login, provider entitlement forgery, external upload, push, merge, or release. Existing native runtimes may normally consume their configured credentials.
- Preserve raw failures. Accepted send, process exit, worker verdict, ACK, and artifact acceptance have different meanings. Verify actual output and physical diffs. No automatic model-shopping or consumed-task resubmission.
- Codex quota was observed at 12% remaining in its five-hour window at 2026-10-29T18:17:23Z. Preserve its remaining budget; let AGY take the next temporary execution lead after a verified ledger ACK.

## Current state

Observed checkout: `/private/tmp/g8s-mesh-20261030/worktree`, branch `codex/coordinator-mesh-experiment`, HEAD `f3ced6add2f48951fe669031d826fe9b5570d5c8`.
Primary checkout of `github.com/tamld/g8s` is clean at the same HEAD. All experiment edits are intentional and uncommitted.

Modified tracked files: `cmd/g8s/coordinator.go`, `internal/cli/registry.go`, `internal/mcp/server.go`.
New files: `cmd/g8s/coordinator_mesh.go`, `internal/mcp/mesh.go`, `internal/mcp/mesh_test.go`, `internal/mesh/{types,store,messages,messages_test,claims,claims_test}.go`, `spec/openspec/46-peer-coordinator-ledger-spec.md`, `plans/261030-coordinator-interoperation/`, this continuation contract.

Runtime root: `/private/tmp/g8s-mesh-20261030` (P). Built executable: `P/g8s-experiment` (includes core, CLI, MCP). Shared same-host ledger: `P/shared-ledger`. Codex worker queue: `P/runtime-codex`; do not reuse it concurrently. AGY should use `P/runtime-agy` for its own bounded workers.

There are no live source workers. Declarations task `8192ff7b-e222-46bb-b0c2-bf74cafbe225` reached WORKER_COMPLETED with `ok=false`, timeout 300 seconds; its worker checkout was removed, so do not assume declarations were delivered. Existing delivered source worker checkouts remain under `P/runtime-codex/worktrees`.

## Decisions and rationale

- Native Go CLI and MCP call the same provider-independent mesh API. SQLite WAL is the local same-host ledger for this experiment. A future remote/distributed transport must preserve the contract; sharing a WAL database over network filesystems is not the design.
- Knowledge synchronization uses revision-bound artifact references and SHA-256, not repeated conversation replay. Scoped claims serialize conflicting work; lease/claim records do not physically fence Git or arbitrary OS writes.
- Quota is self-observed with provenance, timestamp, nullable unknown windows, and reset metadata. Token consumption is not subscription remaining. Cached observations must remain visibly stale.
- Zero-LLM `wait` is bounded to 60 seconds. Idempotent send, durable recipient inbox, recipient-only ACK, incarnation checks, scoped claims, and artifact acceptance are distinct operations.
- D2 equal-peer and budget contract supersedes D1's fixed Codex integration assignment. See the plan directory and private debate responses under P.

## Work performed

Core implementation was produced by native AGY Gemini 3.8 Flash medium workers THROUGH g8s, reviewed as physical source diffs, and delivered to the experiment checkout. Delivered tasks: base `aaf549b2-9666-410b-a972-86e80d0bc819`; messages `78997ce4-1c36-45c9-805f-f7fe93b63733`; claims `ace9070a-1002-4aad-b64f-7a98e539b90c`; CLI `978f28a7-cbd0-41eb-a135-9b3060dccf75`; MCP `779dd809-7dfa-440d-8052-ae30876537b2`. Their packets and receipts are under `P/packets/` and `P/runtime-codex`.

The native Codex usage tool supplied its observation. CLI register and quota-publish succeeded, and CLI snapshot physically returned the Codex node and quota. Actor: node `codex`, incarnation `codex-01a18893-native`. Shared revision is currently 0. No AGY/Zcode actor has been registered by Codex; peers must register and publish themselves.

Three 9router source attempts failed (two timeouts, one unsupported Cursor IDE tool); do not restart them. The failed declarations worker overran its bounded documentation packet and timed out; preserve its raw result rather than treating it as delivered.

Native Zcode app-server was investigated with documented bundled interfaces and the existing session. Resume worked; session/send returned accepted, then a terminal prompt_failed was observed. Empty model.available and last_outcome error prove no successful native quota task. Only the adapter process created for this experiment was closed; original desktop processes were left intact. No inference retry is pending.

## Verification

- `GOCACHE=P/go-cache GOPROXY=off CGO_ENABLED=0 go build -o P/g8s-experiment ./cmd/g8s`: built executable exists and native CLI operations above succeed.
- `go test -json -count=1 ./internal/mesh`: 11 core tests pass, covering overlapping claims single winner, disjoint scopes, revision/idempotence, expiry, traversal/replay rejection, symmetric peer rights, message dedup, quota ownership, reopen durability, stale incarnation, wait cancellation. Summary: `P/core-tests-summary.json`.
- `go test -json -count=1 ./internal/mesh ./internal/mcp ./internal/cli`: 98 individual tests pass; two MCP tests fail. New mesh/MCP tests pass. Failures: `TestToolsListExposesExactlyTheSpecElevenTools` (add mesh tool to exact expected registry; preserve strict assertion) and `TestMCPLLMCallTool` (cause not established; possibly sandbox loopback restriction, needs bounded diagnostics and baseline/appropriate permission). Summary/logs: `P/native-packages-tests-summary.json`, `.jsonl`, `.stderr`.
- Not done: full native CLI/MCP parity and rejection drill; independent review/simplification; complete feature/spec/link/structure gates; actual three-peer native drill; Zcode self-quota; final fresh snapshot; local final artifact acceptance.
- `git diff --check` alone omits untracked additions; inspect the full owned delta too. Do not claim a full suite or production readiness.

## Open risks and blockers

Zcode's standalone account-provider runtime has no admitted models without the authenticated desktop host's native entitlement and runtime-header bridge. Existing desktop IPC is not a public attach endpoint. AGY's source-only binding report is `P/zcode-native-binding.json`. It suggests a credential extraction/decryption path; that path is NOT authorized and MUST NOT be followed. Seek a supported native auth broker/extension inside the already authenticated host. If unavailable, finish unaffected work and report the exact remaining integration requirement; never fabricate quota or entitlement.

Zcode session `session_4b7f13f3508910f51a463175a32b358c`, account provider `account:zai-start-plan`, model `GLM-5.3-Flash`. Native source paths and reverse RPC evidence are in `P/zcode-runtime-paths.json`, `P/zcode-native-binding.json`, `P/zcode-native-admission-verdict.json`, `P/zcode-native-client.py`. Config paths are metadata only, not permission to read their contents. Token-usage methods are not subscription-balance APIs.

AGY's earlier quota artifact `P/agy-quota-observation.json` used native Orca account metadata and agy models, but provider metadata may be cached and one five-hour reset has already passed. AGY must refresh via supported native facilities or explicitly label age/unknown. Never restamp cached data as current.

Declarations/spec46 are PROPOSED, not APPLIED. Required manifest, feature ownership, domain href/test pins, README index work is not delivered. Independent review may identify further core defects.

## Exact next actions

1. **First safe step**: Revalidate checkout/HEAD/diff, read the D2 budget contract, then AGY self-registers on the shared ledger, self-publishes a native quota observation, and sends a `handoff-ready` message to Codex with this file's SHA-256. Codex verifies that ledger event before announcing transfer.
2. After Codex ledger authorization, AGY takes temporary execution lead for this experiment. Claim a bounded task, keep role ownership explicit and renewable through new task IDs after release/expiry. Use g8s workers for code/diagnostics. Do not reopen a debate or require human relay.
3. Repair only the exact MCP tool-list fixture and diagnose the other failure in bounded packets. Complete PROPOSED declarations using small packets; avoid large exploratory workers. Run relevant deterministic gates in the integration checkout.
4. Perform native CLI/MCP parity, rejection, concurrent-claim, stale-incarnation, wait/reopen drills and independent source review. Resolve findings before final acceptance.
5. Safely establish native Zcode participation through an authorized native host bridge; require Zcode to self-detect/publish quota. Actual platform output is mandatory. Do not substitute AGY/Codex simulations or a second instance of a model.
6. Have all three actual peers exchange task-bound messages/ACKs and revision-bound knowledge through g8s, exercise conflict avoidance and temporary integration ownership, then write a concise evidence-backed outcome including remaining limits. Keep the global goal active until the full outcome is achieved.

## Source pointers

- Public charter: root `AGENTS.md`, `spec/constitution.md`, `skills/g8s-supervisor/SKILL.md`, its hard-boundary references.
- Contract: `plans/261030-coordinator-interoperation/peer-budget-contract.md`, `design.md`, `plan.md`, `round-receipt.md`; PROPOSED `spec/openspec/46-peer-coordinator-ledger-spec.md`.
- CLI: `g8s-experiment coordinator mesh --mesh-dir P/shared-ledger --op <operation> --input <JSON file or inline JSON> --json`. CLI omits input as `{}`. Read actual request structs in `internal/mesh` for exact fields; operations register/quota-publish/send/inbox/ack/claim/release/accept/snapshot/wait.
- MCP: `g8s_coordinator_mesh` accepts `{mesh_dir,operation,input}` and calls the same core. Do not assume MCP transport is configured in a native platform until verified.
- Existing AGY peer terminal: `term_5f1fdb61-9463-4cc5-ba2a-944bc2cf9d50`, reachable through authorized `orca terminal send`; CLI delivery is an attention adapter, not ledger proof.
- Codex observation: `P/codex-quota-publish.json`. Raw quota/evidence belongs outside public release files.
