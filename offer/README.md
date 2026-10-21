# g8s Offer Bundle — for sibling projects

Pull-based distribution (ADR-0024 distribution model): each project's main
agent decides when to adopt. Nothing is pushed. Findings flow back as
sanitized contribution packets (g8s-supervisor Mode 3).

## What a project gets

1. **A profile** (`profiles/<type>.md`) — trust-boundary seed, lane-bundle
   recommendation, audit dimensions, and cadence tuned to the project's
   nature.
2. **Three generic gate scripts** — copy from `g8s/tools/`:
   `ci_spec_code_sync.sh` (+test), `ci_structure_sync.sh` (+test),
   `ci_link_integrity.sh` (+test). They are project-agnostic: point them at
   a repo root and they enforce spec↔code sync, README structure truth, and
   link integrity.
3. **The supervisor charter** — [`skills/g8s-supervisor`](../skills/g8s-supervisor/SKILL.md)
   v4.3.0: the operating practice for dispatching work through g8s
   (admission-gated, fan-out, evidence-verified, leak-free reporting).
4. **Cross-platform search standard** — [`SEARCH_GUIDE.md`](SEARCH_GUIDE.md):
   why `ripgrep` (`rg`) replaces raw `grep -rn` / `find + xargs` across
   macOS (BSD), Linux (GNU), and Windows, plus the dual-engine (`rg`-first
   with POSIX `grep` fallback) pattern used in portable CI gates.
5. **Native Cross-Platform Agent I/O & MCP Suite (`DELTA-23`)**: `g8s search`, `g8s read`, `g8s edit`, `g8s select` (CLI & MCP `g8s_search`, `g8s_read`, `g8s_edit`, `g8s_select`) with in-process `WriteReceipt` enforcement.
6. **Config-Driven Multi-Protocol LLM Consumer (`DELTA-25`)**: `g8s llm-call` and `g8s_llm_call` MCP tool allowing sibling repos to consume any `providers.json` HTTP gateway (`openai`, `anthropic`, `9router`).

## Onboard in four steps

See [`ONBOARDING.md`](ONBOARDING.md). Short version: pull → scaffold `.g8s/`
from your profile → wire the three gates into CI → run your first brief.

## Contribute back

Running the bundle produces operational evidence: gate catches, PRI scores,
audit findings, escalation patterns. When a finding is a **g8s defect or a
reusable improvement**, ship it back per the charter's Mode 3 (sanitized
contribution packet → PR). That is the learning loop: projects pull tactics,
g8s crystallizes their patterns. See
[`../docs/designs/gate-lane-routing.md`](../docs/designs/gate-lane-routing.md)
for the architecture and its falsification clauses.
