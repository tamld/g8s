# Orca Architecture & License Audit for g8s Provider Registry (DEBT-52)

> **Document SSoT**: Architecture & Licensing Reference for Pluggable Agent Providers  
> **Source Repository Studied**: [`stablyai/orca`](https://github.com/stablyai/orca)  
> **Target System**: `g8s` (Zero-CGO, Pure-Go Orchestrator)  
> **Issue**: [#176 (DEBT-52)](https://github.com/tamld/g8s/issues/176)  
> **Date**: 2026-08-30  

> ## Legal Disclaimer
> 
> This document is an **independent architectural study** of
> `stablyai/orca` (https://github.com/stablyai/orca) for the
> purpose of designing a clean-room provider registry in g8s.
> 
> **NO source code from orca is reproduced, copied, translated,
> or otherwise derived from in this document or in any g8s source
> file.** Only architectural concepts (provider registry,
> detection algorithm, adapter dispatching, handle lifecycle)
> are referenced and re-implemented from scratch in Go.
> 
> orca is the property of Lovecast Inc. Despite its MIT license,
> we have chosen clean-room re-implementation to:
> 1. Mitigate any future licensing dispute risk
> 2. Avoid any inadvertent patent / trade-secret claim
> 3. Maintain full independence of g8s's codebase
> 
> If you are reviewing this audit and want to verify the claim,
> diff every g8s source file under `internal/provider/`,
> `cmd/g8s/providers.go`, and `cmd/g8s/orchestrate.go` against
> the corresponding orca files. The diffs will be empty (modulo
> standard Go package boilerplate).

---

## 1. Executive Summary & Legal Audit

| Dimension | Details |
| :--- | :--- |
| **Upstream Repository** | `stablyai/orca` (`github.com/stablyai/orca`) |
| **Upstream License** | **MIT License** (Copyright &copy; 2026 Lovecast Inc.) |
| **Upstream Technology** | TypeScript, Electron, Vite, node-pty, JSON-RPC, SQLite |
| **Legal Permissibility** | The MIT license allows commercial use, modification, distribution, and private use, provided the copyright notice and permission notice are preserved in any verbatim software redistributions. |
| **g8s Implementation Strategy** | **Clean-room Pure-Go re-implementation**. Zero upstream code is copied verbatim. Only architectural patterns (provider registry, detection algorithms, adapter dispatching, and handle lifecycle) are adapted to g8s's Zero-CGO, statically compiled Go runtime. |

---

## 2. Upstream License Text

```text
MIT License

Copyright (c) 2026 Lovecast Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## 3. Upstream System Architecture

Orca is structured as an Electron/Node-based multi-agent workbench with a centralized coordinator and a distributed worker runtime:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                            ORCA COORDINATOR                                 │
│  (Desktop UI / Headless Daemon / JSON-RPC Orchestrator Service)             │
│                                                                             │
│  ┌─────────────────────────┐   ┌───────────────────────┐   ┌──────────────┐ │
│  │ Task & Run DB (SQLite)  │   │  JSON-RPC API Surface │   │ Mail / Fence │ │
│  │  - Runs, Tasks, Status  │   │  - workerStart/Stop   │   │ Coordination │ │
│  │  - Mutation Ledger      │   │  - ask / gateResolve  │   │ Authority    │ │
│  └─────────────────────────┘   └───────────────────────┘   └──────────────┘ │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       │ RPC Dispatch & Topology Management
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     AGENT PROVIDER & DETECTION REGISTRY                     │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │ TUI_AGENT_CONFIG Dictionary & Detection Engine                        │  │
│  │   • Probes: PATH check (detectCmd, detectCmdAliases, requiredCmds)    │  │
│  │   • Launchers: launchCmd, launchCmdByPlatform, promptInjectionMode     │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│        │                      │                       │                     │
│        ▼                      ▼                       ▼                     ▼
│  ┌───────────┐         ┌─────────────┐         ┌─────────────┐       ┌────────────┐
│  │    agy    │         │ cursor-agent│         │   claude    │       │   gemini   │
│  │flag-prompt│         │    argv     │         │ stdin-after │       │flag-prompt │
│  └─────┬─────┘         └──────┬──────┘         └──────┬──────┘       └─────┬──────┘
└────────┼──────────────────────┼───────────────────────┼────────────────────┼────────┘
         │                      │                       │                    │
         ▼                      ▼                       ▼                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ISOLATED WORKER INSTANCES                            │
│  • PTY Process Group & Session Multiplexing (node-pty)                      │
│  • Worktree Sandboxing (new-child, new-top-level, current)                  │
│  • Stdout/Stderr Streaming & Receipt Harvest                                │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Provider Detection & Execution Patterns in Orca

### 4.1. Provider Detection Algorithm
In Orca (`src/shared/tui-agent-detection-commands.ts` and `src/shared/tui-agent-config.ts`):
1. **Config Map (`TUI_AGENT_CONFIG`)**: Every supported agent declares:
   - `detectCmd`: Primary executable name (e.g. `agy`, `cursor-agent`, `vibe`).
   - `detectCmdAliases`: Alternate binary names for wrapped installations.
   - `detectRequiredCommands`: Auxiliary tools that must also be in PATH.
   - `detectUnsupportedRuntimes`: Platform exclusion filters (e.g. unsupported OSes).
2. **Probing**: The runtime aggregates all candidate commands across configured providers and runs a batch discovery against the system `PATH`.
3. **Filtering**: `resolveDetectedTuiAgentIds` marks an agent as available only if both its primary command and all required dependencies exist in the discovered set.

### 4.2. Heterogeneous Prompt & Input Injection Modes
Different AI CLI tools parse instructions and user prompts differently. Orca categorizes them into explicit `promptInjectionMode` strategies:
* `flag-prompt` / `flag-prompt-interactive`: Invokes `--prompt "<text>"` or `-p "<text>"` directly (e.g. `agy`, `gemini`).
* `argv`: Passes prompt as a positional argument, often preceded by `--` to prevent flags inside prompt text from being parsed as CLI options (e.g. `grok`, `cursor-agent`, `droid`, `prime-agent`).
* `stdin-after-start`: Spawns the CLI in interactive REPL mode and writes the prompt to standard input after the process emits a readiness signal (e.g. `aider`, `goose`, `cline`).
* `hermes-query`: Communicates over dedicated query protocol pipes.

### 4.3. Worktree & Terminal Isolation
Orca coordinates multi-agent parallelism by spawning workers into dedicated Git worktrees (`new-child` or `new-top-level`), preventing concurrent uncommitted edits from colliding in the working directory.

---

## 5. Reusable Design Patterns for g8s

We extract five strategic architectural patterns from Orca for implementation in `g8s`:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                  5 CORE DESIGN PATTERNS FOR g8s ADOPTION                    │
├───────────────────────────────────┬─────────────────────────────────────────┤
│ 1. Uniform Provider Interface     │ Provider with Name, Binary, Version,    │
│    & Registry Pattern             │ Available, and Spawn methods.           │
├───────────────────────────────────┼─────────────────────────────────────────┤
│ 2. Fail-Closed Health Probing     │ Fast exec.LookPath + Version smoke probe│
│    & Auto-Detection               │ returning OK / NO status + reason.      │
├───────────────────────────────────┼─────────────────────────────────────────┤
│ 3. Heterogeneous CLI Adapters     │ Dedicated adapters per CLI (agy, claude,│
│    (Prompt & Flag Synthesis)      │ codex, ollama) hiding CLI quirks.       │
├───────────────────────────────────┼─────────────────────────────────────────┤
│ 4. Asynchronous Subprocess Handle │ Non-blocking Spawn returning Handle with│
│    & Receipt Harvesting           │ PID(), Wait(), Cancel(), StdoutStream() │
├───────────────────────────────────┼─────────────────────────────────────────┤
│ 5. Backward Compatibility &       │ Defaulting to 'agy' when unspecified to │
│    Explicit Flag Override         │ preserve existing v0.5.0 behavior.      │
└───────────────────────────────────┴─────────────────────────────────────────┘
```

### Pattern 1: Pluggable Provider Interface & Registry
A decoupled `Provider` interface isolating provider implementations (`AgyProvider`, `CodexProvider`, `ClaudeProvider`, `OllamaProvider`) from the orchestrator and CLI entry points.

```go
type Provider interface {
    Name() string
    Binary() string
    Version(ctx context.Context) (string, error)
    Available(ctx context.Context) error
    Spawn(ctx context.Context, spec Spec) (Handle, error)
}
```

### Pattern 2: Auto-Detection with Health & Version Semantics
The registry discovers available CLIs via `AutoDetect(ctx)` and exposes formatted status listings via `List()`:
* Resolves binary location with `exec.LookPath`.
* Probes binary version with `--version` command context.
* Returns clear status (`OK` / `NO`) and diagnostic hints for missing dependencies.

### Pattern 3: CLI Subprocess Adapters
Each provider manages its own invocation arguments and environment variables:
* **agy**: `agy --prompt <brief> --model <model> ...`
* **claude**: `claude -p <prompt> ...`
* **codex**: Future stub (returns `Available() == ErrNotFound`).
* **ollama**: Probes HTTP endpoint `http://127.0.0.1:11434/api/tags` and manages local inference requests.

### Pattern 4: Handle Lifecycle with Process-Group Cancellation
Spawning returns a `Handle` interface:
* Non-blocking start.
* `Wait(ctx)` yields execution receipt and captures output/exit codes.
* `Cancel(ctx)` terminates the isolated OS process group cleanly (`Setpgid: true` / `CREATE_NEW_PROCESS_GROUP`).
* `StdoutStream()` provides optional live streaming for real-time observability.

### Pattern 5: Seamless CLI Integration
* New subcommand: `g8s providers` to display detected providers and status.
* Orchestrator flag: `g8s orchestrate --provider <name>` for explicit backend pinning, defaulting to `agy` to preserve backward compatibility.

---

## 6. Implementation Plan (3-PR Roadmap)

1. **PR 1 (Doc-only)**: `docs/ORCA_AUDIT.md` (Orca MIT license analysis and architecture mapping).
2. **PR 2 (Core Engine)**: `internal/provider` package:
   - `internal/provider/registry.go` (Provider interface, Spec, Handle, Registry).
   - `internal/provider/agy.go` (AgyProvider).
   - `internal/provider/codex.go` (CodexProvider stub).
   - `internal/provider/claude.go` (ClaudeProvider).
   - `internal/provider/ollama.go` (OllamaProvider).
   - `internal/provider/registry_test.go` (Table-driven detection & spawn tests).
3. **PR 3 (CLI Integration & Wiring)**:
   - `cmd/g8s/main.go`: `g8s providers` subcommand + `g8s orchestrate --provider` flag.
   - Backward compatibility: default provider remains `agy`.
   - Comprehensive end-to-end and regression tests.
   - Closes #176 (DEBT-52).

---

## Anti-Patterns (Things We Do NOT Copy from orca)

Even though the MIT license technically permits verbatim copying,
we deliberately avoid these patterns because they would create
future legal risk:

1. **No verbatim source code translation** (TS to Go line-by-line)
2. **No borrowed code comments** (even with attribution)
3. **No copied JSON schemas** (we design our own for g8s)
4. **No parallel function/variable names** (we use g8s's idioms)
5. **No replicated test cases** (we test what g8s needs)
6. **No documentation prose** (we write in g8s's voice)
7. **No README structure** (we have our own docs/ structure)

If a future contributor notices ANY orca code in g8s, please
open an issue immediately. We have a zero-tolerance policy on
this.
