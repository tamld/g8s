<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers) — Bản tiếng Việt

> **Harness thực thi tiến trình zero-trust bằng binary tĩnh thuần Go (<25MB, không CGO) cho các AI agent CLI worker.**  
> *"k8s điều phối container tính toán của bạn; g8s điều phối các AI subagent của bạn."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26.10-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)
[![Release](https://img.shields.io/github/v/release/tamld/g8s)](https://github.com/tamld/g8s/releases)

<p align="center">
  <a href="README.md">English</a> | <b>Tiếng Việt</b>
</p>

*Bản tiếng Việt là bản tóm lược chuẩn mực. Bản kỹ thuật đầy đủ và cây cấu trúc tự động: [README.md](README.md).*

---

## 1. Tổng quan & Tại sao cần g8s?

Khi các AI agent tự hành (Claude, GPT, Codex, Gemini) thực thi công việc trên máy cục bộ, việc trao toàn quyền shell trực tiếp dễ dẫn đến sai sót nghiêm trọng: xóa nhầm file, sửa ngoài phạm vi cho phép, bỏ sót thay đổi, vòng lặp vô hạn và rò rỉ biến môi trường.

`g8s` giải quyết bài toán này bằng cách thiết lập **ranh giới zero-trust đối xứng** giữa mô hình tư duy chiến lược ("Brain") và các tiến trình thực thi cơ học ("Muscle"). Được đóng gói dưới dạng một binary tĩnh thuần Go duy nhất (zero CGO), `g8s` cách ly thực thi vào các Git worktree độc lập, kiểm soát quyền sửa file bằng biên nhận có phạm vi, và niêm phong bằng chứng bền vững.

### Khi không có g8s vs. Khi có g8s

| Chiều hướng | Khi không có g8s (Ambient Shell) | Khi có g8s (Zero-Trust Harness) |
| :--- | :--- | :--- |
| **Quyền hạn thực thi** | Trao toàn quyền máy chủ; worker có thể can thiệp bất kỳ đâu. | Cấu hình quyền theo role chặt chẽ (`read_only`, `workspace_write`). |
| **An toàn hệ thống file** | Thay đổi file trực tiếp làm hỏng working tree chính. | Biên nhận `WriteReceipt` một lần dùng, giới hạn vùng ghi (`AllowedPaths`). |
| **Cách ly tiến trình** | Tiến trình con sót lại dưới dạng tiến trình ma (zombie) khi crash. | Nhóm tiến trình OS (`Setpgid`) kill sạch toàn bộ cây tiến trình khi dừng. |
| **Tính bền vững của task** | Lưu trong bộ nhớ agent tạm bợ; mất khi ngắt kết nối. | Hàng đợi SQLite WAL với khóa Compare-And-Swap (CAS) nguyên tử. |
| **Truy cập Gateway LLM** | Dùng script Python chắp vá hoặc nền tảng cloud đóng kín. | Tiêu thụ gateway hướng cấu hình thuần Go (`g8s llm-call`, `9router`, OpenAI, Anthropic). |
| **Ngữ cảnh Supervisor** | Bị tràn ngập bởi hàng megabyte log thô kiểm thử / CI. | Tường lửa thông tin không suy hao (`RoundReceiptSchema` lọc nhiễu, giữ nguyên lỗi). |

### Bảng thuật ngữ cốt lõi

| Thuật ngữ | Định nghĩa kiến trúc |
| :--- | :--- |
| **Brain (`F0`)** | Orchestrator tầng cao (Claude 3.7 Sonnet / Opus, GPT-4o, Codex) giữ quyền tư duy và commit Git. |
| **Worker (`F1`)** | Tiến trình cơ học (`agy`, `claude`, `gemini`, `ollama`) hoặc gateway consumer thực thi gói việc được giao. |
| **Subagent (`F2`)** | Subagent tạm thời được sinh đệ quy bởi F1, chia sẻ chung worktree và biên nhận của F1 (`F2 ⊆ F1 ⊆ F0`). |
| **Task** | Đơn vị công việc nguyên tử, bền vững nằm trong hàng đợi SQLite WAL kèm ngân sách và mã chống trùng. |
| **Receipt** | Biên nhận quyền ghi file: dùng một lần, có thời hạn và giới hạn vùng đường dẫn (`AllowedPaths`). |
| **Attempt** | Một lần chạy cụ thể của task, được cô lập trong một Git worktree và nhóm tiến trình riêng biệt. |
| **Evidence Lake** | Kho lưu trữ append-only niêm phong biên nhận, sự kiện quan sát viễn từ và diff thực tế trên đĩa. |

---

## 2. Kiến trúc & Vòng đời thực thi

`g8s` tổ chức cộng tác đa agent theo kiến trúc trực giao 2 trục (**Trục X**: Vòng cô lập `F0 / F1 / F2` theo [ADR-0032](docs/decisions/0032-containment-levels.md) $\times$ **Trục Y**: Phân tầng điều khiển `S0..S3 + O-Plane` theo [ADR-0035](docs/decisions/0035-orthogonal-stratum-federation.md)):

<p align="center">
  <img src="assets/architecture.svg" alt="Kiến trúc Zero-Trust Trực giao của g8s (F0/F1/F2 x S0..S3)" width="100%"/>
</p>

### 5 bước vòng đời thực thi & Biểu đồ luồng trực quan

1. **Nạp & Kiểm cổng (Submit & Gate)**: Người vận hành / Brain (`F0`) kiểm tra brief (`g8s brief-issue --lane <0|D|R|F|S>`), nén ngữ cảnh (`g8s context-pack`), cấp biên nhận `WriteReceipt` (`g8s receipt issue`) và nạp task (`g8s submit`).
2. **Khóa (Lease)**: Worker (`F1`) nhận task qua khóa Compare-And-Swap (CAS) nguyên tử trong SQLite WAL (`g8s worker --once`).
3. **Cách ly & Hydrate (Contain & Hydrate)**: Harness tạo Git worktree riêng, nhóm tiến trình OS (`Setpgid`), và nạp skill gắn mã băm (`skill://g8s/<tier>/<name>`).
4. **Kiểm chứng 6 tầng (Verify & QA Audit)**: Cổng kiểm chứng (`g8s verify` & `g8s qa-report`) chạy bộ test thực tế, holdout độc lập và canary đột biến, xuất `RoundReceiptSchema` qua Tường lửa thông tin.
5. **Niêm phong & Giao nhận CAS (Seal & Deliver)**: Kết quả niêm phong vào receipt; Brain kiểm tra diff và hợp nhất an toàn qua `g8s deliver --expected-head-oid <sha>`.

<p align="center">
  <img src="assets/workflow.svg" alt="Vòng đời thực thi 5 bước & Bảng chọn lệnh nhanh của g8s" width="100%"/>
</p>

---

## 3. Năng lực cốt lõi

| Lĩnh vực | Năng lực kiến trúc | Bề mặt chính |
| :--- | :--- | :--- |
| **Cách ly Zero-Trust** | Biên nhận `WriteReceipt` dùng 1 lần, process group POSIX, và Git worktree riêng biệt. | `g8s receipt issue`, `g8s worker` |
| **Hàng đợi bền vững** | Lưu trữ SQLite WAL, khóa CAS nguyên tử, chống trùng lặp idempotency, và tự động thử lại. | `g8s submit`, `g8s watch`, `g8s status` |
| **I/O Agent & ContextPack** | Tìm kiếm đa nền tảng (`search`), đọc giới hạn (`read`), sửa CAS (`edit`), trích xuất JSON (`select`), và nén ngữ cảnh AST 4 tầng + PageRank + Knapsack (`context-pack`). | `g8s search`, `g8s read`, `g8s edit`, `g8s select`, `g8s context-pack` |
| **Tiêu thụ Gateway LLM** | Thực thi CLI đồng bộ (`llm-call`) và hàng đợi worker cho OpenAI/Anthropic/9Router kèm bảng điểm chất lượng model & tự động cách ly. | `g8s llm-call`, `g8s providers discover`, `g8s providers scorecard` |
| **QA AI 6 Tầng & Blind Verify** | Chuẩn hóa BA-to-Spec, cô lập Holdout, kiểm thử đột biến Canary và kiểm toán nhận thức luận (`MECHANICAL_VERIFIED` vs `UNVERIFIED_PROSE`). | `g8s verify`, `g8s qa-report` |
| **Kho tri thức tách rời** | Đánh chỉ mục FTS5 + xếp hạng BM25 sub-millisecond trong SQLite cho quyết định kiến trúc và spatial whisper JIT. | `g8s vault store`, `g8s vault query` |

---

## 4. Cài đặt

`g8s` được phân phối dưới dạng một file binary tĩnh duy nhất (<25MB), không yêu cầu cài thêm runtime nào khác.

### Binary biên dịch sẵn (GitHub Releases)

Tải file chạy trực tiếp từ [GitHub Releases](https://github.com/tamld/g8s/releases):
- macOS (Apple Silicon `arm64` / Intel `amd64`)
- Linux (`amd64` / `arm64`)
- Windows (`amd64`)

### Cài qua Go (`go1.26.10`+)

```bash
go install github.com/tamld/g8s/cmd/g8s@latest
```

### Biên dịch từ mã nguồn

```bash
git clone https://github.com/tamld/g8s.git
cd g8s
make local
./bin/g8s doctor --json
```

---

## 5. Mô phạm sử dụng (Quickstart)

### Paradigm 1: Trinh sát chỉ đọc (Read-Only Reconnaissance)
Duyệt và thu thập thông tin codebase an toàn tuyệt đối mà không cấp quyền ghi:
```bash
# Nạp task trinh sát với quyền chỉ đọc
g8s submit \
  --role scout \
  --permission read_only \
  --prompt "Khảo sát internal/provider và liệt kê các interface xuất khẩu"

# Chạy một lượt worker để xử lý task
g8s worker --once
```

### Paradigm 2: Đột biến giới hạn quyền (Capability-Scoped Mutation)
Cấp biên nhận dùng 1 lần khóa chặt đường dẫn trước khi sửa đổi:
```bash
# 1. Cấp biên nhận ghi có thời hạn cho thư mục chỉ định
RECEIPT_ID=$(g8s receipt issue --path "internal/config/*.go" --json | g8s select --selector .data.receipt_id)

# 2. Giao task cho worker kèm mã biên nhận
g8s submit \
  --role test-runner \
  --permission workspace_write \
  --receipt-id "$RECEIPT_ID" \
  --prompt "Thêm unit test kiểm tra validate config trong internal/config"

g8s worker --once
```

### Paradigm 3: Tiêu thụ Gateway LLM hướng cấu hình (Config-Driven LLM Consumer)
Gọi trực tiếp hoặc nạp task qua hàng đợi tới các gateway LLM (OpenAI, Anthropic, 9Router):
```bash
# Gọi trực tiếp đồng bộ với cấu hình effort suy luận
g8s llm-call \
  --provider 9router \
  --model reasoning-high \
  --effort high \
  --prompt "Rà soát race condition trong internal/fsops"

# Khám phá danh sách model trực tiếp từ gateway
g8s providers discover --provider 9router --json
```

### Paradigm 4: I/O Agent thuần Go đa nền tảng
Thay thế toàn bộ công cụ shell ngoài bằng các lệnh thuần Go hiệu năng cao:
```bash
# Tìm kiếm trong kho mã nguồn (thay thế rg / grep / find)
g8s search "WriteReceipt" --globs "*.go"

# Đọc file có giới hạn dòng kèm mã băm SHA-256 (thay thế sed -n / cat)
g8s read internal/provider/registry.go --lines 20:50

# Sửa file nguyên tử compare-and-swap (thay thế sed -i / awk)
g8s edit internal/config/config.go --old "timeout = 30s" --new "timeout = 60s"

# Trích xuất giá trị JSON dot-path (thay thế jq)
cat result.json | g8s select --selector .data.status
```

---

## 6. Tích hợp MCP, Agent Skills & Offer Bundle

### Model Context Protocol (MCP) Server

`g8s` khởi chạy như một MCP server stdio chuẩn cho các IDE AI (Claude Desktop, Cursor, Windsurf, Antigravity):

```bash
g8s mcp
```

#### Danh mục 22 công cụ MCP

| Tên công cụ | Mục đích & Thao tác |
| :--- | :--- |
| `g8s_run` | Thực thi đồng bộ một worker task cô lập (yêu cầu Phase 4 supervisor). |
| `g8s_submit` | Nạp một task bền vững với role, quyền và ngân sách thời gian. |
| `g8s_get` | Tra cứu trạng thái task đã làm sạch theo `task_id`. |
| `g8s_receipt_issue` | Cấp biên nhận ghi có phạm vi đường dẫn và thời hạn dùng một lần. |
| `g8s_self_awareness` | Báo cáo danh sách provider, model khả dụng và số slot đồng thời. |
| `g8s_blast_radius` | Phân tích phạm vi ảnh hưởng LSP và AST cho file/symbol (`DELTA-07`). |
| `g8s_dispatch` | Thực thi lệnh dispatch chỉ đọc qua lớp bọc an toàn. |
| `g8s_list_tasks` | Liệt kê danh sách task theo trạng thái (`QUEUED`, `SUCCEEDED`, v.v.). |
| `g8s_cancel_task` | Yêu cầu hủy task hợp tác kèm lý do kiểm toán. |
| `g8s_list_roles` | Liệt kê toàn bộ 10 role worker chuẩn cùng quyền mặc định. |
| `g8s_list_permissions` | Liệt kê các profile quyền hạn (`read_only`, `automation_read`, `workspace_write`, `supplied_text`). |
| `g8s_search` | Tìm kiếm regex và chuỗi ký tự thuần Go tôn trọng `.gitignore` (`DELTA-22/23`). |
| `g8s_read` | Đọc giới hạn dòng file và xác thực mã băm SHA-256 an toàn (`DELTA-23`). |
| `g8s_edit` | Chỉnh sửa file nguyên tử CAS bảo toàn định dạng xuống dòng CRLF/LF (`DELTA-23`). |
| `g8s_select` | Trích xuất giá trị JSON qua đường dẫn dot-path (thay thế `jq`; `DELTA-23`). |
| `g8s_llm_call` | Thực thi gọi LLM đồng bộ trực tiếp tới gateway được khai báo (`DELTA-25`). |
| `g8s_charter_issue` | Cấp phát Stratum Charter gắn epoch & fence token (`S0/S1 -> S2`; `DELTA-33`). |
| `g8s_charter_verify` | Xác thực epoch và fence token của Stratum Charter (`DELTA-33`). |
| `g8s_portfolio_status` | Chiếu trạng thái O-Plane chỉ đọc của các Stratum Charter liên repo (`DELTA-33`). |
| `g8s_skills_list` | Liệt kê danh mục skill gắn mã băm (`skill://g8s/<tier>/<name>`) theo tầng F0/F1/F2 (`DELTA-34`). |
| `g8s_skills_read` | Đọc `SKILL.md` có kiểm tra chống giả mạo TOCTOU qua `expected_digest` SHA-256 (`DELTA-34`). |
| `g8s_context_pack` | Nén ngữ cảnh AST 4 tầng thuần Go, PageRank + BM25F và Knapsack (`DELTA-36`). |

### Agent Skills & Hiến chương Giám sát

Hiến chương chuẩn cho giám sát viên `g8s` được lưu tại [`skills/g8s-supervisor/SKILL.md`](skills/g8s-supervisor/SKILL.md) (`v4.3.0`), đăng ký trong [`skills/manifest.json`](skills/manifest.json), và tuân thủ tiêu chuẩn tài liệu [`skills/g8s-supervisor/references/doc-standard.md`](skills/g8s-supervisor/references/doc-standard.md).

### Áp dụng cho Repo khác qua Offer Bundle

Các dự án khác có thể tích hợp `g8s` thông qua Offer Bundle ([`offer/README.md`](offer/README.md)) gồm 4 hồ sơ miền ([`knowledge`](offer/profiles/knowledge.md), [`security`](offer/profiles/security.md), [`infra`](offer/profiles/infra.md), [`utility`](offer/profiles/utility.md)) và 3 cổng đồng bộ CI.

> [!NOTE]
> **Điểm khám phá cho AI Agent**: Agent tự hành vào repo này nên đọc [`manifest.json`](manifest.json) để lấy schema máy đọc được và [`AGENTS.md`](AGENTS.md) để nắm thứ tự đọc 5 bước.

---

## 7. Giới hạn phạm vi (Non-Goals)

Để duy trì tốc độ và độ tin cậy tối đa, `g8s` vạch rõ các ranh giới không làm:

| Lĩnh vực | Quyết định kiến trúc & Non-Goal |
| :--- | :--- |
| **Plugin động in-process** | **Non-Goal**: Không dùng plugin dynamic C/Go. `g8s` chọn **Cấu hình khai báo + Giao thức chuẩn** (`providers.json`, JSON-RPC MCP, tiến trình con) để loại bỏ rủi ro biên dịch runtime và thoát sandbox. |
| **Framework nặng nề** | **Non-Goal**: Không phụ thuộc vào môi trường Python cồng kềnh (LangChain, AutoGen). `g8s` là binary Go tĩnh hoàn toàn độc lập. |
| **Web UI cồng kềnh** | **Non-Goal**: Không dựng web dashboard rườm rà. `g8s` ưu tiên CLI; mọi trạng thái được theo dõi qua Evidence Lake và JSON envelope. |
| **Hosting Model Inference** | **Non-Goal**: Không nạp trọng số model trực tiếp trong tiến trình g8s. g8s điều phối tiến trình ngoài và gateway HTTP. |

---

## 8. Bản đồ tài liệu & Bảo chứng chất lượng

### Bản đồ tài liệu chính

| Hướng dẫn / Đặc tả | Trọng tâm nội dung |
| :--- | :--- |
| [Hướng dẫn Quickstart](docs/quickstart.md) | Khởi đầu từ cài đặt đến lần ủy quyền thực thi đầu tiên. |
| [Tra cứu lệnh CLI](docs/user-guide/cli-reference.md) | Sổ tay đầy đủ về mọi lệnh con, cờ tùy chọn và mã thoát. |
| [Tài liệu MCP Tools](docs/user-guide/mcp-tools.md) | Schema JSON, tham số và kiểu dữ liệu trả về của 21 MCP tool. |
| [Hướng dẫn I/O Agent](offer/SEARCH_GUIDE.md) | Tìm kiếm, cắt dòng, sửa nguyên tử và trích xuất JSON dot-path. |
| [Đặc tả Sản phẩm (PRD)](docs/PRD.md) | Vấn đề giải quyết, đối tượng hướng tới và giá trị cốt lõi. |
| [Yêu cầu Phần mềm (SRS)](docs/SRS.md) | Yêu cầu chức năng (FR-1..10) và phi chức năng (NFR-1..4). |
| [Định nghĩa Xong & Sẵn sàng (DOD_DOR)](docs/DOD_DOR.md) | Tiêu chuẩn chất lượng, quy trình xác thực đa vòng và cổng kiểm soát. |
| [Chuẩn Bảo mật OWASP Agentic, SLSA & SBOM](docs/security/owasp-agentic-slsa-sbom.md) | Ánh xạ OWASP Top 10 cho LLM/Agent (2025), SLSA v1.0 và SPDX SBOM. |
| [Sổ ghi Quyết định (ADR)](docs/decisions/) | Lịch sử các quyết định kiến trúc từ ADR-0001 đến ADR-0035. |

### 4 trụ cột cổng chất lượng & AppSec

Mọi commit đều được kiểm tra nghiêm ngặt qua 4 trụ cột AppSec và 54 khẳng định định lượng đăng ký tại [`docs/claims.yml`](docs/claims.yml):

| Trụ cột | Cơ chế bảo đảm chất lượng | Mục tiêu kiểm chứng |
| :--- | :--- | :--- |
| **1. SAST Triad** | `gosec` (trong `golangci-lint`) + `Semgrep` (`.semgrep/g8s-rules.yml`) + `CodeQL` | Không có lỗ hổng bảo mật, bắt buộc xử lý mọi lỗi (`errcheck`). |
| **2. Black-Box DAST** | Fuzzing API và thăm dò biên giới hạn (`dast_test.go`) | Kiểm tra endpoint HTTP và MCP stdio chống payload độc và thoát thư mục. |
| **3. In-Process IAST** | `RuntimeSensor` và bộ probe red-team (`sa-006`..`sa-008`) | Phát hiện vi phạm sandbox, tranh chấp lease và dùng lại biên nhận thời gian thực. |
| **4. Grey-Box Fuzzing** | Go native fuzzing (`FuzzVerifyScope`, `FuzzReadAndEdit`, `FuzzSelectPath`) | Độ bền vững của bộ phân tích trước mọi đầu vào bất thường. |

---

## 9. Lộ trình, Cấu trúc dự án & Giấy phép

### Lộ trình phát triển thực tế (Evidence-Gated)

Mọi mốc phát triển được nghiệm thu dựa trên bằng chứng kiểm thử và kiểm chứng vận hành thực tế:

| Mốc phiên bản | Phạm vi & Năng lực nghiệm thu | Trạng thái |
| :--- | :--- | :---: |
| **`v0.18.0`** (2026-10-21) | **F0 Verified Delegation & Symmetric Zero-Trust Control Plane** ([ADR-0033](docs/decisions/0033-f0-verified-delegation.md)): sổ cái bằng chứng hash-chain, mandate T0–T4, gates G0–G7, tường lửa verify không suy hao. | **Xong** |
| **`v0.19.0`** (2026-10-21) | **Universal Config-Driven LLM Call Consumer & Multi-Model Fleet** ([ADR-0034](docs/decisions/0034-config-driven-llm-consumer-9router.md), `DELTA-25`): `g8s_llm_call`, `g8s_select`, Offer Bundle `v0.19.0`, Skills `v4.3.0` (`doc-standard.md`), đơn giấy phép `MIT`, và `manifest.json` chuẩn hóa. | **Xong** |
| **`v0.20.0`** (2026-10-25, Hiện tại) | **Liên bang Phân tầng Trực giao, Thẻ điểm Chất lượng Model, Hydrate Skill Gắn Mã Băm, Nén Ngữ cảnh AST/Knapsack, Pipeline QA AI 6 Tầng & DeviceLeasePool** ([ADR-0035](docs/decisions/0035-orthogonal-stratum-federation.md), `DELTA-26..38`): động cơ charter `S0..S3` + `O-Plane`, 22 công cụ MCP (`g8s_skills_list`, `g8s_skills_read`, `g8s_context_pack`, `g8s_charter_*`), 10 role chuẩn, chẩn đoán lỗi `FailureDiagnosis` & tự động cách ly, CAS `deliver --expected-head-oid`, cổng tiền thực thi `Lane F/R/S`, `g8s context-pack`, `g8s qa-report` và `DeviceLeasePool`. | **Xong** |
| **`v0.30.0`** (Kế hoạch) | **Bộ Adapter CLI Agent Đa năng (`opencode`, `commandcode`, `codex`), Định tuyến Free-Model Phân tầng & Liên bang MCP F0/F1/F2** (`DELTA-39`, [#672](https://github.com/tamld/g8s/issues/672)): tích hợp các CLI coding agent ngoài dưới sandbox worktree/receipt của `g8s`, định tuyến free-model cho tác vụ chỉ đọc (`scout`/`researcher`), và phân quyền MCP theo biên nhận trong worktree F1/F2. | **Kế hoạch** |
| **`v0.40.0`** (Kế hoạch) | **Cổng Tiền thực thi Mật mã học & Đồng thuận Phản biện Đa tác tử**: biên nhận nghiên cứu CVE/NVD & phản biện Socratic bắt buộc trước khi cấp `WriteReceipt` cùng chứng thực SLSA/SBOM. | **Kế hoạch** |
| **`v1.0.0`** (GA Nghiệm thu Bằng chứng) | **Khóa Chuẩn Sản xuất GA**: nghiệm thu khi hoàn tất `v0.30.0` + `v0.40.0` và vượt qua **$\ge 50$ lượt thực thi thực tế có biên nhận** trên cả 4 hồ sơ miền chuẩn của Offer Bundle (`knowledge`, `security`, `infra`, `utility`) với **0 lỗi P0 lọt lưới** và **$\ge 90\%$ First-Pass Yield**. | **Kế hoạch** |

### Cấu trúc dự án

Cây cấu trúc thư mục tự động kết xuất từ filesystem trực tiếp: xem [README.md](README.md) mục *Project Structure*.

---

## 10. Giấy phép (License)

Phân phối theo **MIT License**. Copyright (c) 2026 TamLD. Xem chi tiết tại [LICENSE](LICENSE).
