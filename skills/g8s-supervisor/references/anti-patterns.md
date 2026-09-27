# Anti-Pattern Catalog (g8s-supervisor)

> **Loaded by**: `skill(name="g8s-supervisor", user_message="anti-patterns")`
> **Purpose**: Prevent common failures in all three modes

---

## Category A: Binary Identity (4 patterns)

| ID | Anti-Pattern | Why It Fails | Correct Behavior |
|----|--------------|--------------|------------------|
| A1 | Trusting `g8s version` string alone | Version can be spoofed; same version ≠ same binary | Verify SHA-256 + immutable provenance |
| A2 | Assuming binary unchanged since last pilot | Rebuilds, cache corruption, supply chain | Re-qualify per pilot (digest + doctor + regression) |
| A3 | Auto-updating binary (git pull + rebuild) | Freshness contract violation; stale behavior = false productivity | Manual re-qualification only; pin in `.g8s-pin.json` |
| A4 | Using local source HEAD as proof | Source ≠ installed binary; CGO/flags/env differ | Use `g8s version --json` + digest as SSoT |

---

## Category B: Evidence & Reporting (6 patterns)

| ID | Anti-Pattern | Why It Fails | Correct Behavior |
|----|--------------|--------------|------------------|
| B1 | Including raw stdout/stderr in reports | Token bloat; secrets leakage; non-reproducible | Sanitized `receipt.json` only |
| B2 | Treating `kind:"error"` envelope as success | Constitution: error = no valid work | Stop immediately; analyze error envelope |
| B3 | Trusting worker assertion of success | Worker may hallucinate; no verification | Supervisor verifies: tests + lint + diff |
| B4 | Citing exit code / release tag / docs as evidence | None prove installed behavior at runtime | Cite `receipt.json`, `status --json`, `doctor --json` |
| B5 | Omitting binary admission from issue/PR | Upstream cannot reproduce; issue suppressed | Always include full binary admission block |
| B6 | Redacting evidence instead of sanitizing | Redaction loses structure; sanitization preserves schema | Use sanitized receipt schema |

---

## Category C: Workflow Boundaries (6 patterns)

| ID | Anti-Pattern | Why It Fails | Correct Behavior |
|----|--------------|--------------|------------------|
| C1 | Supervisor doing worker tasks (scanning, test gen) | Quota waste; Brain tier should strategize | Delegate bounded tasks via g8s roles |
| C2 | Worker using skills (ck:plan, ck:cook, etc.) | Skills = cognitive patterns for Brain only | Workers = CLI execution only (no skills) |
| C3 | Mixing Mode 1 + 2 + 3 in one pilot | State machine confusion; evidence contamination | One mode per pilot/task |
| C4 | Using `workspace_write` without receipt | Gatekeeper rejects; security violation | `receipt-issue` → `submit` with receipt |
| C5 | Long-running task without heartbeat monitoring | Ghost processes; orphan worktrees | `g8s status --worker --json` every 30s |
| C6 | Skipping `g8s cleanup --force` at session end | Resource leaks; state pollution | Mandatory hygiene step |

---

## Category D: Upstream Contribution (4 patterns)

| ID | Anti-Pattern | Why It Fails | Correct Behavior |
|----|--------------|--------------|------------------|
| D1 | Filing bug without binary digest | Upstream cannot qualify; issue stalled | Include full binary admission evidence |
| D2 | Proposing feature without OpenSpec delta | Constitution: Spec-First mandatory | Write OpenSpec delta before code |
| D3 | PR without table-driven test | Race conditions missed; regression risk | TDD: test first, then implement |
| D4 | Publishing issue without cross-check | Duplicate/stale/unknown → suppressed | Fresh `clear` cross-check before draft |

---

## Quick Diagnostic Checklist

Before any g8s operation, verify **NONE** of these are true:

- [ ] Using version string instead of SHA-256
- [ ] Skipping `g8s doctor --json` capability check
- [ ] Including raw logs in any report
- [ ] Trusting worker "success" without evidence verification
- [ ] Supervisor executing bounded tasks directly
- [ ] Worker invoking any skill
- [ ] `workspace_write` without valid receipt
- [ ] No heartbeat monitoring for >60s tasks
- [ ] Filing upstream issue without binary admission block
- [ ] Writing code before OpenSpec delta exists

**If ANY checked → STOP, fix the anti-pattern, then proceed.**