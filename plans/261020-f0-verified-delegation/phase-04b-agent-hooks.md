# Phase 04b: Agent-level intercept — PreToolUse + Stop hooks (#624, red-team C1)

## Why
F0 merged sibling-repo PRs with a direct `gh pr merge`; `tools/merger.sh` is g8s-only and docs-lane-only (`merger.sh:16-19,175-184`). The real commitment points are F0's own tool calls and F0's final message to PiC. Antigravity `hooks.json` can intercept both:
- `PreToolUse` with `decision: deny`
- `Stop` with `decision: continue` and a reason

This works without any forge feature and also covers aegis (private, no branch protection).

## Files
`tools/f0/hook*.go` and tests; `tools/f0/hooks/agy-hooks.json` (template); docs in the Phase 05 skill update. Installing into the operator's customization root (`~/.gemini/…/hooks.json` or the workspace `.agents/hooks.json`) is done by F0 **only with PiC approval**, because it is a T4 config change.

## `f0 hook pre-tool` (stdin: hook JSON; stdout: decision JSON)
- Acts only on `toolCall.name == "run_command"`; everything else ⇒ `allow`. Exploration stays free (axiom 1).
- Classifies `CommandLine` (shell-split; also inspects `bash -c`/`sh -c` payloads and `&&` / `;` chains):

| pattern | action | handling |
|---|---|---|
| `gh pr merge`, `gh api …/merge`, `git push … <default-branch>` | merge | `f0 gate check --action merge`, PASS ⇒ allow, else deny + reason |
| `gh issue close`, PR body / commit with `Closes #N` | close | G7 in Wave B; Wave A: allow + whisper |
| `gh release create`, `git tag … && git push --tags`, `git push --force/-f`, `git push --delete`, `gh repo edit` | T4 | `force_ask` (PiC) |
| `--no-verify`, `git commit -n`, `HUSKY=0`, `SKIP=` hook-skips | bypass | deny |
| edits to `hooks.json` or `~/.local/state/g8s/f0/*` via shell | T4 tamper | deny |

- On deny, the reason includes the failing checks and the exact `f0 status` command to inspect ground truth. These are eyes, not handcuffs.
- Fail-safe: if `f0` crashes or times out (default 25s, below the 30s hook timeout) on a merge/T4 command ⇒ deny. On an unclassified command ⇒ allow.

## `f0 hook stop` (Stop event)
- Reads `transcriptPath` and extracts F0's final assistant message of this turn.
- Runs the G6 claim linter in *report mode*: the repo is inferred from `workspacePaths`; an `ev-` must exist in the repo ledger with status PASS (any SHA, since reports span repos and merges); `actions/runs/<id>` must resolve via the forge with conclusion success when the forge is available.
- If uncited or failed claims are found and the forced continuation count for this turn is < 2 ⇒ `{"decision":"continue","reason":"Uncited status claims: … Cite ev-/run IDs or remove the claim."}`. Otherwise allow the stop (no infinite loops; the 2-continue cap is tracked in the global ledger, kind `stop-hook`).

## Tests
- Classifier table (including `bash -c` wrapping and chained commands).
- Merge allowed/denied with a fake gate.
- Bypass flags denied.
- Timeout ⇒ deny for merge.
- Stop hook with transcript fixtures: uncited "CI GREEN" ⇒ continue; cited and valid ⇒ allow; cap of 2.
