<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers) — Bản tiếng Việt

> **Harness thực thi tiến trình zero-trust, siêu nhẹ, cho các AI agent worker chạy CLI.**
> *"k8s điều phối container tính toán của bạn; g8s điều phối các AI subagent của bạn."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26.0-00ADD8)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)
![Release](https://img.shields.io/github/v/release/tamld/g8s)

<p align="center">
  <a href="README.md">English</a> | <b>Tiếng Việt</b>
</p>

*Bản tiếng Việt là bản tóm lược. Bản đầy đủ và cập nhật nhất: [README.md](README.md).*

---

## g8s là gì

g8s là một binary tĩnh thuần Go (zero CGO) cho phép orchestrator tầng cao ("Brain": Claude, GPT, Codex) giao việc cơ học cho các CLI worker nhanh (`agy`, Claude Code, Gemini CLI, Ollama) **mà không trao chìa khóa máy của bạn**. Tầng model không thể được tin bằng quyền hạn; g8s thi hành điều đó ở tầng tiến trình:

- **Hàng đợi bền vững**: hàng đợi lưu trong SQLite (chế độ WAL), có khóa lease nguyên tử, idempotency key chống nạp trùng, và dòng dõi cha–con. Session chết thì task vẫn còn; hàng đợi chính là bộ nhớ.
- **Receipt (biên nhận quyền ghi)**: worker không thể ghi file nếu không có receipt — mỗi receipt chỉ dùng một lần, có thời hạn và giới hạn đường dẫn, do Brain cấp.
- **Cách ly tiến trình**: mỗi lần chạy (attempt) nằm trong một nhóm tiến trình có thể kill và một worktree riêng; chạy song song cũng không ảnh hưởng nhau.
- **Bằng chứng chứ không phải lời khẳng định**: mỗi lần chạy được niêm phong vào Evidence Lake. Lời "đã xong" của worker chỉ là tuyên bố; file trên đĩa mới là bằng chứng.

**Thuật ngữ dùng trong tài liệu** (giữ nguyên tiếng Anh vì là tên gọi trong sản phẩm):

| Từ | Nghĩa |
|---|---|
| **Brain** | orchestrator tầng cao (Claude, GPT, Codex) — người ra quyết định |
| **worker** | tiến trình AI chạy lệnh (agy, Claude Code, Gemini CLI, Ollama) — người làm |
| **task** | một đơn việc nằm trong hàng đợi của g8s |
| **receipt** | biên nhận quyền ghi file: một lần dùng, có hạn, chặn đúng vùng |
| **attempt** | một lần chạy cụ thể của task |
| **signal** | dòng sự kiện báo "task đã kết thúc" trong `signals/tasks.jsonl` |

## Đã ship trong v0.13.0

- **Giám sát theo sự kiện**: khi task kết thúc (thành công hay thất bại), g8s ghi một dòng vào `<state_dir>/signals/tasks.jsonl`; `g8s watch --failed` giữ đúng một process chờ và đánh thức supervisor khi có tín hiệu. Vòng lặp hỏi-đáp (polling) chỉ là phương án dự phòng.
- **Queue đa provider**: `providers.json` là manifest; claim affinity theo provider, `--provider` trên submit lẫn worker, không fallback thầm lặng.
- **Retry có ngân sách (đang land)**: task FAILED tự resubmit trong giới hạn: 2 lần mỗi task, 10 lần/giờ mỗi state dir, backoff luỹ thừa, flag mặc định OFF. Tick tự động chỉ đụng task không cần receipt.
- **Dispatch đồng thời**: `g8s worker --concurrency N` — N attempt cách ly, worktree riêng từng attempt.
- **Eval đối kháng**: bộ probe chấm điểm semantic-class + Provider Reliability Index; mock provider giúp chạy được trong CI.
- **Fuzz + baseline đo được**: 4 fuzz target trên các parser đầu vào ngoài; độ trễ queue ghi trong [docs/user-guide/performance.md](docs/user-guide/performance.md).

Số liệu định lượng (số lớp containment, ngưỡng PRI, số gate) nằm trong [docs/claims.yml](docs/claims.yml) và [docs/ENTERPRISE_LEDGER.md](docs/ENTERPRISE_LEDGER.md), mỗi claim buộc vào một test kiểm chứng — [tools/claims_check.sh](tools/claims_check.sh) soát lại mỗi commit.

## Cài đặt

Một dòng (macOS / Linux):

```bash
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

Từ [release](https://github.com/tamld/g8s/releases): tải archive, đối chiếu `checksums.txt`. Từ mã nguồn:

```bash
git clone https://github.com/tamld/g8s.git && cd g8s
go build -o bin/g8s ./cmd/g8s
```

## Quickstart

```bash
# 1. Nạp một task scout chỉ-đọc
g8s submit \
  --idempotency-key scout-1 \
  --role scout \
  --permission read_only \
  --add-dir ./src \
  --model gemini-3.8-flash-high \
  --timeout 60s \
  --prompt "Scan ./src and return a JSON inventory of entry points."

# 2. Cho worker nhận task và chạy (mỗi task một worktree cách ly)
g8s worker --once                          # một attempt
g8s worker --once=false --concurrency 4    # tháo song song (cần git checkout)

# 3. Đánh thức khi xong — không polling
g8s watch --task <task-id> --milestone worker-complete
g8s watch --failed                         # exit khi BẤT KỲ task chạm trạng thái terminal

# 4. Đọc kết quả đã niêm phong
g8s get <task-id>
```

Ghi ghi-delegation cần receipt single-use (chú ý `--ttl`: mặc định ngắn có chủ đích — hãy cấp đủ cho thời lượng task):

```bash
g8s receipt issue --issuer "brain-orchestrator" --path "./tests/*.py" --ttl 3600
AGY_MCP_ALLOW_WORKSPACE_WRITE=1 g8s submit --role test-runner --permission workspace_write \
  --receipt-id <receipt-id> --add-dir ./tests \
  --prompt "Generate pytest tests. receipt_id=<receipt-id> allowed_paths=./tests/*.py"
```

Các lệnh kiểm tra và dọn dẹp hằng ngày:

```bash
g8s eval run --provider agy      # bộ probe đối kháng
g8s reflex triage --summary "raise test deadline" --files "internal/runtime/verify_test.go"
g8s autopilot tick               # bảo trì stateless: doctor, retention, hygiene
g8s cleanup --dry-run            # ghost process, session mồ côi, branch rác
```

## Skills

g8s ship chính kỷ luật vận hành của nó dưới dạng agent skill — xem [`skills/README.md`](skills/README.md) và [`manifest.json`](skills/manifest.json):

| Skill | Mục đích |
|---|---|
| [`g8s-supervisor`](skills/g8s-supervisor/SKILL.md) | Hiến chương supervisor/worker: phối việc có kiểm soát đầu vào, chạy song song nhiều worker, nghiệm thu theo bằng chứng, quy trình che dữ liệu nhạy cảm trước khi báo cáo lên. |

Cài bằng cách copy hoặc symlink vào thư mục skill của platform bạn dùng.

## Non-Goals

| Lĩnh vực | Không làm | Lý do |
|------|----------|-----------|
| **Điều phối container** | Kubernetes/nomad, service mesh | g8s là harness *tiến trình* — chạy g8s worker TRÊN k8s/nomad, không phải trong chúng. |
| **Quản lý secret** | Vault/AWS/GCP Secret Manager | Credential không bao giờ vào sandbox của worker; cấp sẵn qua biến môi trường trước khi chạy g8s. |
| **Multi-tenancy** | RBAC, namespace, SaaS audit | CLI single-tenant; một binary + state dir mỗi tenant ([ADR-0028](docs/decisions/0028-multi-project-tenancy.md)). |
| **Dashboard GUI** | Web UI cho task/receipt | CLI-first; Evidence Lake + `g8s status` là bề mặt quan sát. |
| **Worker SDK** | SDK Go/Rust/Python | Worker là bất kỳ CLI nào nói giao thức AIC. |
| **Hosting model** | Inference, model registry | g8s ủy thác cho CLI ngoài — hosting là việc của họ. |

## Tài liệu

| Theo nhu cầu | Ở đâu |
|---|---|
| Từ 0 đến task ủy quyền đầu tiên | [docs/quickstart.md](docs/quickstart.md) |
| Ma trận lệnh đầy đủ & runbook | [docs/OPERATIONS.md](docs/OPERATIONS.md), [docs/user-guide/cli-reference.md](docs/user-guide/cli-reference.md) |
| Cấu hình & vòng đời service | [docs/user-guide/configuration.md](docs/user-guide/configuration.md), [docs/user-guide/service.md](docs/user-guide/service.md) |
| Bảo mật & kiểm chứng | [docs/security/VERIFICATION_GUIDE.md](docs/security/VERIFICATION_GUIDE.md) |
| Tích hợp provider | [docs/integrations/](docs/integrations/) |
| Decision records | [docs/decisions/](docs/decisions/): ADR-0001…0030 |
| Hiến pháp & delta kỹ thuật | [spec/constitution.md](spec/constitution.md), [spec/openspec/](spec/openspec/) |
| Sổ cái chiến dịch & lịch sử | [plans/](plans/), [docs/history/](docs/history/) |
| Lịch sử release | [CHANGELOG.md](CHANGELOG.md) |

*Cấu trúc dự án đầy đủ (tự render từ filesystem, có gate chống drift): xem [README.md](README.md) → Project Structure.*

## Chất lượng

Mọi commit qua **CI kép**: `CGO_ENABLED=0` (vet + test thuần Go) và `CGO_ENABLED=1 -race`. Push phải qua hệ gate pre-push: doc-contract sync, layer ownership, version sync, coverage ratchet, dogfood roundtrip, build đa nền tảng, root hygiene.

## Lộ trình

| Mốc | Nội dung chính | Trạng thái |
|--------|-----------|:---:|
| v0.13.0 (2026-09-30) | Queue đa provider, sóng hardening B–E, tín hiệu event-driven (`signals/tasks.jsonl` + `watch --failed`), release gate 7–8 | **Xong** |
| v0.14.0 (mục tiêu) | Vòng AI-factory: auto-retry ✅ (#501/#502), router tới tay người dùng — [#513](https://github.com/tamld/g8s/issues/513), lane router — [#514](https://github.com/tamld/g8s/issues/514), verifier registry — [#515](https://github.com/tamld/g8s/issues/515), vòng tự-đóng đầu tiên — [#516](https://github.com/tamld/g8s/issues/516); cắt bản = [#517](https://github.com/tamld/g8s/issues/517) · [SCORECARD](plans/261002-factory/SCORECARD.md) | **Đang làm** |
| v0.15.0 (mục tiêu) | Retrospective-as-task — cơ quan tự trưởng thành — [#519](https://github.com/tamld/g8s/issues/519) | Kế hoạch |
| v1.0.0 (2026-12-15) | GA: 6 tháng ổn định homelab, signoff bảo mật doanh nghiệp, fleet mTLS | Kế hoạch |

## Giấy phép

Phân phối theo **MIT License**. Copyright (c) 2026 TamLD. Xem [LICENSE](LICENSE). Các biến thể: [LICENSE-MIT](LICENSE-MIT), [LICENSE-APACHE-2.0](LICENSE-APACHE-2.0), [LICENSE-DISCLAIMER.md](LICENSE-DISCLAIMER.md).
