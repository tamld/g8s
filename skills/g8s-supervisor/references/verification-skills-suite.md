# g8s Verification Skills Suite — Supervisor Architecture Reference

> **SUPERVISOR REFERENCE**: Canonical operating architecture for the 6-module Verification Skills Suite in `g8s`. Ties automated adversarial verification, the 3-Layer Blind Verification Loop, and ALDC gate-lane routing into the F0 supervisor workflow.

---

## 1. Executive Overview & Philosophy

Modern agentic engineering suffers from three systemic vulnerabilities:
1. **AI Evaluation Awareness & Test Overfitting**: Workers pass visible tests while ignoring edge cases or breaking unspoken invariants.
2. **Reviewer Sycophancy & Prose Inflation**: Verifiers are persuaded by polished self-reports (`worker.Summary`, unverified `ReportedDoD`) rather than raw diffs.
3. **Vacuous Proof**: Tests pass because assertions are tautological or because bug repros were never proven to fail prior to the fix.

The **Verification Skills Suite** eliminates these vulnerabilities by replacing self-reported completion with **mechanical counterexample search, holdout test suites, seeded mutant canaries, and double-blind falsification**.

---

## 2. The 6 Loadable Verification Modules

| Skill Module | Domain / Focus | Key Threat Defeated | Primary Invocation |
|---|---|---|---|
| [`g8s-blind-verifier`](../../g8s-blind-verifier/SKILL.md) | Double-Blind Adversarial Review | Reviewer sycophancy, AI evaluation awareness | `g8s verify --blind` |
| [`g8s-red-prove`](../../g8s-red-prove/SKILL.md) | Mechanical Repro & Mutation Proof | Vacuous tests, phantom bugfixes | `g8s verify --red-green --canary-mutant` |
| [`g8s-scenario-matrix`](../../g8s-scenario-matrix/SKILL.md) | 12-Dimension Edge Matrix & Holdouts | Incomplete test suites, concurrency blindspots | `g8s matrix gen --holdout-dir` |
| [`g8s-ux-visual-gate`](../../g8s-ux-visual-gate/SKILL.md) | DOM Geometry, Contrast & Media | Visual regressions, clipping, accessibility | `g8s verify-ux --url` |
| [`g8s-sec-gate`](../../g8s-sec-gate/SKILL.md) | Zero-Trust SAST & Live DAST Probes | SSRF, path traversal, auth bypasses | `g8s sec probe --targets` |
| [`g8s-epistemic-audit`](../../g8s-epistemic-audit/SKILL.md) | Claim Decomposition & Purity | Epistemic inflation, observational confusion | `g8s epistemic audit --input` |

---

## 3. The 3-Layer Blind Verification Loop

F0 orchestrates verification across three independent layers before accepting any mutation:

```text
[ F1 Worker Worktree ]
         │ (Submits patch & diff)
         ▼
┌────────────────────────────────────────────────────────┐
│ Layer 1: Out-of-Worktree Holdout Suite                 │
│ Hidden tests in $G8S_STATE_DIR/holdout/<feature-id>    │
│ Mounted only during verification; invisible to F1/F2   │
└────────────────────────┬───────────────────────────────┘
                         │ Passed holdout
                         ▼
┌────────────────────────────────────────────────────────┐
│ Layer 2: Sanitized Double-Blind Review Packet          │
│ Strips worker model, prose summaries, commit messages  │
│ Independent Blind Verifier attempts falsification      │
└────────────────────────┬───────────────────────────────┘
                         │ No counterexample found
                         ▼
┌────────────────────────────────────────────────────────┐
│ Layer 3: Seeded Mutant Canary Check                    │
│ Deliberate synthetic defect injected (--canary-mutant) │
│ Rejection confirmed (Canary Caught = true)             │
└────────────────────────┬───────────────────────────────┘
                         │ Canary killed
                         ▼
                 [ VERIFIED MERGE ]
```

1. **Layer 1: Holdout Suite (`--holdout-dir`)**:
   Tests generated prior to implementation by `g8s-scenario-matrix` and stored in `$G8S_STATE_DIR/holdout/<feature-id>`. The worker cannot inspect or overfit to these tests because they lie outside `G8S_SCOPE_ROOT`.
