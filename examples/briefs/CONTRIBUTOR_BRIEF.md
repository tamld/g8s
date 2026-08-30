# Brief template for cross-project contributors

## How to use
1. Copy this file into your project
2. Fill in the 5 sections below
3. Run: `g8s submit --from-file brief.md --role <your-role>
   --add-dir <your-project-path>`

## Template

# Brief: <short summary>

## Context (what were you doing)
<1-2 sentences: your project, your workflow>

## Repro (how to see the bug)
```bash
<minimal commands>
```

## Expected (what g8s should do)
<what success looks like>

## Actual (what g8s did)
<error message, log snippet, file:line>

## Evidence (commit SHA, file:line, log)
<paste of the relevant log; or a `git log` snippet>

## DoD for g8s to ship
- [ ] The fix is in a PR on github.com/tamld/g8s
- [ ] The fix has CGO_ENABLED=0 go test ./... passing
- [ ] The fix has a test that reproduces your bug
- [ ] A release v0.X.0 is published with your fix

## Out of scope
<what g8s should NOT change>

## Do NOT do
- Do not write tests that pin fabricated symbols (DEBT-49)
- Do not use a directive brief (use open questions instead, DEBT-47)
- Do not skip the g8s doctor check (DEBT-51)
