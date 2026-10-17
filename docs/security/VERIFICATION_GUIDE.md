# Security & Binary Verification Guide for g8s

> **Zero-Trust Verification**: How to verify the cryptographic authenticity, integrity, and pure-Go build properties of downloaded `g8s` binaries and packages.

---

## 1. Verifying SHA-256 Checksums

Every official release publishes a canonical `checksums.txt` file containing cryptographic SHA-256 hashes of all release tarballs, deb/rpm installers, and zip archives.

### Linux / macOS

```sh
# 1. Download binary and checksum file
curl -LO https://github.com/tamld/g8s/releases/latest/download/g8s_0.1.0_linux_amd64.tar.gz
curl -LO https://github.com/tamld/g8s/releases/latest/download/checksums.txt

# 2. Verify SHA-256 integrity
# On Linux:
sha256sum --ignore-missing -c checksums.txt

# On macOS:
shasum -a 256 --ignore-missing -c checksums.txt
```

### Expected Output
```text
g8s_0.1.0_linux_amd64.tar.gz: OK
```

---

## 2. Verifying Zero-CGO & Pure-Go Linkage

Per the `g8s` Constitution (Axiom 1), all `g8s` binaries are strictly pure-Go (Zero-CGO). They contain **zero dynamic C runtime dependencies** (`libc.so`, `libpthread`, `libsqlite3`).

### On Linux
```sh
# 1. Verify dynamic linking dependencies (should report statically linked or not a dynamic executable)
ldd ./g8s

# Expected output:
# not a dynamic executable (or statically linked)

# 2. Inspect file header
file ./g8s

# Expected output:
# ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=...
```

### On macOS
```sh
# Verify Mach-O load commands (only libSystem.B.dylib bound by macOS kernel ABI)
otool -L ./g8s
```

---

## 3. macOS Gatekeeper & Quarantine Handling

When downloading pre-compiled binaries outside of the Mac App Store or Apple Notarization pipeline, macOS flags the binary with the `com.apple.quarantine` extended attribute.

### Removing the Quarantine Flag
```sh
# Inspect quarantine attribute
xattr -l ./g8s

# Remove quarantine attribute
xattr -d com.apple.quarantine ./g8s

# Verify execution
./g8s version
```

Alternatively, navigate to **System Settings $\rightarrow$ Privacy & Security** and click **"Allow Anyway"** next to the blocked `g8s` notification.

---

## 4. GPG / Cosign Signatures & Attestation

For enterprise environments requiring supply chain security (SLSA Level 3):

```sh
# Verify Cosign blob attestation
cosign verify-blob \
  --certificate-identity "https://github.com/tamld/g8s/.github/workflows/release.yml@refs/tags/v0.17.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature checksums.txt.sig \
  checksums.txt
```

---

## 5. Source-Level SAST Triad (`Gosec` + `Semgrep` + `CodeQL`)

Every commit and pull request is verified against a 3-layer static application security testing (SAST) pipeline:

1. **Layer 1A — Go AST Security (`Gosec` via `golangci-lint`)**:
   Enforces POSIX `0600`/`0700` file permissions (`G306`/`G301`/`G302`, Constitution Axiom 4), cryptographic strength (`G401`–`G505`), and HTTP server hardening (`G112`/`G114`).
   ```sh
   golangci-lint run ./...
   ```
2. **Layer 1B — Custom Offline AST Policy (`Semgrep`)**:
   Enforces `g8s`-specific structural invariants defined in [`.semgrep/g8s-rules.yml`](../../.semgrep/g8s-rules.yml) (`g8s-zero-cgo-import`, `g8s-writefile-perm-0600`, `g8s-http-listenandserve-no-timeout`, `g8s-github-actions-untrusted-input`).
   ```sh
   bash tools/ci_semgrep_check_test.sh && bash tools/ci_semgrep_check.sh
   ```
3. **Layer 2 — Inter-Procedural Dataflow & Taint Analysis (`CodeQL`)**:
   Executed automatically in [`.github/workflows/codeql.yml`](../../.github/workflows/codeql.yml) across `go` (`CGO_ENABLED=0`) and `actions` using the `security-extended` query suite, publishing SARIF reports to GitHub Code Scanning.

