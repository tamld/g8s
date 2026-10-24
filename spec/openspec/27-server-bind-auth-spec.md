# OpenSpec DELTA-27: Server Bind Address & API Token Authentication Enforcement (`#659`)

* **Specification ID**: `DELTA-27`
* **Title**: Daemon Server Bind Address & API Token Authentication Enforcement
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Daemon Hardening & Control Plane Security)
* **Target Packages**: `internal/server`
* **Related Issues**: `#348`, `#371`, `#659`
* **Foundational Axioms**: Axiom 1 (Zero-CGO Pure-Go Binary), Axiom 2 (Capability-Scoped Write Receipts), Principle of Least Privilege

---

## 1. Context & Motivation

Security issues `#348` and `#371` established loopback-by-default binding and bearer-token authentication for the `g8s` daemon HTTP control plane (`g8s serve`). However, configuration validation in `Config.Validate()` allowed unauthenticated wildcard binds (e.g. `":8080"`, `"0.0.0.0:8080"`, `"[::]:8080"`) or routable network interfaces (e.g. `"192.168.1.50:8080"`) without requiring an `ApiToken`. This created an exposure risk where misconfiguration could expose sensitive task execution, write receipt, and filesystem APIs to the local network or internet unauthenticated.

Issue `#659` establishes strict pre-flight validation in `Config.Validate()`:
1. **Loopback Exception**: Unauthenticated binds (`ApiToken == ""`) are ONLY permitted for literal IPv4 and IPv6 loopback addresses (`127.0.0.0/8`, `::1`).
2. **Wildcard & Non-Loopback Rejection**: Wildcard addresses (`0.0.0.0`, `::`, or empty host `":8080"`) and non-loopback IP addresses MUST require a valid, non-whitespace API token.
3. **Whitespace Token Rejection**: Whitespace-only API tokens (e.g. `"   "`, `"\t"`, `"\n"`) are rejected for non-loopback and wildcard binds.
4. **Conservative Hostname & Address Handling**: Hostnames are treated conservatively: they are not literal loopback IPs and require authentication; malformed addresses and invalid hostnames are explicitly rejected.
5. **Authenticated Non-Loopback Valid**: Non-loopback and wildcard addresses configured with a non-whitespace `ApiToken` remain valid and supported.
6. **Constant-Time Verification**: Constant-time token verification (`subtle.ConstantTimeCompare`) in `authMiddleware` and default timeouts are strictly preserved.
7. **Runtime Startup Enforcement Point (`Server.Start`)**: While constructor `NewServer()` performs non-fatal validation logging to preserve constructor API compatibility, `Server.Start()` is the actual normative enforcement point. `Server.Start()` MUST invoke `Config.Validate()` before any state mutation (`s.running = true`) and before spawning the `http.Server.ListenAndServe` goroutine. An invalid configuration causes `Start()` to fail immediately with an informative error, leaving the server stopped (`IsRunning() == false`) without opening any network listener.

---

## 2. Normative Requirements

### Requirement: Default Configuration & Loopback Binds (`internal/server/config.go`)

`DefaultConfig()` MUST return `Address: "127.0.0.1:8080"` with empty `ApiToken`, and `Config.Validate()` MUST accept literal loopback addresses without an `ApiToken`.

#### Scenario: Default configuration validates cleanly
<!-- tests: TestConfig_Default -->
- `DefaultConfig()` initializes with `Address: "127.0.0.1:8080"` and empty `ApiToken`.
- Calling `Validate()` on `DefaultConfig()` returns no error.
- Calling `Validate()` on an empty `Config{}` populates `Address: "127.0.0.1:8080"` and default timeouts without error.

#### Scenario: Literal IPv4 and IPv6 loopback binds allowed without token
<!-- tests: TestConfig_LoopbackBinds -->
- `Address: "127.0.0.1:8080"` with `ApiToken: ""` validates successfully.
- `Address: "127.0.0.2:9000"` (any `127.0.0.0/8` IP) with `ApiToken: ""` validates successfully.
- `Address: "[::1]:8080"` (IPv6 loopback) with `ApiToken: ""` validates successfully.
- Literal loopback binds with a non-empty `ApiToken` validate successfully.

---

### Requirement: Unauthenticated Wildcard & Non-Loopback Bind Rejection (`internal/server/config.go`)

`Config.Validate()` MUST reject wildcard and non-loopback binds when `ApiToken` is empty or contains only whitespace characters.

