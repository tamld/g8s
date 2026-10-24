---
name: g8s-sec-gate
description: "Zero-trust SAST and live DAST probe runner enforcing SSRF private-IP blocking, path traversal containment, CSP/XSS sanitization, auth bypass probing, and trust boundary integrity."
domain: security
version: 1.0.1
last_updated: "2026-10-24"
risk_level: high
evidence_level: runtime_required
args_syntax: "g8s sec probe [--targets <paths>] [--endpoint <url>] [--config .g8s/trust-boundaries.yml] [--json]"
---

# g8s Security Gate Verification Skill

The `g8s-sec-gate` skill provides an automated zero-trust security gate combining static application security testing (SAST) and dynamic active security testing (DAST). It enforces capability confinement, input sanitization, SSRF protection, path traversal defenses, and Layer 3 trust boundary integrity as defined in [`.g8s/trust-boundaries.yml`](file:///.g8s/trust-boundaries.yml).

---

## 1. Purpose & Threat Defeated

### Threats Defeated
- **Server-Side Request Forgery (SSRF)**: Attempts by agents or incoming requests to query local metadata endpoints (`http://169.254.169.254`), loopback services (`127.0.0.1`), or private RFC1918 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
- **Path Traversal & Sandbox Escapes**: Directory climbing payloads (`../`, URL-encoded `%2e%2e%2f`, double-decoding, null-byte poisoning) seeking to read or write files outside designated worktrees or allowed `WriteReceipt` paths.
- **Insecure Dynamic HTML Injection / XSS**: Unescaped user-controlled or agent-generated strings rendered into HTML/DOM templates without strict Content Security Policy (CSP) headers or contextual escaping.
- **Authentication & Authorization Bypasses**: Missing session validation, token forging, privilege escalation vectors, or omitted signature checks on webhook/dispatch transports.
- **Trust Boundary Elevation**: Unauthorized modifications or mutations targeting protected security kernels (e.g. receipt generation, process isolation groups, server authentication) without explicit Lane S sign-off.

### Mechanism
1. **SAST Analysis**: Uses Go AST analysis (`gosec`, Semgrep rulesets, and custom g8s lint passes) to flag unpinned file reads, SQL/command string interpolations, missing timeouts, and unsafe CGO/reflection invocations.
2. **Boundary Auditing**: Cross-checks file diffs against [`.g8s/trust-boundaries.yml`](file:///.g8s/trust-boundaries.yml). Any unapproved touch of P0 security directories (`internal/receipt/*`, `internal/worker/proc_*`, `internal/server/*`, `internal/harness/*`, `internal/memory/*`, `internal/dispatch/*`) triggers immediate blocking.
3. **Active DAST Probing**: Spins up an ephemeral local server instance and fires active fuzzing probes:
   - Private IP SSRF payload suite
   - Path traversal traversal vectors against file-serving routes
   - Auth header tampering and missing signature simulations
   - CSP header presence and header injection resistance checks

---

## 2. Deterministic Inputs

| Input | Description | Format / Source |
|---|---|---|
| **AST Source Trees** | Worktree Go code and configuration files. | `internal/...`, `cmd/...`, `pkg/...` |
| **Trust Boundary Registry** | SSoT registry of P0 security-critical paths. | [`.g8s/trust-boundaries.yml`](file:///.g8s/trust-boundaries.yml) |
| **Active Probe Vectors** | Deterministic list of SSRF test IPs, traversal payloads, and XSS probe strings. | `tools/security/payloads/` or built-in test fixtures |
| **Target Service Endpoint** | Ephemeral loopback endpoint under test. | `http://127.0.0.1:<port>` |
| **Write Receipt Scope** | Allowed write boundaries defined for the active worker task. | `WriteReceipt.AllowedPaths` |

---

## 3. Execution Commands

### Run Security Audit & Probe Suite
Execute both static analysis and dynamic security probes against targeted packages or the whole worktree:
```bash
g8s sec probe \
  --targets "internal/server,internal/dispatch" \
  --endpoint "http://127.0.0.1:8080" \
  --config ".g8s/trust-boundaries.yml" \
  --json
```

### Run Comprehensive Repository Security Audit Script
Execute the standardized security audit gate script incorporating SAST and boundary scans:
```bash
tools/run_security_audit.sh --json
```

### Static Analysis Scanner Invocations
Direct execution of underlying SAST tooling for granular inspection:
```bash
# Go security analyzer
gosec -fmt=json -out="$G8S_STATE_DIR/evidence/gosec-report.json" ./...

# Semgrep zero-trust security checks
semgrep --config=p/golang --config=tools/security/semgrep-rules.yml --json ./...
```

---

## 4. JSON Verdict Schema

The verification output produces a machine-readable JSON verdict complying with the following schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "SecurityGateVerdict",
  "type": "object",
  "required": [
    "verdict",
    "trust_boundary_violations",
    "sast_findings",
    "dast_probe_results",
    "ssrf_blocked",
    "traversal_blocked"
  ],
  "properties": {
    "verdict": {
      "type": "string",
      "enum": ["SECURE", "INSECURE"]
    },
    "trust_boundary_violations": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["file_path", "matched_boundary", "authorized"],
        "properties": {
          "file_path": { "type": "string" },
          "matched_boundary": { "type": "string" },
          "authorized": { "type": "boolean" },
          "reason": { "type": "string" }
        }
      },
      "description": "List of files touched that intersect with .g8s/trust-boundaries.yml without authorization"
    },
    "sast_findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["rule_id", "severity", "file", "line", "description"],
        "properties": {
          "rule_id": { "type": "string" },
          "severity": { "type": "string", "enum": ["LOW", "MEDIUM", "HIGH", "CRITICAL"] },
          "file": { "type": "string" },
          "line": { "type": "integer" },
          "description": { "type": "string" }
        }
      }
    },
    "dast_probe_results": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["probe_type", "payload", "status_code", "passed"],
        "properties": {
          "probe_type": { "type": "string", "enum": ["SSRF", "PATH_TRAVERSAL", "AUTH_BYPASS", "XSS_CSP"] },
          "payload": { "type": "string" },
          "status_code": { "type": "integer" },
          "passed": { "type": "boolean" },
          "details": { "type": "string" }
        }
      }
    },
    "ssrf_blocked": {
      "type": "boolean",
      "description": "True if all SSRF probe attempts to private/internal IPs were dropped or rejected"
    },
    "traversal_blocked": {
      "type": "boolean",
      "description": "True if all path traversal probe attempts beyond the worktree root were blocked"
    }
  }
}
```

### Example Verdict Output (Secure)
```json
{
  "verdict": "SECURE",
  "trust_boundary_violations": [],
  "sast_findings": [],
  "dast_probe_results": [
    {
      "probe_type": "SSRF",
      "payload": "http://169.254.169.254/latest/meta-data/",
      "status_code": 403,
      "passed": true,
      "details": "Rejected by private IP blacklisting filter"
    },
    {
      "probe_type": "PATH_TRAVERSAL",
      "payload": "/api/v1/fs/read?path=../../../../etc/passwd",
      "status_code": 400,
      "passed": true,
      "details": "Path traversal attempt blocked outside scope root"
    }
  ],
  "ssrf_blocked": true,
  "traversal_blocked": true
}
```

### Example Verdict Output (Insecure)
```json
{
  "verdict": "INSECURE",
  "trust_boundary_violations": [
    {
      "file_path": "internal/receipt/verifier.go",
      "matched_boundary": "internal/receipt/*",
      "authorized": false,
      "reason": "Task packet does not have Lane S clearance"
    }
  ],
  "sast_findings": [
    {
      "rule_id": "G204",
      "severity": "HIGH",
      "file": "internal/server/exec.go",
      "line": 48,
      "description": "Audit use of command execution with variable input"
    }
  ],
  "dast_probe_results": [
    {
      "probe_type": "SSRF",
      "payload": "http://127.0.0.1:20128/v1/models",
      "status_code": 200,
      "passed": false,
      "details": "Private IP reachable without loopback filter enforcement"
    }
  ],
  "ssrf_blocked": false,
  "traversal_blocked": true
}
```

---

## 5. F0 -> F1 -> F2 Delegation Contract

```
[ F0 Supervisor ]
       |
       | 1. Evaluates PR/Task scope against .g8s/trust-boundaries.yml
       | 2. Routes to Lane S if P0 boundaries are touched
       | 3. Issues path-restricted WriteReceipt for F1 Worker
       v
