# Mode 1: Pilot / Release Qualification (Original Workflow)

> **Loaded by**: `skill(name="g8s-supervisor", user_message="mode1")`
> **Depends on**: `hard-boundary.md`, `binary-admission.md`, `assets/pilot-record.schema.json`, `scripts/validate_g8s_pilot.py`

---

## Lifecycle FSM

```text
INTAKE → CLASSIFIED → READY → IDENTITY_CAPTURED
      → QUALIFIED_CURRENT | QUALIFIED_LIMITED | UNQUALIFIED | QUARANTINED
QUALIFIED_* → PILOT_SPECIFIED → AWAIT_DISPATCH_AUTH → DISPATCHED → RECEIPT_CAPTURED
error envelope → THEATER → fix issue draft | suppressed | retest | stop
success + declared requirement fail → REQUIREMENT_VIOLATION → optimization issue draft | suppressed | retest | stop
success + all declared checks pass → SUBSTANCE_VERIFIED → new-scope proposal | learning
missing, unknown, or extra acceptance evidence → EVIDENCE_GAP → retest | stop
terminal → LEARNING_RECORDED
```

### State Meanings

| State | Meaning | Action |
|-------|---------|--------|
| `UNQUALIFIED` | Binary admission failed | Block dispatch entirely |
| `QUALIFIED_LIMITED` | Partial admission | Only `read_only` regression pilot |
| `QUARANTINED` | Evidence anomalies | Sanitized drafting only, no dispatch |
| `QUALIFIED_CURRENT` | Full admission | Proceed to pilot spec |

---

## Required Pilot Packet (All Fields Mandatory)

```text
Objective:
Task class and expected result:
Executable identity/provenance/capability evidence:
Admission state:
Allowed paths and permission:
Worker role and timeout:
Acceptance checks:
Evidence sink and redaction declaration:
Competing hypotheses:
Stop conditions:
```

**DoR**: All fields exist; action authorization explicit; default permission `read_only`.
**DoD**: Output substance + acceptance checks pass; claims cite sanitized evidence; unknowns visible; tactic `reuse`/`avoid`/`retest`.
**DnD**: Any result lacks identity, scope, redaction, receipt/output substance, acceptance evidence, or contradiction resolution.

---

## Binary Admission (Per Pilot — Volatile Executable)

> **Detail**: `binary-admission.md`

Record per pilot:
- Canonical path (e.g., `/Volumes/RAMDisk/g8s/bin/g8s`)
- File SHA-256 digest (identifies file, NOT freshness)
- Self-reported version/build (`g8s version --json`)
- Immutable release/build provenance (Git commit from version)
- Safe capability evidence (`g8s doctor --json`)
- Regression result (`g8s cleanup --dry-run` + `g8s status --worker --json` PASS)

**Never** hard-code command as universally safe. Discover + prove each capability. Unsafe/unavailable probes → `unknown`, never guess.

---

## Drafts & Publication

| Draft Type | Template | When |
|------------|----------|------|
| Fix issue | `assets/issue-packet.md.tmpl` | Observed `kind:"error"` envelope |
| Optimization issue | `assets/issue-packet.md.tmpl` | Successful run violates declared acceptance check |
| Proposal | `assets/proposal-packet.md.tmpl` | Verified behavior + new scope |

**Pre-issue**: Caller supplies sanitized cross-check evidence for exact admitted upstream ref.
**Publication gate**: Only fresh `clear` → `ISSUE_DRAFTED`; `duplicate`/`stale`/`unknown`/expired/ref mismatch → `ISSUE_SUPPRESSED`.
**GitHub publish**: Separate explicit review + authorization required.
**Artifact root**: `plans/reports/g8s/` (explicitly authorized project path).

---

## Freshness Contract

> **Detail**: `binary-admission.md#freshness-policies`

"Latest" requires explicit policy:
- Latest released artifact
- Latest default-branch build
- Approved pinned release

Qualify every executable by: SHA-256 + immutable provenance + upstream retrieval timestamp + capability evidence + bounded `read_only` regression.

Any upstream ref/release/checksum change, local digest change, or freshness expiry → downgrade admission until re-qualified.

**Inversion**: If provenance and behavior evidence diverge → g8s unqualified; dispatching = stale behavior → false productivity + bad upstream reports.

→ **Skipped**: Auto-updater/checker. Add only when upstream ships signed immutable manifests + authorized adapter verifies without g8s runtime mutation.