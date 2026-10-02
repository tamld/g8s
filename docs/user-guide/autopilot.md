# Autopilot & Periodic Maintenance

The `g8s autopilot` subsystem provides automated, periodic housekeeping and diagnostics for `g8s` installations.

## The Staged Path Summary (#485)

Autopilot development follows a strict 4-stage hardening roadmap to prevent rogue process execution, runaway submissions, and state divergence:

- **Stage 1 — Honesty Semantics & In-Process Loop (Merged)**:
  Subcommands (`start`, `stop`, `status`, `trigger`, `config`) operate transparently without pretending to be a background system daemon. `start` runs strictly in-process guarded by a state-dir lockfile (`autopilot.lock`).
- **Stage 2 — Submit Throttling & Admission Control (Merged)**:
  Protects downstream workers from burst floods using rate limits and admission control (`submit_rate_limit_per_hour`).
- **Stage 3 — Stateless Tick & Native Schedulers (Current)**:
  Decouples periodic execution from long-running in-process daemons. Introduces `g8s autopilot tick` alongside native OS scheduler installers (`install-schedule` and `uninstall-schedule`).
- **Stage 4 — Budgeted Retry & Task Resubmission (NOT Implemented Yet)**:
  Automated retry of transient task failures under explicit attempt budgets. This stage is intentionally deferred and is not active in this release.

---

## Architectural Model: "Ticks are Hints; the Queue is the Memory"

Autopilot does not rely on persistent in-memory queues or long-running worker loops:

- **Ticks are Hints**: A tick signals that time has elapsed and safe housekeeping jobs should run. A missed tick (such as when a laptop sleeps) does not corrupt state; the subsequent tick simply catches up.
- **The Queue is the Memory**: Durable state, active worker leases, and task claims live exclusively in SQLite and the `.heartbeat` directory.
- **Stateless & Ephemeral**: `g8s autopilot tick` executes once, evaluates its configured maintenance jobs, flushes atomic updates, and immediately terminates.

---

## The `g8s autopilot tick` Command

`g8s autopilot tick` executes one cycle of idempotent background maintenance tasks.

```sh
# Run all default jobs (doctor, retention, hygiene)
g8s autopilot tick

# Run a specific job subset
g8s autopilot tick --jobs retention
g8s autopilot tick --jobs doctor,hygiene
```

### Default Jobs

1. **`doctor`**:
   - Executes non-mutating environmental diagnostic checks via `internal/doctor`.
   - Summarizes overall health status (`HEALTHY`, `DEGRADED`, or `UNHEALTHY`), platform details, and individual check results.
2. **`retention`**:
   - Sweeps and purges stale execution evidence directories under `evidence/`.
   - Respects the persistent configuration key `evidence_retention_days` (default is unlimited/0).
   - Only deletes directories exceeding the age threshold; never alters live or unexpired task evidence.
3. **`hygiene`**:
   - Runs state-dir scoped sweeps via `internal/cleanup` post-#468.
   - Cleans ghost worker processes matching project identity.
   - Prunes unregistered or abandoned worktree directories under `<state_dir>/worktrees`.
   - Reaps zombie or dead supervisor sessions from the SQLite database.
   - Tolerates empty registries and fresh states cleanly.

### Envelope Output

Each executed job emits a structured JSON envelope to standard output:

```json
{
  "v": 1,
  "kind": "autopilot_tick",
  "cmd": "autopilot",
  "sub": "tick",
  "data": {
    "job": "doctor",
    "status": "ok",
    "detail": {
      "overall_status": "HEALTHY",
      "checks_count": 8,
      "summary": "overall: HEALTHY (8 checks evaluated)"
    }
  },
  "at": "2026-10-02T01:45:00Z"
}
```

> [!NOTE]
> `g8s autopilot tick` is strictly a maintenance mechanism. It **never** automatically creates, submits, or resubmits tasks into the queue.

---

## OS-Native Schedule Installation

Rather than maintaining a custom background daemon process, `g8s` delegates periodic invocation directly to the host operating system's native scheduler.

### `install-schedule`

Installs a native scheduler unit to invoke `g8s autopilot tick` periodically:

```sh
g8s autopilot install-schedule --every 10m
g8s autopilot install-schedule --every 1h
```

Flags:
- `--every <duration>`: Minimum interval `1m` (e.g. `5m`, `15m`, `1h`).
- `--generate-only` / `--dry-run`: Emits the generated schedule definition to stdout without altering system configuration.

### Platform Backends

| Platform | Native Mechanism | Target Location / Spec |
|---|---|---|
| **Linux** | `crontab` | User crontab entry tagged `# g8s-autopilot` |
| **macOS** | `launchd` | `~/Library/LaunchAgents/g8s.autopilot.plist` |
| **Windows** | `schtasks.exe` | Scheduled Task `g8s-autopilot` |

#### Linux (`crontab`)
Appends or updates an entry via `crontab -l` and `crontab -`. The entry uses a unique comment marker for safe, idempotent updates:
```cron
*/10 * * * * /usr/local/bin/g8s autopilot tick # g8s-autopilot
```

#### macOS (`launchd`)
Writes an unprivileged user LaunchAgent plist to `~/Library/LaunchAgents/g8s.autopilot.plist` with `StartInterval` defined in seconds:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>g8s.autopilot</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/g8s</string>
    <string>autopilot</string>
    <string>tick</string>
  </array>
  <key>StartInterval</key>
  <integer>600</integer>
  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
```
The schedule is then registered with `launchctl load`.

#### Windows (`schtasks.exe`)
Registers a recurring task under the current user context via:
```cmd
schtasks /Create /F /SC MINUTE /MO 10 /TN g8s-autopilot /TR "C:\Program Files\g8s\g8s.exe autopilot tick"
```

### Safety & Invariants

- **Binary Resolution Guard**: `install-schedule` resolves the executing `g8s` binary via `os.Executable()`. It refuses to install if the path cannot be resolved to a real binary on disk.
- **Idempotency**: Running `install-schedule` multiple times replaces the existing scheduled unit without creating duplicate entries or leaking stale configurations.

### `uninstall-schedule`

Removes the OS-native schedule unit and cleans up associated artifacts:

```sh
g8s autopilot uninstall-schedule
```

- **Linux**: Removes the marked line from the user's crontab.
- **macOS**: Unloads the job via `launchctl unload` and deletes `~/Library/LaunchAgents/g8s.autopilot.plist`.
- **Windows**: Deletes the scheduled task via `schtasks /Delete /F /TN g8s-autopilot`.
- Idempotent: Can be run safely even if no schedule was installed.
