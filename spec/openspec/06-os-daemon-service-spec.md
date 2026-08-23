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
