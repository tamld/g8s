# Service Management

The service manager installs g8s as a background daemon with a
platform-specific backend, selected automatically at runtime:

| Platform | Backend | Mechanism |
|----------|---------|-----------|
| macOS | `launchd` | User LaunchAgent (no root required) |
| Linux | `systemd` | User system unit |
| Windows | `sc.exe` | Windows service with failure-recovery restart actions |

## Commands

```sh
g8s service install    # validate, write unit/plist, register + start
g8s service start      # start without reinstall
g8s service stop       # stop the daemon
g8s service status     # loaded flag + database presence
g8s service uninstall  # remove unit, PRESERVE database state
```

## Hardening guarantees

- Unit file mode `0644`, stdout/stderr logs mode `0600`, launchd umask `0077`.
- The pinned binary is resolved to its canonical real path and rejected if
  group/world-writable.
- `AGY_BIN` environment pinning; `PATH` scrubbed of `~/.local/bin`.
- Encoded plist is scanned and must never contain API/TOKEN substrings.
- Install refuses while tasks are LEASED/RUNNING and spans a maintenance gate
  so claims stay blocked until install completes.
- Symlinked unit/log targets fail closed; victims stay untouched.
- Failed bootstrap restores the previous unit bytes exactly.
- Windows services register failure recovery (`sc failure` restart actions).
- Uninstall preserves the SQLite state directory on every platform.

## Requirements

- A supported platform: macOS, Linux, or Windows (other platforms fail
  closed with a clear error).
- The control-plane database path must be set or default-resolvable.
