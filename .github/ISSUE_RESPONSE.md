# Issue Response Rules — how maintainers answer external issues

These rules govern every reply to an issue opened by someone outside the maintainer team. They exist so that responses stay **honest, verifiable, and free of theater** — the same standards the repository's own gates enforce on its code.

## 1. Triage before answering (verify, never guess)

Every external issue gets classified within the first response into exactly one of:

| Class | Signal | Action |
|---|---|---|
| **Actionable** | A reproduction, a command that failed with its output, or a feature gap against documented behavior | Reproduce locally (`g8s doctor --json` + the reporter's steps) BEFORE replying with any diagnosis; cite the commit you tested on |
| **Needs-info** | Template placeholders unfilled, no version/env, no repro, or a report that cannot be exercised | Apply the `needs-info` label; ask for the specific missing pieces (see §3); **never guess** at what the reporter meant |
| **Duplicate** | Existing issue covers it | Close as duplicate with a cross-link, saying what would reopen it |
| **Demand-gated** | A request the roadmap has explicitly gated (e.g. package-manager channels) | State the gate + its reopen trigger; a REAL, filled-out request from a user **counts toward and can satisfy the trigger** — link it into the gated issue |
| **Invalid/Spam** | Empty automation dumps, unrelated advertising | Label `invalid`, close, and say why in one factual sentence — no shaming |

## 2. The reply contract

1. **Say what you verified.** Every factual claim in a reply carries its evidence (commit SHA, command, output snippet). If we did not run it, we say we did not run it.
2. **Say what is missing, precisely.** "Needs-info" replies enumerate exactly which template sections were left as placeholders — the reporter should never have to diff their issue against the template themselves.
3. **No promises without artifacts.** "We will fix this" only appears when the fix has an issue + owner + gate. Otherwise: "tracked in #N" or "not planned, because X".
4. **No sycophancy, no shaming.** Factual, plain, respectful. Thank the reporter for the report once, concretely (what it enabled), not effusively.
5. **Language**: reply in English (repository language); a bilingual summary is welcome when the reporter wrote in another language.
6. **Every reply ends with the next step** — for the reporter (what to send), for maintainers (what happens next), or for the issue (when it closes).

## 3. The environment block we ask for

Bug reports should include the output of:

```bash
g8s version && g8s doctor --json
```

This is the project's own diagnostic surface — asking for it keeps report→repro distance short and exercises the same fail-loud envelope the product ships.

## 4. Labels & lifecycle

- `needs-info` — waiting on the reporter. Auto-close after **14 days** of no response, with a reopen-on-comment note.
- `triage:accepted` / `triage:declined` — the maintainer verdict after verification, always with the reason in the closing comment.
- `demand-gate` — real user demand accumulated toward a gated roadmap item (currently: winget/Chocolatey in the closed #833 record).
- Security-sensitive reports: do **not** diagnose in public — point to [SECURITY.md](../SECURITY.md) immediately.

## 5. What maintainers must NOT do

- Do not answer from memory of the code — read the code the reporter's version ships (version drift is the #1 wrong-answer source).
- Do not close an issue because the reporter is unresponsive **before** the 14-day window, or because fixing it is inconvenient.
- Do not convert an issue into work silently: an accepted issue gets a linked PR whose body says `Closes #N`.
- Do not reply with generated filler. Every reply is written for that issue; templates are scaffolding for the reporter, not for the reply.
