# Service Management (macOS)

The v0.1.x service manager installs g8s as a hardened macOS launchd user agent
(LaunchAgent). Linux systemd and Windows service backends are deferred to a
later milestone; attempting them fails closed with a clear error.

## Commands

```sh
g8s service install    # validate, write plist, bootstrap + kickstart
g8s service start      # kickstart without reinstall
g8s service stop       # bootout
g8s service restart    # bootout -> bootstrap -> kickstart
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
- Symlinked plist or log targets fail closed; victims stay untouched.
- Failed bootstrap restores the previous plist bytes exactly.
- Uninstall preserves the SQLite state directory.

## Requirements

- macOS only for v0.1.x (launchd user domain, no root required).
- The control-plane database path must be set or default-resolvable.
