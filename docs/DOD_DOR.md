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
3. **Lint Clean**: Passes `gofmt -l`, `gofumpt -d` (strict), `golangci-lint`, zero errors/warnings.
4. **Security**: Zero secrets in history (`gitleaks`), path normalization handles symlinks and `..` traversal.
5. **Error Contracts**: All error paths return Feynman diagnostic + standard exit code (0-5).
6. **Documentation**: CLI subcommands have `--help` descriptions, ADRs up to date.

### 2.2 Agent Execution Done (Task/Run)
A dispatched agent run is **Done** when:
1. **Receipt Atomicity**: Write Receipt atomically consumed (if write), or zero mutations detected by post-scan (if read).
2. **Prompt Redaction**: Raw prompt deleted, replaced with SHA-256 in WAL.
3. **Resource Cleanup**: Ephemeral worktrees + child process groups pruned, zero zombies.
4. **Lineage Edge**: DAG edge recorded in `task_events`.
5. **Telemetry Emit**: Completion event emitted with exit code + root-cause signature (see #253).
