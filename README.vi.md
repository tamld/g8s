<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers) — Bản tiếng Việt

> Giống như **k8s** điều phối các container tính toán của bạn, **g8s** điều phối các AI subagent của bạn.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8)
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

- **Zero-CGO**: một binary ~11MB, khởi động <15ms, RAM <15MB — không dependency native.
- **6 vai trò**: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
- **3 hồ sơ quyền**: `read_only`, `automation_read`, `workspace_write` (chỉ nhận qua receipt).
- **Chặn lệnh nguy hiểm** & bảo vệ đường dẫn nhạy cảm.
- **Ủy quyền ghi qua receipt**: single-use, TTL 1..3600s, giới hạn theo path.
- **Control plane SQLite WAL** bền vững: CAS lease, idempotency-key, lineage cha–con.
- **MCP stdio protocol**: kết nối Claude Desktop, Cursor, Codex, Windsurf.
- **Service manager macOS** (LaunchAgent, hardened) — systemd/Windows sắp tới.

## Cài đặt nhanh

Xem [docs/quickstart.md](docs/quickstart.md):

```sh
brew tap tamld/homebrew-tap
brew install g8s
```

hoặc tải archive từ [Releases](https://github.com/tamld/g8s/releases).

## Hướng dẫn sử dụng & Tích hợp

- [Tham chiếu CLI](docs/user-guide/cli-reference.md)
- [Tích hợp Claude Desktop](docs/integrations/claude-desktop.md)
- [Tích hợp Cursor IDE](docs/integrations/cursor.md)
- [Tích hợp Google Antigravity](docs/integrations/antigravity.md)
- [Tích hợp Windsurf](docs/integrations/windsurf.md)
- [Quy trình receipt delegation](docs/user-guide/receipt-workflow.md)
- [Quản lý service daemon](docs/user-guide/service.md)

## Kiến trúc & Đặc tả

- [Kiến trúc Decoupled Memory & Cognitive Whitepaper](docs/DECOUPLED_MEMORY_ARCHITECTURE.md)
- [Kiến trúc 3-Plane System Architecture](docs/ARCHITECTURE.md)
- [Hiến pháp Spec Kit](spec/constitution.md)
- [Danh mục đặc tả OpenSpec](spec/openspec/)

## Giấy phép

MIT — xem [LICENSE](LICENSE).
