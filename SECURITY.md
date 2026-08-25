# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| 0.1.x | yes |

## Reporting a vulnerability

Open a private security advisory via GitHub ("Security" tab -> Advisories -> New draft advisory),
or contact the maintainer directly if you cannot use advisories. Please include:

- affected version / commit,
- reproduction steps or proof-of-concept,
- expected vs actual behavior.

We aim to acknowledge reports within 72 hours.

## Hardening posture

- Pure Go, Zero-CGO: no C toolchain attack surface; SQLite via modernc.org/sqlite.
- Receipts are single-use, TTL-bounded (<=3600s), path-scoped, atomically consumed.
- Worker prompts embed explicit scope blocks; mutation without a receipt is a policy violation.
- MCP surface never accepts workspace-write receipts; receipts cross only the CLI path.
- Service units are hardened (canonical binary pinning, umask 0077, scrubbed PATH, secret-free plists).

## Scope notes

The dispatch layer spawns official platform CLIs (e.g. agy) with explicit sandbox flags.
Credential handling inside those CLIs follows each vendor's own security model.
