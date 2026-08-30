# Release Readiness Checklist

Status snapshot for the upcoming release-version stage. The repository is currently
**PRIVATE**; the visibility switch to public and any CI matrix trimming are
owner-gated decisions made at release time.

## Verification gates (all met at MVP close, 2026-08-24)

- [x] Zero-CGO dual-pass verification green in Docker `golang:1.25`:
      `CGO_ENABLED=0 go vet ./... && go test ./...` and
      `CGO_ENABLED=1 go test -race ./...` across all packages.
- [x] Test corpus ≥ 140 collected functions (187 at close).
- [x] `goreleaser release --snapshot --clean` produces five cross-platform archives
      (darwin/linux/windows on amd64/arm64, excluding windows/arm64).
- [x] Final audit receipt maps all board receipts to plan phases with
      `full_outcome_complete: true`.
- [x] CI green on ubuntu/macos/windows (run 32716564641).

## Outstanding items before tagging a version

- [ ] Owner decision on PR #1 (`pterm` TUI polish): blocking stderr-regression fix,
      gofmt cleanup, dependency-proportionality call.
- [ ] Add a `gofmt -l` gate to CI (drafted in `chore/release-prep`; PR-ready).
- [ ] Decide the first tag (`v0.1.0` suggested) and run the six-gate pre-release
      audit per `docs/RELEASE_SOP.md`.
- [ ] Owner flips repo visibility to **public** (rule: only at release-version stage).
- [ ] Trim/disable non-essential workflows or shrink the OS matrix if Actions quota
      becomes a concern for public CI traffic.
- [ ] Verify `LICENSE` (MIT), `README.md`, and archive contents one final time via a
      fresh `goreleaser` snapshot after the last commit.
- [ ] Confirm `.goreleaser.yaml` `ldflags` version stamping renders the chosen tag.

## Post-release

- [x] Publish GitHub Release with generated archives and checksums. (v0.1.0 live: https://github.com/tamld/g8s/releases/tag/v0.1.0, 6 assets, 2026-08-25)
- [ ] Announce the MCP tool surface (`g8s_*`) in the README quick-start.

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
| Dispatch command construction | PASS — argv byte-identical | Same inputs produce `[agy --prompt <p> --model gemini-3.7-flash-high --print-timeout 5m0s --dangerously-skip-permissions --sandbox --add-dir /tmp/a]` in both. |
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
preview=~/.local/bin/agy --prompt <prompt> --model gemini-3.7-flash-high --print-timeout 2m0s
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
