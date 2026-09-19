# Anti-Patterns (Long-term Prevention)

## CI/CD Anti-Patterns

### 1. Binary Files in Git Repository
**Problem**: Committing compiled binaries (e.g., `g8s`) causes gofmt failures on CI since gofmt tries to format binary files.

**Solution**: 
- Add binary names to `.gitignore`
- Build binaries in CI, don't commit them
- Use `git rm --cached <binary>` to remove from tracking if already committed

### 2. Go Formatting Issues on New Test Files
**Problem**: New test files (e.g., `test_windows.go`) not formatted with gofmt before commit.

**Solution**:
- Run `gofmt -w <file>` on new files before commit
- Pre-push hook catches this locally
- CI enforces gofmt on all platforms

### 3. Cross-Layer Changes in Single PR
**Problem**: PR touches both `internal/orchestrator/` (supervisor layer) and `internal/worker/` (worker layer) - violates DEBT-34.

**Solution**:
- Separate cross-layer features into dedicated PRs
- Use `tools/ci_layer_check.sh` to validate locally
- Each layer should have single ownership

## Verification Commands

```bash
# Check gofmt on all files
gofmt -l .

# Fix gofmt issues
gofmt -w <file>

# Check layer ownership
./tools/ci_layer_check.sh

# Full pre-push verification
./tools/pre_push.sh --fast
```

## CI Gates That Prevent These Issues

| Gate | Purpose |
|------|---------|
| Check formatting (gofmt) | Catches unformatted files |
| Layer Ownership (DEBT-34) | Prevents cross-layer PRs |
| AI Anti-Pattern (DEBT-21) | Catches AI-generated code smells |
| Brief Anti-Pattern (DEBT-51) | Validates brief structure |
| Doc-Code Contract (#208) | Keeps docs in sync with code |
