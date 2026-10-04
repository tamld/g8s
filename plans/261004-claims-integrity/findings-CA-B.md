```
CLAIMS-AUDIT-B
BOUND: 7 (claim-containment-layers, claim-containment-attempt-isolation, claim-containment-receipt-single-use, claim-containment-session-registry, claim-containment-cleanup-reapers, claim-containment-poison-surface, claim-eval-pri-score)
OPERATIONAL-UNBOUND: 17
  - Generic CI gate scripts enforce spec-code sync, structure truth, and link integrity | demo: bash tools/ci_spec_code_sync.sh && bash tools/ci_structure_sync.sh && bash tools/ci_link_integrity.sh
  - Brief issuance enforces DoR and generates structured brief with TTL and permission | demo: bin/g8s brief-issue --title "Smoke Test" --payload "Payload referencing internal/worker" --dod "DoD" --permission read_only
  - Worker concurrency supports multi-attempt processing gated on worktree isolation | demo: bin/g8s worker --concurrency 2 --once=false
  - Offer subcommand deterministically scaffolds repository profiles and embedded gate scripts | demo: bin/g8s offer check
  - Supervisor watch monitors task states until terminal milestone | demo: bin/g8s watch --task non-existent-id --milestone worker-complete --timeout 1s
  - Terminal task failure events wake supervisor via signal file watching | demo: bin/g8s watch --failed --timeout 1s
  - Application version and build metadata emitted as JSON envelope | demo: bin/g8s version --json
  - Worker status report provides JSON observability of active, stale, and dead workers | demo: bin/g8s status --worker --json
  - Cleanup sweep detects and removes ghost processes and orphan resources | demo: bin/g8s cleanup --force --dry-run
  - Doctor diagnostics report system environment, database, workspace, and CLI health | demo: bin/g8s doctor --json
  - Delegated workspace_write is disabled by default unless explicitly enabled via environment variable | demo: bin/g8s submit --idempotency-key k1 --prompt "p" --permission workspace_write --receipt-id r1
  - Delivery is supervisor-owned provenance via receipt-validated apply | demo: bin/g8s deliver --help
  - Provider registry enforces two-class providers manifest | demo: bin/g8s providers
  - Verifier CLI evaluates task deliverables against declared verifier classes | demo: bin/g8s verify --help
  - Autonomy level configuration enforces binary {0,1} posture | demo: bin/g8s config get autonomy_level
  - Lesson subcommand creates, verifies, and lists retrospective lessons against telemetry database | demo: bin/g8s lesson list
  - Failed tasks can be resubmitted with supervisor receipt boundary | demo: bin/g8s resubmit --help
AMBIGUOUS: 10
  - offer/ONBOARDING.md:71 | Total: 5 fetches, zero git state in the target repo, ~30 seconds. | 5 HTTP GET requests retrieve all bundle files with zero git clones.
  - offer/profiles/infra.md:4 | Changes have physical blast radius — a bad apply takes down real machines. | Host mutation commands execute without container/virtualization boundaries.
  - offer/profiles/knowledge.md:4 | Writes are often human-led; agents audit, link-check, and distill | Markdown edits default to read_only role; workspace_write requires receipts.
  - offer/profiles/utility.md:4 | Low ceremony, high test determinism. | Package has zero third-party dependencies and passes go test ./... at 100%.
  - skills/g8s-supervisor/SKILL.md:17 | an agy-dispatch plugin is a compatibility bridge only. | The agy-dispatch plugin translates g8s JSON-RPC requests to AGY CLI args.
  - skills/g8s-supervisor/SKILL.md:48 | A session that never dispatches has no artifacts to optimize from | Optimization issue drafting requires at least one completed task receipt.
  - skills/g8s-supervisor/references/mode-2-supervisor-worker.md:42 | Parallelize only independent mechanical slices with disjoint roots. | Concurrent workers must execute against mutually disjoint path subtrees.
  - skills/g8s-supervisor/references/security-redaction-playbook.md:67 | Synthetic reproductions (L2) are the gold standard | Level-2 issue packets provide a minimal isolated tempdir reproduction.
  - skills/g8s-supervisor/references/operational-hard-lessons.md:46 | Worker verdicts (ok=true) lied repeatedly while the disk was empty. | Worker result ok=true does not assert file existence; verify diff on disk.
  - docs/ENTERPRISE_LEDGER.md:69 | UNPROVEN rows are aspirations with owners | UNPROVEN ledger rows describe designed capabilities lacking CI test proofs.
FALSE/STALE: 13
  - offer/README.md:18 | v4.0.0: the operating practice for dispatching work through g8s | contradiction: skills/g8s-supervisor/SKILL.md:5
  - offer/ONBOARDING.md:44 | g8s submit --role verifier --receipt-id <id> ... | contradiction: cmd/g8s/submit.go:99
  - cmd/g8s/offer.go:19 | const offerBundleVersion = "0.12.0" | contradiction: internal/version/version.go:12
  - skills/g8s-supervisor/references/anti-patterns.md:39 | receipt-issue → submit with receipt | contradiction: cmd/g8s/main.go:103
  - skills/g8s-supervisor/references/anti-patterns.md:40 | g8s status --worker --json every 30s | contradiction: skills/g8s-supervisor/references/operational-hard-lessons.md:19
  - skills/g8s-supervisor/references/operational-hard-lessons.md:96 | merging stays with the interactive supervisor | contradiction: docs/user-guide/verifier-and-autonomy.md:234
  - skills/g8s-supervisor/assets/issue-packet.md.tmpl:56 | sqlite3 control.db "SELECT * FROM tasks WHERE id='{{TASK_ID}}'" | contradiction: internal/controlplane/store.go:218
  - skills/g8s-supervisor/scripts/verify_g8s_pin.py:72 | if version_info.get("git_commit") != prov.get("git_commit"): | contradiction: cmd/g8s/version.go:86
  - manifest.json:10 | "commit": "90252b7b", "date": "2026-09-27" | contradiction: git tag v0.14.0 (04392854, 2026-10-04)
  - manifest.json:38 | "LSP Client (Stdio)" | contradiction: internal/codeintel/ast_adapter.go:98
  - manifest.json:132 | "id": "DELTA-06", ... "status": "APPLIED" | contradiction: spec/openspec/06-os-daemon-service-spec.md:3
  - docs/ENTERPRISE_LEDGER.md:10 | Last updated: 2026-10-02 (issue #480 closure: QA Hardening rows... | contradiction: CHANGELOG.md:10
  - docs/ENTERPRISE_LEDGER.md:44 | Runner wired to release checklist pending (ADR-0026 §1) | contradiction: docs/RELEASE_SOP.md:19
LEDGER-FLIP-CANDIDATES: 7
  - Cross-session containment on one host (multi-project) | PARTIAL → PROVEN | ADR-0028 accepted; PR #468 (identity-scoped ghost kill, state-dir worktree root) and PR #491 (instance identity, lease resilience) shipped and verified.
  - Harness self-audit probes registered in the eval suite | PROVEN (pending release wiring) → PROVEN (fully wired) | RELEASE_SOP Gate 8 (D-02) wired; 4/4 probes passed in v0.14.0 release gate via g8s eval run --category self-audit.
  - Verifier-class registry | UNTRACKED → PROVEN | SCORECARD S-7, PR #532 (g8s verify --task <id>, .g8s/verifier-classes.yml, RC1 red-cell suite passing).
  - Gated auto-merge | UNTRACKED → PROVEN | SCORECARD S-8, PR #533 (tools/merger.sh --pr <num> --task <id>, four fail-closed gates, RC1 red-cell suite passing).
  - Autonomy ladder configuration | UNTRACKED → PROVEN | SCORECARD S-8, PR #533 (g8s config get/set autonomy_level {0,1}, RC1 red-cell suite passing).
  - Budgeted auto-retry of failed tasks | UNTRACKED → PROVEN | ADR-0029, PR #501, #502, #539 (root-lineage budget, 5→20→60m backoff, g8s resubmit, RC2 red-cell suite passing).
  - Retrospective lessons pipeline | UNTRACKED → PROVEN | SCORECARD S-9, PR #545, #546 (g8s lesson create|verify|list, docs/lessons/ledger.jsonl, RT-I red-cell tests passing).
```
```
