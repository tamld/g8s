# System Architecture

## 1. Overview
`subdispatch` is architected as a modular, pure-Go system designed around bounded multi-agent capability delegation.

```
┌─────────────────────────────────────────────────────────────┐
│                      Client Layer                           │
│  ┌───────────────────────┐       ┌───────────────────────┐  │
│  │   CLI (Cobra/Viper)   │       │   Stdio MCP Server    │  │
│  └───────────┬───────────┘       └───────────┬───────────┘  │
└──────────────┼───────────────────────────────┼──────────────┘
               ▼                               ▼
┌─────────────────────────────────────────────────────────────┐
│                     Harness Engine                          │
│  • RoleProfile Validator (6 Roles)                          │
│  • PermissionProfile Gate (read_only, write, auto_read)     │
│  • Pattern Matcher (Blocked Commands & Denied Paths)        │
│  • Contract Prompt Injector                                 │
└──────────────────────────────┬──────────────────────────────┘
                               │
               ┌───────────────┴───────────────┐
               ▼                               ▼
┌──────────────────────────────┐ ┌────────────────────────────┐
│      Write Receipt Gate      │ │   Durable Control Plane    │
│  • Time-to-Live (TTL <=3600s)│ │  • SQLite WAL Database     │
│  • Glob Allowed Paths        │ │  • Atomic CAS Leases       │
│  • Atomic Consumption (1-use)│ │  • Dedupe & State Machine  │
│  • Brain Revocation & Audit  │ │  • Redacted Prompt Hashes  │
└──────────────────────────────┘ └─────────────┬──────────────┘
                                               │
                                               ▼
┌─────────────────────────────────────────────────────────────┐
│                    Execution Layer                          │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ Pluggable Provider Interface (agy, claude, gemini...) │  │
│  └───────────────────────────┬───────────────────────────┘  │
│                              ▼                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ Process Group Supervisor (Setpgid, Timeout, SIGTERM)  │  │
│  └───────────────────────────┬───────────────────────────┘  │
│                              ▼                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ Post-Run Violation Detector (Mutation Scan, Code 3)   │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## 2. Pluggable Providers
The `WorkerProvider` interface abstracts worker CLI invocations:

```go
type WorkerProvider interface {
    Name() string
    ResolveBinary(customPath string) (string, error)
    BuildCommand(req TaskRequest, effectivePrompt string) (*exec.Cmd, error)
    DetectViolations(stdout, stderr string) []ContractViolation
}
```

## 3. Directory Layout
```text
subdispatch/
├── cmd/
│   └── subdispatch/          # CLI Entrypoint
├── internal/
│   ├── harness/              # Roles, Permissions, Safety filters
│   ├── controlplane/         # SQLite WAL task queue
│   ├── receipt/              # Receipt delegation engine
│   ├── worker/               # Process supervisor & runner
│   ├── provider/             # Pluggable CLI adapters (agy, claude, gemini)
│   ├── mcp/                  # Stdio JSON-RPC MCP server
│   └── service/              # OS Daemon manager (launchd/systemd/windows)
├── pkg/
│   └── client/               # Go SDK for programmatic dispatch
└── docs/                     # PRD, SRS, DoD/DoR, Architecture
```
