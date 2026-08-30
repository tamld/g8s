# Windows Installation & Execution Guide

This document details installation paths, multi-profile isolation, and performance baselines for `g8s` on Microsoft Windows.

---

## 1. Installation Scopes & Canonical Paths

`g8s` supports two installation and execution scopes:

| Dimension | User Scope (Default, No Admin) | System Scope (Admin Required) |
| :--- | :--- | :--- |
| **Data Directory** | `%LOCALAPPDATA%\Programs\g8s` | `%PROGRAMFILES%\g8s` |
| **Config Directory** | `%APPDATA%\g8s` | `%PROGRAMDATA%\g8s` |
| **Cache Directory** | `%LOCALAPPDATA%\Programs\g8s\cache` | `%PROGRAMFILES%\g8s\cache` |
| **Logs Directory** | `%LOCALAPPDATA%\Programs\g8s\logs` | `%PROGRAMFILES%\g8s\logs` |
| **Database File** | `%LOCALAPPDATA%\Programs\g8s\g8s.db` | `%PROGRAMFILES%\g8s\g8s.db` |

Operators can override the database path directly via the `G8S_DB` environment variable or select scope via `--scope user|system`.

---

## 2. Multi-Profile Isolation

In multi-user Windows environments:
- Each user profile (`C:\Users\<Username>`) maintains dedicated and isolated `%LOCALAPPDATA%` and `%APPDATA%` directories.
- `g8s doctor --detect-paths` enumerates all g8s profiles on the system for auditing.
- `g8s cleanup --user-profile <username>` purges artifacts and ghost resources belonging only to the specified user.

---

## 3. Migration from Legacy CWD-Relative Paths (v0.3.0 -> v0.4.0+)

To migrate legacy cwd-relative database and heartbeat files (`./g8s.db`, `./.g8s/`, `./.heartbeat/`):

```powershell
# Simulate migration (dry-run)
g8s migrate --from .\ --dry-run

# Execute migration to canonical user data directory
g8s migrate --from .\
```

---

## 4. Performance Baselines

- **Install Time**: < 5 seconds
- **Uninstall Time**: < 3 seconds
- **First-Run Latency**: < 200ms
- **Binary Size**: ~11MB static binary (Zero-CGO)

---

## 5. Verification & Diagnostics

```powershell
# Verify installation and PATH registration
g8s version

# Run full diagnostic suite
g8s doctor

# Inspect canonical data directory
g8s config get data_dir
```