2. **Layer 2: Double-Blind Review (`BlindReviewPacket`)**:
   The independent review worker receives only `SpecContract + Invariants + RawGitDiff + MechanicalTestOutput`. It evaluates pure code against formal invariants without reviewer sycophancy.
3. **Layer 3: Seeded Mutant Canary (`--canary-mutant`)**:
   Verifies that the test harness is sensitive by injecting a synthetic bug. If the mutant passes tests, verification fails with `VETO_CANARY_SURVIVED`.

---

## 4. F0 Supervisor Workflow & FSM Gate Mapping

The supervisor triggers verification skills deterministically at specific lifecycle transitions:

```text
       DoR Phase               Execution Phase              Pre-Merge Gate
┌─────────────────────┐     ┌─────────────────────┐     ┌─────────────────────┐
│ g8s-scenario-matrix │ ──> │ g8s-red-prove       │ ──> │ g8s-blind-verifier  │
│ (Generate holdouts) │     │ (Prove Red -> Green)│     │ g8s-ux-visual-gate  │
│                     │     │                     │     │ g8s-sec-gate        │
│                     │     │                     │     │ g8s-epistemic-audit │
└─────────────────────┘     └─────────────────────┘     └─────────────────────┘
```

### Stage 1: Definition of Ready (DoR)
- **Invoke `g8s-scenario-matrix`**: Expand feature requirements into 12-dimension test matrix. Split generated tests into visible in-tree specs and hidden out-of-worktree holdout suites.

### Stage 2: Defect Triage & Implementation
- **Invoke `g8s-red-prove`**: Require workers addressing bug reports to first produce an isolated failing repro (`Stage 1 Red`) and prove resolution (`Stage 2 Green`). Verify test sensitivity with `--canary-mutant`.

### Stage 3: Acceptance & Pre-Merge Gate
- **Invoke `g8s-blind-verifier`**: Generate `BlindReviewPacket` and run falsification review.
- **Invoke `g8s-ux-visual-gate`**: For frontend/UI changes, execute headless browser DOM bounding-box and WCAG contrast audit.
- **Invoke `g8s-sec-gate`**: Execute SAST triad and live DAST traversal/SSRF probes against `.g8s/trust-boundaries.yml`.
- **Invoke `g8s-epistemic-audit`**: Classify worker claims and release notes. Require `epistemic_purity_ratio >= 0.80` and zero violations of the 4 Epistemic Laws.

---

## 5. ALDC Lane Routing (`0/D/R/F/S`) Alignment

Verification skills map directly to ALDC gate lanes in `.g8s/lane-bundles.yml`:

| ALDC Lane | Name | Trigger Conditions | Required Verification Skills |
|---|---|---|---|
| **0** | Zero Gate | Trivial comment / whitespace changes | None (direct commit) |
| **D** | Docs Gate | Prose, plans, specs, schemas | `g8s-epistemic-audit`, doc-contract, link integrity |
| **R** | Review Gate | Non-critical bugfixes, internal utils | `g8s-red-prove`, `g8s-blind-verifier` |
| **F** | Feature Gate | New capabilities, cross-package changes | `g8s-scenario-matrix`, `g8s-blind-verifier`, `g8s-ux-visual-gate` |
| **S** | Security Gate | Network, IPC, auth, storage, receipts | `g8s-sec-gate`, `g8s-blind-verifier`, `g8s-red-prove` |

---

## 6. Recursive Delegation Protocol (`F0 -> F1 -> F2`)

To maintain containment and cognitive purity:
- **`F0`** retains sole authority for issuing `WriteReceipt`, signing `BlindReviewPacket`, and executing final `g8s deliver`.
- **`F1`** runs bounded verification harnesses in isolated process groups and worktrees.
- **`F2`** subagents are spawned recursively by F1 to generate test vectors, probe endpoints, or verify citations, sharing F1's isolation boundary (`F2 ⊆ F1 ⊆ F0`). F2 never interacts directly with F0.
