# Release Standard Operating Procedure (SOP)

This document defines the strict, repeatable step-by-step procedure for preparing, validating, and publishing a new release of **g8s**.

---

## 1. Pre-Release Verification Checklist (The 6-Gate Audit)

Before cutting any release tag (`vX.Y.Z`), the release manager (Human or Agent) must verify all 6 gates:

- [ ] **Gate 1 (Clean Worktree)**: `git status` reports working tree clean, no untracked files.
- [ ] **Gate 2 (Test & Race Detector)**: `CGO_ENABLED=0 go test -v -race ./...` passes 100% on all packages.
- [ ] **Gate 3 (Multi-OS CI)**: GitHub Actions CI workflow on `main` is completely GREEN across macOS, Linux, and Windows.
- [ ] **Gate 4 (Secret Hygiene)**: Run `gitleaks detect` or `trufflehog` to guarantee 0 secrets in Git history.
- [ ] **Gate 5 (Spec Parity)**: All OpenSpec deltas targeted for this release in `spec/openspec/` are marked `APPLIED`.
- [ ] **Gate 6 (Docs & Version Bump)**: Version string in `cmd/g8s/main.go` and `README.md` is updated.

---

## 2. Release Execution Procedure

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ 1. BUMP VERSION │ ──► │ 2. RUN TESTS    │ ──► │ 3. GIT TAG      │ ──► │ 4. GORELEASER   │
│ Update main.go  │     │ Verify -race    │     │ git tag vX.Y.Z  │     │ Publish Binaries│
└─────────────────┘     └─────────────────┘     └─────────────────┘     └─────────────────┘
```

### Step 1 — Bump Version String
Edit `cmd/g8s/main.go`:
```go
const Version = "0.1.0"
```

### Step 2 — Commit Version Bump
```bash
git commit -am "chore(release): bump version to v0.1.0"
git push origin main
```

### Step 3 — Create and Push Annotated Git Tag
```bash
git tag -a v0.1.0 -m "Release v0.1.0: Pure-Go SQLite WAL Control Plane & Capability Receipts"
git push origin v0.1.0
```

### Step 4 — Automated GitHub Release via GoReleaser
GitHub Actions automatically detects the tag push and executes GoReleaser to:
1. Compile static binaries for:
   * `darwin/arm64` (Apple Silicon)
   * `darwin/amd64` (Intel Mac)
   * `linux/amd64` (Linux x86_64)
   * `linux/arm64` (Linux ARM64 / Raspberry Pi / Ampere)
   * `windows/amd64` (Windows x64)
2. Generate SHA-256 checksums (`checksums.txt`).
3. Generate automated Changelog based on Conventional Commits.
4. Update Homebrew Tap recipe (`tamld/homebrew-tap/g8s.rb`).

---

## 3. Post-Release Smoke Test

Run a 60-second end-to-end smoke test on a clean machine:

```bash
# 1. Download & Verify Binary
./g8s version

# 2. Check Roles & Permissions
./g8s roles
./g8s permissions

# 3. Issue & Verify Receipt
./g8s receipt issue --issuer test --allow "./src/*" --ttl 300
```
