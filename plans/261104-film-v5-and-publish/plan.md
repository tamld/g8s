---
title: "Film v5 music re-selection + site publish round (imagery, instrumentation, bridges)"
issues: []
related-plans: ["261103-f0-skill-vault-optimization"]
status: in_progress
priority: P1
created: 2026-11-04
mode: auto

**Session type**: T2 (execution)
---

# Film v5 music re-selection + publish round

## Summary

Operator accepted film v4's SFX/mix/ducking but rejected two things, both now MEASURED: (1) the BGM track's real grid is human-drummed (residual ±120ms vs canon gate ≤±15ms) so rigid anchoring drifts 104-194ms at 7/10 anchors; (2) the genre itself doesn't fit a dark/technical film. This plan: re-select music UNDER a mechanical acceptance gate, anchor to MEASURED beat times, re-render; ship the site round (imagery wave 6 + instrumentation + stake/bridge lines + body 16px); then publish and instrument success.

## Measured evidence (2026-11-04, librosa least-squares on out/master_with_bgm.m4a)

- Chosen BGM: BPM=130.58, t0=0.787s, **residual ±120ms** → canon acceptance (≤±15ms) FAILED — should have been rejected at selection.
- Anchor alignment: 3/10 cuts ON (≤40ms); gate ticks 28/30/34/36s off by 104-194ms; 60s end card off 60ms.
- SFX/mix/ducking: operator-accepted. Keep the stem chain, keep the SFX pack (manifest verified: fs-546088 keyboard, fs-558238 impact, fs-859482 riser, 6 switch clicks).

## Phases

### Phase 1 — Film v5 music re-selection + re-alignment (F1 worker, video-shotcraft canon)
1. Grid-acceptance gate FIRST for every candidate (5 bundled + up to 4 FreePD/Mixkit dark-cinematic candidates — Mixkit license, no attribution; record URL+license per candidate). Reject any residual >±15ms for rigid anchoring.
2. Tone filter: dark / technical / cinematic — no house/hiphop vocals-for-vibes; document one-line tone fit per candidate.
3. Anchor mode: if the winner's grid is machine-regular → least-squares grid; if human-drummed → anchor scene events to the MEASURED per-beat times (beat_track output), not an idealized even grid.
4. Re-time scenes (only ±40 frame shifts; frames computed last), re-render Harness + HarnessVertical, mux with the existing stem chain (SFX pack + sidechain mix unchanged — operator accepted), two-version delivery (BGM / no-BGM).
5. Gates: FV1 residual proof for the winner · FV2 loudnorm both files · FV3 concurrency ≤2 under VO · FV4 integrity · **NEW FV6: every scene cut + gate tick lands within ±40ms of its anchor beat (librosa re-measure of the FINAL master)**.
Deliverable: worktree assets/video/* + docs/research/27-film-v5-music.md.

### Phase 2 — Site round (F1 worker, index.html + assets/v5/*)
1. Instrumentation: Cloudflare Web Analytics beacon (token via CF dashboards by operator if needed — free, cookieless, CSP-compatible) + define success metric: **100 visitors → ≥1 inbound email**; add `?ref` tracking on outbound repo links.
2. Stake line: 1 sentence under H1 naming the reader's stake (what breaks when agents have no brakes — for THE READER, not the builder).
3. Terminology bridge: first occurrence of receipt/lease/gate gets a one-line plain-language parenthetical (EN).
4. Body 15→16px; remaining 10 sub-13px blocks ≥13px (terminal 12px idiom exempt).
5. Imagery wave 6 per docs/research/25: SEAL-01 full 3-state SVG seal system (dividers+bullets), SPECIMEN-01 forensic redacted receipt panel (real capture data, █ redaction bars), PLATE-01 ledger blueprint sheet, TEX-01 vellum grain+scanline+vignette, OG-01 1200×630 (Remotion still) wired into og:image.
6. Gates: taste INSPECT re-run — small-text FAIL must drop 10→≤3; body-size FAIL gone; pictures count ≥3; SRI PASS; RG1 node checks.

### Phase 3 — Publish + instrument verify (F0)
1. PR → CI → merge → deploy → prod curl checks (film sizes, og:image 1200×630, analytics beacon 200).
2. Post-publish: re-run taste INSPECT on prod; record the score delta in this plan's ledger.
3. First success-metric reading scheduled: 2 weeks after publish.

### Phase 4 — JEV v2 adoption (watch, F0)
- Argon's PR #731 (workspace-write opt-in, issue #724) — when merged: `git pull` + `make local` + adopt the flag in dispatch scripts; eval-after-close per standing law.

## Eval gates

- P1: FV1-FV6 all pass (film v5)
- P2: INSPECT small-text ≤3, body-size gone, pictures ≥3, SRI pass
- P3: analytics beacon live on prod + metric line recorded in this plan
- P4: #731 merged → binary rebuilt → flag used in next dispatch

## Non-goals

No harness code changes beyond adopting #731. No new BGM composition. No analytics beyond CF Web Analytics (no cookies, no third-party trackers — CSP stays `script-src 'self' + cloudflareinsights`).
