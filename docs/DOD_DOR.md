# Definition of Done (DoD) & Definition of Ready (DoR)

## 1. Definition of Done (DoD)

A user story, module, or release is marked as **Done** only when:
1. **Unit & Integration Test Coverage**: Core business logic (harness, receipt validation, control plane state machine, process tree killer) achieves $\ge 90\%$ test coverage with 0 race conditions (`go test -race ./...`).
2. **Cross-Platform Compilation**: Clean compilation for Darwin, Linux, and Windows with `CGO_ENABLED=0`.
3. **Linting & Code Quality**: Passes `golangci-lint` and `gofmt` with zero errors or warnings.
4. **Security Verification**: Zero secrets in Git history (`gitleaks`), path normalization handles symlinks and `..` traversal attacks.
5. **Documentation**: All CLI subcommands have clear `--help` descriptions, and architecture ADRs are up to date.
6. **Artifact Deliverables**: Binary packaging verified via GoReleaser (dry-run).

---

## 2. Definition of Ready (DoR)

A task or feature is **Ready** to enter development only when:
1. **Explicit Scope & Boundary**: Input parameters, target files, and execution role/permissions are unambiguously defined.
2. **Failure Mode Analysis**: Potential error cases (timeout, corrupt DB, process kill, invalid receipt, denied paths) are specified with expected error codes.
3. **Schema & Contract Alignment**: Any modifications to task schemas, receipt formats, or database tables have corresponding migration logic and schema version bumps.
4. **Test Plan Defined**: Happy path, edge cases (Unicode, SQL injection, race conditions), and abuse scenarios are outlined before implementation begins.
