# Research Report: Product-Site Patterns for Dev-Tools & Infrastructure OSS

- **Document ID**: `docs/research/28-product-site-patterns.md`
- **Scope**: Rebuilding `g8s` as an engineering-grade product website (install, quickstart, capability demos, docs, community)
- **Status**: Complete & Evidence-Grounded

---

## Executive Summary & Study List

To transition `g8s` from an internal specification repository into an adoption-focused open-source product site, we audited 6 best-in-class infrastructure, developer tooling, and AI-trust websites. Each site was evaluated for its information architecture (IA), installation flow, capability demonstration devices, documentation entrypoints, tone, and conversion mechanics.

| # | Product | URL | Core Paradigm Studied |
|---|---|---|---|
| 1 | **Oxide Computer** | `https://oxide.computer` | High-taste hardware/software co-design; "Fig. 1" schematic device framing |
| 2 | **Temporal** | `https://temporal.io` | Durable execution engine; dual CTA (cloud vs local); failure-resilience demo |
| 3 | **Dagger** | `https://dagger.io` | Containerized CI engine; "Ask your agent" copy-prompt; Asciinema terminal replays |
| 4 | **Tailscale** | `https://tailscale.com` | Zero-config mesh networking; multi-OS install tabs; progressive mental models |
| 5 | **GitHub Codespaces / Next** | `https://github.com/features/codespaces` & `https://githubnext.com` | Instant environment provisioning; research-to-prototype capability cards |
| 6 | **Anthropic** | `https://www.anthropic.com` | AI safety & governance; empirical System Cards; constitutional framing |

---

## 1. Per-Site Pattern Analysis

### Comparative Pattern Matrix

