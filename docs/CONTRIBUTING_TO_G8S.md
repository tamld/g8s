# Contributing to g8s from another project

You are a cross-project contributor to g8s. This page is your
one-stop onboarding.

## When to contribute back

You depend on g8s as a tool, library, orchestrator, or service.
When you find:

- A bug that surfaces in your integration with g8s
- A missing feature that you need to use g8s effectively
- A documentation gap that confused you
- A performance issue that you measured
- A security concern
- A UX issue (e.g. error message is misleading)

→ File an issue on github.com/tamld/g8s

## How to file a great issue

The g8s maintainers (Sisyphus) read your issues. To help them
fix your issue in < 1 hour, include:

1. **Context** (1-2 sentences): What were you doing? What
   project were you using g8s from?
2. **Repro** (commands or code): Minimal steps to reproduce.
3. **Expected**: What did you expect g8s to do?
4. **Actual**: What did g8s do instead?
5. **Evidence**: cite file:line, commit SHA, or log snippet.
6. **Severity**: P0 (data loss / security), P1 (broken), P2 (UX),
   P3 (polish).

Example (real, from aegis agent on 2026-08-30):

```
## BUG: worker dispatch passes flags as the CLI subcommand
('Unknown command --prompt-file') yet task is marked SUCCEEDED

## Repro
g8s submit --role scout --prompt "..." → g8s worker --once →
task state SUCCEEDED, result.ok: true. But result.stdout is an
error envelope from g8s itself:
{"kind":"error","cmd":"--prompt-file",...}

## Impact
Task state is a claim, not evidence. A SUCCEEDED receipt whose
stdout is an error envelope is success-theater.
```

That issue (#189) was fixed in 28 minutes.

## What NOT to do

- ❌ Don't copy g8s code into your project (g8s is MIT OR
  Apache-2.0 — but use a normal dependency declaration, not
  verbatim copy)
- ❌ Don't open an issue for a feature that requires changes
  to your project (open the issue in your own repo)
- ❌ Don't blame an individual commit. g8s is built by Sisyphus
  + agy + contributors. The commit graph is the cause; humans
  are not.
- ❌ Don't write a bug report without evidence. Sisyphus will
  close it as "needs more info".

## How to dispatch a task back to g8s

If your project has g8s installed and an issue you need fixed:

```bash
# Copy examples/briefs/CONTRIBUTOR_BRIEF.md
# Fill in: title, context, repro, evidence, expected output
g8s submit --from-file brief.md --role scout --add-dir /path/to/your/project
```

g8s will dispatch a worker agent to fix the issue. PR opens
automatically on g8s repo.

## Reading the g8s codebase first

Before filing a bug, spend 5 minutes reading:

1. [docs/ARCHITECTURE_ROADMAP.md](ARCHITECTURE_ROADMAP.md) — what
   g8s is and isn't
2. [docs/ORCA_AUDIT.md](ORCA_AUDIT.md) — g8s's design lineage
3. [docs/ORCA_CICD_AUDIT.md](ORCA_CICD_AUDIT.md) — CI/CD choices
4. [tools/ai_lint.sh](../tools/ai_lint.sh) — 10 rules g8s enforces
5. [tools/brief_lint.sh](../tools/brief_lint.sh) — 3 rules for briefs

If your proposed change violates one of these, the g8s
maintainer will push back. That's expected.

## What you get out of it

When you file a good issue:
- Sisyphus triages within 1h
- A fix PR opens within 1-2h
- g8s ships a new release (v0.X.0) that includes your fix
- You upgrade to the new version
- The cycle continues

When 3+ projects do this, g8s evolves rapidly toward real-world
needs.