#### Scenario: Wildcard and empty-host binds rejected without token
<!-- tests: TestConfig_WildcardAndEmptyHostBinds -->
- Empty host bind `":8080"` with empty `ApiToken` is rejected with an error.
- Empty host bind `":8080"` with whitespace-only `ApiToken` (`"   "`) is rejected with an error.
- Wildcard IPv4 bind `"0.0.0.0:8080"` with empty or whitespace-only `ApiToken` is rejected with an error.
- Wildcard IPv6 bind `"[::]:8080"` with empty or whitespace-only `ApiToken` is rejected with an error.
- Wildcard and empty-host binds configured with a valid non-empty `ApiToken` validate successfully.

#### Scenario: Non-loopback IPv4 and IPv6 binds rejected without token
<!-- tests: TestConfig_NonLoopbackBinds -->
- Routable IPv4 bind `"192.168.1.100:8080"` with empty `ApiToken` is rejected.
- Routable IPv4 bind `"10.0.0.1:8080"` with whitespace-only `ApiToken` is rejected.
- Routable IPv4 bind `"192.168.1.100:8080"` with valid `ApiToken: "secret123"` validates successfully.
- Routable IPv6 bind `"[2001:db8::1]:8080"` with empty or whitespace-only `ApiToken` is rejected.
- Routable IPv6 bind `"[2001:db8::1]:8080"` with valid `ApiToken: "secret123"` validates successfully.

---

### Requirement: Conservative Hostname & Malformed Address Validation (`internal/server/config.go`)

`Config.Validate()` MUST validate address structure and port range conservatively, rejecting malformed addresses and requiring authentication for hostnames.

#### Scenario: Malformed addresses and invalid ports rejected
<!-- tests: TestConfig_MalformedAddressesAndPorts -->
- Address without port (e.g. `"127.0.0.1"`, `"localhost"`) is rejected with an error from `net.SplitHostPort`.
- Address with non-numeric port (e.g. `"127.0.0.1:http"`, `"127.0.0.1:abc"`) is rejected.
- Address with invalid port number (e.g. `"127.0.0.1:0"`, `"127.0.0.1:65536"`, `"127.0.0.1:-1"`) is rejected.
- Address with invalid IPv6 bracket syntax (e.g. `"::1:8080"`, `"[::1:8080"`) is rejected.

#### Scenario: Hostnames handled conservatively
<!-- tests: TestConfig_Hostnames -->
- Hostname `"localhost:8080"` without token is rejected because unauthenticated binds are restricted strictly to literal IP loopback addresses.
- Hostname `"localhost:8080"` with valid `ApiToken` validates successfully.
- Hostname `"daemon.internal:8080"` with valid `ApiToken` validates successfully.
- Hostnames with invalid RFC 1123 characters (e.g. spaces `"bad host:8080"`, invalid labels `"-bad:8080"`, or double dots `"bad..host:8080"`) are rejected as malformed.

---

### Requirement: Constant-Time Bearer Token Enforcement (`internal/server/server.go`)

The HTTP server `authMiddleware` MUST enforce constant-time bearer token comparison against `s.config.ApiToken` when configured, leaving liveness probe endpoints open.

#### Scenario: HTTP requests with missing, wrong, and valid bearer tokens
<!-- tests: TestServerAuth_HTTPEndpoints -->
- A request to `/api/v1/tasks` or `/openapi.json` without an `Authorization` header receives `401 Unauthorized` with header `WWW-Authenticate: Bearer realm="g8s"` and JSON error code `E_UNAUTHORIZED`.
- A request with an invalid or incorrect bearer token receives `401 Unauthorized`.
- A request with the correct bearer token receives `200 OK`.
- Probe endpoints (`/healthz`, `/readyz`, `/metrics`) remain accessible (`200 OK`) without an `Authorization` header even when `ApiToken` is configured.

---

### Requirement: Server Startup Pre-flight Validation Enforcement (`internal/server/server.go`)

`Server.Start()` MUST enforce configuration validation as a prerequisite to server execution. It MUST reject invalid configurations before transitioning to running state and before spawning any listener goroutine.

#### Scenario: Server Start rejects invalid remote configuration
<!-- tests: TestServerStart_InvalidRemoteConfigRejected -->
- Given a `Server` initialized with an unauthenticated non-loopback address (e.g. `192.168.1.100:8080` with empty or whitespace `ApiToken`) or wildcard bind (e.g. `:8080`, `0.0.0.0:8080` with empty or whitespace `ApiToken`),
- When `Server.Start()` is invoked,
- Then `Start()` returns a descriptive error indicating that the server cannot start with an invalid configuration,
- And `Server.IsRunning()` returns `false`,
- And no network listener is opened (subsequent connection attempts or port checks confirm the address is not listening).
