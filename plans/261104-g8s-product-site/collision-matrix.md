# 34 — Collision-domain matrix: every collision this campaign hit, and its standing check

Mỗi va chạm đều có bằng chứng thật trong campaign 2026-11-01→05. Trạng thái: SHIPPED (đã có check/law tự chạy) · LAW (quy trình bắt buộc, kiểm bằng kỷ luật) · OPEN.

| # | Va chạm | Bằng chứng | Standing check | Status |
|---|---|---|---|---|
| C1 | VO × music bed (masking 2–5kHz) | docs/research/23 autopsy — 158 clacks, 7–8 layers | V6-1: bed RMS trong VO span thấp hơn ≥8dB (đo mỗi render) | SHIPPED |
| C2 | Craft gates × semantic truth (lệnh không chạy được lên phim) | film M2 thiếu 2 flag bắt buộc — PR #11–#13 | `tools/command_truth.py` (extract mọi lệnh hiển thị → flags valid + required present) | SHIPPED |
| C3 | hover × transform (flip flicker ở biên) | PR #9 — edge-hover flip fail | Law: rotation trên inner wrapper, hit-box tĩnh; browser edge-hover test | SHIPPED |
| C4 | Worktree lifetime × harvest (pruned = mất deliverables ×3) | wt pruned sau contract-violation/error | LAW: harvest ngay khi worker xong; deliverables bền đặt template dir hoặc commit | LAW |
| C5 | Receipt scope × brief deliverables (E_DIFF_FIREWALL ×3) | wave-5 v1: docs/26 ngoài scope; INSPECT artifacts `.taste-skill/**` ngoài scope ×2 | LAW: receipt `-path` ⊇ mọi file brief yêu cầu (kể cả gate artifacts) | LAW |
| C6 | Timeout × effort (bare int / omitted → kill 240s) | P2/P2b chết đúng 240.018s ×2 | FIXED in CLI (#721 validate, #731 flag) | SHIPPED |
| C7 | Base staleness × dispatch (clone stale → command sai base) | task cbc399ed trên pre-v5 base | LAW: clone → checkout origin/main SHA trước dispatch | LAW |
| C8 | Session × queue (parallel coordinators) | sess-d9s2 dispatch song song | state-dir isolation (đã giữ, 0 contention) | SHIPPED |
| C9 | Caption orthography × phonetic VO ("g eight s") | caption screenshot — tên riêng bị phiên âm | Law: caption = orthography, không phiên âm; audit bảng caption | SHIPPED |
| C10 | Gate enforcement không đều giữa các site (aegis page merged không INSPECT) | INSPECT aegis: 10 tells | LAW: INSPECT mandatory per-site tại build + publish; polish waves có gate trong brief | LAW→AP/GP running |
| C11 | Render fleet × template dir | renders song song 2 composition | Remotion bundle dirs riêng per-process — holds | NOTE |
| C12 | Retry budget × orphaned lease (cancellation kill → budget cạn) | fxw11 ×2 FAILED "retry budget exhausted" | `-max-attempts 3` + dispatch script resilient (durable workspace, không /tmp) | SHIPPED |
| C13 | Task permission default `read_only` × platform_dispatch isolation (codex free-wrote main checkout) | D66: receipt `permission: read_only` nhưng codex exec sửa 5 file thẳng main checkout — không worktree, không receipt firewall (#776) | LAW: task implement → submit `-permission workspace_write` (isolator mới bật) + F0 harvest ngay; gạch enforcement gap = #776 | LAW |

## Craft-domain eval matrix (design/imagery/animation/video/VO)

| Domain | Gate/tool | Kết quả gần nhất | Còn nợ |
|---|---|---|---|
| Design/layout | taste INSPECT | flowaxon 13→3 FAIL (còn: monoexception, pictures) | g8s/aegis polish waves running |
| Imagery | Purpose Contract + specs 25 | SEAL/TEX/OG-01 shipped per spec | raster fragments (g8s polish) |
| Animation/3D | route (a) engine + B7 law | rig v3 live trên flowaxon | g8s site: static SVG → 3D polish |
| Video/audio | FV2 loudnorm + FV6 beat grid + V6-1 ducking + command_truth | film v7: -14.08 LUFS, cuts ≤±35ms, canonical commands | — |
| VO/captions | caption orthography law + V6 track | captions live cả 2 cut | — |
| CI/CD | branch protection 7 required checks | enabled (admin-escape) | — |
| C14 | Brief deliverable paths × repo assignment (2 subagents, 1 worktree) | A8 brief giao video-shotcraft nhưng deliverables viết đường dẫn flowaxon (`assets/video/…`) → subagent commit vào rig3 ĐÚNG LÚC F0 đang rebase → commit lạc vào PR #19 | LAW: 1 subagent = 1 repo; deliverable paths phải nằm trọn trong repo được giao; không rebase/harvest khi subagent còn chạy trên cùng clone | LAW |
| C15 | Header-parity × single-header edit (worker xóa nhầm nosniff khi đổi frame-ancestors; 3 surfaces lệch nhau) | #19 Stage 5 DAST: container missing x-content-type-options — BT gate chỉ check đúng header đang sửa | LAW: mọi header edit phải chạy full hardened-set parity trên cả 3 surfaces (_headers/nginx/main.go); làm tool `header_parity.py` + wire vào Stage 3 | LAW |
  - **C15 UPDATE 2026-11-06**: header_parity.py SHIPPED (flowaxon PR #22, Stage 3 step) + drift fixed (nginx CSP/HSTS/Permissions, _headers Permissions) + deploy-trigger gap fixed (PR #23: _headers added to deploy-pages.yml paths — edge config was silently never re-deployed). LAW → SHIPPED.
