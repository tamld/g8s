# Definition of Done (DoD) & Definition of Ready (DoR)

> **Status**: v0.6.1 governance baseline (see #255 for full overhaul). Sections
> split into **Engineering** (code/feature) and **Agent** (dispatch/run) tracks
> per the lifecycle governance proposal.

## 1. Definition of Ready (DoR)

### 1.1 Engineering Ready (Code/Feature)
A code/feature task is **Ready** when:
1. **Explicit Scope & Boundary**: Input parameters, target files, role/permissions unambiguous.
2. **Failure Mode Analysis**: Timeout, corrupt DB, process kill, invalid receipt, denied paths specified with expected exit codes.
3. **Schema & Contract Alignment**: Task schemas, receipt formats, DB table changes have migration logic + schema version bump (`PRAGMA user_version`).
4. **Test Plan Defined**: Happy path, edge cases (Unicode, SQL injection, races), abuse scenarios outlined before implementation.

### 1.2 Agent Task Ready (Dispatch Brief)
A dispatched agent task is **Ready** when:
1. **RoleProfile + Scoped Path**: Explicit `RoleProfile`, narrow `allowed_paths` globs (no `**` without justification).
2. **Bounded Budget**: Memory cap + timeout ≤ 3600s. Idempotency key assigned.
3. **Write Receipt Pre-Issued**: Valid single-use Write Receipt if mutation requested.
4. **Negative-Pattern Query**: Knowledge Vault searched for known failure modes before brief dispatch.

## 2. Definition of Done (DoD)

### 2.1 Engineering Done (Code/Feature)
A code/feature task is **Done** when:
1. **Test Coverage**: Core business logic ≥ 90% coverage, zero races (`go test -race ./...`).
2. **Pure-Go Build**: Clean compilation Darwin/Linux/Windows with `CGO_ENABLED=0`.
3. **Lint & SAST Clean**: Passes `gofmt -l`, `gofumpt -d` (strict), `golangci-lint` (including `gosec`), `tools/ci_semgrep_check.sh` (`.semgrep/g8s-rules.yml`), and `CodeQL`, zero errors/warnings.
4. **Security**: Zero secrets in history (`gitleaks`), `0600`/`0700` POSIX permissions enforced by `G306`/Semgrep, `govulncheck` clean, path normalization handles symlinks and `..` traversal.
5. **Error Contracts**: All error paths return Feynman diagnostic + standard exit code (0-5).
6. **Documentation**: CLI subcommands have `--help` descriptions, ADRs up to date.

### 2.2 Agent Execution Done (Task/Run)
A dispatched agent run is **Done** when:
1. **Receipt Atomicity**: Write Receipt atomically consumed (if write), or zero mutations detected by post-scan (if read).
2. **Prompt Redaction**: Raw prompt deleted, replaced with SHA-256 in WAL.
3. **Resource Cleanup**: Ephemeral worktrees + child process groups pruned, zero zombies.
4. **Lineage Edge**: DAG edge recorded in `task_events`.
5. **Telemetry Emit**: Completion event emitted with exit code + root-cause signature (see #253).

## 3. Campaign-Proven Governance Standards (ADR-0020/0021, 2026-09-25)

Standards validated across the reflex-gated debt campaign (10+ worker tasks,
20+ PRs, live Jev triages) — they extend the tracks above with the operating
model the project now runs on:

### 3.1 Campaign DoR additions (before any mutation)
1. **Reflex Triage Verdict**: every planned mutation passes `g8s reflex triage`
   (Jev sensor + deterministic policy). `instant_kill` abandons; `escalate_hitl`
   with risk ≥ 1.0 routes to operator review via PR — never silent.
2. **Worker Packet Ceiling**: recon/audit packets bounded well below the
   measured worker ceiling (~450K tokens for gemini-3.8-flash-high); explicit
   paths, no blind filesystem searches.
3. **Scope Roots Declared**: delegated-write submissions declare jail roots
   (`--scope-root`); cross-root access is explicit opt-in (#348).
4. **Defensive Framing**: security/audit prompts use verification vocabulary —
   attacker vocabulary trips provider content filters and yields non-evidence.

### 3.2 Campaign DoD additions (before merge)
1. **Layer Ownership**: PR never mixes `internal/worker` with `cmd/g8s` or
   `internal/orchestrator` (DEBT-34 gate); test-fake ripples split accordingly.
2. **Gates as Code**: dual-pass CI + pre-push suite + version-sync +
   docs-contract + layer-ownership all green; no gate bypassed — spurious
   failures are fixed at the tool level (isolate, never `--no-verify`).
3. **Evidence over Assertion**: every finding carries file:line evidence
   reproducible by a different agent (ADLC WA-8); worker transcripts ending in
   `error_message` classify as failures, never as results.
4. **Ledger Trail**: verdicts, adjudications, and ceilings recorded in the
   campaign ledger; ADRs carry the decisions; issues track the residue.
5. **Zero Garbage**: no orphan worktrees, no untagged scratch branches, no
   draft releases, no unpushed local state without a safety tag.
6. **Delegated-Write Proof**: workspace_write runs show `consumed=1` owned by
   the task and host-materialized outputs (verified E2E, 2026-09-25).

## 4. Symmetric Zero-Trust: Supervisor (`F0`) vs. Worker (`F1`) & Multi-Round Schema Governance (ADR-0033, 2026-10-20)

### 4.1 Differentiated Trust Model (`F1` vs. `F0`)
Both `F0` (Supervisor) and `F1` (Worker) run on stochastic LLMs; neither is trusted by role title. Trust is constructed differently for each layer:
* **Worker (`F1`) Trust = Physical Capability Containment + Artifact Verification**:
  * **Entry**: Locked into an isolated git worktree (`wt-<id>`) with a single-use, path-scoped `WriteReceipt` (`AllowedPaths`).
  * **Exit**: Evaluated strictly by physical disk state (`git status --porcelain`, blob hashes, and worktree test execution), never by worker prose.
* **Supervisor (`F0`) Trust = Information Fidelity + Schema-Checked Commitment Completeness**:
  * **In-Flight (Information Firewall)**: `F0` never reads raw test/CI logs (`go test -json`, `gh pr checks --watch`) directly in its control-plane context. Raw logs are persisted to disk (`$G8S_STATE_DIR/logs/`) and digested into a `RoundReceiptSchema` that strips 100% of mechanical noise while preserving 100% of failure diagnostics and DoD mappings (no arbitrary fixed-line truncation).
  * **Commitment**: Iteration rounds (`1..N`) are free in topology, but `F0` cannot transition from *Iterate* to *Commit/Merge/Close* until Go code verifies 1-to-1 completeness between `BriefSchema` (`D1..Dn`) and `RoundReceiptSchema` (`all D1..Dn == MET`).

### 4.2 Multi-Round Supervisor DoR (Before Dispatching Round `k`)
1. **`BriefSchema` Defined (`D1..Dn`)**: The round brief explicitly enumerates atomic acceptance items `D1..Dn`, target files, and the verifier class.
2. **Disjoint WriteReceipt**: `1 issue/slice = 1 worker = 1 receipt` with narrow `AllowedPaths`.
3. **Clean Control-Plane Context**: `F0` context is anchored to the living `BriefSchema` / `RoundReceiptSchema` state, not raw log streams.

### 4.3 Multi-Round Supervisor DoD (Before Accepting Round `k` or Closing/Merging)
1. **Worktree-Targeted Verification (`g8s verify`)**: Verification executes inside the F1 worktree (`res.Deliverable.Dir`), captures both `stdout` and `stderr`, persists raw logs to disk, and confirms `tests_ran > 0` (non-vacuous).
2. **`RoundReceiptSchema` Completeness (1-to-1 `D1..Dn` Mapping)**:
   * Every `Di` in `BriefSchema` has an explicit entry in `dod_matrix` (`MET | UNMET | PARTIAL`) with `file:line` or test evidence.
   * Every distinct failure in the raw log is preserved in `failure_diagnostics` without fixed-line truncation (`Size = f(|DoD| + |Failures| + |Boundaries|)`).
3. **Zero Dropped or Unattributed Files (`G3`)**: `applied` and `deleted` blob hashes in the worktree match the delivered commit tree (`HEAD^{tree}`) 1-for-1; `untracked_in_worktree == 0` and `unattributed_in_main == 0`.
4. **Live CI & Reversibility Tier Gate (`T0–T4`)**: All forge CI checks are terminal `pass` (`pending == 0`, `failing == 0`); `T3` trust-boundary touches pass dual independent verification; `T4` irreversible actions stop for explicit PiC approval.
5. **Universal Config-Driven LLM & 9Router Gate (`DELTA-25`)**: All `api_call` providers wire cleanly to `g8s llm-call --worker-envelope`, with zero hardcoded provider execution branches in `internal/worker`, valid model-effort catalog mapping in `.g8s/agent-models.yml`, and live discovery parity (`g8s providers discover`).
