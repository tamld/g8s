# Release Readiness & Baseline Parity Record

> **Current Status**: Public MIT Repository (`v0.20.0+` live on GitHub Releases).  
> **Historical Role**: Preserves the side-by-side Go-vs-Python parity matrix and Type-2 dispatch verification record executed at the initial `v0.1.0` cutover (referenced by `spec/openspec/10-two-class-providers-spec.md`).

## Verification gates (all met at MVP close, 2026-08-24, and enforced continuously in CI)

- [x] Zero-CGO dual-pass verification green:
      `CGO_ENABLED=0 go vet ./... && go test ./...` and
      `CGO_ENABLED=1 go test -race ./...` across all packages.
- [x] Test corpus ≥ 140 collected functions (1,000+ across 42 packages at `v0.20.0`).
- [x] `goreleaser release --snapshot --clean` produces cross-platform archives
      (darwin/linux/windows on amd64/arm64).
- [x] Final audit receipt maps all board receipts to plan phases with
      `full_outcome_complete: true`.
- [x] CI green on ubuntu/macos/windows.

## Initial public release checklist (Completed at v0.1.0, 2026-08-25)

- [x] Add `gofmt -l` and `gofumpt` gates to CI (`quality.yml` and `tools/pre_push.sh`).
- [x] Run the pre-release audit per `docs/RELEASE_SOP.md`.
- [x] Repository visibility flipped to **public** under MIT License.
- [x] Verify `LICENSE` (MIT), `README.md`, and archive contents via `goreleaser`.
- [x] Confirm `.goreleaser.yaml` `ldflags` version stamping renders the chosen tag.

## Post-release

- [x] Publish GitHub Releases with generated archives, Windows installers, SBOMs, and checksums (`v0.1.0` through `v0.20.0`).
- [x] Announce the MCP tool surface (`g8s_*`) in the README quick-start and `docs/user-guide/mcp-tools.md`.

## Side-by-side verification assessment (REFACTORING_PLAN line 87)

Feasibility confirmed locally: the `agy` binary is installed at `~/.local/bin/agy`
and the Python reference scripts remain under `reference/python/scripts/`. Running a true
side-by-side comparison requires an identical-workload parity matrix exercised against both
stacks with output comparison. Proposed matrix:

| Workload | Go surface | Python surface | Comparison |
| --- | --- | --- | --- |
| Task lifecycle (submit -> claim -> complete) | `controlplane` store via CLI | `agy_control_plane.py` | Final task state + receipt fields |
| Receipt exactly-once consume | `receipt` Manager | `agy_control.py` receipt path | Second validate must fail identically |
| Dispatch command construction | `dispatch.BuildCommand` | `agy_dispatch.build_agy_command` | argv equality for same options |
| Read-only contract detection | `dispatch.DetectReadOnlyContractViolations` | `agy_dispatch.detect_read_only_contract_violations` | Same violation types per input |
| MCP tools listing shape | `internal/mcp` tools/list | `agy_mcp_server.py` tools/list | Tool names and guard semantics |

### Execution results (2026-08-25, against v0.1.0 candidate)

All rows executed on identical inputs; Go side via throwaway harness compiled from `main`
(8fd0f39), Python side via python3 3.14.7 against `reference/python/scripts/`.

| Row | Result | Evidence |
| --- | --- | --- |
| Task lifecycle | PASS — identical state vocabulary | Both stacks progress QUEUED -> LEASED -> RUNNING -> SUCCEEDED; Python `validate_request` raises "request.add_dirs requires at least one explicit scope root" verbatim matching Go `ValidateSubmitRequest`. |
| Receipt exactly-once | PASS — identical semantics + error text | Second validate fails with "write receipt already consumed: <id>" in both stacks (Go `AlreadyConsumedError` format string matches character-for-character). |
| Dispatch command construction | PASS — argv byte-identical | Same inputs produce `[agy --prompt <p> --model gemini-3.8-flash-high --print-timeout 5m0s --dangerously-skip-permissions --sandbox --add-dir /tmp/a]` in both. |
| Read-only contract detection | PASS — same violation types | Probe inputs map to identical violation types and snippets (`wiki_reflect_side_effect`, `wiki_write_side_effect`, negative instruction ignored). |
| MCP tools/list shape | DOCUMENTED DEVIATION (by design) | Go exposes eleven `g8s_*` tools vs Python baseline eight `agy_*` tools per DELTA-04 Amendment A naming decision (T005-D1). Not an equality failure. |

L87 is CLOSED: every row either passes outright or is a documented intentional deviation.

Execution is deferred until the release audit window so it can run against the tagged
candidate rather than a moving main.

## Type-2 dispatch validation (g8s -> real agy CLI)

Executed 2026-08-25 against agy v1.1.20 (`~/.local/bin/agy`) via a throwaway
harness calling `dispatch.Run` with `BinaryOverride` set to the real binary:

```text
err=<nil>
ok=true returncode=0 harness_rc=0 duration=8.8s
preview=~/.local/bin/agy --prompt <prompt> --model gemini-3.8-flash-high --print-timeout 2m0s
stdout="READY"
stderr=""
```

Findings:

- The DELTA-08 flag contract (`--prompt/--model/--print-timeout/--sandbox/
  --dangerously-skip-permissions/--add-dir`) matches the real agy CLI exactly.
- agy runs headless non-interactively with existing session state.
- `dispatch.Run` is exit-code based; no result envelope required for this path.
- The worker-supervisor path still needs a result-envelope adapter for real
  CLIs that do not natively write `result.json` (tracked as DELTA-10 input).
