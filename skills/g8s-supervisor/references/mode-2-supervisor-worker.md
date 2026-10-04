# Mode 2: Supervisor–Worker Pattern

> For project reconnaissance and mechanical drafts after task-level admission.
> Read `hard-boundary.md` first.

## Role split

| Supervisor: Claude | Worker: g8s-supervised AGY/other provider |
|---|---|
| Chooses debt slice, oracle, scope, and stop conditions | Bounded inventory, extraction, summary, or draft |
| Proves admission and authorizes one task | Returns sanitized cited evidence and unknowns |
| Verifies source, scope, diff, and acceptance checks | Stops on missing input, scope drift, or denied access |
| Accepts/rejects sidecar result and owns integration | Cannot select provider, widen scope, promote knowledge, or perform Git actions |

## Admitted task shapes

| Role | Allowed output | Default permission |
|---|---|---|
| `collector` | Deterministic file/metadata inventory | `read_only` |
| `scout` | Bounded pattern/reference report | `read_only` |
| `summarizer` | Redacted digest from explicit inputs | `read_only` |
| `verifier` | Declared check result | `read_only` |

`test-runner` and `workspace_write` are outside routine use. They need a separately approved
writable campaign, enforceable path scope, receipt proof, isolation, diff/test/rollback audit.

## Admission and dispatch

1. Record objective, read root, role, permission, sandbox, timeout, evidence sink, oracle, and
   zero-change check.
2. Prove current executable digest/provenance and exact callable g8s CLI or MCP schema.
3. Prove worker binary identity and binding. For AGY, do not infer it from a model label, installed
   CLI, g8s source, or documentation.
4. Confirm a task-scoped `read_only` permission, sandbox, root scope, durable receipt fields, and
   redaction contract. Any missing field blocks dispatch.
5. Submit only independent packets with disjoint roots. Do not use direct AGY shell as fallback.
6. Claude checks receipt shape, cited findings, source/oracle, `git status`, and diff. Accept only
   sidecar evidence; otherwise reject, retest, or block.

## Useful parallelism

Concurrent workers must execute against mutually disjoint path subtrees. For the
full decomposition playbook — role matrix, canonical fan-out shapes
(inventory / hunt-verify / digest-join / concurrent drain), budgets, and the
join protocol — see `multi-worker-fanout.md`. Minimal example:

```text
collector: frontmatter/metadata inventory under one explicit subtree
scout: exact rule/reference inventory under a separate subtree
summarizer: digest one already-redacted artifact packet
```

One worker never reviews or continues another worker's output. Claude joins results only after each
packet independently passes its oracle.

## Failure handling

| Result | Supervisor action |
|---|---|
| `kind:"error"` | Stop attempt; reproduce before a g8s defect claim |
| Failed declared oracle | Reject project result; classify g8s optimization only after reproduced g8s transport/contract failure |
| Missing/cited-insufficient output | `EVIDENCE_GAP`; do not broaden prompt/scope |
| Scope drift/unexpected local change | Hard stop; preserve sanitized evidence and run zero-change audit |
| Reproduced g8s defect blocking project | Use Mode 3 packet for PiC review |

Never run `orchestrate`, receipt issuance, cleanup force, write submission, configuration, provider
inspection, credentials, remote Git, or bypass flags as part of routine project dispatch.
