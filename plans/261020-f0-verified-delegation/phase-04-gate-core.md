# Phase 04: Gate core G0/G1/G2/G3/G6 + status/waive/manual/anchor/audit (#624)

> Revised after red-team review: C3 (G0), M2, M3, M4, M5, M6, M7, M8, m3, m4, m5.

## Files
`tools/f0/{gate,forge,status,waive,manual,anchor,audit,config,claims}*.go`, their tests, `tools/f0/main.go`, `tools/merger.sh`, `tools/merger_test.sh`.

## Repo policy file (optional): `.g8s/f0.yml`
```yaml
ci: github            # github | none  (none = repo declares no forge CI)
verify_commands:      # canonical commands accepted by G1 (argv prefix match)
  - ["go", "test", "./..."]
manual_free: ["plans/**"]   # F0-authored paths where manual tags do not debit
```
Defaults when absent:
- `ci: github` if `origin` is on github.com, otherwise `none`.
- `verify_commands` inferred: go.mod ⇒ `go test ./...`; package.json ⇒ `npm test` / `pnpm test`; Cargo.toml ⇒ `cargo test`.
- `manual_free: []`.

## Commands
- `f0 gate check --repo <path> --action merge|close|publish (--pr N | --head-sha S --base B) [--report <file>] [--json]`
  - With `--pr`, the head is resolved via the forge (`gh pr view N --json headRefOid,baseRefName`) and fetched (`git fetch origin pull/N/head`).
  - Offline mode uses `--head-sha` / `--base` (red-team m5).
  - Output: `{overall, head_sha, checks:[{id,status,detail}]}`; exit 0 on PASS, 1 on FAIL. Appends a `gate` entry to the repo ledger.
- `f0 status --repo <path> [--pr N] [--report-block]`: read-only ground truth. `--report-block` emits a markdown status block with evidence IDs and run IDs; F0 pastes it into PRs and reports.
- `f0 waive --repo <path> --pr N --check "<name>" --issue <url> --reason <text>`
- `f0 manual --repo <path> --file <rel> --reason <text>`
- `f0 anchor --repo <path>` prints the `F0-Ledger-Head: <seq>:<hash>` trailer line.
- `f0 audit --repo <path> [--since 30d]` lists default-branch commits (merge/squash) that have no `gate` PASS entry for their head SHA and no `F0-Ledger-Head` trailer. Detection-after-the-fact, Wave A (red-team C1).

## Checks
- **G0 mandate**: `mandate check --action <action> --paths <diff paths>` must allow it (red-team C3).
- **G1 evidence at head**:
  - There is a repo-ledger entry with `sha == head_sha`, `dirty == false`, status PASS, and `cmd` matching one of `verify_commands` by argv prefix (red-team M2).
  - VACUOUS, UNKNOWN or dirty entries do not count.
- **G2 forge CI** (`ci: github`):
  - Every check is completed with bucket `pass` or `skipping`.
  - **Zero checks ⇒ FAIL** "no checks reported yet" (red-team M8).
  - pending ⇒ FAIL.
  - A failing check passes only with a waiver bound to `(repo, pr, check name, head_sha)` whose issue is in the **same repo**, is OPEN, and mentions the check name, and only while the budget is not exhausted (red-team M7).
  - `ci: none` ⇒ SKIP.
- **G3 provenance** (red-team M5):
  - Task set T: task IDs from `F0-Tasks:` lines in the PR body, or, when absent, all non-dry-run `g8s-deliver` entries in the repo ledger with `ts` ≥ the time of the branch's merge-base commit.
  - Delivered = applied ∪ deleted from T, with recorded blob hashes.
  - D = `git diff --name-only base...head`.
  - FAIL "unattributed: f" if f ∈ D but f ∉ Delivered ∪ Manual.
  - FAIL "dropped: f" if f ∈ Delivered but f ∉ D, unless f was deleted and is also absent at base.
  - FAIL "altered-after-delivery: f" if the blob of f at head ≠ the recorded delivered blob and there is no later manual tag for f.
  - **Manual tags** (red-team M6): each costs 1 `debit` unless the path matches `manual_free`. Manual-tagged files are listed in the gate output.
- **G6 claim citations** (red-team M3, M4):
  - Scope: the PR body (or `--report`). Strip fenced blocks, inline code, quoted lines (`>`), and URLs.
  - A **status claim** = a subject token (`CI|checks?|tests?|build|lint|gate|verify|audit|trivy|clippy|cargo|pipeline|main`) within 4 tokens of a verdict token (`pass(ed|es)?|green|xanh|ok|succeed(ed|s)?|success(ful)?|fixed|resolved|đạt|thành công|✅`). It is skipped when a negation (`not|no|chưa|không|fail|đỏ|pending`) sits within 3 tokens before the verdict.
  - Each status claim line must contain a reference:
    - `ev-xxxxxxxx`: must exist in the repo ledger, be PASS, and have `sha == head_sha`.
    - `actions/runs/<id>`: must resolve via the forge with `head_sha == head` and conclusion success. Under `ci: none`, run refs are rejected.
  - A block emitted by `f0 status --report-block` (marked `<!-- f0:status -->`) is trusted and verified as a whole.
  - Plain prose like "fixed-width", "closed-loop", "merged into one list" has no subject+verdict pair, so it does not trigger (prevents strangling, axiom 1).

## merger.sh
Add **Gate e (f0)** (the name `Gate d` is already taken by CI, `merger.sh:254`):
`"${F0_CMD:-bin/f0}" gate check --repo "$REPO_ROOT" --action merge --pr "$PR_NUM"` must pass. `merger_test.sh` stubs `F0_CMD` for pass and fail.

## Tests
Table tests with a fake forge and temp git repos:
- G0: deny, and allow, plus T3 escalation.
- G1: dirty, non-canonical cmd, VACUOUS, other SHA.
- G2: zero checks, pending, waiver valid / bound to the wrong SHA / issue in another repo / budget exhausted.
- G3: dropped, unattributed, altered-after-delivery, dry-run excluded, manual debit.
- G6: uncited claim, ev- from another SHA, fake run ID, prose false-positive corpus (no trigger), negation, Vietnamese tokens, trusted status block.