| Site & URL | Section Order (IA) | Install / Quickstart Pattern | Capability Demo Device | Docs IA & Entrypoints | Tone & Voice | What to Steal for g8s |
|---|---|---|---|---|---|---|
| **Oxide Computer**<br>`https://oxide.computer` | 1. Nav (Product, Solutions, Resources, Try now)<br>2. Hero ("On-prem that feels like public cloud")<br>3. "Fig. 1" Multi-surface interactive rack<br>4. 4 Architectural pillars (Compute, Network, Power, Storage)<br>5. The False Dichotomy (Cloud vs DIY)<br>6. Workload solutions (VMware exit, AI, HPC)<br>7. Tool ecosystem (Terraform, K8s)<br>8. Contact CTA | Hardware appliance: "Try now" / "Contact Sales". Open-source CLI/firmware (Hubris, Propolis): binary download & `cargo install`. | Interactive schematic viewer labelled "Fig. 1" with tab toggles: `[CLI]`, `[API]`, `[Console]`. Visitors inspect real command output and JSON payloads. | RFD (Requests for Discussion) public archive, hardware schematics, operator manuals, and API reference guides. | Uncompromising craftsmanship, proud systems engineering, deep reverence for physical and architectural reality. Zero buzzwords. | The "Fig. 1" artifact aesthetic; the 3-way toggle (`CLI` / `API` / `MCP`) showing the exact same underlying primitive across different consumption layers. |
| **Temporal**<br>`https://temporal.io` | 1. Nav & Announcement banner<br>2. Hero ("The world's best AI runs on Temporal")<br>3. Dual CTA ([Get Started Free] vs [Run Locally])<br>4. Failure resilience proof ("Write code as if failure doesn't exist")<br>5. "How it Works" (5 sequential primitives)<br>6. Use-case grid (AI, MCP, CI/CD, Humans-in-the-loop)<br>7. Social proof (OpenAI, NVIDIA, Mitchell Hashimoto quotes)<br>8. Open Source Pedigree (MIT, 24k stars, SQS/Cadence lineage)<br>9. Deployment choices (Self-hosted vs Cloud) | Dual-track: One-click cloud sandbox vs local dev server (`brew install temporal` / `temporal server start-dev` / Docker). | Interactive failure simulation: demonstrates what happens when an API dies mid-execution, proving state picks up without orphaned processes. | Quickstart (<5 min), language SDK guides (Go, TypeScript, Python, Java), architectural concepts, and Temporal 101/102 courses. | Systems-grade, authoritative, reassuring. Focuses on the eradication of developer anxiety and firefighting. | Dual CTA ([Run Locally] vs [Quickstart]); explicit data-privacy pledge ("We never see your code"); "Without X vs With X" failure resilience framing. |
| **Dagger**<br>`https://dagger.io` | 1. Nav (Integrations, Pricing, Docs, Changelog, Discord)<br>2. Hero ("CI that keeps up: 3rd-generation CI")<br>3. "Ask your agent" copy-prompt block<br>4. Peer testimonials (Fastly, Typesafe, Nvidia)<br>5. "How it Works" (modules, typed artifacts, lazy DAG)<br>6. Core features with terminal recordings<br>7. Open-source cost model & transparent pricing<br>8. Colophon | "Ask your agent: [Copy Prompt]" on homepage. In docs: one-line script `curl -fsSL https://dl.dagger.io/dagger/install.sh \| BIN_DIR=$HOME/.local/bin sh` + PowerShell script for Windows. | Embedded Asciinema (`.cast`) terminal players showing real CLI runs, interactive test inspection, and container shell attachment. | Clean progression: How it works -> Install -> Quickstart -> Core Concepts -> Language SDKs -> Cookbooks -> API Reference. | Pragmatic, direct, peer-to-peer developer tone. Rebellious against YAML sprawl ("bash-in-YAML-in-Git", "No more push and pray"). | The "Ask your agent" copy-paste setup prompt; embedded Asciinema terminal recordings; generational categorization ("third-generation harness"). |
| **Tailscale**<br>`https://tailscale.com` | 1. Nav (Platform, Solutions, Customers, Pricing)<br>2. Hero ("Zero Trust connectivity platform for AI era")<br>3. Feature cards (VPN, PAM, Securing AI, Infra)<br>4. Deep architectural narrative ("How Tailscale Works")<br>5. Social proof wall ("Wall of Love" developer tweets)<br>6. Security guarantees (WireGuard, key exchange only)<br>7. Download CTA per OS | Dedicated `/download` hub with OS switcher tabs (macOS, iOS, Windows, Linux). Prominent `curl -fsSL https://tailscale.com/install.sh \| sh` with "View script source" button. | Interactive step-by-step packet flow diagrams showing peer-to-peer tunnels punching through NAT without traversing a central server. | Progressive disclosure: 2-minute quickstart -> How-to guides by device -> Concepts (MagicDNS, ACLs, DERP) -> API/CLI reference. | Disarmingly humble, lucid, conversational engineering prose. Explains complex cryptography and networking simply without dumbing down. | OS switcher tabs with "View script source" next to `curl \| sh`; separation of Control Plane vs Data Plane; "Zero trust one device at a time" progressive adoption curve. |
| **GitHub Codespaces / Next**<br>`https://github.com/features/codespaces`<br>`https://githubnext.com` | 1. Nav<br>2. Hero ("Code from any device / Onboard at speed of thought")<br>3. One-click IDE launch preview<br>4. Value pillars (zero configuration, devcontainers)<br>5. GitHub Next prototype cards (Autoloop, Repo Mind, Knowledge Compressor)<br>6. Pricing & self-hosting FAQ | Instant cloud boot (no local install needed) via repository `Code` button or CLI `gh codespace create`. | Animated browser frame showcasing devcontainer initialization, terminal spin-up, and live port forwarding in <10s. | Task-oriented guides: Quickstart, devcontainer specifications, lifecycle hooks, and resource quota management. | Polished, aspirational, developer-enablement tone focused on removing onboarding friction and environment drift. | Framing environment isolation as "Instant Onboarding"; GitHub Next's modular research cards showcasing tangible prototypes before full feature shipping. |
| **Anthropic**<br>`https://www.anthropic.com` | 1. Nav (News, Research, Policy, Commitments, Try Claude)<br>2. Hero ("AI research and products that put safety at frontier")<br>3. Model releases linked directly to System Cards<br>4. Safety foundations (Claude Constitution, RSP)<br>5. Enterprise trust & compliance standards | API keys via web console, SDKs (`pip install anthropic`, `npm install @anthropic-ai/sdk`), and desktop client. | Empirical benchmark tables, jailbreak resistance graphs, and published evaluation transcripts. | Clear split: API Developer Guides (Prompt engineering, Tool use / MCP) vs Safety Research & Model System Cards. | Institutional, scientific, measured, empirical. Zero marketing fluff; every capability claim is tied to an evaluation artifact. | The "System Card" pattern: backing every safety claim with a downloadable, machine-checked artifact (`claims.yml` and test logs); constitutional governance framing. |

