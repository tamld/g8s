# Task: #457 slice 1 — provider-aware CLI for the durable queue path

Repo: g8s @ main. Tracking: tamld/g8s#457 (P1 — operator directive: CLI args first).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- cmd/g8s/submit.go
- cmd/g8s/main.go (ONLY the runWorker function and the templates/provider
  template maps inside it — do not touch other commands in this file)
- internal/worker/worker.go
- internal/worker/worker_provider_test.go (new file)
- internal/controlplane/store.go
- internal/controlplane/claim_filter_test.go (new file)
- internal/settings/settings.go
- internal/settings/settings_provider_test.go (new file)
- internal/dispatch/dispatch.go
- internal/dispatch/result_provider_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker is
operating on docs/ in parallel — touching anything outside the list above
corrupts their delivery.

## Context (verified at main HEAD)

The queue path is single-provider by construction today:

- cmd/g8s/submit.go builds `payloadMap` (prompt, model, role, permission,
  timeout, add_dirs, actor, …) and no provider key exists. Flag `--model`
  defaults to "gemini-3.8-flash-high".
- cmd/g8s/main.go runWorker (~line 1484) loads providers.json
  (`G8S_PROVIDERS` env override, default ~/.config/g8s/providers.json) and
  builds `templates[modelID] = entry.Args` for platform_dispatch entries
  only; the resolver closure is registered via `worker.WithCommandResolver`
  and keyed by model ID only.
- internal/worker/worker.go: taskRequest (~line 116) parses the payload and
  has no Provider field; when req.Model is empty it is defaulted to
  "gemini-3.8-flash-high" (~line 516); buildArgv falls back to
  dispatch.BuildWorkerArgv (default binary "agy", internal/dispatch/argv.go).
- internal/controlplane/store.go ClaimTask (~line 1310) selects
  `WHERE state = 'QUEUED' AND cancel_requested = 0 AND attempts < max_attempts
  ORDER BY priority DESC, created_at ASC` — no provider filter.
- internal/dispatch/dispatch.go Result (~line 471) records `model` and
  `agy_bin` (agy-specific JSON tag) per dispatch run.
- internal/settings/settings.go allows keys: data_dir, scope, evidence_dir,
  default_timeout, default_model, default_role, log_level — no
  default_provider. Managed by `g8s config get|set|list|unset`.

providers.json schema (internal/config/config.go): root `{providers: []}`,
entry `{class: api_call|platform_dispatch, name, base_url, auth_env,
models[{id, context_window}], slots, args[]}`. Args templates support
placeholders {prompt}, {model}, {timeout} substituted verbatim.

## Required implementation

1. **`g8s submit --provider <name>`** (cmd/g8s/submit.go): string flag,
   default "". When non-empty, add `"provider": <name>` to payloadMap
   (alongside "model"). Empty flag → key absent (byte-identical payload
   for legacy submits). If the operator did not pass --provider, fall back
   to the settings value `default_provider` when it is set (see item 6).
   Validation: flag must be a non-empty trimmed string when present;
   reject whitespace-only values with a usage error.

2. **Envelope**: internal/worker/worker.go taskRequest gains
   `Provider string \`json:"provider,omitempty"\``.

3. **Resolution order** in runWorker's command resolver (signature becomes
   resolver(prompt, provider, model, timeout) — update the
   WithCommandResolver option and its call site in worker.go accordingly):
   - provider == "" (legacy task): provider-name map skipped; model-ID
     template match (existing behavior); miss → buildArgv legacy agy
     default. This path must remain byte-identical to today.
   - provider != "": platform_dispatch template keyed by provider name
     (providers.json entry `name`) wins first; if the name is not found
     among platform_dispatch entries, FAIL the attempt with a clear error
     naming the requested provider and listing the available
     platform_dispatch names from providers.json. NEVER silently fall back
     to the agy default when a provider was explicitly requested (silent
     fallback is the failure class documented in #448). An api_call entry
     with the same name must produce the distinct error that api_call
     providers are not executable on the queue path.
   - Keep the existing {prompt}/{model}/{timeout} placeholder substitution.

4. **Claim affinity** (internal/controlplane/store.go): add
   `ClaimTaskProvider(ctx, workerID string, leaseDurationSeconds int,
   provider string) (*Task, error)`. Empty provider = no filter (delegate
   to existing ClaimTask logic; keep ClaimTask's exported signature
   unchanged). Non-empty provider adds
   `AND json_extract(request_json, '$.payload.provider') = ?` to the claim
   SELECT. IMPORTANT: first verify with a test where `provider` lands in
   request_json (SubmitTask marshals SubmitTaskRequest whose Payload holds
   payloadMap — confirm the exact JSON path on your branch and pin it in
   the test; adjust the json_extract path if the nesting differs). Tasks
   without a provider key are NOT claimable by a provider-filtered worker.
   internal/worker/worker.go RunOptions gains `Provider string`; RunOnce
   uses ClaimTaskProvider when opts.Provider != "". cmd/g8s/main.go
   runWorker gains `--provider` flag (default "") passed into RunOptions.

5. **Result envelope** (internal/dispatch/dispatch.go): Result gains
   `Provider string \`json:"provider,omitempty"\`` stamped wherever
   `model`/`agy_bin` are stamped today, sourced from the task request's
   provider. `agy_bin` field and its JSON tag remain unchanged.

6. **Settings** (internal/settings/settings.go): add key
   `default_provider` to the allowed-keys list with the same validation
   style as existing string keys. submit.go consumes it as described in
   item 1 (flag > settings > absent). No other keys.

## Tests — red first, table-driven

- worker_provider_test.go: payload with provider="codex" +
  providerTemplates hit → resolver returns the codex args template with
  placeholders substituted; provider="ghost" → attempt error lists
  available names and does NOT contain the agy default argv; provider=""
  + model match → legacy path unchanged; provider="" + no match → agy
  default (regression guard).
- claim_filter_test.go: submit two tasks (one provider="codex", one
  without provider) → ClaimTaskProvider(…, "codex") returns only the
  codex task; ClaimTask (unfiltered) still returns both across two calls;
  a worker-filtered claim of an empty queue returns no task.
- settings_provider_test.go: set/get/unset default_provider round-trip;
  unknown key still rejected.
- result_provider_test.go: Result JSON carries provider when set, omits
  the key when empty; agy_bin still marshals.
- If submit.go has an existing test file pattern for flag validation,
  follow it only inside the allowed file list (submit.go itself);
  otherwise cover flag validation through the new worker/dispatch tests
  and note it in your report.

## Constraints

- stdlib only, no new dependencies, no new manifest format.
- Backward compatibility is a hard gate: with no new flags and no
  providers.json entry changes, every existing test must pass unchanged.
- Match surrounding code style; comments only for non-obvious constraints.
- Do not touch docs/ (parallel worker owns them) or docs/claims.yml.