[ F1 Worker (wt-xxx) ]
       |
       | 1. Implements code changes strictly within WriteReceipt.AllowedPaths
       | 2. May invoke F2 Subagent for linting or localized security fixups
       v
[ F2 Subagent (Fix/Scan) ]
       | Scans code locally, runs unit checks within F1 scope
       | Submits code fixes back to F1
       v
[ F1 Worker (wt-xxx) ]
       | Runs `tools/run_security_audit.sh` and `g8s sec probe`
       | Asserts `ssrf_blocked == true` and `traversal_blocked == true`
       | Attaches SecurityGateVerdict to completed task packet
       v
[ F0 Supervisor ]
       | Verifies verdict == "SECURE"
       | Validates zero trust boundary violations and zero high/crit SAST findings
       | Executes final gate acceptance check
```

- **F0 Supervisor Authority**: Exclusive authorization authority over trust boundary promotions. If any file within `internal/receipt/*`, `internal/worker/proc_*`, `internal/server/*`, etc. is modified, F0 mandates Lane S acceptance and signature.
- **F1 Worker Boundary**: F1 operates under an active `WriteReceipt`. F1 must never attempt to bypass `WriteReceipt.AllowedPaths`. Any SAST/DAST failures must be resolved before submitting evidence.
- **F2 Subagent Containment**: Subagents operate strictly inside F1's process group and file scope. They cannot spawn elevated processes or bypass network security controls.
- **Gate Enforcement**: A verdict of `INSECURE` or any unblocked SSRF/traversal probe fails the pipeline with exit code 1.
