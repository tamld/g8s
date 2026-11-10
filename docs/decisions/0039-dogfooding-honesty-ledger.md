# ADR-0039: Dogfooding honesty ledger

Status: Accepted (operator-ratified batch)  
Date: 2026-11-07

## Context

[Reform review F4](../research/35-g8s-reform-review.md) found that the
delegation mandate could not be falsified from machine-readable provenance:
about 45 substantive direct-to-main commits appeared in the last 200 commits.
The operator's 2026-11-07 session tally was 14 receipt/PR-backed landings out
of 15, with `3cbd263` explicitly described as F0-direct in its message. This
is session evidence, not a claim that those older messages satisfy this new
trailer grammar. R3 and issue #787 establish a prospective ledger.

## Decision

Every commit landing on main carries exactly one additive Git trailer:

```text
Receipt: <task-id>
```

or

```text
Provenance: F0-direct — <reason>
```

`Provenance: Receipt <task-id> [<annotation>]` and
`Provenance: worker task <task-id> [<annotation>]` are accepted aliases for
`Receipt: <task-id>`. Optional `<annotation>` must begin with `(`, `;`, or
case-insensitive `via ` (followed by nonempty text). `Provenance: F0-direct —
<reason>` may hard-wrap the reason across continuation lines without colons
in the final trailer paragraph.

Use a final trailer paragraph separated from the subject/body by a blank
line, as with `git interpret-trailers`. Trailer keys in that paragraph must be
ASCII tokens matching `[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?` (the same
token grammar `git interpret-trailers` uses); keys are case-insensitive and
whitespace around keys, colons, values, and the em dash is tolerated. Lines in
the trailer whose text before the first colon is not such a token — including
colon-bearing explanatory prose — render the commit unclassified. The shared
`<task-id>` token for `Receipt:` and both `Provenance:` receipt aliases is one
whitespace-free ASCII token of at least 6 characters matching
`[A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9]` with at least one digit (`0-9`). A
logical reason is nonempty; in `Provenance: F0-direct — <reason>`, it may sit
on one physical line or hard-wrap across continuation lines without colons in
the final trailer paragraph (normalized to single spaces).
Other single-line trailers may coexist. Missing, malformed, duplicate, or
conflicting provenance is unclassified; a subject/body mention is insufficient.
Normal commit messages continue to work; no commit-writing hook is imposed.
The ledger checks landed messages, not receipt authenticity or task completion.

The receipt ratio is `receipts / (receipts + f0_direct)`, over classified
landings only. With zero classified landings it is undefined. Unclassified
landings are a separate honesty gap: `unclassified / total`. The allowed
unclassified budget is **≤30%**; a larger gap fails the weekly job. The
enforcement value has one code definition, `maxUnclassifiedPercent`, in
`tools/dogfood_provenance/main.go`.

The honest target band is **≥70% receipt-backed for substantive Go/CI
changes; F0-direct legitimate for mechanical gate wiring, docs-registry
sweeps, and harness-bug hotfixes — each still tagged**. This target is a
review policy; the aggregate ledger does not infer substantive change scope
and does not enforce a receipt-ratio floor.

The existing Monday dogfood workflow publishes counts, ratio, and honesty
gap to stdout and `$GITHUB_STEP_SUMMARY`, even when another check fails.
It requires full history and measures at most the latest 200 commits reaching
main, including merged branch commits. A merge commit must itself carry
provenance too. First-parent traversal is used only to discover the epoch.

The start anchor is the earliest first-parent main commit that adds this
ADR (the PR's merge/squash landing). The reporter discovers that immutable
SHA from Git history and reads `anchor..origin/main`, excluding the anchor
itself. Deleting/re-adding the ADR does not reset the epoch. Before landing,
the report says “before convention”; prior history is never retro-tagged.
For **n<20**, it says “window too small (n<20), ratio not yet meaningful”
and does not fail on the gap. An explicit `--baseline` mode reports the
real last-200 historical log diagnostically, without budget enforcement.

## Consequences

Missing provenance becomes visible and bounded without inventing historical
receipts. Legitimate F0 work remains possible with a recorded reason. A
small window, an all-unclassified window, and a healthy classified ratio
with a large honesty gap have distinct, explicit report states. Weekly
enforcement allows the stated gap budget; it does not guarantee that every
individual landing complied. F0 reviews individual exceptions at landing.
