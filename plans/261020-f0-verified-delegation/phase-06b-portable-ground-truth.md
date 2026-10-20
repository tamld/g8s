# Phase 06b: Information Firewall (`BriefSchema` ↔ `RoundReceiptSchema`), Worktree-Targeted `g8s verify` (`L3`/`L4`), & Portable Ground Truth

- **Parent Epic**: `#620` (Post-Wave-A Architectural Refinement — Socratic RCA 2026-10-20)
- **AllowedPaths (F1 receipts, split by layer per DEBT-34)**:
  - **Slice 06b-1 (`internal/verifier` + `cmd/g8s/verify.go` + `cmd/g8s/watch.go`)**: Fix `L3` (worktree-targeted `g8s verify`), `L4` (`stdout` capture, vacuous guard, raw log persistence to `$G8S_STATE_DIR/logs/`, and lossless failure signal extraction), `L2` (`watch --pr` repo resolution), and `RoundReceiptSchema` (`execution_facts` + `dod_matrix` + `failure_diagnostics`).
  - **Slice 06b-2 (`tools/f0/**` + `.g8s/f0.yml`)**: `TreeSHA` (`HEAD^{tree}`) equivalence, `normalizeExtra` canonical JSON hashing, clean `PASS`-only `f0 status --report-block` with portable `f0:bundle`, declarative `.g8s/f0.yml` parsers (`pytest`, `vitest`, custom `parsers`), and narrowed `G6` verdict-only claim check.
- **Depends on**: Wave A Phases 01–06 (merged in PR `#634` at `3c4675944094cf20eed6e6791e3f6acca2989c83`)

---

## 1. Root Cause Analysis (Socratic RCA — 2026-10-20)

1. **RCA-1 — Why Supervisor (`F0`) Context Drifted (> 6.2 MB Raw Logs in Control Plane)**:
   - During Wave A, `F0` ingested `3,061,079` bytes (`task-896`) and `3,067,017` bytes (`task-961`) from `go test -json ./...`, plus `121,999` bytes (`task-965`) from `gh pr checks --watch`.
   - Flooding `F0` with raw execution logs causes attention dilution, context compaction loss, and **Role Collapse** (`F0` degrading into `F1`).
   - Inspection of `cmd/g8s/verify.go` and `internal/verifier/verifier.go` revealed two bugs that forced `F0` to run raw tests manually:
     - **Defect L3 (`cmd/g8s/verify.go:133-147`)**: `g8s verify --task <id>` resolved `targetRepoDir` from `reqPayload.AddDirs[0]` (the unmodified main checkout) instead of `res.Deliverable.Dir` (the isolated F1 worktree).
     - **Defect L4 (`internal/verifier/verifier.go:597`)**: `runGoTestCheck` discarded `stdout` (`_`), where `go test` prints failing test names and `file:line` diagnostics, returning only `"detail": "exit code 1"`, and treated `[no tests to run]` as `OK: true`.
     - **Defect L2 (`cmd/g8s/watch.go:268`)**: `checkPR` hardcoded `--repo tamld/g8s`.
2. **RCA-2 — Why Arbitrary Fixed-Line Truncation (e.g., "30 Lines") Is Rejected**:
   - Truncating a receipt to a fixed line count causes **Decision Blindness (`mù mờ quyết định`)** when a round has many DoD items or multiple failing packages.
   - Instead, compression must obey **Lossless Signal, Zero Mechanical Noise**:
     $$\text{Size}(\text{RoundReceipt}) = f\big(|\text{DoD Items}| + |\text{Distinct Failures}| + |\text{Changed Boundaries}|\big)$$
     100% of passing test chatter and polling frames are stripped; 100% of failing test diagnostics, root causes, and `D1..Dn` mappings are preserved.
