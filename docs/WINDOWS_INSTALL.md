# Windows Installation Guide for g8s (The Gatekeepers)

This guide walks you through installing, configuring, verifying, and troubleshooting `g8s` on Windows systems.

---

## 1. System Requirements

* **Operating System**: Windows 10, Windows 11, or Windows Server 2016+ (64-bit).
* **Architecture**: `x86_64` (`amd64`).
* **Privileges**: Administrator privileges for standard installation to `C:\Program Files\g8s` (portable ZIP requires no admin rights).
* **Dependencies**: Zero runtime dependencies. `g8s` is compiled as a 100% static, Zero-CGO pure-Go binary with embedded SQLite WAL.

---

## 2. Download Options

Official releases are hosted on GitHub at [github.com/tamld/g8s/releases](https://github.com/tamld/g8s/releases). Choose the installer format that fits your deployment workflow:

| Package Format | Filename Pattern | Best For | Features |
| :--- | :--- | :--- | :--- |
| **NSIS Wizard (`.exe`)** | `g8s_<version>_windows_amd64_installer.exe` | Individual Developers & Power Users | GUI wizard, Desktop/Start Menu shortcuts, Auto-PATH registration, Add/Remove Programs entry. |
| **Enterprise MSI (`.msi`)** | `g8s_<version>_windows_amd64.msi` | Corporate IT, Intune, GPO, SCCM | Standard Windows Installer, silent unattended deployments, clean rollback management. |
| **Portable ZIP (`.zip`)** | `g8s_<version>_windows_amd64.zip` | Non-admin environments & CI runners | Standalone executable, extract anywhere, no registry modifications. |

---

## 3. Installation Walkthrough

### Option A: Interactive NSIS Setup Wizard (Recommended)

1. Download `g8s_0.4.0_windows_amd64_installer.exe`.
2. Double-click the installer (accept the UAC administrator prompt).
3. Select your target installation directory (defaults to `C:\Program Files\g8s`).
4. Click **Install**. The installer will:
   * Copy the static `g8s.exe` binary and license documentation.
   * Register `C:\Program Files\g8s` into the Windows System `PATH`.
   * Create a Start Menu group under `g8s (The Gatekeepers)`.
   * Register `g8s` in Windows **Add or Remove Programs**.
   * Create an uninstaller executable (`Uninstall.exe`).
5. Click **Finish**.

### Option B: Silent & Headless NSIS Installation

For automated provisioning or scripted developer setups:

```cmd
:: Run silent installation in default directory (C:\Program Files\g8s)
g8s_0.4.0_windows_amd64_installer.exe /S

:: Run silent installation in a custom directory
g8s_0.4.0_windows_amd64_installer.exe /S /D=D:\Tools\g8s
```

> **Note**: The `/D=` parameter must be the last argument and should not contain surrounding quotes.

### Option C: Enterprise MSI Installation (WiX)

For domain administrators deploying via Microsoft Endpoint Configuration Manager (SCCM), Intune, or PowerShell:

```powershell
# Interactive MSI install
msiexec /i g8s_0.4.0_windows_amd64.msi

# Silent enterprise rollout (no UI, write verbose install log)
msiexec /i g8s_0.4.0_windows_amd64.msi /qn /l*v "C:\Temp\g8s_install.log"

# Silent install with custom destination directory
msiexec /i g8s_0.4.0_windows_amd64.msi INSTALLFOLDER="D:\Software\g8s" /qn
```

### Option D: Portable Standalone ZIP

1. Download `g8s_0.4.0_windows_amd64.zip`.
2. Extract the archive to any folder (e.g., `C:\Users\<username>\bin\g8s`).
3. Add the extraction directory to your user `PATH` environment variable.

---

## 4. Verifying Installation

Open a **new** Command Prompt (`cmd.exe`) or PowerShell window and verify the CLI:

```powershell
# 1. Verify binary execution and version output
g8s --version
```
Expected output:
```text
g8s version 0.4.0 (windows/amd64) pure-go
```

```powershell
# 2. Run diagnostic health checks
g8s doctor
```
Expected diagnostic table on Windows:
```text
g8s Doctor Diagnostics
Platform: windows/amd64 | Runtime: go1.25.0 | Zero-CGO: true | Status: HEALTHY

┌─────────────────────────┬────────┬────────────────────────────────────────────────────────────┬────────────────────────┐
│ Check                   │ Status │ Message                                                    │ Details                │
├─────────────────────────┼────────┼────────────────────────────────────────────────────────────┼────────────────────────┤
│ Database State          │ OK     │ Database does not exist yet (will initialize on submit)    │ ...\g8s.db             │
│ Workspace Integrity     │ OK     │ Current workspace path valid and safe                      │ C:\Users\User\projects │
│ Windows Install Source  │ OK     │ Install source: MSI/NSIS (registry HKLM\...\Uninstall\g8s) │ msi-or-nsis            │
│ Windows Install Path    │ OK     │ Install path: C:\Program Files\g8s                         │ C:\Program Files\g8s   │
│ Windows PATH State      │ OK     │ ✓ INSTDIR is on system PATH                                │ C:\Program Files\g8s   │
│ Security Harness        │ OK     │ 6 roles, 3 permissions active and validated                │                        │
└─────────────────────────┴────────┴────────────────────────────────────────────────────────────┴────────────────────────┘
```

---

## 5. PATH Troubleshooting

If typing `g8s` gives `'g8s' is not recognized as an internal or external command`:

1. **Restart your terminal**: Environment variable changes do not affect already open `cmd.exe` or PowerShell sessions. Close and re-open your terminal.
2. **Inspect System PATH**:
   Run the following in PowerShell:
   ```powershell
   $env:Path -split ';' | Select-String "g8s"
   ```
   If nothing is returned, append `C:\Program Files\g8s` to the System PATH:
   ```powershell
   [Environment]::SetEnvironmentVariable(
       "Path",
       [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::Machine) + ";C:\Program Files\g8s",
       [EnvironmentVariableTarget]::Machine
   )
   ```
3. **Run Self-Healing Diagnostics**:
   ```cmd
   "C:\Program Files\g8s\g8s.exe" doctor --fix
   ```

---

## 6. Uninstallation

### Via Windows GUI
1. Open **Settings** > **Apps** > **Installed apps** (or `appwiz.cpl` in Run dialog).
2. Locate **g8s (The Gatekeepers)**.
3. Click **Uninstall** and confirm.

### Via Command Line
* **NSIS Uninstaller**:
  ```cmd
  "C:\Program Files\g8s\Uninstall.exe" /S
  ```
* **MSI Package**:
  ```cmd
  msiexec /x g8s_0.4.0_windows_amd64.msi /qn
  ```

---

## 7. Upgrades & Safe Rollback (`g8s.old`)

When upgrading `g8s` manually or in CI environments:

1. Rename the existing executable to `g8s.old.exe`:
   ```cmd
   ren "C:\Program Files\g8s\g8s.exe" "g8s.old.exe"
   ```
2. Copy the new `g8s.exe` into `C:\Program Files\g8s\g8s.exe`.
3. If an issue is encountered, easily roll back by restoring `g8s.old.exe`:
   ```cmd
   del "C:\Program Files\g8s\g8s.exe"
   ren "C:\Program Files\g8s\g8s.old.exe" "g8s.exe"
   ```
