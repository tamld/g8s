# Profile: security (security tools / guard repos)

**Nature**: repositories whose deliverable IS protection (git guards, policy
enforcers, scanners). Every change is trust-boundary adjacent by definition.

## Trust boundaries (seed — maximal by default)

```yaml
trust_boundaries:
  - "**"                 # deny-all, allow-some: everything is P0-adjacent;
  # add explicit allow-lanes only after a review cycle proves the surface.
```

## Lane bundle recommendation

| Lane | Usage |
|---|---|
| S (security) | Default — redaction scan mandatory on every PR, independent review mandatory |
| F | Tooling only, still redaction-scanned |
| Cadence | Audit per PR + full deep-audit per release |

## Audit dimensions (weight order)

1. **security-redaction** (mandatory every PR) — leak scan per the
   g8s-supervisor security playbook: home paths, key shapes, raw secrets,
   first-person leaks in reports.
2. **sast-dast-and-cve-triage** — execute `g8s-sec-gate` for SSRF private-IP
   blocking, path traversal/symlink containment, command injection, and
   mandatory CVE/CWE world-state lookup before any `Lane S` mutation.
3. **tests** — deterministic behavior is the product; coverage ratchet and
   `g8s-red-prove` (`--canary-mutant`) on.
4. **lifecycle** — escalation history, gate falsification review (guard
   rules that never fire are guard rot).

## Role & Skill Routing Matrix

| Task Phase | Recommended Role | Permission | Mandatory Skills |
|---|---|---|---|
| CVE / CWE & Threat Research | `researcher` | `read_only` | `g8s-epistemic-audit`, `ck:xia` |
| STRIDE / OWASP / Boundary Audit | `sec-auditor` | `read_only` | `g8s-sec-gate`, `ck:security`, `g8s-blind-verifier` |
| Failure & Attack Matrix Modeling | `analyst` | `read_only` | `g8s-scenario-matrix`, `wiki-failure-design` |
| Guard / Hardening Implementation | `implementer` | `workspace_write` | `g8s-red-prove`, `g8s-sec-gate` |
| Red-to-Green & Canary Proof | `test-runner` | `workspace_write` | `g8s-red-prove` |

## Worker recommendation

- `read_only` (`sec-auditor`, `researcher`, `verifier`) for all security audits and double-blind verification. `workspace_write` (`implementer`, `test-runner`) strictly receipt-gated with `Lane S` pre-execution CVE & Socratic debate gate enforced.
- Model: strongest available (`deep-coding` / `reasoning-high`) for security review lanes; `docs-retriever` for mechanical scans.

## Contribute back

Guard-evasion attempts caught by tests, redaction-scan recall improvements,
false-positive rates of policy patterns.
