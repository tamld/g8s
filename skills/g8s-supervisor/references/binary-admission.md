# Binary Admission & Freshness Contract

> **Loaded by**: `skill(name="g8s-supervisor", user_message="binary-admission")`
> **Referenced by**: Mode 1, Mode 2, Mode 3

---

## Binary Admission Evidence (Per Pilot / Per Dispatch)

Installed executable is **volatile**. Every pilot/dispatch requires fresh evidence:

| Evidence | Command | Purpose |
|----------|---------|---------|
| Canonical path | `which g8s` or explicit `/path/to/g8s` | Identify exact binary |
| SHA-256 digest | `sha256sum bin/g8s` | File identity (NOT freshness) |
| Self-reported version | `g8s version --json` | Build info, Git commit |
| Immutable provenance | From `version --json` + GitHub release | Upstream attestation |
| Safe capability evidence | `g8s doctor --json` | Capability discovery |
| Regression result | `g8s cleanup --dry-run` + `g8s status --worker --json` | Behavioral baseline |

**Key principle**: A digest identifies a file, not freshness. Source checkout, issue state, release name, documentation **cannot** prove installed behavior.

---

## Freshness Policies

> Choose ONE per project/workspace. Record in `.g8s-pin.json`.

| Policy | Description | When to Re-qualify |
|--------|-------------|---------------------|
| `pinned` | Explicit SHA-256 + provenance locked | Manual update only |
| `latest-released` | Latest GitHub release asset | New release published |
| `latest-build` | Latest default-branch CI build | New CI build artifact |

**Any change** → upstream ref/release/checksum change, local digest change, freshness expiry → downgrade admission until re-qualified.

---

## .g8s-pin.json Schema

```json
{
  "canonical_path": "/Volumes/RAMDisk/g8s/bin/g8s",
  "sha256": "a1b2c3d4e5f6...",
  "provenance": {
    "git_commit": "abc1234def567",
    "build_timestamp": "2026-09-01T05:00:00Z",
    "builder": "go1.23.0",
    "release_tag": "v0.1.0"
  },
  "capability_evidence": {
    "doctor_json": "{...}",
    "regression_pass": true,
    "checked_at": "2026-09-01T05:30:00Z"
  },
  "freshness_policy": "pinned",
  "expires_at": "2026-10-01T00:00:00Z",
  "qualified_by": "supervisor-session-001"
}
```

---

## Verification Script (`scripts/verify_g8s_pin.py`)

```bash
# Verify pin before dispatch
python scripts/verify_g8s_pin.py --pin .g8s-pin.json --strict

# Output: PASS/FAIL with details
# Exit code: 0 = pass, 1 = fail
```

**Dispatch gate**: Must pass before any `g8s submit` in Mode 2.

---

## Inversion Test (Provenance vs Behavior Divergence)

If `g8s doctor --json` behavior ≠ provenance attestation:
- g8s = **unqualified**
- Dispatching = stale behavior → false productivity + bad upstream reports
- **Action**: Stop, investigate, re-qualify, or report upstream

---

## Anti-Patterns (Quick Reference — Full: `anti-patterns.md`)

| Category | Pattern | Fix |
|----------|---------|-----|
| **A. Binary Identity** | Trusting version string | Verify SHA-256 + provenance |
| | Assuming binary unchanged | Re-qualify per pilot |
| | Auto-update binary | Never — explicit re-qualification |
| **B. Evidence** | Raw receipts in reports | Sanitize → receipt.json only |
| | Error envelope as success | `kind:"error"` = no valid work |
| | Worker decisions trusted | Supervisor verifies all evidence |
| **C. Workflow** | Supervisor doing worker tasks | Delegate bounded tasks only |
| | Worker using skills | Workers = CLI execution only |
| | Mixing modes | One mode per pilot/task |
| **D. Upstream** | Bug without digest | Always include binary admission |
| | Feature without verification | Spec-First: OpenSpec delta first |