3. **RCA-3 — Free Iteration Topology vs. Code-Enforced Boundary Schemas**:
   - Code cannot and must not hardcode how many rounds (`1..N`) `F0` and `F1` iterate.
   - Code **must** enforce the **Schemas at each round boundary**:
     - **Downward (`F0 -> F1`)**: `BriefSchema` (`goal`, `dod_items: [{id: "D1..Dn", description, verifier_hint}]`, `allowed_paths`).
     - **Upward (`F1/Harness -> F0`)**: `RoundReceiptSchema` (`execution_facts` + `dod_matrix[D1..Dn]` + `failure_diagnostics`).
     - **Commitment Gate**: Transition from *Iterate* to *Deliver/Merge/Close* is deterministically blocked unless `all(D1..Dn == MET) && tests_failed == 0 && untracked_files == 0`.
4. **RCA-4 — Portable Ground Truth (`tree_sha` + `f0:bundle`)**:
   - `.git/f0/ledger.jsonl` is local to `F0`'s machine. Embedding a signed/hashed `<!-- f0:bundle: ... -->` inside `<!-- f0:status -->` + checking `HEAD^{tree}` (`tree_sha`) allows both local and remote CI runners (`--mode auto|local|remote`) to verify `G1`, `G2`, `G3` (against actual git blob hashes in the clone), and `G6`.

---

## 2. Technical Specification

### 2.1. Slice 06b-1: Worktree-Targeted `g8s verify` (`L3`/`L4`), `g8s watch` (`L2`), & `RoundReceiptSchema`

1. **Fix `L3` in `cmd/g8s/verify.go`**:
   - Inspect `task.Result` (`deliverableResult`). When `res.Deliverable != nil && res.Deliverable.Mode == "worktree" && res.Deliverable.Dir != ""` and the worktree directory exists on disk, set `targetRepoDir = res.Deliverable.Dir` so `g8s verify --task <id>` runs checks inside the **F1 worktree** before delivery!
2. **Fix `L4` in `internal/verifier/verifier.go` (Information Firewall & Lossless Failure Signal)**:
   - Capture both `stdout` and `stderr` in `runGoTestCheck` and `runScriptCheck`.
   - Persist full raw output to `$G8S_STATE_DIR/logs/verify-<task-id>.log` (permissions `0o600`, dir `0o700`) and compute `raw_log_sha256`.
   - Parse `go test` output:
     - Count actual tests executed (`tests_ran`) and failed (`tests_failed`).
     - If `exitCode == 0 && tests_ran == 0` (e.g., `[no tests to run]`), mark `OK: false`, `Detail: "vacuous pass: 0 tests ran"`.
     - If `exitCode != 0`, extract **every distinct failing test block** (`--- FAIL: <TestName>` + all associated `file:line` diagnostic lines from `stdout`/`stderr`, stripping `=== RUN` and `--- PASS` lines) into `FailureDiagnostics []FailureDiagnostic` (zero fixed-line truncation on failures; zero passing-test noise).
3. **`RoundReceiptSchema` in `Verdict` (`internal/verifier/verifier.go`)**:
   - Extend `Verdict` (backward-compatible JSON fields) with:
     - `WorktreeDir string `json:"worktree_dir,omitempty"``
     - `TreeSHA string `json:"tree_sha,omitempty"``
     - `RawLogPath string `json:"raw_log_path,omitempty"``
     - `RawLogSHA256 string `json:"raw_log_sha256,omitempty"``
     - `TestsRan int `json:"tests_ran"` `
     - `TestsFailed int `json:"tests_failed"` `
     - `FailureDiagnostics []FailureDiagnostic `json:"failure_diagnostics,omitempty"``
     - `DoDMatrix []DoDItemStatus `json:"dod_matrix,omitempty"`` (populated from active brief `--brief <id>` if provided, verifying 1-to-1 coverage of `D1..Dn`).
