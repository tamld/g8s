# g8s AI Anti-Pattern Catalog (DEBT-51)

> **Authority**: Spec-Driven Development & Constitution Axioms 1, 3, 4, 5  
> **Enforcement Gates**: `tools/ai_lint.sh` (Rules 1–7) & `tools/brief_lint.sh` (Rules 8–10)  
> **Diagnostics**: `g8s doctor --anti-pattern-catalog`

---

## 1. Overview & Catalog Design Principles

Autonomous AI agents (such as Claude, Cursor, Antigravity) generate predictable failure modes when writing Go code or dispatching supervisor-worker workflows. Rather than creating an unbounded, noisy style guide, `g8s` enforces a **minimal 10-rule anti-pattern catalog**.

Every rule in this catalog satisfies four strict criteria:
1. **Prevents a Real Failure Mode**: Addresses catastrophic crashes, data loss, deadlocks, brittle test debt, or AI hallucination loops.
2. **Historical Anecdote**: Rooted in concrete failures observed in `g8s` development.
3. **Sub-Second Execution**: Total CI gate execution time across all 10 checks is under 2 seconds.
4. **Actionable Remediation**: Produces exact `file:line` locations and unambiguous 1-line fix guidance.

---

## 2. The 10 Anti-Pattern Rules

| # | Rule Identifier | Linter | Severity | What It Prevents | Check Cost |
|---|-----------------|--------|----------|-------------------|------------|
| 1 | `no_panic` | `tools/ai_lint.sh` | **HIGH** | Production crashes from LLM panic in non-test code | ~15ms |
| 2 | `no_ignored_errors` | `tools/ai_lint.sh` | **HIGH** | Silent data loss from discarded `Close()` / `defer` errors | ~15ms |
| 3 | `no_type_assertion_in_library` | `tools/ai_lint.sh` | **HIGH** | Runtime panic on untyped `any` / `interface{}` downcasts | ~20ms |
| 4 | `todo_owner` | `tools/ai_lint.sh` | **MED** | Forgotten `// TODO` debt that drifts without accountability | ~15ms |
| 5 | `no_ai_artifacts` | `tools/ai_lint.sh` | **MED** | Conversational LLM boilerplate polluting production source | ~15ms |
| 6 | `test_pins_fabricated_symbol` | `tools/ai_lint.sh` | **HIGH** | TDD trap: unit test pins hallucinated symbols before design | ~100ms |
| 7 | `test_locks_impl_detail` | `tools/ai_lint.sh` | **MED** | Brittle tests asserting on private unexported struct state | ~100ms |
| 8 | `supervisor_thinks` | `tools/brief_lint.sh` | **HIGH** | Supervisor acts as dictator with busy polling loops vs event triggers | ~20ms |
| 9 | `directive_brief` | `tools/brief_lint.sh` | **MED** | Rigid directive brief bypassing LLM risk analysis (needs v2 framing) | ~20ms |
| 10 | `missing_dual_blind` | `tools/brief_lint.sh` | **HIGH** | Single-agent architecture mistakes on complex multi-state systems | ~20ms |

---

## 3. Deep-Dive Rule Specifications

### Rule 1: `no_panic`
* **Linter Function**: `check_no_panic` in `tools/ai_lint.sh`
* **Severity**: HIGH
* **What It Prevents**: Production server crashes and control-flow abuse from LLMs inserting `panic("...")` or `panic(fmt.Sprintf(...))` in library and CLI paths.
* **Real g8s Anecdote**: During early SQLite controlplane integration, an LLM worker inserted `panic("failed to open db")` on nil pointer errors during CLI shutdown instead of returning a structured JSON error envelope, crashing the operator terminal without exit telemetry.
* **Cost**: < 20ms (ripgrep/find regex scan on non-test Go code).
* **How to Fix**: Return explicit Go errors using `errors.New(...)` or `fmt.Errorf(...)` and bubble errors up the call stack.

### Rule 2: `no_ignored_errors`
* **Linter Function**: `check_no_ignored_errors` in `tools/ai_lint.sh`
* **Severity**: HIGH
* **What It Prevents**: Silent data corruption and unrecorded I/O failures caused by `_ = f.Close()` or `defer func() { _ = f.Close() }()`.
* **Real g8s Anecdote**: On Windows host file-locking teardown, SQLite WAL checkpoint flushes failed silently because an agent wrote `_ = db.Close()`, leaving orphaned `.db-wal` locks that caused subsequent worker processes to hang.
* **Cost**: < 20ms.
* **How to Fix**: Use clean `defer f.Close()` (checked by `errcheck`) or explicitly handle and log return errors.

### Rule 3: `no_type_assertion_in_library`
* **Linter Function**: `check_no_type_assertion_in_library` in `tools/ai_lint.sh`
* **Severity**: HIGH
* **What It Prevents**: Panic at runtime when untyped `any` or `interface{}` values are cast without comma-ok validation (`val := raw.(string)`).
* **Real g8s Anecdote**: Provider payload decoder crashed when an MCP worker returned integer status codes instead of string metadata, triggering an unchecked downcast panic in `internal/provider`.
* **Cost**: < 25ms.
* **How to Fix**: Always use checked type assertions with comma-ok idiom (`v, ok := raw.(string)`) or explicit type switches (`switch v := raw.(type)`).

