# OpenSpec Registry for g8s

This directory contains the **Technical Delta Specifications (OpenSpec)** governing all incremental modifications, schema additions, and engine enhancements for `g8s`.

---

## 📋 OpenSpec Index

| Spec ID | Title | Target Package | Milestone | Status |
| :--- | :--- | :--- | :--- | :--- |
| **[`DELTA-01`](01-core-harness-spec.md)** | Core Role & Permission Harness | `internal/harness` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-02`](02-receipt-delegation-spec.md)** | Write Receipt Delegation Engine | `internal/receipt` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-03`](03-controlplane-sqlite-spec.md)** | SQLite WAL Control Plane & Leases | `internal/controlplane` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-04`](04-mcp-stdio-server-spec.md)** | Stdio JSON-RPC 2.0 MCP Server | `internal/mcp` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-05`](05-provider-and-resource-pool-spec.md)**| Provider Registry & Resource Pool Governor | `internal/provider` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-06`](06-os-daemon-service-spec.md)** | Cross-Platform OS Daemon Service | `internal/service` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-07`](07-blast-radius-analyzer-spec.md)** | LSP & Dependency Blast Radius Analyzer | `internal/analyzer` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-08`](08-dispatch-wrapper-spec.md)** | Bounded AGY CLI Dispatch Wrapper | `internal/dispatch` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-09`](09-worker-supervisor-spec.md)** | Worker Process Supervisor & Sandbox | `internal/worker` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-10`](10-two-class-providers-spec.md)** | Two-Class Providers Architecture (API vs Dispatch) | `internal/provider` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-11`](11-knowledge-vault-spec.md)** | Pure-Go Decoupled Knowledge Vault | `internal/vault` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-11-ROADMAP`](11-orchestration-roadmap-spec.md)** | Supervisor Fix Loop & Orchestration Roadmap | `internal/supervisor` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-12`](12-lineage-cte-and-stream-pipe-spec.md)** | Recursive CTE Lineage & Stream Pipe | `internal/controlplane` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-13`](13-dx-ax-init-and-autorepair-spec.md)** | DX/AX Multi-IDE Init & Auto-Repair | `internal/initwiz` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-14`](14-core-hardening-and-doctor-spec.md)** | Core Hardening, Doctor Security Guards, Autopilot & HTTP Daemon | `internal/doctor`, `internal/autopilot`, `internal/server` | M4 (Hardening & DX/AX) | `APPLIED` |
| **[`DELTA-15`](15-orchestrator-fsm-and-brief-spec.md)** | Orchestrator 8-State FSM Lifecycle & Brief Dispatch Store | `internal/orchestrator`, `internal/state`, `internal/brief` | M4 (Orchestrator FSM) | `APPLIED` |
| **[`DELTA-16`](16-lifecycle-hygiene-and-envelope-spec.md)** | Lifecycle Hygiene, Worker Heartbeats, Process Lister & Unified JSON Envelope v1 | `internal/heartbeat`, `internal/process`, `internal/cleanup`, `internal/cli` | M4 (Lifecycle Hygiene) | `APPLIED` |
| **[`DELTA-17`](17-receipt-and-evidence-lake-spec.md)** | Receipt Lake Control-Plane Wiring & Closed-Loop Telemetry Ledger | `internal/controlplane`, `internal/telemetry` | M4 (Evidence Lake) | `APPLIED` |
| **[`DELTA-18`](18-aic-and-diffintel-review-spec.md)** | AIC Automated PR Review, DiffDistiller & From-Intent FanOut | `cmd/g8s`, `internal/diffintel`, `internal/review` | M4 (AIC & Intent) | `APPLIED` |
| **[`DELTA-19`](19-lego-mounts-and-reflex-governance-spec.md)** | Stateful Worker Lego Mounts, System-1 Reflex & Governance Registries | `internal/orchestrator`, `internal/reflex`, `internal/routing`, `internal/verifier` | M5 (Lego Mounts & Reflex) | `APPLIED` |
| **[`DELTA-20`](20-code-intel-adapter-spec.md)** | Multi-Tier Code Intelligence Adapter & Dynamic Blast Radius | `internal/codeintel` | M5 (Robustness & Evals) | `APPLIED` |
| **[`DELTA-21`](21-unified-memory-adapter-spec.md)** | Unified Decoupled Memory Layer & Hybrid Retrieval Adapter | `internal/memory` | M5 (Robustness & Evals) | `APPLIED` |
| **[`DELTA-22`](22-brief-dor-floor-spec.md)** | Brief DoR Floor + Advisory Skill Routing | `internal/brief` | M6 (Memory & Routing) | `APPLIED` |
| **[`DELTA-23`](23-native-agent-io-spec.md)** | Native Cross-Platform Agent I/O (`search`, `read`, `edit`, `--select`) | `internal/fsops`, `internal/search` | M6 (Agent Ergonomics) | `APPLIED` |
| **[`DELTA-24`](24-differential-governance-hitl-spec.md)** | Differential Governance, Spatial JIT Whisper, Net-Delta Diff Guard & Sovereign HITL Calibration | `internal/vault`, `internal/verifier`, `internal/telemetry` | M6 (Governance Hardening) | `APPLIED` |
| **[`DELTA-25`](25-config-driven-llm-consumer-spec.md)** | Universal Config-Driven LLM Call Consumer & Multi-Model Gateway | `internal/provider`, `internal/config`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-26`](26-worker-isolation-fail-closed-spec.md)** | Fail-Closed Worktree Isolation for Workspace Write Tasks | `internal/worker` | M6 (Safety Hardening) | `APPLIED` |
| **[`DELTA-27`](27-server-bind-auth-spec.md)** | Daemon Server Bind Address & API Token Authentication Enforcement | `internal/server` | M6 (Daemon Hardening) | `APPLIED` |
| **[`DELTA-28`](28-dod-mechanical-binding-spec.md)** | Definition of Done Mechanical Evidence Binding | `internal/verifier`, `cmd/g8s` | M6 (Verifier Binding) | `APPLIED` |
| **[`DELTA-29`](29-output-schema-validation-spec.md)** | Task Output Schema Validation (JSON Schema Draft 7) | `internal/worker` | M6 (Agent Ergonomics) | `APPLIED` |
| **[`DELTA-30`](30-worker-claim-affinity-spec.md)** | Worker Claim Affinity Enforcement (Provider and Model) | `internal/controlplane`, `internal/worker`, `cmd/g8s` | M6 (Claim Affinity) | `APPLIED` |
| **[`DELTA-31`](31-receipt-export-status-spec.md)** | Receipt Export Status Persistence and Idempotent Operator Recovery | `internal/controlplane`, `internal/worker`, `cmd/g8s` | M6 (Evidence Lake) | `APPLIED` |
| **[`DELTA-32`](32-bounded-agentic-loop-f1f2-fleet-jev-spec.md)** | Bounded Agentic Loop, Adaptive Effort Timeout, `F1->F2` Subtask Containment, `g8s fleet status`, and Monotonic `Jev` Routing | `cmd/g8s`, `internal/routing` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-33`](33-stratum-federation-and-charter-spec.md)** | Orthogonal Stratum Federation (`S0..S3` + `O-Plane`) and Epoch-Fenced Charter Engine | `internal/charter`, `internal/fsops`, `internal/mcp`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-34`](34-mcp-skill-hydration-and-diagnosis-spec.md)** | F0/F1/F2 Digest-Pinned Skill Hydration, Typed FailureDiagnosis & CAS Head Delivery | `internal/brief`, `internal/mcp`, `internal/routing`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-35`](35-pre-execution-research-debate-gate-spec.md)** | Mandatory Pre-Execution Research, CVE & Socratic Debate Gate and 10-Role Taxonomy | `internal/brief`, `internal/harness`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-36`](36-contextpack-ast-knapsack-spec.md)** | Pure-Go AST Elision, Personalized PageRank, and Multiple-Choice Knapsack Context Compressor | `internal/contextpack`, `internal/mcp`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-37`](37-six-stage-ai-qa-pipeline-spec.md)** | End-to-End 6-Stage AI QA Pipeline (BA-to-Spec Matrix, Holdout Partition, and Epistemic QA Report) | `internal/verifier`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-38`](38-device-lease-and-mobile-gate-spec.md)** | Exclusive Device-Lease Concurrency Governor & `g8s-mobile-gate` Emulator Contract | `internal/provider`, `skills/g8s-mobile-gate` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-39`](39-universal-cli-agent-adapter-spec.md)** | Universal CLI Agent Adapter Suite (`opencode`, `commandcode`, `codex`) & Free-Model Tiered Routing | `internal/provider`, `internal/worker`, `internal/routing` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-40`](40-lifecycle-uninstall-update-spec.md)** | Binary Lifecycle CLI (`g8s update`, `g8s uninstall`), Build Manifest & Merged Worker-Branch Sweep Repair | `internal/lifecycle`, `internal/cleanup`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-41`](41-feature-map-v2-collision-domains-spec.md)** | Feature Map v2 — Collision Domains, Exclusive Ownership & Pre-Dispatch Collision Verdicts | `internal/featuremap`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-42`](42-premise-challenge-and-sycophancy-gate-spec.md)** | Premise Challenge & Anti-Sycophancy Evidence Gate | `internal/premise`, `internal/harness/probe`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-43`](43-provider-conformance-matrix-spec.md)** | Provider Conformance Ground-Truth Matrix & Role-Tier Gating | `internal/provider/conformance`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-45`](45-five-axis-kaizen-scorecard-spec.md)** | 5-Axis F1/F2/Tool/Skill/Receipt Cross-Evaluation & Kaizen Engine | `internal/routing`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `APPLIED` |
| **[`DELTA-46`](46-peer-coordinator-ledger-spec.md)** | Peer Coordinator Ledger | `internal/mesh`, `internal/mcp`, `cmd/g8s` | M6 (LLM Consumer & Fleet) | `PROPOSED` |

---

## 📜 OpenSpec Lifecycle

```
PROPOSED ──► ACCEPTED ──► IMPLEMENTING ──► APPLIED (Passes -race tests) ──► RETIRED
```