### Architectural Deep-Dives by Site

1. **Oxide Computer (`https://oxide.computer`)**:
   - *Core Strength*: Treating hardware and low-level software with the aesthetic reverence of high-precision laboratory instruments. The "Fig. 1" component immediately anchors the product as tangible engineering.
   - *Key Lesson for g8s*: Rather than presenting `g8s` as an invisible daemon or abstract concept, present the harness as a concrete instrument ("Fig. 1 The g8s Process Harness") with interactive inspection tabs switching between CLI terminal output, JSON-RPC MCP frames, and SQLite WAL events.

2. **Temporal (`https://temporal.io`)**:
   - *Core Strength*: Conquering developer cynicism through concrete failure simulation. Instead of saying "reliable," Temporal shows an API call crashing, a worker restarting, and the exact state resuming without data corruption.
   - *Key Lesson for g8s*: Adopt Temporal's "Without X vs With X" framing and dual CTA ([Run Locally] vs [Quickstart Guide]). The primary fear of running coding agents locally is silent damage: accidental deletions, leaked secrets, or orphan processes. Showing `g8s` intercepting a bad write receipt eliminates this fear directly.

3. **Dagger (`https://dagger.io`)**:
   - *Core Strength*: The "Ask your agent" copy-paste block is a breakthrough in developer-tool onboarding for the AI era. Dagger recognizes that developers now delegate setup to AI coding agents, providing an optimized prompt alongside the traditional `curl | sh` script. Embedded Asciinema (`.cast`) terminal sessions show real CLI execution with zero marketing exaggeration.
   - *Key Lesson for g8s*: Implement both the "Ask your agent" prompt block and real terminal session captures. Frame `g8s` as a third-generation execution harness: *First generation*: Ambient shell scripts $\rightarrow$ *Second generation*: Proprietary cloud agent sandboxes $\rightarrow$ *Third generation*: Pure-Go local zero-trust process harness.

4. **Tailscale (`https://tailscale.com`)**:
   - *Core Strength*: Making complex networking and zero-trust concepts immediately understandable. Tailscale distinguishes between the Coordination Plane (public keys only) and the Data Plane (encrypted peer-to-peer mesh), assuring users that Tailscale never inspects their traffic.
   - *Key Lesson for g8s*: Emulate Tailscale's clean separation of Control Plane (SQLite task queue, write receipts, F0 brain orchestration) and Execution Plane (isolated Git worktrees and POSIX process groups). Use Tailscale's download tab design with a clear "View script source" button next to `curl | sh`.

5. **GitHub Codespaces & GitHub Next (`https://github.com/features/codespaces`, `https://githubnext.com`)**:
   - *Core Strength*: Visualizing sandboxed development environments not as an operational burden, but as instant freedom. GitHub Next packages research prototypes into modular cards with tangible problem statements and short screen recordings.
   - *Key Lesson for g8s*: Showcase capability modules (ContextPack AST compression, 6-Stage QA pipeline, Stratum Charters, Peer Coordinator Mesh) as clear capability cards with reproducible CLI invocations.

6. **Anthropic (`https://www.anthropic.com`)**:
   - *Core Strength*: Replacing marketing hype with empirical evaluation artifacts (System Cards) and explicit governance frameworks (Constitutional AI).
   - *Key Lesson for g8s*: Anchor `g8s` trust in its constitutional invariants (Zero-CGO, 5 Foundational Axioms, `claims.yml`, SAST Triad). Link every safety claim directly to a machine-verifiable test suite or specification document.

---

## 2. Synthesis: The Universal Skeleton of Elite Dev-Tool Product Sites