### Rule 4: `todo_owner`
* **Linter Function**: `check_todo_owner` in `tools/ai_lint.sh`
* **Severity**: MED
* **What It Prevents**: Unassigned technical debt comments (`// TODO`, `// FIXME`, `// XXX`) that drift indefinitely in the codebase.
* **Real g8s Anecdote**: An orphaned `// TODO: fix race condition in worktree mount` remained in the codebase across 3 minor releases until it caused concurrent worktree collisions.
* **Cost**: < 15ms.
* **How to Fix**: Annotate with an explicit owner (`// TODO(OWNER=username): ...`) or resolve the debt prior to committing.

### Rule 5: `no_ai_artifacts`
* **Linter Function**: `check_no_ai_artifacts` in `tools/ai_lint.sh`
* **Severity**: MED
* **What It Prevents**: Conversational LLM commentary ("Certainly!", "I am an AI", "Here is the updated code", "I hope this helps!") contaminating production documentation and comments.
* **Real g8s Anecdote**: An AI agent generated a helper docstring starting with `"Certainly! I have implemented the POSIX process table inspector."`, polluting godoc and public package references.
* **Cost**: < 15ms.
* **How to Fix**: Remove conversational filler from code comments and docstrings before committing.

### Rule 6: `test_pins_fabricated_symbol`
* **Linter Function**: `check_tdd_trap_fabricated_symbol` in `tools/ai_lint.sh` (backed by `g8s doctor --tdd-trap-check`)
* **Severity**: HIGH
* **What It Prevents**: The TDD Trap where an agent writes a unit test referencing non-existent types, fields, or methods before the production contract is designed, forcing flawed implementations to match hallucinated test signatures.
* **Real g8s Anecdote**: During DEBT-49 development, an agent wrote a test asserting `u := &User{loyalty_points: 100}` before designing the data model, locking in snake_case fields that violated Go naming conventions.
* **Cost**: ~100ms (AST parser pass via `go/parser`).
* **How to Fix**: Define exported structs, interfaces, and methods in production code before asserting against them in unit tests.

### Rule 7: `test_locks_impl_detail`
* **Linter Function**: `check_tdd_trap_impl_detail` in `tools/ai_lint.sh` (backed by `g8s doctor --tdd-trap-check`)
* **Severity**: MED
* **What It Prevents**: Brittle unit tests that assert against private internal fields (e.g. `internalConnStatus`, `privateState`), preventing internal refactoring without breaking test suites.
* **Real g8s Anecdote**: Unit tests asserted against private mutex states inside `ProcessManager`, blocking migration to lock-free atomic channels until all tests were rewritten.
* **Cost**: ~100ms.
* **How to Fix**: Assert against public exported behaviors, contracts, and method outputs rather than inspecting internal unexported struct state.

### Rule 8: `supervisor_thinks`
* **Linter Function**: `check_supervisor_thinks` in `tools/brief_lint.sh`
* **Severity**: HIGH
* **What It Prevents**: Supervisor acting as a dictator by executing busy polling loops (`for { time.Sleep(...) }`), inline file mutations, or blocking on interactive stdin prompts instead of dispatching and triggering workers.
* **Real g8s Anecdote**: A supervisor CLI command ran a tight `for { time.Sleep(100*time.Millisecond) }` loop checking for process exits, starving goroutines and preventing prompt dispatch.
* **Cost**: < 20ms.
* **How to Fix**: Replace polling loops with event-driven channels, heartbeat leases, or asynchronous notification triggers.

### Rule 9: `directive_brief`
* **Linter Function**: `check_directive_brief` in `tools/brief_lint.sh`
* **Severity**: MED
* **What It Prevents**: Rigid directive briefs ("Implement X. DoD: Y. Constraints: Z.") that cause worker agents to bypass failure-mode analysis and spend 90% of token compute on hasty code generation.
* **Real g8s Anecdote**: An agent received a directive brief to implement process cleanup, immediately wrote `killall agy`, and accidentally terminated the operator's active IDE session in a foreign workspace.
* **Cost**: < 20ms.
* **How to Fix**: Rewrite brief to Brief v2 format with an `## Open Questions` / Framing section (DEBT-47) forcing upfront consideration of failure modes.

### Rule 10: `missing_dual_blind`
* **Linter Function**: `check_missing_dual_blind` in `tools/brief_lint.sh`
* **Severity**: HIGH
* **What It Prevents**: Single-agent blind spots on high-complexity systems (state machines, schemas, parsers, RPC contracts, concurrency models, garbage collectors, lock-free structures).
* **Real g8s Anecdote**: A single worker modified the SQLite lineage CTE migration schema without peer validation, introducing an unrecoverable deadlock until dual-blind convergence (`--blind-converge`) was introduced in DEBT-48.
* **Cost**: < 20ms.
* **How to Fix**: Dispatch complex architecture briefs using dual-blind convergence (`g8s orchestrate --blind-converge 2` per DEBT-48).

---

## 4. What Is NOT in the Catalog (and Why)

We intentionally reject generic or subjective style rules that produce high false-positive rates:

* **"No copy-paste from LLM response"**: >20% false positive rate on legitimate documentation quotes and test payloads.
* **"Max file length"**: Arbitrary metric that penalizes comprehensive single-file packages and table-driven test suites.
* **"Prefer X over Y for style"**: Enforced upstream by `gofumpt` and `staticcheck`.
* **"No comments in production code"**: False positive on godoc comments, security disclaimers, and architectural rationales.

---

## 5. CLI Verification

Inspect the live anti-pattern catalog and recent firing telemetry at any time:

```bash
# Terminal output
g8s doctor --anti-pattern-catalog

# Structured JSON envelope
g8s doctor --anti-pattern-catalog --json
```
