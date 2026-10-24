# Phase 02 — Upgrade F1 `9router` to Bounded Agentic Loop (`K` turns) & Adaptive Effort Timeout

- **Status**: `pending`
- **Targets**: `cmd/g8s/llm_call.go`, `cmd/g8s/llm_call_test.go`, `cmd/g8s/submit.go`, `cmd/g8s/submit_route_test.go`

## Steps
- [x] **2.1 (Bounded Agentic Loop in `llm-call`)**: Add `--max-turns <int>` (default `1`, clamped `1..5`) and `--verify-cmd <string>` to `g8s llm-call`. When `applyFilesEnabled && *verifyCmdFlag != ""`, run `*verifyCmdFlag` inside `scopeRoot` after applying file blocks. If exit code != 0 and `turn < maxTurns`, append verification failure output (`<= 4096` bytes) to the conversation prompt and iterate.
- [x] **2.2 (Adaptive Timeout by Effort Class in `submit`)**: In `cmd/g8s/submit.go`, track whether `--timeout` was explicitly passed (`timeoutPassed`). When `!timeoutPassed`, scale the default timeout based on resolved effort (`effortRes.Applied`):
  - `none`, `minimal`, `low`: `45s`
  - `medium`, `adaptive`, `dynamic`: `90s`
  - `high`, `xhigh`, `max`: `240s`
- [x] **2.3 (Unit Tests)**: Add unit tests in `cmd/g8s/llm_call_test.go` (multi-turn self-repair on failing verify command) and `cmd/g8s/submit_route_test.go` (adaptive timeout scaling when `--timeout` is omitted vs explicit `--timeout`).