Analyzing these high-converting developer tools reveals an invariant structural skeleton. Developers arrive skeptical, impatient, and protective of their local machines. The site must answer questions in an exact psychological sequence across 9 distinct stages:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                       VISITOR COGNITIVE JOURNEY                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ 1. The Mental Model (0–5s)       ──► "What category is this & what replaces it?" │
│ 2. The Friction / Why Now (5–15s) ──► "What breaks if I don't use this?"     │
│ 3. The 60-Second Install (15–30s)──► "Can I install it without signup/bloat?"│
│ 4. Capability Proof (30–60s)     ──► "Show me real CLI output, not marketing."│
│ 5. Architecture & Primitives     ──► "How does this work under the hood?"   │
│ 6. Interoperability & Runtimes   ──► "Does it work with my models and tools?"│
│ 7. Security & Grounded Trust     ──► "Will this brick my machine or leak data?"│
│ 8. Adoption Pathways & FAQ       ──► "How do I adopt incrementally?"        │
│ 9. Docs Hub & Community          ──► "Where do I go to build?"              │
└─────────────────────────────────────────────────────────────────────────────┘
```

Each stage must be addressed by an explicit visual section that eliminates friction before the developer has time to hit the back button.

---

## 3. Information Architecture Proposal for the g8s Product Site

The proposed `g8s` product site is organized into 9 explicit sections. Each section answers one mandatory visitor question, guiding the visitor from initial curiosity to a verified, running binary in $\le 60$ seconds.

### Section 1: Navigation & Header
- **Visitor Question**: *"Where am I, is this actively maintained, and where are the docs and source?"*
- **Section Role**: Sticky orienting context and instant trust signals.
- **Component Layout**:
  - Left: `g8s` SVG icon + monospace version tag (`v0.22.0`) linked to GitHub releases.
  - Center: Nav anchors: `Overview`, `Features`, `Quickstart`, `Architecture`, `Docs`, `Offer Bundle`.
  - Right: Real-time GitHub Star counter badge + Primary Action button: `Install g8s`.

### Section 2: Hero — The Core Mental Model & Instant Installation
- **Visitor Question**: *"What is g8s, what does it replace, and what is the single command to run it right now?"*
- **Section Role**: Category anchoring, immediate mental model alignment, and zero-click conversion.
- **Content & Copy**:
  - **Category Badge**: `Open Source Zero-Trust Process Harness (<25MB Pure-Go Binary)`
  - **Headline**: *"Contain and orchestrate your AI coding agents with zero ambient shell authority."*
  - **Subheadline**: *"k8s orchestrates compute containers; g8s orchestrates AI agent CLI workers. Private Git worktrees, path-scoped write receipts, and lossless verification firewalls in a single static binary."*
  - **The Dual-Action Hub**:
    1. *Direct Developer CLI Copy-Paste*:
       ```bash
       curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
       ```
    2. *2026 AI Agent Copy-Prompt (The Dagger Pattern)*:
       ```text
       Install g8s from https://github.com/tamld/g8s, run `g8s doctor --json`,
       and configure g8s stdio MCP server in my agent settings.
       ```
  - **Hero Visual**: "Fig. 1 The g8s Harness" — Tabbed terminal viewport displaying real `g8s doctor --json` diagnostics completing in 14ms across environment, sandbox, and provider boundaries.

### Section 3: The Problem — Ambient Shell Danger vs. Zero-Trust Harness
- **Visitor Question**: *"Why can't I just let Claude or Cursor run raw bash commands on my machine?"*
- **Section Role**: Validating developer pain, establishing the threat model, and proving urgency.
- **Content & Visual**:
  - Two-column comparative matrix:
    - **Ambient Shell (The Status Quo)**:
      - Execution: Unbounded user-level execution; worker processes can touch any file or run destructive commands.
      - Process Safety: Runaway child processes survive crashes as orphan zombies consuming 100% CPU.
      - Filesystem: Accidental edits and deletions directly corrupt the primary working tree.
      - Context Flooding: Megabytes of raw compiler logs flood the coordinator context window.
    - **g8s Zero-Trust Harness (The New Standard)**:
      - Execution: Role-based capability profiles (`read_only`, `workspace_write`).
      - Process Safety: POSIX process groups (`Setpgid`) terminate cleanly on timeout or abort signals.
      - Filesystem: Path-scoped, single-use `WriteReceipt`s enforced in-process (`AllowedPaths`).
      - Context Hygiene: Lossless Information Firewall (`RoundReceiptSchema`) extracts structured failures and strips noise.

### Section 4: 60-Second Install & Multi-Platform Quickstart
- **Visitor Question**: *"How do I install this on my specific OS, and how do I know the install script is safe?"*
- **Section Role**: Frictionless onboarding and security transparency.
- **Content & Layout**:
  - Platform Switcher Tabs: `macOS (Apple Silicon & Intel)`, `Linux (amd64 & arm64)`, `Windows (PowerShell & Winget)`, `Go Toolchain (`go install`)`.
  - Code snippet block with copy button and auto-detected platform tab.
  - Security Transparency Link: `[View install.sh source code on GitHub]` (The Tailscale trust pattern).
  - Immediate Verification Command:
    ```bash
    g8s doctor --json
    ```
  - Callout: *"Single static pure-Go binary with zero CGO dependencies. Runs 100% locally with zero phone-home network calls."*

### Section 5: Capability Showcase — 3 Concrete Workflows (Show, Don't Tell)
- **Visitor Question**: *"Show me what it actually does with real commands, outputs, and exit codes."*
- **Section Role**: Tangible proof of capability using embedded Asciinema terminal recordings or tabbed CLI replay cards:
  - **Workflow A: Read-Only Codebase Reconnaissance**:
    `g8s submit --permission read_only --prompt "Scan internal/provider for exported interfaces"` $\rightarrow$ Worker is jailed in a read-only worktree; attempts to write fail immediately.
  - **Workflow B: Path-Scoped Mutation via Write Receipt**:
    `RECEIPT=$(g8s receipt issue --path "internal/config/*.go")` $\rightarrow$ Worker attempts to modify an out-of-scope file and receives `E_RECEIPT_SCOPE_EXCEEDED`; only designated files are updated.
  - **Workflow C: 6-Stage AI QA & Information Firewall**:
    `g8s verify --task <id> --json` $\rightarrow$ Runs out-of-worktree holdout test suites, executes seeded mutant canary checks, and returns an 800-byte structured receipt rather than 10MB of raw compiler logs.

### Section 6: How It Works — Architecture, Containment Rings & Lifecycle
- **Visitor Question**: *"How does it guarantee safety and coordination under the hood?"*
- **Section Role**: Architectural depth for senior engineers and platform architects.
- **Content & Diagram**:
  - Interactive SVG architecture diagram showing the 2-Axis Zero-Trust Architecture:
    - **Axis X (Physical Containment)**: `F0 Brain` (Strategic planner) $\rightarrow$ `F1 Worker` (Private Git worktree + POSIX process group) $\rightarrow$ `F2 Subagent` (Inherited sandbox).
    - **Axis Y (Control Strata)**: `S0 Human` $\rightarrow$ `S1 Charter` $\rightarrow$ `S2 Domain Governor` $\rightarrow$ `S3 Mutator & Adjudicator`.
  - The 5-Step Execution Lifecycle:
    `1. Submit & Gate` $\rightarrow$ `2. CAS Lease (SQLite WAL)` $\rightarrow$ `3. Worktree Containment` $\rightarrow$ `4. 6-Stage QA Verification` $\rightarrow$ `5. CAS Delivery`.

### Section 7: Ecosystem & Interoperability — Works With What You Have
- **Visitor Question**: *"Does this work with my current AI models, IDEs, and coding tools?"*
- **Section Role**: Eliminating adoption barriers and platform lock-in concerns.
- **Content & Integration Grid**:
  - **IDE & MCP Layer**: Native stdio MCP server (`g8s mcp`) exposing 22 tools to Claude Desktop, Cursor, Windsurf, and Antigravity.
  - **Worker Runtimes Supported**: Pluggable CLI adapters for `agy`, `claude-code`, `codex`, `opencode`, `gemini`, `ollama`, or custom bash scripts.
  - **Universal LLM Consumer**: Direct synchronous CLI execution (`g8s llm-call`) and background task queuing across OpenAI, Anthropic, and 9Router with continuous scorecard monitoring.
  - **Native Agent I/O**: High-performance pure-Go replacements for external tools (`g8s search`, `g8s read`, `g8s edit`, `g8s select`, `g8s context-pack`).

### Section 8: Grounded Trust, AppSec & System Card
- **Visitor Question**: *"Why should I trust this binary running on my development machine or CI pipeline?"*
- **Section Role**: Converting security-conscious developers, security leads, and enterprise platform teams.
- **Content & Trust Metrics**:
  - **Quantitative Guarantees**: Zero CGO (`modernc.org/sqlite`), <25MB binary footprint, sub-millisecond SQLite WAL lease acquisition, zero network telemetry.
  - **The SAST Triad**: Every commit verified via `gosec`, offline `Semgrep` rules (`.semgrep/g8s-rules.yml`), and GitHub `CodeQL`.
  - **Evidence-First Verification**: Direct links to `docs/claims.yml` and machine-verifiable compliance matrices (OWASP Top 10 for Agentic Apps 2025, SLSA v1.0, SPDX SBOM).
  - **Epistemic Classification**: Strict distinction between `MECHANICAL_VERIFIED` machine test outputs and `UNVERIFIED_PROSE`.

### Section 9: Persona Pathways, Direct FAQ & Docs Colophon
- **Visitor Question**: *"Is this right for my specific use-case, how does it compare to Docker, and where do I learn more?"*
- **Section Role**: Self-selection, objection handling, and deep dive links.
- **Content**:
  - **3 Persona Pathways**:
    1. *Solo Builder / Hacker*: Run long-running agent coding loops overnight without waking up to broken git trees.
    2. *Platform / Enterprise Team*: Standardize agent governance with immutable audit receipts and CAS merge locks.
    3. *OSS Maintainer*: Triage agent pull requests through out-of-worktree holdout suites.
  - **Direct FAQ Accordion**:
    - *"Does g8s replace Docker?"* (No; g8s provides lightweight sub-second process and worktree isolation, pairing with Docker when full container virtualization is required).
    - *"How does g8s compare to LangChain or CrewAI?"* (Those are prompt/application frameworks; g8s is an operating system execution harness and safety supervisor for CLI binaries).
    - *"Does g8s require an external database?"* (No; g8s uses an embedded, zero-CGO SQLite WAL database stored locally in `~/.local/state/g8s/`).
  - **Documentation Hub Index**: Quickstart Guide, CLI Reference (50+ subcommands), MCP Schema Guide, ADR Index (35+ decisions), PRD/SRS.
  - **Colophon**: MIT License, GitHub Repository, Discord Community, Copyright.

---

## 4. Documentation Information Architecture (Docs IA) Proposal

Synthesizing documentation patterns from Temporal, Dagger, and Tailscale, the `g8s` documentation site should adopt the **Diátaxis framework**, dividing knowledge into 4 distinct quadrants:

```text
┌──────────────────────────────────────┬──────────────────────────────────────┐
│  LEARNING-ORIENTED                   │  PROBLEM-ORIENTED                    │
│  1. Tutorials (<10 min)              │  2. How-To Guides (Recipes)          │
│  • 60-Second Quickstart              │  • Setting up Claude Desktop MCP     │
│  • First Delegated Mutation Loop     │  • Running Offline with Ollama       │
│  • Triaging PRs with Holdout Suites  │  • Multi-Coordinator Mesh Setup      │
├──────────────────────────────────────┼──────────────────────────────────────┤
│  UNDERSTANDING-ORIENTED              │  INFORMATION-ORIENTED                │
│  3. Architecture & Concepts          │  4. Technical Reference              │
│  • The 2-Axis Architecture (F0/F1/F2)│  • Complete CLI Reference (50+ cmds) │
│  • Single-Use Write Receipts         │  • 22-Tool MCP Schema Specification  │
│  • 6-Stage AI QA & Verification      │  • JSON Envelopes & Exit Codes       │
│  • SQLite WAL & CAS Leases           │  • Architecture Decision Records     │
└──────────────────────────────────────┴──────────────────────────────────────┘
```

---

## 5. Tone Guide: How g8s Must Sound

To communicate technical authority and establish developer trust, `g8s` must speak with calibrated, physical precision.

### 1. The Core Voice Principles
- **Physical & Mechanical, Not Mystical**: Describe software as physical mechanisms. Use terms like *harness, containment ring, lease, write receipt, process group, fence, firewall, and CAS delivery*. Avoid magical abstractions like *agent intelligence, AI magic, seamless orchestration, supercharged workflows*.
- **The Feynman Clarity Principle**: Explain complex safety concepts in plain, undeniable terms before introducing formal terminology.
  - *Example*: *"Before an agent edits a file, g8s issues a single-use Write Receipt listing only the allowed file paths. If the worker attempts to touch anything else, the write is aborted in-process."*
- **Calibrated & Empirical, Zero Hype**: Never use hyperbolic adjectives (*revolutionary, lightning-fast, ultra-secure*). State the exact metric: *"<25MB static pure-Go binary, zero CGO dependencies, sub-millisecond SQLite WAL lease acquisition, zero telemetry phone-home"*.
- **Safety as an Accelerator (The Brake Metaphor)**: Frame containment not as a restrictive leash that limits AI, but as the high-performance disc brakes that allow developers to run agents at maximum speed without fear.

### 2. Copy Transformation Table (Before vs. After)

| Marketing Hype (Avoid) | Jargon-Wall Bloat (Avoid) | The g8s Product Voice (Adopt) |
|---|---|---|
| "Supercharge your agent workflow with our game-changing AI manager!" | "Implements non-autoregressive S0..S3 stratum CAS FTS5 BM25 tri-anchor reflex." | "A lightweight zero-trust process harness that isolates AI coding agents into private Git worktrees with path-scoped write receipts." |
| "Completely secure AI coding that guarantees zero bugs!" | "Enforces epistemic closure over mechanical state transitions via hash-chained WAL." | "Run autonomous coding loops overnight. Workers operate in isolated process groups and private worktrees, so broken code never touches your main branch." |
| "Seamlessly integrates with all your favorite LLM tools!" | "Heterogeneous multi-provider protocol abstraction with dynamic quorums." | "Use Claude, Codex, Gemini, or local Ollama models through a single CLI, stdio MCP server, or SQLite task queue." |

---

## 6. Anti-Patterns Observed in Dev-Tool OSS Sites

During research, 6 recurring anti-patterns were observed that consistently degrade developer trust and kill conversion on open-source product websites:

1. **The Mysterious Tagline (Zero Category Clarity)**:
   - *The Flaw*: Sites featuring abstract poetry like *"Unleash the collaborative cognitive graph"* without stating whether the product is a CLI, a Go library, a desktop app, or a hosted SaaS.
   - *g8s Fix*: The hero subhead immediately defines the form factor: *"Single-binary zero-trust process execution harness (<25MB static pure-Go binary) for AI agent CLI workers."*
2. **The Hidden Install Command**:
   - *The Flaw*: Forcing developers to navigate through "Get Started" buttons that redirect to multi-step registration forms, email gates, or buried documentation sub-pages just to find the download link.
   - *g8s Fix*: The `curl | bash` one-liner and OS switcher tabs appear above the fold on the homepage, accompanied by a 1-click copy button and a "View script source" security audit link.
3. **Marketing Screenshot & Mockup Theater**:
   - *The Flaw*: Displaying stylized 3D illustrations, floating emojis, or fictional dashboard mockups rather than actual command output.
   - *g8s Fix*: Embed actual Asciinema terminal replays and tabbed JSON outputs showing exact commands (`g8s submit`, `g8s worker`, `g8s verify`) and real exit codes.
4. **The Unanchored YAML/Config Avalanche**:
   - *The Flaw*: Overwhelming first-time visitors with a 150-line configuration file before explaining what the tool produces or why the configuration matters.
   - *g8s Fix*: Progressive disclosure. The homepage shows zero-config 1-line execution (`g8s worker --once`); detailed configuration files and OpenSpec deltas remain in the docs hub.
5. **The Stale Badge & Broken Promise Syndrome**:
   - *The Flaw*: Showing failing build badges, stale release tags (e.g., claiming v0.1 while Git is at v0.22), or dead Discord community links.
   - *g8s Fix*: The website build pipeline reads version, commit SHA, and release dates directly from the root `manifest.json` single source of truth.
6. **The Enterprise Bait-and-Switch**:
   - *The Flaw*: Masquerading as an open-source tool while all prominent CTAs lead to "Book an Enterprise Sales Demo" with self-hosting instructions hidden or deprecated.
   - *g8s Fix*: Maintain open-source primacy. The top actions are `Install g8s` and `View on GitHub`, with enterprise governance presented as an architectural capability of the MIT-licensed binary.
