# Quick Start Guide

> From zero to delegated agent tasks in 60 seconds.

## Prerequisites

- macOS, Linux, or Windows (amd64 or arm64)
- No runtime dependencies: g8s is Zero-CGO, pure Go, statically linked

## Install

### Homebrew (macOS and Linux)

```sh
brew tap tamld/homebrew-tap
brew install g8s
```

### Download a release archive

Grab the matching archive from the [releases page](https://github.com/tamld/g8s/releases):

| OS | Archive |
| --- | --- |
| macOS Apple Silicon | `g8s_<ver>_darwin_arm64.tar.gz` |
| macOS Intel | `g8s_<ver>_darwin_amd64.tar.gz` |
| Linux arm64 | `g8s_<ver>_linux_arm64.tar.gz` |
| Linux amd64 | `g8s_<ver>_linux_amd64.tar.gz` |
| Windows amd64 | `g8s_<ver>_windows_amd64.zip` |

```sh
tar -xzf g8s_0.3.0_darwin_arm64.tar.gz
./g8s version
```

### From source

```sh
go install github.com/tamld/g8s/cmd/g8s@latest
```

## First cycle

```sh
# Verify the installation.
./g8s version

# Inspect role and permission profiles.
./g8s roles
./g8s permissions

# Submit a task for a worker to claim.
./g8s submit \
  --idempotency-key demo-1 \
  --payload '{"prompt": "inventory the module", "timeout": "30s"}' \
  --model gemini-3.7-flash-high \
  --role collector \
  --permission read_only \
  --add-dir . \
  --timeout 30s

# Read it back.
./g8s get <task-id>

# Issue a single-use delegated write receipt (5 minute TTL).
./g8s receipt-issue -issuer me -path './src/*' -ttl 300
```

## Next steps

- [User guide](user-guide/cli-reference.md) — every subcommand, flag, and exit code.
- [Receipt delegation workflow](user-guide/receipt-workflow.md) — how write receipts gate mutations.
- [MCP integration](user-guide/mcp-tools.md) — connect Claude Desktop, Cursor, or any stdio client.
