# Living Domain Spec 07 — Operator CLI Surface, Canonical Flags & MCP Daemon

- **Domain ID**: `07-cli-mcp-and-daemon`
- **Collision Domains**: `mcp-daemon`, `cli-surface`
- **Feature IDs**: `FEAT-MCP-DAEMON-01`, `FEAT-PLATFORM-CLI-01`
- **Constitutional Laws**: Law 5 (Self-Describing Surface)

---

### R1 — Generative CLI Surface Contract & Canonical Flag Dictionary {#R1-cli-surface-contract}

<!-- inherited-from: DELTA-04, DELTA-05, DELTA-06, DELTA-13, DELTA-27, DELTA-34, DELTA-40 -->
<!-- feature_id: FEAT-PLATFORM-CLI-01 -->

Every top-level subcommand registered in `cmd/g8s/main.go` MUST appear in `internal/cli.CanonicalCommands()`, `printUsage()` (`g8s --help`), and `.g8s/feature-map.yml` (`interfaces.cli`). Any Markdown documentation invoking an unregistered `g8s <subcmd>` MUST fail with `E_UNBOUND_CLI_CLAIM`.

#### Scenario: PrintUsage and canonical CLI registry maintain 1-to-1 parity
<!-- tests: TestCLISurfaceContractParity -->
- **GIVEN** the `g8s` CLI entrypoint
- **WHEN** `printUsage()` is rendered
- **THEN** every public command in `cli.CanonicalCommands()` is listed in `--help`

#### Scenario: Markdown CLI invocation guard rejects unbound subcommands
<!-- tests: TestScanMarkdownCLIInvocationsDetectsUnbound -->
- **GIVEN** a Markdown document containing `./bin/g8s nonexistent-cmd --json`
- **WHEN** `ScanMarkdownCLIInvocations` scans the document
- **THEN** it returns an `E_UNBOUND_CLI_CLAIM` violation
