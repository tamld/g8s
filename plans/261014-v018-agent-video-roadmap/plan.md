# g8s v0.18.0 Roadmap — Agent Video Production & Multi-Modal Orchestration (`AgentVid`)

**Session type**: T1 (strategy)

> **Target Release**: `v0.18.0`  
> **Source Assets**: `AgentVid` Archive Bundle (`agentvid-motion-agent-diagram-9x16`, `agentvid-motion-comic-hype-9x16`, `agentvid-explainer-ai-for-business-music`, `agentvid-explainer-ai-for-business-voice`, `AgentVid-Bo-Welcome`)  
> **Architectural Thesis**: Unify the `AgentVid` multi-stage video production skills with `g8s`'s Zero-Trust `F0 / F1 / F2` containment hierarchy, path-scoped single-use Write Receipts, and cross-model verification gates (`A content`, `B UI`, `C render`).

---

## 1. Asset Inventory (`AgentVid` Archive Bundle)

| Archive | Skill / Package | Target Format & Domain | Key Pipeline Scripts |
| :--- | :--- | :--- | :--- |
| `agentvid-motion-agent-diagram-9x16 (1).zip` | `agentvid-motion-agent-diagram-9x16` | Vertical `9:16` (`1080x1920`, 30fps) continuous-board architectural explainer (`swarm → gauge → handoff → fan-out → split → recap → outro`) | `new-project.mjs`, `generate-voiceover.mjs`, `align-voiceover.mjs`, `build-timeline.mjs`, `snapshot-scenes.mjs`, `render-and-verify.mjs` |
| `agentvid-motion-comic-hype-9x16 (1).zip` | `agentvid-motion-comic-hype-9x16` | Vertical `9:16` & Square `1:1` kinetic comic/hype motion graphics | Same core pipeline + `aspects/square-1x1.json` + comic typography (`Anton`, `Bangers`, `PermanentMarker`) |
| `agentvid-explainer-ai-for-business-music.zip` | `agentvid-explainer-ai-for-business-music` | Business AI explainer with beat-grid music arrangement (`fit-beat-grid.py`, `arrange-music.mjs`, `verify-arrangement.py`) | Beat-locked BGM (`±2 ms` segment tolerance) + stem mix balance (`measure-mix-balance.py`) |
| `agentvid-explainer-ai-for-business-voice.zip` | `agentvid-explainer-ai-for-business-voice` | Voice-led business AI explainer (`VieNeu` local Vietnamese TTS / `ElevenLabs` / `Gemini` + `Soniox` / `faster-whisper` word alignment) | Single-take voiceover synthesis + word-level anchor injection (`inject-timing.mjs`) |
| `AgentVid-Bo-Welcome.zip` | `AgentVid-Bo-Welcome` | Operator onboarding manuals (`01-Huong-dan-su-dung-AgentVid.pdf`, `02-Cam-nang-edit-video-cho-nguoi-moi.pdf`) | Reference documentation for director/operator workflows |

---

## 2. Why `AgentVid` + `g8s` (`v0.18.0`) Is a Natural Structural Fit

Inspection of `references/agent-roster.md`, `references/pipeline.md`, and `references/review-gates.md` inside the `AgentVid` packages reveals three bottlenecks that `g8s` solves natively at the OS/SQLite harness layer:

1. **Procedural vs. Cryptographic "Builder ≠ Reviewer" Separation (`review-gate.py`)**:
   - *Current `AgentVid` limitation*: `references/review-gates.md` states: *"The session that built the video does not approve it... The script only compares the two names: this is procedural separation, not proof of identity."*
   - *`g8s v0.18.0` Upgrade*: Enforce **Builder ≠ Reviewer** via `g8s` path-scoped `WriteReceipt`s and distinct task/actor provenance:
     - **F1 Builder (`role: collector` / `builder`)** receives a `WriteReceipt` scoped strictly to `data/facts.md`, `data/script.json`, `data/storyboard.json`, `data/cues.json`, `data/timeline-config.json`. It is physically blocked by `receipt.VerifyScope` from writing to `review/**`.
     - **F1 Verifier (`role: verifier`)** runs in a separate `g8s` task (and optionally a distinct provider/model via `--provider` / `--model`), executes Gate A (`scripts/review-motion-script.py`), Gate B (`scripts/review-ui.py --capture`), and Gate C (`scripts/render-and-verify.mjs --require-review`), and holds the exclusive `WriteReceipt` for `review/**`.
