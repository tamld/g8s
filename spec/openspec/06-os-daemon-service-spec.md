# OpenSpec DELTA-06: Cross-Platform Background Daemon Service Manager

**Status**: `PROPOSED`  
**Milestone**: M3 (OS Daemon & Packaging)  
**Package**: `internal/service`  

---

## 1. Goal & Context
Provide seamless, native 24/7 background worker queue processing across macOS (`launchd` user LaunchAgent), Linux (`systemd` user unit), and Windows (`windows service`) without requiring root/sudo privileges.

---

## 2. Supported Commands

* `g8s service install`: Installs the service unit definition into the user's OS service registry.
* `g8s service start`: Starts the background queue runner daemon.
* `g8s service stop`: Gracefully stops the worker runner.
* `g8s service status`: Inspects daemon state, CPU/RAM utilization, and worker queue health.
* `g8s service uninstall`: Removes the service unit (preserves SQLite data and state).

---

## 3. Go Interface Definition

```go
package service

type ServiceManager interface {
    Install() error
    Start() error
    Stop() error
    Status() (*ServiceStatus, error)
    Uninstall() error
}
```

---

## Amendment A (T017 port — hardened service manager)

**Status**: `ACCEPTED` (supersedes the thin skeleton above where they conflict)
**Baseline**: `reference/python/scripts/agy_service.py` (16-test suite)

### A.1 Platform support decision (explicit)

MVP targets **macOS launchd user LaunchAgents only** (`bootstrap gui/<uid>` domain,
no root/sudo). Linux systemd and Windows service backends are **deferred post-MVP**;
`NewManager` fails closed with an explicit unsupported-platform error on any other
`runtime.GOOS`. The `kardianos/service` dependency is deferred post-MVP per the T012
CLI decision; this slice is stdlib-only behind an injectable `Runner` seam so tests
never invoke a real init system.

### A.2 ADDED Requirements

- **Unit hardening**: the generated LaunchAgent plist pins `KeepAlive=true`, no
  `RunAtLoad`, `ProcessType=Background`, `Umask=0o077`; `ProgramArguments[:2]` are
  absolute paths; `EnvironmentVariables.AGY_BIN` is the pinned canonical binary;
  the sanitized `PATH` excludes `~/.local/bin`; the encoded plist never embeds
  secret material (`API`/`TOKEN` substrings forbidden).
- **Binary validation**: symlinked binaries resolve to their canonical path before
  pinning; a group/world-writable binary is rejected (`group/world-writable`).
- **Install sequence**: plist written `0644`, stdout log prepared `0600`;
  commands run in order `launchctl bootstrap gui/<uid> <plist>` then
  `launchctl kickstart -p gui/<uid>/<label>`; result reports `loaded=true`,
  `operation=install`. Reinstall over a loaded service issues
  `bootout gui/<uid>/<label>` BEFORE `bootstrap`.
- **Rollback**: when `bootstrap` exits non-zero, the previous plist bytes are
  restored exactly and the error names the failure (`bootstrap failed`).
- **Lifecycle gate**: install/restart refuse while any task is LEASED or RUNNING
  (`leased or running`) without issuing a single runner command.
- **Maintenance gate**: `BeginMaintenance` spans the whole install; claim attempts
  made inside runner callbacks return no task, and claims succeed again after
  install completes.
- **Fail-closed path safety**: a symlinked stdout log aborts preparation
  (`cannot prepare private service log`) leaving the victim file untouched; a
  symlinked LaunchAgent plist aborts install (`symlinked LaunchAgent plist`)
  leaving the victim untouched.
- **Timeout semantics**: runner command timeouts fail closed (`timed out`).
- **Uninstall preserves state**: `bootout` issued, plist removed, SQLite database
  preserved (`state_preserved=true`).
- **Status safety**: status never creates the database file; a corrupt database
  surfaces `cannot inspect control-plane state`; status output never contains a
  `prompt` member.
- **Restart guard**: restart without a loaded install fails (`not installed and loaded`).
