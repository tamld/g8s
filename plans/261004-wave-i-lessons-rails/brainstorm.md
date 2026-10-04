# Brainstorm: lessons-pipeline rails against LLM self-dealing (Wave I #519)

**Status**: PROPOSED — awaits operator ratification (factory law 5: factory
proposes, operator ratifies). Nothing below is implemented.

**Session type**: T1 (strategy)

**Operator's falsification challenge (2026-10-04)**: "Nếu vòng tự học tự
hình thức hóa, bypass thủ tục thì sao? LLM thừa sức làm điều này."

## The standing answer (one sentence)

The pipeline never rests on the LLM being honest — it makes the LLM unable
to enact: **the LLM proposes, the machine verifies, the operator ratifies,
the machine records.** Same zero-trust posture as receipts (ADR-0029 D3).

## Threat model (6 vectors, grounded)

| # | Vector | Grounding |
|---|--------|-----------|
| T1 | Fabricate/hallucinate citations; cite real events with false meaning | RT-I checks existence only — GAP |
| T2 | Cherry-pick rounds supporting a pre-chosen conclusion | no population rule — GAP |
| T3 | Self-grade (retro reviews its own/adjacent class's round) | "no verifier grades its own class" exists but undefined for retro — GAP |
| T4 | Telemetry poisoning (workers emit events crafted for later citation) | REAL: worker is a telemetry writer (internal/worker/worker.go:1541) |
| T5 | Procedure capture (lessons conclude "remove the gate") | law 5 blocks enactment; the lesson→proposal flow lacks schema |
| T6 | Attention flooding → operator rubber-stamps | no budget/dedup — GAP |

## Chosen rail set (Approach A — one-way gate; B/C earned by catches later)

1. **Lesson schema, two layers**: `observation` (derived from telemetry;
   machine-checkable) strictly separated from `recommendation` (LLM
   opinion, tagged as such).
2. **Machine checks at lesson creation (fail-closed)**:
   - every `cited_event_id` resolves to a real telemetry row with fields
     matching the observation's claim (T1);
   - the lesson's author task class ≠ the class under review (T3);
   - dedup hash on the cited-catch set (T6);
   - per-round lesson budget (T6);
   - any failed check ⇒ the lesson is discarded, never queued (the
     deny-by-default floor: no verifiable catch = no lesson).
3. **One-way gate**: `recommendation` NEVER auto-applies. Ratified lessons
   become operator-edited directive/spec changes only. T5 dies here.
4. **No-self-review (T3)**: the retrospective task for round N must be
   dispatched against telemetry of classes the retro task did not perform.
5. **RT-I red-test extension**: a hostile case — feed the retro task a
   fabricated catch; the machine check must REFUSE the lesson (fail-closed
   proof, not prose).
6. **T4 posture (recorded, not built)**: telemetry poisoning is a real
   writer-level vector; the first countermeasure is detection (a red-cell
   slice asserting lesson-cited events against controlplane task state
   transitions — a fabricated event cannot correlate with a real
   transition). Earned by catch, per the factory's promotion pattern.

## Explicitly deferred (Approaches B/C — earned by catches)

- **B (adversarial reviewer class)**: a second independent task attacks
  each lesson before the operator. Cost: +1 slice + tokens per lesson.
  Trigger: the first ratified lesson later proven wrong.
- **C (falsification clocks + periodic re-ratification)**: every ratified
  lesson carries a D-08-style clock; the operator re-reads the whole rule
  set quarterly. Trigger: accumulated-drift evidence.

## Success metric

RT-I passes with the hostile-fabrication case refusing a lesson; a real
retrospective round produces ≥1 machine-verified lesson on the SCORECARD
(S-9) with the two-layer schema visible in the ledger.
