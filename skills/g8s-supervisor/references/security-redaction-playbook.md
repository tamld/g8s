# Security & Redaction Playbook — operate → detect → report without leaking

> The triage knowledge for turning a discovered problem into an upstream issue
> WITHOUT exposing the user's project. Load when drafting any issue, PR packet,
> or external report. The publication gate in `operating-contract.md` is the
> FSM view; this file is the hands-on procedure.

## The non-leak invariant

An issue packet must be **understandable without knowing the user's project**.
Describe the CONTRACT failure (what the tool promised, what it did), never the
project's identity, structure, or content. If a reader can reconstruct what the
user is building from your report, you leaked.

## Stage 1 — Operate (leak prevention starts at dispatch)

- Redaction is planned BEFORE dispatch: the worker packet declares what the
  output will contain and what must never appear in it.
- Prompts are secrets: they encode project structure. g8s redacts prompts to
  SHA-256 hashes in stored artifacts — never copy the raw prompt into notes,
  issues, or receipts.
- `read_only` default keeps workers from exfiltrating by writing; sandbox and
  allowed-root bound what they can read. Widening either is a leak-vector
  decision, not a convenience.
- Evidence sinks live inside the workspace; never point an evidence dir at a
  shared/synced location.

## Stage 2 — Detect (classify what you found)

| Finding class | Example | Report route |
|---|---|---|
| Project bug (oracle failed on valid transport) | worker output wrong; pipeline logic error | internal issue/ledger — NOT upstream |
| g8s contract failure (reproduced, transport/CLI level) | dispatch builds argv the provider rejects; receipt field missing | Mode 3 packet → upstream |
| Environment/provider fault | quota, auth, network | note + retry; never report as a defect |
| Sensitive-data exposure observed in evidence | secret, credential, PII in a receipt/log | REDACT IMMEDIATELY at capture; treat the exposure itself as a project finding |

Classification law (from the operating contract): `kind:"error"` → fix only
after reproduction; a successful run failing its own declared check →
optimization; missing/contradictory evidence → evidence gap, no report.

## Stage 3 — Report (sanitize, verify, then ask)

### The never-copy list

Never place in any packet, draft, log, or issue — internal or external:

- raw prompts and raw worker outputs (quote the minimal excerpt WITH the
  worker's citation instead)
- secrets, tokens, API keys, passwords, `.env` values, connection strings
- provider configuration, model quotas, account identifiers
- absolute paths containing usernames (`~<user>/…`, `<drive>:\Users\<user>\…`)
- hostnames, internal URLs, IPs, repo names/URLs of private projects
- emails, real names, team/org identifiers
- raw receipts and raw logs (use the sanitized, bounded capture)

### Redaction levels

- **L0 — public**: facts about the tool itself (flags, exit codes, schemas,
  file:line inside the TARGET repository of the report).
- **L1 — sanitized**: relative paths from an anonymized root (`<root>/internal/…`),
  shapes not contents ("a 3-key JSON object", not the keys), counts, timings.
- **L2 — minimal**: the contract statement alone ("the CLI accepted an empty
  response as success") with a synthetic reproduction.

Default to the LOWEST level that still proves the finding. Upstream g8s issues
take L1/L2 — the upstream reviewer needs the contract failure, not your
workspace. Level-2 issue packets provide a minimal isolated tempdir reproduction: build a minimal
fixture in a temp dir that reproduces the defect; then nothing real is exposed
at all.

### Packet hygiene rules

1. Write the packet with placeholders from the start — sanitizing after the
   fact is how leaks survive copy-paste.
2. Mechanical leak scan BEFORE human review: grep the draft for the never-copy
   patterns (absolute-path prefixes, `sk-`/token shapes, email regex, hostname
   patterns, the project's distinctive identifiers). `g8s-supervisor audit`
   runs this scan.
3. Every claim carries an evidence class: `[CODE]` (read), `[RUNTIME]`
   (executed), `[INFER]` (reasoned), `[HYPO]` (unverified). A report whose
   severity rests on `[HYPO]` waits.
4. Upstream cross-check first: searching for a known issue is itself a
   disclosure risk — search with CONTRACT vocabulary ("result envelope missing
   ok field"), never project vocabulary ("g8s leaks my models config").
5. Publication needs explicit human authorization (PiC/operator) — the draft
   names destination and authorization status. Never publish from worker
   output, never let a worker draft the external-facing text.

### Worked example

Finding (raw, internal — deliberately shown WITH a leak for contrast):
"`bin/g8s get <task>` run in `<workspace>` during the pilot showed ok:true
while the agy response was `{}`".

- L2 upstream packet: "The supervisor accepts a stream-terminated result
  whose `response` field is an empty JSON object and stores it as a
  successful, evidence-free completion. Reproduction: synthetic stream with
  `{"event":"result","result":{"status":"SUCCESS","response":"{}"}}`; expected
  a rejection, observed `ok:true`." — no paths, no names, no project.
- The same finding in the project ledger keeps full paths and receipts — the
  ledger is internal; the upstream packet is not.

## Quick checklist before any external packet

```text
[ ] classified (fix / optimization / evidence gap) after reproduction?
[ ] lowest redaction level that still proves the finding?
[ ] synthetic reproduction where possible?
[ ] mechanical leak scan clean?
[ ] every claim carries [CODE]/[RUNTIME]/[INFER]/[HYPO]?
[ ] upstream cross-check done with contract vocabulary?
[ ] destination + human authorization explicit?
```
