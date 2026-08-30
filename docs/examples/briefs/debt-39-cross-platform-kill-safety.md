# Brief — DEBT-39 Cross-Platform Ghost Process Kill Safety (Brief v2 Example)

> **Format**: Brief v2 (Attentioner / Open-Question Framing)  
> **Topic**: Project-Scoped Ghost Process Filtering & Non-Destructive Cleanup  
> **Reference**: DEBT-39 (#145), Issue #163

---

## 1. Intent

`g8s cleanup` must aggressively terminate orphaned background workers without EVER terminating foreign projects' workers or the operator's active IDE / CLI session.

---

## 2. Context (What you discover first)

- Read `internal/cleanup/cleanup.go:115-160` (filter logic for ghost processes).
- Read `cmd/g8s/cleanup.go:160-185` (`KillProcess` call site and safety flags).
- Run `g8s cleanup --target ghost-process --dry-run` against your own current session:
  - *Observation*: The command should inspect live process table and report zero false-positive matches for foreign repos.
- Invariant Rules ([`spec/constitution.md`](../../../spec/constitution.md) §1.4):
  - POSIX process group containment & Windows Job Object safety.
  - Zero unintentional kills outside the current workspace root.

---

## 3. Open Questions to Answer Before Writing Code

### Question 1: What does "foreign process" mean for THIS project?
**Answer**:
A foreign process is an `agy`, `claude`, `gemini`, or `ollama` CLI process running on the host whose working directory (`CWD`) is outside the current repository tree AND whose command-line arguments do NOT reference the current workspace (`--add-dir <repo-path>`).
Processes with active heartbeat leases in another repository's `.heartbeat/` directory must be strictly ignored.

### Question 2: What 2-3 anti-patterns could destroy a user if you write this wrong?
**Answer**:
1. **Naive Process Name Match**: Killing any process named `agy` or `claude` without inspecting CWD or command line, terminating the user's active editor, interactive chat session, or background tasks in other repositories.
2. **Missing CWD Resolution Fallback**: On macOS/Linux without `/proc`, failing to resolve process CWD (via `lsof`) and defaulting to "match all" or panicking.
3. **Ghost vs Active Race**: Killing a worker that just started (<60s) before its first heartbeat write lands on disk.

### Question 3: Which test would you write FIRST to prove the design is safe?
**Answer**:
Write `TestFindGhostProcesses_ForeignProcessIgnored` first. It mocks processes running in `/tmp/other-project` with binaries `agy` and `claude`, and asserts that `FindGhostProcesses` returns exactly 0 targets for the current repository.

---

## 4. Implementation

1. **Triple-Verification Containment**:
   - Step 1: Filter process list by known AI CLI binaries (`agy`, `claude`, `gemini`, `ollama`).
   - Step 2: Resolve process `CWD` (via `/proc/<pid>/cwd` on Linux, `lsof -p <pid>` on macOS, `QueryFullProcessImageName` on Windows).
   - Step 3: Match against `RepoDir` or `--add-dir` arguments. If foreign, skip immediately.
2. **Heartbeat Correlation**:
   - Cross-reference active heartbeat files in `.heartbeat/`.
   - If a heartbeat exists and is fresh (<5m), process is NOT a ghost.
3. **Audit Logging**:
   - Append structured JSONL entry to `.cleanup-audit.jsonl` on every killed process (PID, binary, CWD, timestamp, operator PID).

---

## 5. Definition of Done (DoD)

- [x] Answers to open questions documented in commit description and inline brief
- [x] `TestFindGhostProcesses_ForeignProcessIgnored` written FIRST and passing
- [x] `TestFindGhostProcesses_AddDirFlagRecognized` written and passing
- [x] `TestGhostProcessCleanup_AuditLogAppended` written and passing
- [x] `g8s cleanup --target ghost-process --dry-run` verified safe against live session
- [x] Pure-Go (Zero-CGO) compilation across Darwin, Linux, and Windows
