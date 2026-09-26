# Implementation Plan - Issue #360: Windows Cleanup CWD and Worker Executable Identity

**Session type**: T2 (execution)

## Context
Forensic analysis on Windows revealed three cross-platform gaps in process cleanup and worker verification:
1. `internal/cleanup/cwd_windows.go`: `resolveProcessCWD` called `QueryFullProcessImageName` and returned `filepath.Dir(imgPath)`, confusing the binary installation path with process CWD. This caused false positives/negatives in ghost process detection.
2. `internal/process/lister_windows.go`: `TasklistLister.List` set `CommandLine: binName`, discarding all command-line arguments and preventing `--add-dir` or repository path inspection.
3. `internal/worker/worker.go`: `verifyExecutableIdentity` did not strip `.exe`, `.cmd`, or `.bat`, so Windows executable basenames never matched `"python"`, `"node"`, `"go"`, bypassing executable spoofing detection. Furthermore, it lacked timeout and Go version command support.

## User Review Required
> [!NOTE]
> - CWD extraction on Windows x64 uses NtQueryInformationProcess and ReadProcessMemory to read the true working directory from the process PEB without external dependencies or CGO. If PEB cannot be accessed (e.g. system or protected processes), it cleanly returns `""` rather than misattributing the executable directory as CWD.
> - Command-line listing queries CIM (`Get-CimInstance Win32_Process`) when available, returning true argument strings and parent PIDs, with automatic fallback to `tasklist`.
> - Worker executable verification sanitizes Windows extensions (`.exe`, `.cmd`, `.bat`), enforces a 5-second context timeout, and tests multi-style version invocations.

## Proposed Changes

### Component 1: `internal/cleanup/cwd_windows.go`
- Rewrite `resolveProcessCWD(pid int) string` using `NtQueryInformationProcess` + `ReadProcessMemory` to read `ProcessParameters.CurrentDirectory.DosPath`.
- Normalize with `filepath.Clean`.
- On error or inaccessible process, safely return `""`.

### Component 2: `internal/process/lister_windows.go`
- Implement CIM-based process listing via `Get-CimInstance Win32_Process` to capture `CommandLine` and `ParentProcessId`.
- Gracefully fall back to `tasklist /FO CSV /V /NH` if PowerShell / CIM is unavailable.
- Implement `ResolveCWD(pid int) string` using native PEB inspection.

### Component 3: `internal/worker/worker.go`
- In `verifyExecutableIdentity`, strip `.exe`, `.cmd`, `.bat` from executable base name.
- Add 5-second `context.WithTimeout`.
- Add support for `-v`, `--version`, `-version`, and `version` (for Go).
- Inspect command output even when exit codes are non-zero.

### Component 4: Testing & Verification
- Unit test in `internal/cleanup/cwd_windows_test.go` verifying PID CWD matches expected directory and non-existent PID returns empty string.
- Unit test in `internal/process/lister_windows_test.go` verifying CIM parsing and ResolveCWD.
- Unit test in `internal/worker/worker_identity_test.go` verifying `.exe` stripping and spoof detection on Windows.
- Full `go vet ./...` and `go test` across modified packages.
