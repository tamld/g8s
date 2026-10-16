# Phase 1 (WF1): Spatial JIT Whisper & ContextBroker Wiring (`#594` — Hypothesis H1)

## 1. Objective
Connect `internal/vault` (`SpatialAnchor`) and `internal/telemetry` (`GetRelevantPatterns`) to live agent execution without in-flight mechanical blocking, bounded by a configurable carrying capacity $K_{\max}$ (`DefaultMaxActiveRulesPerPkg = 3`, overridable via `G8S_JIT_MAX_RULES` for empirical `H1` testing).

## 2. Target Files
- `internal/vault/vault.go` & `internal/vault/vault_test.go`
- `internal/fsops/fsops.go` & `internal/fsops/fsops_test.go`
- `internal/worker/worker.go`
- `cmd/g8s/brief_issue.go`

## 3. Implementation Details
1. **`Vault.MatchSpatial(ctx context.Context, filePath string, maxRules int) ([]DistillationRecord, error)`**:
   - Normalizes `filePath` (`filepath.ToSlash(filepath.Clean(filePath))`) and derives its package directory (`filepath.ToSlash(filepath.Dir(cleanPath))`).
   - Queries `vault_records` where `status IN ('APPLIED', 'ACCEPTED')` and `(file_path = ? OR package_name = ? OR ? LIKE '%' || file_path)` ordered by `updated_at DESC`.
   - Caps results at `maxRules` (defaulting to `ResolveMaxActiveRules()`, which reads `G8S_JIT_MAX_RULES` or defaults to `3`).
   - Helper `FormatJITWhisper(records []DistillationRecord) []string`: formats each matched record into a single concise advisory line (`[jit:<id>] <title> — avoid: <trade_off/denied_fragments>`).
2. **Non-Blocking `JITWhisper` in `internal/fsops`**:
   - Add `JITWhisper []string `json:"jit_whisper,omitempty"`` to `ReadResult` and `EditResult`.
   - When `G8S_VAULT_DB` is set (or default state-dir `vault.db` exists and `G8S_JIT_WHISPER != "0"`), best-effort query `Vault.MatchSpatial` on the target path and attach up to $K_{\max}$ 1-line whispers. Never fail or block `Read` or `Edit` if the vault is absent or locked.
3. **Scoped Pre-Flight & ContextBroker Wiring**:
   - In `cmd/g8s/brief_issue.go`: update `preflightSection(title, payload)` to try `eng.GetRelevantPatterns(ctx, "worker", title, payload)` first and fall back to `eng.QueryTopPatterns(ctx, maxK)`, capped at `ResolveMaxActiveRules()`.
   - In `internal/worker/worker.go`: wire `VaultNotes` in `g8scontext.Sources` so `ContextBroker.Assemble(ctx)` fetches up to $K_{\max}$ spatial notes from the vault when available.
