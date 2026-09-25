<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers) — Bản tiếng Việt

> Giống như **k8s** điều phối các container tính toán của bạn, **g8s** điều phối các AI subagent của bạn.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26.0-00ADD8)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

<p align="center">
  <a href="README.md">English</a> | <b>Tiếng Việt</b>
</p>

*Tài liệu này là bản dịch tiếng Việt của [README.md](README.md).*

## Tổng quan

g8s là một **hệ thống đa tác tử hai tầng** thuần Go, Zero-CGO:

```text
┌─────────────────┐         ┌──────────────────────────────────────┐
│   BRAIN TIER    │         │            g8s BINARY                │
│  Orchestrator   │ ──────► │  Role&Perm Gate · Write Receipt DB   │
│  (LLM agent)    │         │  WAL Task Queue · CLI Providers      │
└─────────────────┘         └──────────────┬───────────────────────┘
                                           │
                            ┌──────────────▼───────────────────────┐
                            │           WORKER TIER                 │
                            │  collector · scout · summarizer ...   │
                            └──────────────────────────────────────┘
```

## Tính năng chính

- **Zero-CGO**: một binary ~15MB, khởi động <15ms, RAM <15MB — không dependency native.
- **6 vai trò**: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
- **3 hồ sơ quyền**: `read_only`, `automation_read`, `workspace_write` (chỉ nhận qua receipt).
- **Chặn lệnh nguy hiểm** & bảo vệ đường dẫn nhạy cảm.
- **Ủy quyền ghi qua receipt**: single-use, TTL 1..3600s, giới hạn theo path.
- **Control plane SQLite WAL** bền vững: CAS lease, idempotency-key, lineage cha–con.
- **Supervisor & Điều phối Ý định (Intent Orchestrator)**: FSM 8 trạng thái, phân tích nguyên nhân gốc rễ (RCA) tự động, vòng lặp tự sửa lỗi (`g8s orchestrate`).
- **Meta-optimizer metrics**: 8 chỉ số tổng hợp qua các supervisor run (`g8s supervisor-metrics --aggregate`).
- **AIC review PR tự động** (`g8s orchestrate-aic`) và điều phối from-intent qua FanOut (DELTA-18).
- **Jev AI reflex sensor**: System-1 risk gate cho mọi mutation (`g8s reflex triage`) — **ADR-0020**.
- **Heartbeat & Giám sát tiến trình thời gian thực**: theo dõi liveness của worker (`g8s status --worker`).
- **Dọn dẹp tài nguyên & Vệ sinh vòng đời**: tự động dọn process ma, worktree mồ côi, scratch branch và artifact rác (`g8s cleanup`).
- **Quy trình điều phối Brief**: cấp phát và sử dụng brief (`g8s brief-issue`, `g8s brief-consume`).
- **MCP stdio protocol (11 tools)**: kết nối Claude Desktop, Cursor, Codex, Windsurf.
- **Service manager đa nền tảng**: macOS LaunchAgent, Linux systemd, Windows sc.exe — hardened.

## Cài đặt nhanh

Xem [docs/quickstart.md](docs/quickstart.md):

```sh
brew tap tamld/homebrew-tap
brew install g8s
```

hoặc tải archive từ [Releases](https://github.com/tamld/g8s/releases).

## Lộ trình phát hành

| Thời điểm | Mốc | Trạng thái |
|-----------|-----|:---:|
| 2026-09-24 | **v0.10.1** — Sửa lỗi campaign trả nợ (#319/#321), Windows CI guards, probe suite | **Done** |
| 2026-10-05 | v0.11.0 — DELTA-21 Unified Memory (#325), Telemetry #253, Adversarial Evals #254 | Đang tiến hành |
| 2026-11-01 | v0.12.0 — DELTA-20 Code Intel Tiers, kardianos/service | Kế hoạch |
| 2026-12-15 | v1.0.0 — GA: ổn định homelab 6 tháng, security signoff | Kế hoạch |

## Kiểm thử & Cổng chất lượng

```sh
CGO_ENABLED=0 go vet ./... && go test -count=1 ./...
CGO_ENABLED=1 go test -race -count=1 ./...
```

- **38 packages** / **820+ test functions** — xanh trên cả hai cổng.

## Hướng dẫn sử dụng & Vận hành

- [Sổ tay vận hành (Operations Runbook)](docs/OPERATIONS.md)
- [Hướng dẫn xác minh bảo mật & Checksum](docs/security/VERIFICATION_GUIDE.md)
- [Tham chiếu CLI](docs/user-guide/cli-reference.md)
- [Cấu hình hệ thống](docs/user-guide/configuration.md)
- [Tham chiếu MCP Tools](docs/user-guide/mcp-tools.md)
- [Tích hợp Claude Desktop](docs/integrations/claude-desktop.md)
- [Tích hợp Cursor IDE](docs/integrations/cursor.md)
- [Tích hợp Google Antigravity](docs/integrations/antigravity.md)
- [Tích hợp Windsurf](docs/integrations/windsurf.md)
- [Quy trình receipt delegation](docs/user-guide/receipt-workflow.md)
- [Quản lý service daemon](docs/user-guide/service.md)

## Kiến trúc & Đặc tả

- [Kiến trúc Decoupled Memory & Cognitive Whitepaper](docs/DECOUPLED_MEMORY_ARCHITECTURE.md)
- [Đặc tả Chưng cất Tri thức Tri-Anchor](docs/CONTEXTUAL_DISTILLATION_SPEC.md)
- [Kiến trúc 3-Plane System Architecture](docs/ARCHITECTURE.md)
- [Hiến pháp Spec Kit](spec/constitution.md)
- [Danh mục đặc tả OpenSpec](spec/openspec/)

## Giấy phép

MIT — xem [LICENSE](LICENSE).
