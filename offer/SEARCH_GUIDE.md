# Cross-Platform Search Standard: `ripgrep` (`rg`) in `g8s` & Sibling Repositories

> **Scope**: `g8s` Core (`tools/*.sh`, `internal/context`, `g8s doctor`), Offer Bundle (`offer/`, `tools/ci_*.sh`), and `F0 / F1 / F2` Agent Execution (`skills/g8s-supervisor`).  
> **Supported Targets**: macOS (`darwin/arm64`, `darwin/amd64`), Linux (`linux/amd64`, `linux/arm64`), and Windows (`windows/amd64` via Git Bash / PowerShell / WSL2).

---

## 1. Why `ripgrep` (`rg`) Over `grep` and Legacy Search Tools?

In multi-platform, polyglot agentic engineering (`Go`, `TypeScript/Node.js`, `Python`, `Markdown/OpenSpec`, `IaC`), traditional search invocations (`grep -rn`, `find . | xargs grep`, `ack`, `ag`) introduce **three systemic failure modes** during `F0 → F1` worker execution:

1. **Cross-OS Regex & Flag Divergence (`BSD grep` vs `GNU grep` vs `Windows`)**:
   - **macOS** ships **BSD `grep`**, which lacks `-P` (PCRE2 lookaround/non-greedy quantifiers) and differs from **GNU `grep`** (Linux) in word-boundary escapes (`\<`/`\>` vs `\b`), whitespace shorthand (`\s`), `--include` glob expansion, and `-r` vs `-R` symlink traversal.
   - **Windows** splits between MSYS2/Git Bash `grep` and PowerShell `Select-String`, causing shell quoting and path-separator (`\` vs `/`) breakages.
   - **`ripgrep` (`rg`)** compiles a single deterministic Rust `regex` engine (with optional `-P` PCRE2 support) that behaves identically on **macOS, Linux, and Windows**, normalizing `/` path globs (`-g`) across all three OS families.
2. **Unbounded Directory Traversal & Context Pollution**:
   - `grep -rn` and `find . -exec grep` blindly scan `.git/`, `node_modules/`, `vendor/`, `.venv/`, `dist/`, build caches, and nested `$G8S_STATE_DIR/worktrees/` unless the caller remembers a long chain of `! -path` or `--exclude-dir` flags.
   - On sibling repositories like `aegis` or `tuneflow`, an unguarded `grep -rn` over `node_modules/` burns tens of thousands of tokens in worker stdout and stalls F1 workers toward the `360s` wall-clock timeout.
   - **`rg`** automatically respects `.gitignore`, `.ignore`, and `.rgignore`, skips hidden directories (`.git/`) and binary files by default, and provides explicit opt-in flags (`--hidden` / `-u` / `-uu` / `-uuu`) when raw inspection is required.
3. **Throughput & Structured Telemetry**:
   - `rg` combines SIMD-accelerated literal scanning (Teddy / Aho-Corasick), memory-mapped I/O, and work-stealing parallel directory traversal, achieving **`5x–20x` lower latency** than `grep -rn` or `ag`.
   - `rg --json` emits machine-readable JSON Lines (`begin`, `match`, `context`, `end`, `summary`) that `g8s` verifiers and the Context Broker can parse without fragile `cut -d: -f1` regex splitting (which breaks on Windows `C:\...` drive letters).

---

## 2. Tool Comparison Matrix

| Capability / Dimension | `ripgrep` (`rg`) | `GNU grep` (Linux) | `BSD grep` (macOS) | `git grep` | `ag` (Silver Searcher) | `find + xargs grep` |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Cross-Platform Parity (`macOS` / `Linux` / `Windows`)** | **Identical** (Rust `regex` + `-P` PCRE2) | Linux default; requires `ggrep` on macOS | macOS default; **no `-P`**, flag drift vs GNU | High, but **fails outside Git repos** or on untracked dirs unless `--no-index` | Unmaintained; slower & weaker Unicode support | Low (`find -E` vs `find -regextype`, `xargs -0` flags differ) |
| **Respects `.gitignore` by Default** | **Yes** (nested `.gitignore`, `.ignore`, global gitignore) | No (must pass `--exclude-dir` manually) | No (must pass `--exclude-dir` manually) | Yes (tracked files only by default) | Yes (partial `.gitignore` spec support) | No (must chain `! -path "*/node_modules/*"`) |
| **Skips Binary & `.git/` Automatically** | **Yes** | Requires `-I` and `--exclude-dir=.git` | Requires `-I` and `--exclude-dir=.git` | Yes | Yes | No |
| **Fast File Listing (Replaces `find`)** | **`rg --files -g '*.go'`** | No | No | `git ls-files` (tracked only) | `ag -g` | `find . -type f -name '*.go'` (slow on `node_modules`) |
| **Fixed-String Literal Search** | **`rg -F`** (SIMD-accelerated) | `grep -F` | `grep -F` | `git grep -F` | `ag -Q` | `grep -F` |
| **Multiline Search** | **`rg -U`** | `grep -Pzo` (GNU only, NUL-delimited) | Not supported natively | `git grep -P` (if compiled with PCRE) | `ag --multiline` | Fragile |
| **Structured JSON Output** | **`rg --json`** (JSONL stream) | No | No | No | No | No |

---

## 3. Translation Cheat Sheet (`grep` / `find` → `rg`)

| Task | Legacy `grep` / `find` (Fragile / Slow) | Canonical `ripgrep` (`rg`) Equivalent |
| :--- | :--- | :--- |
| Recursive regex search | `grep -rnE "pattern" .` | `rg -n "pattern" .` |
| Fixed-string literal search (e.g. `func Foo(`) | `grep -rnF "func Foo(" .` | `rg -n -F "func Foo(" .` |
| Filter by file extension / glob | `grep -rn "pattern" --include="*.go" .` | `rg -n -g "*.go" "pattern" .` (or `rg -t go "pattern"`) |
| Exclude test files | `find . -name "*.go" ! -name "*_test.go" \| xargs grep -n "panic("` | `rg -n -g "*.go" -g "!*_test.go" "panic\(" .` |
| Quiet existence check in scripts (`if ...`) | `grep -rq "pattern" dir/` | `rg -q "pattern" dir/` |
| List files matching glob (replaces `find`) | `find . -type f -name "*.md" ! -path "*/node_modules/*"` | `rg --files -g "*.md"` |
| Search hidden files (`.github/`, `.g8s/`, `.agents/`) | `grep -rn "pattern" .` | `rg --hidden -g "!.git" "pattern" .` |
| Search everything including `.gitignore`d files | `grep -rn "pattern" .` | `rg -uu "pattern" .` |
| Multiline struct/block match | *(non-portable `awk` / `grep -Pzo`)* | `rg -U "type \w+ struct \{[\s\S]*?\}"` |

---

## 4. How `g8s` Uses `rg` Across Layers (Cross-Platform Pattern)

### 4.1. Portable CI Gates (`tools/ci_*.sh` & Offer Bundle): `rg`-First with POSIX Fallback

Because CI gate scripts in the `offer/` bundle (`ci_spec_code_sync.sh`, `ci_structure_sync.sh`, `ci_link_integrity.sh`) must run both on developer workstations (where `rg` is installed) and inside minimal container images (where only POSIX `grep`/`find` may exist), `g8s` enforces a **Dual-Engine (`rg`-first + `grep` fallback)** pattern:

```bash
# 1. Fixed-string symbol lookup across Go source files (ci_spec_code_sync.sh)
search_go_func_pin() {
    local func_name="$1"
    local root_dir="$2"
    if command -v rg >/dev/null 2>&1; then
        rg -q -F "func ${func_name}(" -g '*.go' "$root_dir" 2>/dev/null
    else
        grep -rqF "func ${func_name}(" "$root_dir" --include='*.go' --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=vendor 2>/dev/null
    fi
}

# 2. Fast file discovery by basename or glob under a directory
find_named_file() {
    local file_name="$1"
    local root_dir="$2"
    if command -v rg >/dev/null 2>&1; then
        rg --files -g "**/${file_name}" -g "${file_name}" "$root_dir" 2>/dev/null | head -n 1 | grep -q .
    else
        find "$root_dir" -name "$file_name" 2>/dev/null | grep -q .
    fi
}
```

### 4.2. F0 Supervisor & F1 Worker Dispatch Discipline

1. **In F0 Pre-Dispatch Reconnaissance**:
   - Always use `rg -n` and `rg --files` instead of `find` or `grep -rn` when locating target symbols and line numbers for F1 worker prompts.
   - Remember `.agents/`, `.g8s/`, and `.github/` are dot-directories: pass `--hidden -g '!.git'` when searching governance/agent configs (`rg --hidden -g '!.git' "pattern" .agents/`).
2. **In F1 Worker Prompts**:
   - Instruct F1 workers to use `./bin/g8s search`, `./bin/g8s read`, `./bin/g8s edit`, and `--select` (or `rg` as secondary fallback), avoiding `sed -i`, `awk`, `grep -rn`, `find`, and `jq`.
3. **On Windows (`windows/amd64`)**:
   - Always invoke `./bin/g8s search` (which normalizes paths to `/` natively) or `rg --path-separator /` when comparing file paths against `WriteReceipt` allowed globs or OpenSpec manifests.

---

## 5. Native Cross-Platform Agent I/O Suite (`DELTA-23` / `#591` & `#593`)

To eliminate external OS tool dependencies (`rg`, `grep`, `find`, `sed -i`, `awk`, `head`, `tail`, `jq`) and enforce **Write Receipt (`AllowedPaths`)** boundaries in-process across **macOS, Linux, and Windows**, `g8s` embeds a 100% pure-Go (`CGO_ENABLED=0`) Agent I/O suite exposed across **CLI, MCP, and HTTP REST API**:

| Capability | Legacy Shell (OS-Dependent & Unscoped) | Native `g8s` CLI | MCP Tool (`g8s mcp`) | HTTP Daemon (`g8s serve`) |
| :--- | :--- | :--- | :--- | :--- |
| **Search & File Discovery** | `rg`, `grep -rn`, `find . -name` | `g8s search [-F] [-i] [-g glob] [--files] <pattern> [path...]` | `g8s_search` | `POST /api/v1/fs/search` |
| **Bounded File Read + SHA-256** | `cat`, `head`, `tail`, `sed -n 'M,Np'` | `g8s read --lines M:N [--max-bytes B] <file>` | `g8s_read` | `POST /api/v1/fs/read` |
| **Atomic CAS File Edit** | `sed -i`, `awk`, `perl -pi`, `cat <<EOF >` | `g8s edit --old <str> --new <str> [--all] [--if-sha256 <h>] <file>` | `g8s_edit` | `POST /api/v1/fs/edit` |
| **JSON Field Extraction** | `jq -r`, `python3 -c "import json..."` | `g8s <cmd> --select .data.field` or `g8s select <expr>` | `g8s_select` | *(JSON REST native)* |

### 5.1. F0 / F1 / F2 Cognitive & Capability Enforcement (`G8S_TIER=F1`)

When **F0 (Supervisor)** dispatches an **F1 (Worker)** via `g8s worker`, the harness automatically injects:
- **Process Environment (`internal/worker`)**: `G8S_TIER=F1`, `G8S_TASK_ID`, `G8S_PERMISSION`, `G8S_RECEIPT_ID`, and `G8S_SCOPE_ROOT` (inherited automatically by any **F2** subagent spawned inside F1).
- **Contract Prompt (`internal/harness`)**: Explicit `Native I/O & Containment Policy (F1/F2)` instructions directing F1/F2 to use `g8s search`, `g8s read`, `g8s edit`, and `--select`.
- **Fail-Closed Receipt Verification (`internal/fsops`)**: Under `G8S_TIER=F1`, `g8s edit` automatically reads `G8S_RECEIPT_ID` and `G8S_SCOPE_ROOT`, verifies the receipt against the SQLite ControlPlane store, resolves symlinks via `filepath.EvalSymlinks`, and blocks any write outside `WriteReceipt.AllowedPaths`.