4. **Fix `L2` in `cmd/g8s/watch.go`**:
   - Add optional `--repo <owner/name>` flag to `g8s watch`; default to omit `--repo` (so `gh pr checks` uses the current working directory's git remote) unless `--repo` is explicitly passed.

### 2.2. Slice 06b-2: Portable Ground Truth, `TreeSHA`, Declarative Parsers & `G6` Precision in `tools/f0/`

1. **`normalizeExtra` & `TreeSHA` in `tools/f0/ledger.go`**:
   - Normalize `Entry.Extra` via JSON round-trip into `map[string]any` before hashing/appending so struct values (`[]GateCheckResult`, `*Mandate`) never cause key-order hash mismatches in `f0 ledger verify`.
   - Record `TreeSHA` (`git rev-parse HEAD^{tree}`) on clean entries; accept `e.SHA == headSHA || e.TreeSHA == headTreeSHA` in `G1`.
2. **Clean `PASS`-Only `f0 status --report-block` & Portable `f0:bundle` (`status.go`, `gate.go`)**:
   - Filter `--report-block` to clean `PASS` entries matching `HEAD`/`HEAD^{tree}` and embed `<!-- f0:bundle: <json> -->` with `bundle_sha256` + delivered `blobs` map.
   - Support `--mode auto|local|remote` in `f0 gate check` so a fresh clone or CI runner without `.git/f0/ledger.jsonl` verifies `G1`, `G2`, `G3` (`git rev-parse HEAD:<file>` against bundle `blobs`), and `G6`.
3. **Declarative Parsers in `.g8s/f0.yml` (`config.go`, `run.go`, `parsers.go`)**:
   - Built-in `pytest` and `vitest`/`jest` parsers + custom `parsers:` rules (`argv_prefix`, `count_regex`, `vacuous_regex`, `pass_on_exit_zero`) in `.g8s/f0.yml`.
4. **Precision `G6` Claim Check (`claims.go`)**:
   - Only flag lines combining a verification subject (`ci`, `test`, `tests`, `gate`, `build`, `coverage`, `audit`) with a completion verdict (`pass`, `passed`, `green`, `0 findings`, `100%`, `xanh`, `thành công`), never flagging ordinary code-change descriptions (`"Fixed bug in..."`).

---

## 3. Definition of Ready (DoR) & Definition of Done (DoD)

### 3.1. DoR
- [x] **DoR-1**: Root causes `L2`, `L3`, `L4`, and `P1–P4` verified directly against repository source lines (`cmd/g8s/verify.go:133`, `internal/verifier/verifier.go:597`, `cmd/g8s/watch.go:268`, `tools/f0/ledger.go:70`).
- [x] **DoR-2**: Layer ownership respected (Slice 06b-1 for `internal/verifier` + `cmd/g8s`; Slice 06b-2 for `tools/f0` + `.g8s/f0.yml`).
- [x] **DoR-3**: Updated `docs/PRD.md`, `docs/SRS.md`, `docs/DOD_DOR.md`, and `docs/decisions/0033-f0-verified-delegation.md`.

### 3.2. DoD
- [ ] **DoD-1 (Worktree Verification `L3` + Lossless Signal `L4`)**: Unit tests prove `g8s verify --task <id>` executes in the F1 worktree (`res.Deliverable.Dir`), saves full raw log to `$G8S_STATE_DIR/logs/`, rejects vacuous `[no tests to run]`, and extracts 100% of failing test diagnostics from `stdout` without dumping passing test lines.
- [ ] **DoD-2 (`RoundReceiptSchema` `D1..Dn` Completeness)**: Unit tests prove `DoDMatrix` maps 1-to-1 against `BriefSchema` items `D1..Dn` and rejects incomplete mappings.
- [ ] **DoD-3 (`Extra` Hash & `TreeSHA` Equivalence)**: `f0 ledger verify` passes 100% after `f0 gate check`; `git commit --amend` (same `HEAD^{tree}`) preserves `G1: PASS`.
- [ ] **DoD-4 (Portable Remote Mode & Declarative Parsers)**: `f0 gate check` passes in a fresh clone without `.git/f0/ledger.jsonl` using `<!-- f0:bundle: ... -->` and catches post-delivery file tampering via git blob hash mismatch; `pytest` and `.g8s/f0.yml` declarative parsers pass unit tests.
- [ ] **DoD-5 (Zero Context Pollution in F0)**: Next session executes the entire `ck:cook` workflow using `g8s verify --task <id>` and `g8s watch --pr <N> --json` without dumping raw `go test -json` or `gh pr checks --watch` streams into `F0`.
