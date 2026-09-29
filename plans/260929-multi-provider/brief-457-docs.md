# Task: #457 slice 2 — multi-provider docs (operator guide + CLI reference)

Repo: g8s @ main. Tracking: tamld/g8s#457 (docs must land before the issue closes).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- docs/user-guide/providers.md (new file — the operator guide)
- docs/user-guide/cli-reference.md (edit existing sections only)
- docs/user-guide/README.md (ONLY if it is an index listing guides — add
  one link line; if it is prose without a guide index, skip it and note
  that in your report)

Do NOT run git commit. Do NOT modify any other file, especially not code
(cmd/, internal/) — another worker is implementing #457 slice 1 there in
parallel; touching anything outside the list above corrupts their delivery.

## Pinned semantics (authoritative — document exactly this)

These semantics are being implemented by slice 1 in the same change window.
Document them as shipped behavior of the `--provider` feature:

1. `g8s submit --provider <name>` — default "" (auto/legacy). Non-empty
   value is stored durably in the task payload as the `provider` key,
   alongside `model`. When the flag is absent, the settings key
   `default_provider` is used if set. Precedence: CLI flag >
   settings default > absent (legacy).
2. `g8s worker --provider <name>` — strict claim affinity: the worker
   claims only tasks whose payload `provider` equals the flag value.
   Workers started WITHOUT `--provider` keep claiming every queued task
   (backward compatible). Provider-filtered workers never claim legacy
   (provider-less) tasks.
3. Argv resolution order per task, performed by the worker from
   providers.json (`~/.config/g8s/providers.json`, env override
   `G8S_PROVIDERS`):
   - Task without provider: platform_dispatch args template matched by
     model ID (existing behavior), else legacy default (agy binary,
     default model gemini-3.8-flash-high).
   - Task with provider P: platform_dispatch entry whose `name` == P wins
     first; if absent, the attempt fails with an error listing available
     platform_dispatch names. api_call entries are never executed on the
     queue path (orchestrate/HTTP only) and produce a distinct error.
     There is NEVER a silent agy fallback when a provider was explicitly
     requested.
4. providers.json remains the ONLY provider/worker manifest (DELTA-10):
   no second format is introduced. Schema recap for the guide:
   `{providers: [{class: "api_call"|"platform_dispatch", name, base_url,
   auth_env, models: [{id, context_window}], slots, args: []}]}`;
   api_call requires base_url + models + slots>=1; platform_dispatch
   requires name + models; args templates substitute {prompt}, {model},
   {timeout}.
5. Result envelopes gain a generic `provider` field (omitted when empty);
   the legacy `agy_bin` field is unchanged.
6. New settings key `default_provider` via `g8s config set default_provider
   <name>` / `g8s config get default_provider`.

## Required content

### docs/user-guide/providers.md (new)

Structure it as an operator guide, in the tone of the existing
docs/user-guide files:

- Why: run workers from different agent providers against one durable
  queue; providers.json is the single manifest (DELTA-10).
- File location and precedence: default ~/.config/g8s/providers.json,
  env override G8S_PROVIDERS; config is read by `g8s worker` at startup
  and by `g8s providers`.
- Schema reference table for ProviderEntry fields (class, name, base_url,
  auth_env, models, slots, args) with the validation rules from
  internal/config/config.go.
- Worked example: a providers.json declaring agy (platform_dispatch,
  args template with {prompt}/{model}/{timeout}) and codex
  (platform_dispatch, its own args template) — one JSON block.
- Running mixed fleets: submit two tasks with different --provider
  values, then start two workers side by side
  (`g8s worker --provider agy` and `g8s worker --provider codex`),
  including the claim-affinity rule (strict equality; unfiltered workers
  claim everything).
- Resolution order and failure modes (explicit provider not found →
  attempt error listing names; api_call on queue path → distinct error;
  no silent fallback).
- Settings: default_provider example with `g8s config set`.
- Explicitly state what is out of scope: api_call providers on the queue
  path; repo-level config layering.

### docs/user-guide/cli-reference.md (edit)

- `g8s submit` flag table: add `--provider` row (type string, default "",
  description per pinned semantics).
- `g8s worker` section: add `--provider` row and one sentence on claim
  affinity.
- Env-var table: the G8S_PROVIDERS row already exists — keep it accurate.
- `g8s config` section: add default_provider to the documented keys if
  that section enumerates keys; if keys are documented elsewhere in the
  file, update there instead.

Cross-link: providers.md from the submit/worker sections ("See
providers.md for multi-provider setup").

## Constraints

- English, match the tone/format of existing docs/user-guide files
  (headings, sh code fences, flag tables).
- Do not invent flags or behavior beyond the pinned semantics above; do
  not touch code or CHANGELOG/version files.
- No local filesystem absolute paths in committed content (use
  ~/.config/... style paths).
