```
CLAIMS-AUDIT-A
BOUND: 7 (claim-containment-layers, claim-containment-attempt-isolation, claim-containment-receipt-single-use, claim-containment-session-registry, claim-containment-cleanup-reapers, claim-containment-poison-surface, claim-eval-pri-score)
OPERATIONAL-UNBOUND: 20
  - README.md:104 Single attempt worker execution | demo: bin/g8s worker --help
  - README.md:105 Concurrent worker drain with isolated attempts | demo: bin/g8s worker --help
  - README.md:109 Signal-driven watch on failed/terminal tasks | demo: bin/g8s watch --help
  - README.md:118 Path-scoped write receipt issuance with bounded TTL | demo: bin/g8s receipt issue --issuer brain --path "./tests/*.py" --ttl 3600
  - README.md:128 System-1 reflex triage gate evaluating mutation risk | demo: bin/g8s reflex triage --summary "raise test deadline" --files "internal/runtime/verify_test.go"
  - README.md:129 Autopilot periodic maintenance tick execution | demo: bin/g8s autopilot tick
  - README.md:130 Idempotent dry-run lifecycle cleanup sweep | demo: bin/g8s cleanup --dry-run
  - CHANGELOG.md:14 Budgeted retry disabled by default | demo: bin/g8s config get auto_retry_enabled
  - CHANGELOG.md:14 Budgeted retry per-task retry cap default of 2 | demo: bin/g8s config get auto_retry_max_per_task
  - CHANGELOG.md:14 Budgeted retry per-hour store cap default of 10 | demo: bin/g8s config get auto_retry_max_per_hour
  - CHANGELOG.md:15 Submit command supports context routing modes | demo: bin/g8s submit --help
  - CHANGELOG.md:17 Verifier acceptance checks on completed task write scopes | demo: bin/g8s verify --help
  - CHANGELOG.md:18 Autonomy level posture defaults to manual (0) | demo: bin/g8s config get autonomy_level
  - CHANGELOG.md:37 Adversarial probe self-audit category passes 4/4 probes | demo: bin/g8s eval run --category self-audit --provider mock-compliant
  - docs/user-guide/configuration.md:20 Receipt TTL minimum bound is 1s, maximum bound is 3600s | demo: bin/g8s receipt issue --path "src/**" --ttl 0
  - docs/user-guide/configuration.md:28 Three built-in permission profiles active: read_only, automation_read, workspace_write | demo: bin/g8s permissions
  - docs/user-guide/configuration.md:35 Six built-in roles active: collector, mcp-mapper, scout, summarizer, test-runner, verifier | demo: bin/g8s roles
  - docs/user-guide/mcp-tools.md:3 Stdio MCP JSON-RPC server registers eleven tools | demo: bin/g8s mcp --help
  - docs/user-guide/autopilot.md:104 OS-native scheduler installation with periodic interval | demo: bin/g8s autopilot install-schedule --dry-run --every 10m
  - docs/user-guide/verifier-and-autonomy.md:213 Autonomy level rejects values above 1 | demo: bin/g8s config set autonomy_level 2
AMBIGUOUS: 10
  - README.md:7 | A lightweight, zero-trust process execution and capability harness | Single-binary zero-trust process execution harness (<25MB static pure-Go binary)
  - README.md:23 | delegate mechanical work to fast CLI workers | delegate mechanical work to external CLI worker processes (agy, Claude, Gemini, Ollama)
  - README.md:57 | Fast test generation & log digestion | Bounded-time test generation and log digestion (<60s per attempt)
  - README.vi.md:7 | Harness thực thi tiến trình zero-trust, siêu nhẹ, cho các AI agent worker... | Harness thực thi tiến trình zero-trust bằng binary tĩnh thuần Go (<25MB, không CGO)
  - README.vi.md:25 | giao việc cơ học cho các CLI worker nhanh | giao việc cơ học cho các tiến trình CLI worker (agy, Claude Code, Gemini CLI, Ollama)
  - CHANGELOG.md:20 | docs-only PRs clear a prose gate battery in minutes | docs-only PRs clear the prose gate battery with a targeted runtime under 5 minutes
  - docs/user-guide/autopilot.md:57 | Tolerates empty registries and fresh states cleanly. | Returns status ok with zero reaped items on empty registries or fresh states
  - docs/user-guide/providers.md:11 | ensure predictable, reproducible execution across heterogeneous AI agent CLIs | execute provider-affinity matching and command-template argument substitution
  - docs/user-guide/routing.md:9 | assigns tasks to the best-fit provider, model, and role based on task context | assigns provider, model, and role using deterministic rule evaluation over touched paths
  - docs/user-guide/verifier-and-autonomy.md:14 | assuming correctness across arbitrary write scopes creates unacceptable risk | unregistered write scopes fall back to human acceptance (exit 0) rather than auto-merging
FALSE/STALE: 20
  - docs/user-guide/receipt-workflow.md:13 | g8s receipt-issue -issuer brain -path './src/**' -ttl 300 | contradiction: cmd/g8s/main.go:609
  - docs/user-guide/cli-reference.md:616 | g8s service install --user | contradiction: cmd/g8s/service.go:42
  - docs/user-guide/cli-reference.md:323 | g8s autopilot start --cron "*/15 * * * *" --repo . | contradiction: cmd/g8s/autopilot.go:27
  - docs/user-guide/cli-reference.md:326 | g8s autopilot trigger --type issues | contradiction: cmd/g8s/autopilot.go:78
  - docs/user-guide/cli-reference.md:307 | g8s brief-issue \ --file ./BRIEF.md --title "Security audit" | contradiction: cmd/g8s/brief.go:34
  - docs/user-guide/cli-reference.md:214 | g8s orchestrate "Run security benchmark suite" | contradiction: cmd/g8s/orchestrate.go:462
  - docs/user-guide/cli-reference.md:368 | --delta-id "DELTA-02" --spec-anchor ... --confidence 0.95 | contradiction: cmd/g8s/vault.go:78
  - docs/user-guide/cli-reference.md:376 | g8s vault query --q "write receipts CAS" --limit 10 | contradiction: cmd/g8s/vault.go:142
  - docs/user-guide/cli-reference.md:11 | G8S_DB | Path to shared SQLite control-plane & receipt database | contradiction: cmd/g8s/main.go:606
  - docs/user-guide/cli-reference.md:620 | Persistent Configuration Keys (g8s config) | contradiction: internal/settings/settings.go:23
  - docs/user-guide/performance.md:5 | baseline for the throttle design, see store.go:1762 TODO | contradiction: internal/controlplane/store.go:1329
  - docs/user-guide/tenancy.md:23 | places its state in ~/.local/state/g8s (or %LOCALAPPDATA%\g8s on Windows) | contradiction: internal/pathutil/pathutil_windows.go:59
  - README.md:180 | Architecture decisions | [docs/decisions/](docs/decisions/): ADR-0001…0030 | contradiction: docs/decisions/0032-containment-levels.md:1
  - README.vi.md:142 | Decision records | [docs/decisions/](docs/decisions/): ADR-0001…0030 | contradiction: docs/decisions/0032-containment-levels.md:1
  - README.md:196 | | v0.14.0 (target) | ... | **In progress** | | contradiction: CHANGELOG.md:10
  - README.vi.md:158 | | v0.14.0 (mục tiêu) | ... | **Đang làm** | | contradiction: CHANGELOG.md:10
  - README.md:62 | ## Shipped in v0.13.0 | contradiction: CHANGELOG.md:10
  - README.vi.md:43 | ## Đã ship trong v0.13.0 | contradiction: CHANGELOG.md:10
  - README.md:66 | - Budgeted retry (landing): failed tasks auto-resubmit under caps... | contradiction: CHANGELOG.md:14
  - README.vi.md:47 | - Retry có ngân sách (đang land): task FAILED tự resubmit... | contradiction: CHANGELOG.md:14
```
