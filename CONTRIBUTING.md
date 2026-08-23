# Contributing to g8s

Thank you for your interest in contributing to **g8s (The Gatekeepers)**!  
`g8s` is an open-source, zero-trust process execution and capability delegation runtime for AI CLI workers, built under the **Spec-Driven Development (SDD)** framework.

---

## 1. Development Principles

1. **Spec-First**: Never write code without an approved OpenSpec delta in `spec/openspec/`.
2. **Pure-Go & Zero-CGO**: All code must compile cleanly with `CGO_ENABLED=0`. Never introduce C-library bindings.
3. **Deterministic Testing**: Every feature must be backed by table-driven unit tests, mockable clocks for time-sensitive logic, and pass `go test -race ./...`.
4. **100% English**: All documentation, code comments, docstrings, commit messages, and PRs must be written in clear, professional English.

---

## 2. Standard Development Workflow (SDD Cycle)

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  1. PROPOSE  │ ──► │ 2. TEST (TDD)│ ──► │  3. CODE     │ ──► │  4. VERIFY   │
│ OpenSpec     │     │ Write unit   │     │ Implement    │     │ go test -race│
│ Delta spec   │     │ test cases   │     │ Go logic     │     │ golangci-lint│
└──────────────┘     └──────────────┘     └──────────────┘     └──────────────┘
```

1. **Step 1: Check OpenSpec**:
   - Check [`docs/REFACTORING_PLAN.md`](docs/REFACTORING_PLAN.md) and [`spec/openspec/`](spec/openspec/) to locate the target spec delta.
   - If introducing a new feature, create `spec/openspec/XX-feature-name.md` with status `PROPOSED`.
2. **Step 2: Create a Feature Branch**:
   ```bash
   git checkout -b feat/your-feature-name
   # or
   git checkout -b fix/your-bug-fix
   ```
3. **Step 3: Write Tests First (TDD)**:
   - Implement tests in `internal/<pkg>/<pkg>_test.go`.
4. **Step 4: Implement Pure-Go Logic**:
   - Ensure all SQLite operations use `modernc.org/sqlite`.
5. **Step 5: Run Verification**:
   ```bash
   # Format code
   gofmt -s -w .

   # Run tests with race detector
   CGO_ENABLED=0 go test -v -race ./...
   ```
6. **Step 6: Submit Pull Request**:
   - Commit using Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
   - Open a Pull Request referencing the corresponding OpenSpec delta.
