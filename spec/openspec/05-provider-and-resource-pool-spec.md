# OpenSpec DELTA-05: Provider Registry, Model Governance & Resource Pool Discovery

**Status**: `PROPOSED`  
**Milestone**: M2 (Advanced Capabilities)  
**Package**: `internal/provider`, `internal/pool`  

---

## 1. Goal & Context
Define the unified **Provider Registry**, **Model Governance Architecture**, and **Resource Pool Discovery Engine** for `g8s`. This ensures automatic discovery of available worker backends, deterministic model capability matching, capacity/rate-limit throttling, and zero external plugin dependencies.

---

## 2. Model Governance & Ownership Matrix

| Layer | Responsible Entity | Authority & Scope |
| :--- | :--- | :--- |
| **Layer 1: Host Declaration** | **Developer / Operator** | Configures `~/.config/g8s/providers.yaml`, environment variables (`AGY_BIN`, `CLAUDE_BIN`, `GEMINI_BIN`, `OLLAMA_HOST`), and API quota ceilings. |
| **Layer 2: Discovery & Health** | **`g8s` Discovery Engine** | Automatically probes local binaries (`exec.LookPath`), checks health via synthetic smoke probes, tracks in-flight capacity slots. |
| **Layer 3: Dynamic Selection** | **Supervisor (Brain)** | Matches incoming tasks to the optimal worker model based on task complexity, role constraints, and available resource capacity. |

---

## 3. Resource Pool Discovery Architecture

```
                                  ┌───────────────────────────────┐
                                  │      Supervisor (Brain)       │
                                  └───────────────┬───────────────┘
                                                  │ Queries `g8s_self_awareness`
                                                  ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                           g8s RESOURCE POOL GOVERNOR (Native Go)                        │
│                                                                                         │
│  ┌──────────────────────────┐  ┌──────────────────────────┐  ┌───────────────────────┐  │
│  │     Binary Resolver      │  │   Capacity / Rate Limiter│  │ Model Capability Gate │  │
│  │  (Auto-probe CLI paths)  │  │   (In-flight slot token) │  │ (Match role to model) │  │
│  └─────────────┬────────────┘  └─────────────┬────────────┘  └───────────┬───────────┘  │
└────────────────┼─────────────────────────────┼───────────────────────────┼──────────────┘
                 ▼                             ▼                           ▼
      ┌────────────────────┐        ┌────────────────────┐      ┌────────────────────┐
      │ Provider: AGY      │        │ Provider: Claude   │      │ Provider: Ollama   │
      │ (Gemini 3.7 Flash) │        │ (Claude 3.5 Haiku) │      │ (Local GPU Models) │
      │ Slots: 10 in-flight│        │ Slots: 5 in-flight │      │ Slots: 2 in-flight │
      └────────────────────┘        └────────────────────┘      └────────────────────┘
```

---

## 4. Go Interface & Data Model

```go
package provider

import "context"

type ProviderStatus string

const (
    StatusReady       ProviderStatus = "READY"
    StatusDegraded    ProviderStatus = "DEGRADED"
    StatusUnavailable ProviderStatus = "UNAVAILABLE"
)

type ModelDescriptor struct {
    ID                string   `json:"id"`
    Name              string   `json:"name"`
    SupportedRoles    []string `json:"supported_roles"`
    ContextWindow     int      `json:"context_window"`
    MaxOutputTokens   int      `json:"max_output_tokens"`
    IsLocal           bool     `json:"is_local"`
}

type ProviderInfo struct {
    Name              string            `json:"name"`
    BinaryPath        string            `json:"binary_path,omitempty"`
    Status            ProviderStatus    `json:"status"`
    AvailableModels   []ModelDescriptor `json:"available_models"`
    MaxConcurrency    int               `json:"max_concurrency"`
    CurrentInFlight   int               `json:"current_in_flight"`
    LastHealthCheckAt int64             `json:"last_health_check_at"`
}

type ProviderRegistry interface {
    DiscoverAll(ctx context.Context) ([]ProviderInfo, error)
    GetProvider(name string) (ProviderInfo, error)
    AcquireSlot(ctx context.Context, providerName string) (func(), error)
}
```

---

## 5. Build vs. Plugin Decision

* **Verdict**: **100% NATIVE BUILD IN GO (No external plugins)**.
* **Rationale**:
  1. External plugins introduce version drift, complex installation steps, and break single-binary portability.
  2. The Discovery Engine is lightweight (Pure Go `exec.LookPath`, HTTP `/api/tags` for Ollama, and in-memory Token Bucket rate limiters).
  3. Total code overhead is $< 500$ lines of Go, preserving the 15MB binary footprint.