2. **Single-Take Voiceover Serialization vs. Parallel Asset Fan-Out**:
   - *Current `AgentVid` constraint*: `pipeline.md` mandates **"One take, not one call per scene"** (`generate-voiceover.mjs` must read the entire script in one TTS call so narrator timbre never shifts across scenes; parallel sub-agent voicing is forbidden).
   - *`g8s v0.18.0` Upgrade*: DAG/stage-aware wave orchestration:
     - **Wave 1 (Parallel F1 Fan-Out)**: Scene illustrations (`generate-illustration.py`), SFX & raw BGM (`generate-sfx-and-music.mjs` + `fit-beat-grid.py`), and storyboard validation (`check-storyboard.mjs`) run concurrently across disjoint `assets/**` receipts.
     - **Wave 2 (Serialized Single-Winner F1 Lock, `concurrency=1`)**: `generate-voiceover.mjs` + `align-voiceover.mjs` execute atomically in one take under a single lease.
     - **Wave 3 (Timeline & Mix Assembly)**: `build-timeline.mjs --stems` + `measure-mix-balance.py` (asserting music `4–6 dB` under speech) + `snapshot-scenes.mjs`.
     - **Wave 4 (Cross-Model Gate B & Gate C Render)**: `review-ui.py --capture` → `render-and-verify.mjs --require-review --social` (asserting `1080x1920`, `30 fps`, `-14 LUFS`, `peak <= -1 dBTP`, zero black frames, zero `hyperframes check` occlusion/overlap).
3. **Model & Effort Mapping Alignment (`.g8s/agent-models.yml` ↔ `agent-roster.md`)**:
   - **F0 Director (`Gemini 4 Argon`, `high`/`max`)**: Story architecture, `data/facts.md` truth verification, visual taste on `renders/contact-sheet.jpg`, and final Gate C sign-off.
   - **F1 Builder (`gemini-3.8-flash-high`, `medium`)**: `data/script.json`, `data/storyboard.json`, GSAP/HTML scene extensions.
   - **F1 Mechanic (`gemini-3.8-flash-low`, `low`)**: Deterministic pipeline stages (`fit-beat-grid.py`, `arrange-music.mjs`, `align-voiceover.mjs`, `measure-mix-balance.py`).

---

## 3. Concrete `v0.18.0` Deliverables

1. **Skill Installation & Router Registration (`~/.agents/skills/agentvid-*`)**:
   - Extract and register the 4 `agentvid-*` skills into `~/.agents/skills/` and index their trigger keywords (`video`, `storyboard`, `9x16`, `explainer`, `vieneu`, `hyperframes`) in `~/.agents/skills/_shared/scripts/skill-router.py`.
2. **5th Offer Bundle Profile — `offer/profiles/media.md` (`media` / `video`)**:
   - Extend `g8s offer init --profile media` to scaffold `.g8s/trust-boundaries.yml` (protecting `review/**` stamps and `brand.json`), `.g8s/lane-bundles.yml` (mapping Gate A / Gate B / Gate C to `g8s` verifier lanes), and `.g8s/effort-classes.yml` (`director -> high`, `builder -> medium`, `mechanic -> low`).
3. **Multi-Modal Artifact Evidence Support in `g8s` (`internal/verifier` & `cmd/g8s/deliver.go`)**:
   - Support binary media deliverables (`assets/audio/*.flac`, `assets/audio/*.m4a`, `renders/*.mp4`, `renders/contact-sheet.jpg`) with SHA-256 manifest sealing in `attempt_N.json` and quantitative gate extraction from `data/render-report.json` (`LUFS`, `dBTP`, resolution, FPS, `hyperframes` layout checks).
4. **End-to-End Pilot Video (`g8s` Self-Explainer `9:16` Video)**:
   - Dogfood `g8s v0.18.0` + `agentvid-motion-agent-diagram-9x16` to produce an official 60-second `9:16` explainer video of `g8s`'s `F0 → F1 → F2` architecture (`swarm → gauge → handoff → fan-out → split → recap → outro`) with every metric in `data/facts.md` traced directly to `offer/SCORECARD.md` and `docs/claims.yml`.
