# T020 Postmortem — Bài học + Tiêu chuẩn + Kế thừa

**Phạm vi**: DELTA-11 Concern A + PR #100 (DEBT-22/23/24/25 cleanup)
**Ngày**: 2026-08-29
**Tác giả**: Sisyphus (orchestrator) + agy worker (executor)
**Trạng thái**: Merged (PR #100, commit `911297c` trên main)

---

## 1. Bài học rút ra

### 1.1 Spec-first ngăn deadlock từ CI với pre-existing debt
Mọi gate có `TODO(#XX): re-enable` phải có issue link ngay từ đầu.
Nếu không, code chạy được nhưng CI vẫn đỏ vĩnh viễn — không ai nhớ tại sao.

### 1.2 Coverage gate có nhiều bug đồng thời
- Lần 1: `head -1` chỉ lấy package đầu tiên (cmd/g8s ~9%).
- Lần 2: aggregate bị thiếu test (75%).
- Lần 3: refactor xóa test → aggregate giảm lại (78.75%).
- Bài học: coverage gate tự drift qua mỗi lần refactor. Cần test mới mỗi khi gỡ code.

### 1.3 Dispatch agent ≠ supervise
- Lần 1: agy stuck ở cwd sai, mình không biết → phải kill, redispatch.
- Lần 2: prompt qua stdin bị CLI nuốt → phải inline.
- Lần 3: agy xong 1 phần, mình ngắt, mất dữ liệu.
- Bài học: cần polling loop, log tail, deadline, kill+restart pattern.

### 1.4 Phân chia việc rõ ràng giữa Sisyphus và agy
- Em (Sisyphus): viết brief, setup test infra, review output, merge.
- Agy: xóa findings, refactor files, viết test mới.
- Bài học: scope rõ + DoD rõ + agent khỏe = tự ship được.

### 1.5 Linter `@latest` là bẫy
- `honnef.co/go/tools@latest` yêu cầu Go 1.26, runner 1.25.
- `DominicKramer/go-linter` bị archive.
- Bài học: pin version mọi linter, có policy bump + test trước khi apply.

---

## 2. Tiêu chuẩn (đã chốt ở main `911297c`)

### 2.1 Lint standard
| Gate | Lệnh | Pin |
|---|---|---|
| Format | `gofmt -l` | n/a |
| Strict format | `gofumpt -l` | n/a |
| Vet | `go vet ./...` | n/a |
| Static | `staticcheck ./...` | `v0.7.0` |
| Errors | `errcheck ./...` | latest, dùng `.errcheck_excludes` |
| Security | `gosec -severity=medium -confidence=medium` | latest |

### 2.2 Test standard
- Aggregate coverage ≥ 80% (loại trừ `cmd/g8s`).
- Mỗi package ≥ 60% (cảnh báo < 60%).
- `CGO_ENABLED=0 go test ./...` xanh.
- `CGO_ENABLED=1 go test -race ./...` xanh.
- Test dùng deterministic clock, không `time.Sleep`.
- Windows file lock: store phải `t.Cleanup(func() { _ = store.Close() })`.

### 2.3 Commit standard
- Conventional: `feat|fix|refactor|test|ci|docs(scope): subject`.
- Một commit = một concern (DEBT-22 riêng, DEBT-23 riêng).
- Body list file touched + DoD checklist.
- Không squash rebase trên shared branch.

### 2.4 PR standard
- Title: `[type](scope): summary` + 1-2 dòng mô tả.
- DoD checklist paste nguyên từ issue.
- Số file, +/- lines, local test result.
- `[BREAKING]` nếu touch schema/CLI.

### 2.5 Workflow standard
- Linter versions **pinned** (`@vX.Y.Z`, không `@latest`).
- Go version matrix: 1.25 stable + 1.26 latest.
- Mỗi gate có `set -e` + `if [ -n "$x" ]; then echo ::error::; exit 1; fi`.
- Comment dòng đầu nói rõ gate check gì, exclude gì, vì sao.

---

## 3. Kế thừa về sau này

### 3.1 Template copy-paste
- `.github/workflows/quality.yml` (main `911297c`) → mẫu cho project Go thuần.
- `notes/T020-concern-a-supervisor.md` → mẫu receipt cho mỗi concern.
- `/tmp/agy-brief.md` pattern → mẫu agy brief.

### 3.2 Issue body template (DEBT-XX)
```markdown
## Summary
[Gate] currently disabled in CI because [reason]. [N] pre-existing findings.

## Acceptance criteria
- [ ] Re-enable [gate] in `.github/workflows/quality.yml`
- [ ] Fix [N] findings (file:line per finding)
- [ ] Verify `go test ./...` passes
- [ ] Update aggregate coverage if needed

## Blocks
- [v0.X.0 release / delta-XX]
```

### 3.3 Agy brief template
```
1. Repository (path, branch, head SHA)
2. Context (1 paragraph)
3. Required output (1-3 deliverables)
4. Constraints (NO-list: đừng đụng X, không thêm deps)
5. DoD checklist (copy-paste vào PR body)
6. Out of scope (rõ ràng)
7. Report back (format output mong đợi)
```

### 3.4 Quy trình thường trực
- Mỗi thứ Sáu: `gh run list --limit 50 --json conclusion,name` xem có gate tuột xanh không.
- Mỗi khi bump Go version: `go mod tidy` + re-run Quality + bump linter pin tương thích.
- Mỗi khi mở DELTA-XX: viết DoD + issue liên kết + update `docs/REFACTORING_PLAN.md`.

### 3.5 ADR mới cần viết (T021 prep)
- **ADR-XXX**: Use `gofumpt -l` + `gofumpt -w` (broader than gofmt).
- **ADR-XXX**: Pin linter versions in CI (`@vX.Y.Z`, not `@latest`).
- **ADR-XXX**: Exclude `cmd/g8s` from aggregate coverage (CLI binary).
- **ADR-XXX**: Use `.errcheck_excludes` for `defer _ = X.Close()` pattern, track under DEBT-XX.

---

## 4. CI Status trước/sau

| | Trước (5fc40ac) | Sau (911297c) |
|---|---|---|
| CI (3 OS) | ✅ | ✅ |
| Quality (17 gates) | ❌ (4 disabled + coverage fail) | ✅ |
| Dist Validation | ✅ | ✅ |
| Aggregate coverage | 75% → 78.75% (trong PR) | 82.91% |
| Open DEBT issues | 4 | 0 |

---

## 5. Related

- Issues đã đóng: #91 (Concern A), #94 (DEBT-22), #95 (DEBT-23), #96 (DEBT-24), #97 (DEBT-25)
- PR: #100 (https://github.com/tamld/g8s/pull/100)
- Spec: `spec/openspec/11-orchestration-roadmap-spec.md`
- Receipt Concern A: `notes/T020-concern-a-supervisor.md`
