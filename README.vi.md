<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers) — Bản tiếng Việt

> Giống như **k8s** điều phối các container tính toán của bạn, **g8s** điều phối các AI subagent của bạn.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26.0-00ADD8)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)
![Release](https://img.shields.io/github/v/release/tamld/g8s)

<p align="center">
  <a href="README.md">English</a> | <b>Tiếng Việt</b>
</p>

*Bản dịch tiếng Việt của [README.md](README.md) — bản tiếng Anh là văn bản gốc đầy đủ nhất.*

## g8s là gì

`g8s` (đọc là **"Gates"**) là một runtime đơn-binary cho **hệ đa tác tử hai tầng**: tầng "Brain" cấp cao (Claude, GPT, DeepSeek) giao việc cơ học cho các CLI worker nhanh (Antigravity `agy`, Claude Code, Gemini CLI, Ollama) — và `g8s` đứng ra thực thi **ranh giới tin cậy** mà tầng model không thể tự đảm nhận:

- **Hàng đợi tác tử bền vững** — SQLite WAL, CAS lease nguyên tử, idempotency-key, lineage cha–con.
- **Receipt khả năng** — worker không thể ghi đè filesystem nếu không có receipt single-use, giới hạn thời gian và đường dẫn do Brain cấp.
- **Cô lập tiến trình** — mỗi attempt chạy trong process group kill-able và run directory riêng; các attempt đồng thời nhận worktree riêng.
- **Bằng chứng** — mỗi lần chạy niêm phong receipt đã biên tập vào Evidence Lake; telemetry chưng cất pattern thất bại ngược lại preflight.

## Điểm nhấn (v0.12.0)

- **⚡ Thuần Go, single binary** — Zero CGO, ~15MB, khởi động <15ms.
- **🛡️ Cổng an toàn nhiều lớp** — 6 vai trò × 3 hồ sơ quyền, chặn lệnh nguy hiểm, bảo vệ đường dẫn nhạy cảm (kể cả symlink và `..`).
- **🚀 Dispatch đồng thời** — `g8s worker --concurrency N`: N attempt song song, mỗi attempt một worktree riêng, khôi phục crash qua sessions registry.
- **🧠 Cổng thăng cấp tri thức** — FSM trạng thái + nhãn tin cậy + tombstone theo payload hash: vault không thể bị đầu độc bởi output chưa kiểm chứng.
- **📡 Context Broker** — `ContextPacket` có giới hạn ký tự (vault + telemetry + SOM) làm giàu Jev triage, fail-open theo từng nguồn.
- **⚡ Reflex gate System-1** — `g8s reflex triage`: Jev sensor + policy deterministic → `grant_receipt` / `escalate_hitl` / `instant_kill`.
- **🧪 Eval đối kháng** — bộ 24 probe, chấm điểm semantic-class deterministic, provider thật (agy/claude), chỉ số PRI.
- **💓 Quan sát & vệ sinh** — heartbeat, sessions registry, dọn orphan/zombie, Evidence Lake, telemetry vòng kín.

## Cài đặt

**Từ release** (macOS universal / Linux amd64+arm64 / Windows amd64 — kèm `.deb`/`.rpm`/`.apk`):
tải từ [trang Releases](https://github.com/tamld/g8s/releases) và đối chiếu `checksums.txt`.

**Từ mã nguồn:**

```bash
git clone https://github.com/tamld/g8s.git && cd g8s
go build -o bin/g8s ./cmd/g8s
```

## Quickstart

```bash
# 1. Gửi một tác vụ scout chỉ-đọc
g8s submit --idempotency-key scout-1 --role scout --permission read_only \
  --add-dir ./src --model gemini-3.7-flash-high --timeout 60s \
  --prompt "Quét ./src và trả JSON danh sách entry point."

# 2. Chạy worker để nhận và thực thi
g8s worker --once                                # một attempt
g8s worker --once=false --concurrency 4          # drain đồng thời (cần git checkout)

# 3. Đọc kết quả đã niêm phong
g8s get <task-id> --json
```

**Ghi có ủy quyền** cần receipt do Brain cấp:

```bash
g8s receipt issue --issuer "brain-orchestrator" --path "./tests/*.py" --ttl 600
g8s submit --role test-runner --permission workspace_write \
  --receipt-id <receipt-id> --add-dir ./tests \
  --prompt "Sinh pytest test. receipt_id=<receipt-id> issuer=brain-orchestrator allowed_paths=./tests/*.py"
```

## Skills

g8s ship kèm **skill vận hành chính nó** — xem [`skills/`](skills/):

| Skill | Mục đích |
|---|---|
| [`g8s-supervisor`](skills/g8s-supervisor/SKILL.md) | Hiến chương supervisor/worker: dispatch qua admission gate, fan-out đa worker (1 task → N worker, đa vai trò), nghiệm thu bằng bằng chứng, báo cáo upstream không lộ dữ liệu project. Kèm security-redaction playbook. |

Cài đặt: copy (hoặc symlink) vào thư mục skill của platform — hướng dẫn đầy đủ
và [`manifest.json`](skills/manifest.json) nằm ở [`skills/README.md`](skills/README.md).

## Tích hợp MCP

Kết nối Claude Desktop, Cursor, Codex, Windsurf qua stdio JSON-RPC (11 tools):

```json
{
  "mcpServers": {
    "g8s": { "command": "/usr/local/bin/g8s", "args": ["mcp"] }
  }
}
```

## Non-Goals

| Lĩnh vực | Không làm | Lý do |
|------|----------|-----------|
| Điều phối container | Kubernetes/nomad, service mesh | g8s là *process* harness — chạy g8s worker TRÊN k8s/nomad. |
| Quản lý secret | Vault/AWS/GCP Secret Manager | Secret không bao giờ vào sandbox worker. |
| Đa thuê bao | RBAC, namespace, SaaS | CLI single-tenant; mỗi tenant một binary + state dir. |
| GUI Dashboard | Web UI | CLI-first; Evidence Lake + `g8s status` là bề mặt quan sát. |
| Worker SDK | SDK Go/Rust/Python | Worker là bất kỳ CLI nào nói AIC protocol. |
| Host model | Inference, model registry | g8s ủy quyền cho CLI ngoài. |

## Kiểm thử & Cổng chất lượng

Mọi commit phải qua **dual-pass CI**: `CGO_ENABLED=0` (vet + test thuần Go) và `CGO_ENABLED=1 -race` (race detector) — **45 packages, 988 test functions**, zero race warning, zero CGO dependency. Mọi push phải qua **12 cổng pre-push** (doc-contract, layer ownership, version sync, dual-pass, dogfooding roundtrip, cross-platform build).

## Lộ trình

| Mốc | Nội dung chính | Trạng thái |
|--------|-----------|:---:|
| v0.10.0 (2026-09-24) | Jev AI reflex sensor tách rời, DiffDistiller & Verifier, 11 MCP tools | **Done** |
| v0.11.0 (2026-09-26) | Kiến trúc reflex phân tán (L1/L3/L6), telemetry vòng kín, eval harness, dialectic FSM | **Done** |
| v0.12.0 (2026-09-27) | **Dispatch đồng thời, cô lập sessions, cổng thăng cấp tri thức, Context Broker, DoR floor, eval live** | **Done** |
| v1.0.0 (2026-12-15) | GA: ổn định homelab 6 tháng, security signoff doanh nghiệp, fleet mTLS | Kế hoạch |

## Cấu trúc dự án

```
g8s/
├── cmd/g8s/           # CLI entrypoint (stdlib flag, không cobra)
├── internal/          # Toàn bộ packages (private, không import được từ ngoài)
│   ├── controlplane/  # SQLite WAL task queue (CAS lease, lineage, sessions registry)
│   ├── worker/        # Supervisor, spawn, concurrent drain, telemetry ingestion
│   ├── dispatch/      # Provider CLI wrapper, argv builder, sanitizer
│   ├── reflex/        # Jev sensor System-1 + policy deterministic (L1/L3)
│   ├── context/       # Context Broker: lắp ContextPacket có giới hạn
│   ├── memory/        # Memory lifecycle: FSM, promotion gate, tombstones
│   ├── harness/       # Role/permission gates + bộ probe đối kháng
│   ├── brief/         # Brief contract, DoR floor, skill routing
│   ├── receipt/       # Zero-trust write receipts
│   ├── vault/         # Knowledge vault (DELTA-11)
│   ├── telemetry/     # Ingestion vòng kín + negative patterns
│   ├── cleanup/       # Dọn ghost/orphan/scratch theo vòng đời
│   ├── server/        # HTTP API daemon (loopback + bearer auth)
│   └── ...            # orchestrator, diffintel, review, dialectic, watch
├── skills/            # Agent skills vendored (hiến chương g8s-supervisor + manifest)
├── packaging/         # Windows NSIS/WiX, Chocolatey, winget
├── docs/              # Hướng dẫn, ADRs, specs, bảo mật
├── plans/             # Sổ cái chiến dịch (đánh dấu session-type)
├── spec/openspec/     # OpenSpec deltas (DELTA-01..22)
├── schemas/           # JSON schemas (task, receipt, result)
├── tools/             # Scripts hỗ trợ CI (pre-push gates, release)
└── .github/workflows/ # CI/CD pipelines
```

## Tài liệu

| Theo nhu cầu | Vị trí |
|---|---|
| Từ không đến task đầu tiên | [docs/quickstart.md](docs/quickstart.md) |
| Toàn bộ lệnh & runbook | [docs/OPERATIONS.md](docs/OPERATIONS.md), [docs/user-guide/cli-reference.md](docs/user-guide/cli-reference.md) |
| Cấu hình & vòng đời service | [docs/user-guide/configuration.md](docs/user-guide/configuration.md), [docs/user-guide/service.md](docs/user-guide/service.md) |
| Bảo mật & xác minh | [docs/security/VERIFICATION_GUIDE.md](docs/security/VERIFICATION_GUIDE.md) |
| Tích hợp provider | [docs/integrations/](docs/integrations/) |
| Tham chiếu MCP tools | [docs/user-guide/mcp-tools.md](docs/user-guide/mcp-tools.md) |
| Quy tắc quản trị | [spec/constitution.md](spec/constitution.md) |
| Đặc tả kỹ thuật | [spec/openspec/](spec/openspec/) — DELTA-01..22 |
| Quyết định kiến trúc | [docs/decisions/](docs/decisions/) — ADR-0001…0023 |
| Lịch sử phát hành | [docs/CHANGELOG.md](docs/CHANGELOG.md) |

## Giấy phép

MIT — xem [LICENSE](LICENSE).
