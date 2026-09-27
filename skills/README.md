# g8s Skills

Operator skills shipped with the g8s repository. They encode the proven
operating practice for running g8s itself — the supervisor/worker charter,
dispatch playbooks, and the security-redaction discipline for reporting
findings without leaking project data.

## What ships here

| Skill | Version | Purpose |
|---|---|---|
| [`g8s-supervisor`](g8s-supervisor/SKILL.md) | 4.0.0 | Charter for operating g8s as the canonical supervisor: admission-gated dispatch, multi-worker fan-out (1 task → N workers, mixed roles), evidence-verified acceptance, leak-free upstream reporting. |

The authoritative index is [`manifest.json`](manifest.json) — name, version,
entry point, and the references/assets/scripts each skill carries.

## Install

Copy (or symlink) the skill directory into your agent platform's skill
directory and it becomes loadable by name:

```bash
# Claude-family agent platforms (~/.agents/skills)
cp -R skills/g8s-supervisor ~/.agents/skills/

# or symlink (stays in sync with the repo checkout)
ln -s "$(pwd)/skills/g8s-supervisor" ~/.agents/skills/g8s-supervisor
```

Load it by name (`g8s-supervisor`) or invoke its args grammar directly:

```text
g8s-supervisor dispatch "inventory the auth module" --role scout --root ./internal
g8s-supervisor fanout "map the control plane" --plan plan.json
g8s-supervisor audit
```

## Updating the vendored copies

The home of record for each skill is the repository: edit
`skills/<name>/…` here, bump the version in its SKILL.md frontmatter,
update `manifest.json`, and ship it through the normal PR gates. Your
installed copy under `~/.agents/skills/` is a deployment — re-copy or
re-symlink after pulling.
