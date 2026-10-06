# Design pass-3 — signal-based effort model (operator challenge, 2026-10-06)

**Status**: proposed by the operator ("mức độ effort áp theo repo là
không thuyết phục... cần blast radius, LOC, priority... hoặc các hệ giá
trị được estimate sẵn"); supervisor converged + built with recommended
defaults pending operator veto. Session type: T1→T2.

## The critique (correct)

Path-glob classes are a PROXY for what actually matters. What the
effort should track: **cost-of-wrong × difficulty**. The blind spot is
predictable: the first "docs" task that needed high will be a contract
doc (wrong doc → wrong implementation downstream) — `docs/**→low`
cannot see it. Our own data validates half the critique: LOC is real
for WRITE tasks but proven irrelevant for audits (41–91 files all fine
at low, P1 gauge); blast radius is invisible to globs entirely.

## The three signals, honestly typed

- **Blast radius** (cost of wrong output — who consumes it, does the
  error propagate silently): the strongest signal. Runs into CHECK
  INTENSITY first (verifier strictness), effort second (passing a
  stricter check demands more thought). Two dials, not one.
- **LOC / surface scale** (difficulty of coordination): conditional
  signal — matters for write tasks, does not apply to read-only scans
  (measured).
- **Priority** (urgency): SCHEDULING ONLY — order + check timing
  (check-before-ship vs ship-then-recheck). Never raises effort;
  conflating "how soon" with "how careful" is the incident pattern.
  (Recommended default, flipped by one line if the operator rules
  otherwise.)
- **Declared estimates**: the brief author's knowledge ("this is a
  tricky concurrency fix") — the one channel globs can never have.
  Epistemics per the rails: every declaration is a HYPOTHESIS, recorded
  and calibrated against actuals (token telemetry makes
  estimate-vs-actual measurable from day one).

## Resolution precedence (v1)

```
effort_requested =
  1. --effort                       (explicit — always wins, recorded)
  2. declared signals               (--blast-radius, --loc-estimate →
                                     hypothesis matrix v1 below)
  3. path-class registry            (demoted: fallback prior)
  4. medium floor
```

- Payload records `effort_source` (explicit|declared|registry|floor)
  and `effort_signals` (ALL inputs + what each source suggested) — no
  silent anything (C3-consistent: every conflict recorded, value-winner
  visible).
- **Matrix v1 (hypothesis, calibrated by outcome telemetry)**:
  blast=high → at least medium; blast=high ∧ loc>200 → high;
  blast=low ∧ loc≤50 → low; else → registry/medium decides.
  Thresholds are deliberately coarse — the gauges refine them; the
  model ships before the constants are "right".
- Lane-based classification (task INTENT via ALDC router — a stronger
  prior than path) = follow-up slice; the manual submit path does not
  classify lanes yet.
- Check-intensity wiring (blast → verifier bundles) = follow-up slice;
  v1 keeps blast affecting effort only.

## Falsification hooks

- If declared-signal dispatches show NO better escalation-rate than
  registry-only after ~20 rounds → the signals don't pay → simplify.
- Estimate-vs-actual delta per (blast, loc) bucket = the calibration
  gauge; a declared estimate that misses 2× repeatedly is a wrong
  hypothesis, not a scheduling problem.
