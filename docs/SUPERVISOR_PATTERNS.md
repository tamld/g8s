# Supervisor Patterns & Brief v2 Specification

> **Status**: Active & Standard  
> **Topic**: Supervisor as Attentioner (Open-Question Framing)  
> **Standard Reference**: DEBT-47 (#163)

---

## 1. The 50%-of-Time Test Problem

In multi-agent and supervisor-worker architectures (such as `g8s`), a recurring failure mode occurs when orchestrators issue **directive briefs**:

```text
Directive Brief ("Dictator"):
"Implement X in file Y. Add flags --foo and --bar. DoD: tests pass and PR opened."
```

Under directive framing, the AI implementer allocates nearly 100% of initial cognitive compute and token attention to mechanical code implementation. Testing is treated as an afterthought executed at the very end of the cycle when:
1. Context token budgets are strained.
2. Cognitive momentum biases the worker toward proving its own code "works" (confirmation bias) rather than trying to break it.
3. Edge cases, cross-platform quirks, and security boundary violations are overlooked, resulting in shallow, happy-path-only unit tests.

This phenomenon is the **50%-of-Time Test Problem**: *Implementers allocate compute to implementation first and run out of attention for testing, producing brittle or false-confidence test suites.*

---

## 2. The Attentioner Pattern (vs Dictator)

The supervisor's primary role is not an autocratic **dictator** micromanaging line-by-line syntax, but an **attentioner / cognitive re-distributor**:

| Dimension | Supervisor as Dictator (v1) | Supervisor as Attentioner (v2) |
| :--- | :--- | :--- |
| **Framing** | Prescriptive & directive ("Do X, write Y") | Inquisitive & risk-oriented ("What could break?") |
| **Compute Allocation** | 90% implementation, 10% happy-path test | 40% risk/contract analysis, 30% TDD, 30% code |
| **Test Strategy** | Post-implementation confirmation | Pre-implementation invariant & failure-mode proof |
| **Worker Mindset** | Task completion & token rush | Zero-trust verification & boundary defense |
| **Post-Run Feedback** | Passive acceptance on exit code 0 | Mandatory self-reflection checkpoint before next task |

By framing briefs with **open questions** and injecting **attention checkpoints** into worker lifecycle hooks, the supervisor forces the implementer to pause and re-allocate compute to "what could go wrong" *before* writing code.

---

## 3. Brief v2 Template

All non-trivial task briefs in `g8s` should follow the Brief v2 template:

```markdown
# Brief — [TASK_ID] [Title]

## Intent
[1-2 sentences on the core architectural goal and safety invariant.]

## Context (What you discover first)
- Inspect [file:lines] (critical paths, interfaces)
- Run `[diagnostic / dry-run command]` — observe current behavior
- Key constraints & invariant rules ([spec/constitution.md], layer boundaries)

## Open Questions to Answer Before Writing Code
1. What 2-3 things could you get wrong or miss in this design?
2. Which test would you write FIRST to prove the design is safe against regressions?
3. What contract or invariant from your brief would you violate by accident?

## Implementation
[Design guidelines, interfaces to satisfy, edge cases to handle.]

## Definition of Done (DoD)
- [ ] Answers to open questions documented in commit / PR description
- [ ] Test written FIRST demonstrating the safety invariant
- [ ] Implementation satisfies pure-Go (Zero-CGO) and layer-ownership rules
- [ ] All tests and CI quality gates pass
```

---

## 4. Worked Example: Brief v1 vs Brief v2

### Brief v1 (Directive Framing — Anti-pattern)

```markdown
# Brief — DEBT-39 cross-platform kill safety

## Tasks
1. Create tools/ci_layer_check.sh to prevent worker/orchestrator cross-talk.
2. Update internal/cleanup/cleanup.go to filter ghost processes by project directory.
3. Wire into workflow.

## DoD
- [ ] PR opened
- [ ] Tests pass
```

*Result with v1*: Implementer rushed to add a `strings.Contains(cmd, "g8s")` filter, accidentally matching and killing the user's parent IDE or foreign `agy` sessions in unrelated projects.

---

### Brief v2 (Attentioner Framing — Best Practice)

```markdown
# Brief — DEBT-39 cross-platform kill safety

## Intent
`g8s cleanup` must aggressively terminate orphaned background workers without EVER terminating foreign projects' workers or the operator's active IDE session.

## Context (What you discover first)
- Read `internal/cleanup/cleanup.go:115-160` (current ghost process filter logic)
- Read `cmd/g8s/cleanup.go:160-185` (`KillProcess` call site and safety guards)
- Run `g8s cleanup --target ghost-process --dry-run` against your own current session — what does it list?

## Open Questions to Answer Before Writing Code
1. What does "foreign process" mean for THIS project? (CWD match? `--add-dir` flag inspection? Active heartbeat PID mapping?)
2. What 2-3 anti-patterns could destroy an operator's active workspace if written wrong?
3. Which test would you write FIRST to prove foreign processes in `../other-project` are never killed?

## Implementation
1. Design process containment filters with triple-check: CWD resolution, command-line inspection, and heartbeat lease validation.
2. Ensure `claude` or `agy` running outside the repository root is strictly untouched.
3. Record an audit log for every kill action.

## Definition of Done (DoD)
- [ ] `TestFindGhostProcesses_ForeignProcessIgnored` written and passing
- [ ] `g8s cleanup --target ghost-process --dry-run` verified against live session
- [ ] Audit log JSONL recorded on kill actions
- [ ] PR opened with answers to the 3 open questions inline
```

---

## 5. When to Use v1 vs v2

```text
                      Task Arrives
                           │
             Is it urgent, purely mechanical,
             and zero-ambiguity (e.g. typo,
             dependency version bump)?
                    /             \
                  YES              NO
                  /                 \
          Use Brief v1        Use Brief v2 (Default)
       (Directive Fast-Path)   (Attentioner Framing)
```

- **Use Brief v1**:
  - Urgent hotfixes where the solution is a single line and already known.
  - Mechanical tasks (updating a version string in `go.mod`, fixing a markdown typo).
  - Trivial configuration updates with no branching logic.

- **Use Brief v2 (Default)**:
  - Any task introducing new features, CLI subcommands, or flags.
  - Refactoring across package boundaries or modifying lifecycle state machines.
  - Security, process isolation, file deletion, or cleanup logic.
  - Concurrency, goroutine lifecycle, or channel management.

---

## 6. Runtime Attention Hooks & CLI Diagnostics

To ensure attention redistribution is enforced at runtime, `g8s` integrates supervisor hooks and CLI diagnostics:

1. **`AttentionerHook` (`internal/hooks/attentioner.go`)**:
   - **`PreSpawn`**: Prepends 3 risk and invariant reflection questions to the worker prompt.
   - **`PostWait`**: Dispatches a non-blocking self-review heartbeat prompt upon successful worker completion.
2. **`g8s doctor --attention-check`**:
   - Executes interactive and automated self-reflection diagnostics before a session accepts delegated tasks.
