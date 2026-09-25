# Implementation Plan - Issue #358: Signtool Credential Leakage & Unbounded HTTP Request Body DoS

## Context
Two security vulnerabilities were identified in the source audit:
1. `internal/signing/signer.go`:
   - Password passed via `/p` directly in `signtool` argv without sanitization or redaction in execution errors.
   - `DefaultTimestamper` defaulted to unencrypted HTTP `http://timestamp.digicert.com`.
   - Error messages returned from `s.runner().Run` could leak the private key password in logs / CI build outputs.
2. `internal/server/server.go`:
   - `createTask` and `handleSupervisorUpdateFalseEscalation` read JSON bodies directly via `json.NewDecoder(r.Body)` without bounds checking via `http.MaxBytesReader`, exposing the daemon to memory exhaustion / OOM DoS attacks.

## User Review Required
> [!NOTE]
> - `DefaultTimestamper` is upgraded to HTTPS (`https://timestamp.digicert.com`).
> - `SigntoolSigner` adds `CertPasswordEnv` support (loading password from environment variable) and ensures any errors returned during signing have passwords strictly redacted with `[REDACTED]`.
> - Server request bodies for JSON ingestion endpoints are capped with `http.MaxBytesReader(w, r.Body, 10<<20)` (10 MB).

## Proposed Changes

### Component 1: `internal/signing/signer.go`
- Update `DefaultTimestamper` to `https://timestamp.digicert.com`.
- Add `CertPasswordEnv` field to `SigntoolSigner`.
- Redact passwords from all returned errors and runner outputs.
- Update tests in `internal/signing/signer_test.go` to verify HTTPS default timestamper and password redaction on failure.

### Component 2: `internal/server/server.go`
- Introduce `const maxRequestBodyBytes = 10 << 20` (10 MB).
- Wrap `r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)` in `createTask` and `handleSupervisorUpdateFalseEscalation`.
- Add unit tests in `internal/server/server_test.go` verifying that requests exceeding 10MB are rejected with 400 Bad Request.

### Component 3: Testing & Verification
- `go test -v ./internal/signing/...`
- `go test -v ./internal/server/...`
- `go vet ./...`
