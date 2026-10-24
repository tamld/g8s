// Package server — config_auth_test.go validates server bind address
// and authentication invariants (issue #659, DELTA-27).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type configTestCase struct {
	name     string
	address  string
	apiToken string
	wantErr  bool
}

func runConfigValidationTests(t *testing.T, tests []configTestCase) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				Address:  tc.address,
				ApiToken: tc.apiToken,
			}
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected *net.TCPAddr, got %T", ln.Addr())
	}
	return tcpAddr.Port
}

func stopTestServer(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}
	if srv.IsRunning() {
		t.Fatal("expected server to not be running after Stop()")
	}
}

func assertStartError(t *testing.T, srv *Server, expectedSubstring string) {
	t.Helper()
	err := srv.Start()
	if err == nil {
		t.Fatalf("expected error from Start(), got nil")
	}
	if !strings.Contains(err.Error(), expectedSubstring) {
		t.Fatalf("expected error mentioning %q, got: %v", expectedSubstring, err)
	}
	if srv.IsRunning() {
		t.Fatal("expected server to not be running after invalid Start()")
	}
}

// TestConfig_Default tests default configuration generation and zero-value validation.
func TestConfig_Default(t *testing.T) {
	// 1. DefaultConfig()
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() failed: %v", err)
	}
	if cfg.Address != "127.0.0.1:8080" {
		t.Errorf("expected default address to be 127.0.0.1:8080, got %q", cfg.Address)
	}
	if cfg.ApiToken != "" {
		t.Errorf("expected default ApiToken to be empty, got %q", cfg.ApiToken)
	}
	if cfg.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout 10s, got %v", cfg.ReadHeaderTimeout)
	}
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("expected ReadTimeout 30s, got %v", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 30*time.Second {
		t.Errorf("expected WriteTimeout 30s, got %v", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 120*time.Second {
		t.Errorf("expected IdleTimeout 120s, got %v", cfg.IdleTimeout)
	}

	// 2. Zero-value Config{}
	zeroCfg := Config{}
	if err := zeroCfg.Validate(); err != nil {
		t.Fatalf("Config{}.Validate() failed: %v", err)
	}
	if zeroCfg.Address != "127.0.0.1:8080" {
		t.Errorf("expected zero-value address to default to 127.0.0.1:8080, got %q", zeroCfg.Address)
	}
	if zeroCfg.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected zero-value ReadHeaderTimeout to default to 10s, got %v", zeroCfg.ReadHeaderTimeout)
	}
}

// TestConfig_LoopbackBinds tests literal IPv4 and IPv6 loopback addresses with and without token.
func TestConfig_LoopbackBinds(t *testing.T) {
	runConfigValidationTests(t, []configTestCase{
		{
			name:     "IPv4 127.0.0.1 with no token",
			address:  "127.0.0.1:8080",
			apiToken: "",
			wantErr:  false,
		},
		{
			name:     "IPv4 127.0.0.2 with no token",
			address:  "127.0.0.2:9000",
			apiToken: "",
			wantErr:  false,
		},
		{
			name:     "IPv4 127.255.255.254 with no token",
			address:  "127.255.255.254:8080",
			apiToken: "",
			wantErr:  false,
		},
		{
			name:     "IPv6 ::1 with no token",
			address:  "[::1]:8080",
			apiToken: "",
			wantErr:  false,
		},
		{
			name:     "IPv4 loopback with valid token",
			address:  "127.0.0.1:8080",
			apiToken: "valid-secret-token",
			wantErr:  false,
		},
		{
			name:     "IPv6 loopback with valid token",
			address:  "[::1]:8080",
			apiToken: "valid-secret-token",
			wantErr:  false,
		},
		{
			name:     "IPv4 loopback with whitespace token is rejected",
			address:  "127.0.0.1:8080",
			apiToken: "   \t  ",
			wantErr:  true,
		},
	})
}

// TestConfig_WildcardAndEmptyHostBinds tests wildcard and empty-host bind addresses.
func TestConfig_WildcardAndEmptyHostBinds(t *testing.T) {
	runConfigValidationTests(t, []configTestCase{
		{
			name:     "empty host bind :8080 with no token is rejected",
			address:  ":8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "empty host bind :8080 with whitespace token is rejected",
			address:  ":8080",
			apiToken: "   ",
			wantErr:  true,
		},
		{
			name:     "empty host bind :8080 with valid token is allowed",
			address:  ":8080",
			apiToken: "token-wildcard",
			wantErr:  false,
		},
		{
			name:     "wildcard IPv4 0.0.0.0:8080 with no token is rejected",
			address:  "0.0.0.0:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "wildcard IPv4 0.0.0.0:8080 with whitespace token is rejected",
			address:  "0.0.0.0:8080",
			apiToken: "  \t\n  ",
			wantErr:  true,
		},
		{
			name:     "wildcard IPv4 0.0.0.0:8080 with valid token is allowed",
			address:  "0.0.0.0:8080",
			apiToken: "token-ipv4-wildcard",
			wantErr:  false,
		},
		{
			name:     "wildcard IPv6 [::]:8080 with no token is rejected",
			address:  "[::]:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "wildcard IPv6 [::]:8080 with whitespace token is rejected",
			address:  "[::]:8080",
			apiToken: "   ",
			wantErr:  true,
		},
		{
			name:     "wildcard IPv6 [::]:8080 with valid token is allowed",
			address:  "[::]:8080",
			apiToken: "token-ipv6-wildcard",
			wantErr:  false,
		},
	})
}

// TestConfig_NonLoopbackBinds tests non-loopback IPv4 and IPv6 binds with and without token.
func TestConfig_NonLoopbackBinds(t *testing.T) {
	runConfigValidationTests(t, []configTestCase{
		{
			name:     "IPv4 192.168.1.100 without token is rejected",
			address:  "192.168.1.100:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "IPv4 192.168.1.100 with whitespace token is rejected",
			address:  "192.168.1.100:8080",
			apiToken: "   \t  ",
			wantErr:  true,
		},
		{
			name:     "IPv4 192.168.1.100 with valid token is valid",
			address:  "192.168.1.100:8080",
			apiToken: "secure-nonloopback-token",
			wantErr:  false,
		},
		{
			name:     "IPv4 10.0.0.1 without token is rejected",
			address:  "10.0.0.1:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "IPv4 10.0.0.1 with valid token is valid",
			address:  "10.0.0.1:8080",
			apiToken: "secure-10-token",
			wantErr:  false,
		},
		{
			name:     "IPv6 2001:db8::1 without token is rejected",
			address:  "[2001:db8::1]:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "IPv6 2001:db8::1 with whitespace token is rejected",
			address:  "[2001:db8::1]:8080",
			apiToken: "   ",
			wantErr:  true,
		},
		{
			name:     "IPv6 2001:db8::1 with valid token is valid",
			address:  "[2001:db8::1]:8080",
			apiToken: "secure-ipv6-token",
			wantErr:  false,
		},
		{
			name:     "IPv6 link-local fe80::1 without token is rejected",
			address:  "[fe80::1]:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "IPv6 link-local fe80::1 with valid token is valid",
			address:  "[fe80::1]:8080",
			apiToken: "secure-link-local-token",
			wantErr:  false,
		},
	})
}

// TestConfig_MalformedAddressesAndPorts tests address syntax and port boundary handling.
func TestConfig_MalformedAddressesAndPorts(t *testing.T) {
	runConfigValidationTests(t, []configTestCase{
		{
			name:     "missing port on IPv4 address",
			address:  "127.0.0.1",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "missing port on hostname",
			address:  "localhost",
			apiToken: "token",
			wantErr:  true,
		},
		{
			name:     "empty port",
			address:  "127.0.0.1:",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "non-numeric port letters",
			address:  "127.0.0.1:abc",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "port 0 rejected",
			address:  "127.0.0.1:0",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "port 65536 out of range",
			address:  "127.0.0.1:65536",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "large port 99999 out of range",
			address:  "127.0.0.1:99999",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "negative port rejected",
			address:  "127.0.0.1:-80",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "unbracketed IPv6 address",
			address:  "::1:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "missing closing bracket on IPv6",
			address:  "[::1:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "extra colons in address",
			address:  "127.0.0.1:8080:9090",
			apiToken: "",
			wantErr:  true,
		},
	})
}

// TestConfig_Hostnames tests conservative hostname validation and authentication requirement.
func TestConfig_Hostnames(t *testing.T) {
	runConfigValidationTests(t, []configTestCase{
		{
			name:     "localhost without token rejected (conservative: only literal IP loopback allowed without token)",
			address:  "localhost:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "localhost with whitespace token rejected",
			address:  "localhost:8080",
			apiToken: "   ",
			wantErr:  true,
		},
		{
			name:     "localhost with valid token is valid",
			address:  "localhost:8080",
			apiToken: "localhost-token",
			wantErr:  false,
		},
		{
			name:     "custom hostname without token rejected",
			address:  "my-service.internal:8080",
			apiToken: "",
			wantErr:  true,
		},
		{
			name:     "custom hostname with valid token is valid",
			address:  "my-service.internal:8080",
			apiToken: "internal-token",
			wantErr:  false,
		},
		{
			name:     "hostname with spaces rejected as malformed",
			address:  "invalid host:8080",
			apiToken: "some-token",
			wantErr:  true,
		},
		{
			name:     "hostname with leading hyphen rejected as malformed",
			address:  "-badhost:8080",
			apiToken: "some-token",
			wantErr:  true,
		},
		{
			name:     "hostname with trailing hyphen rejected as malformed",
			address:  "badhost-:8080",
			apiToken: "some-token",
			wantErr:  true,
		},
		{
			name:     "hostname with double dot rejected as malformed",
			address:  "bad..host:8080",
			apiToken: "some-token",
			wantErr:  true,
		},
		{
			name:     "hostname with underscore rejected as malformed",
			address:  "bad_host:8080",
			apiToken: "some-token",
			wantErr:  true,
		},
	})
}

// TestServerAuth_HTTPEndpoints tests bearer token authentication via the existing server API.
func TestServerAuth_HTTPEndpoints(t *testing.T) {
	_, store := setupTestServer(t)
	cfg := DefaultConfig()
	cfg.ApiToken = "secret-token-123"
	cfg.EnableHealthz = true
	cfg.EnableMetrics = true

	srv := NewServer(cfg, store)
	handler := srv.httpSrv.Handler

	// 1. Missing bearer token on API route -> 401 Unauthorized with WWW-Authenticate and JSON envelope
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", rec.Code)
	}
	if wwwAuth := rec.Header().Get("WWW-Authenticate"); wwwAuth != `Bearer realm="g8s"` {
		t.Errorf("expected WWW-Authenticate header %q, got %q", `Bearer realm="g8s"`, wwwAuth)
	}
	var errEnvelope struct {
		V     int    `json:"v"`
		Kind  string `json:"kind"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errEnvelope); err != nil {
		t.Fatalf("failed to decode JSON error envelope: %v (body: %s)", err, rec.Body.String())
	}
	if errEnvelope.Error.Code != "E_UNAUTHORIZED" {
		t.Errorf("expected error code E_UNAUTHORIZED, got %q", errEnvelope.Error.Code)
	}

	// 2. Wrong bearer token on API route -> 401 Unauthorized
	req = httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	req.Header.Set("Authorization", "Bearer wrong-token-xyz")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", rec.Code)
	}

	// 3. Non-bearer authorization scheme -> 401 Unauthorized
	req = httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	req.Header.Set("Authorization", "Basic c2VjcmV0LXRva2VuLTEyMw==")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-bearer auth, got %d", rec.Code)
	}

	// 4. Valid bearer token on API route -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 5. Open liveness and probe endpoints remain accessible without authentication
	for _, openPath := range []string{"/healthz", "/readyz", "/metrics"} {
		req = httptest.NewRequest(http.MethodGet, openPath, nil)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 on probe endpoint %s without auth, got %d", openPath, rec.Code)
		}
	}
}

// TestServerStart_InvalidRemoteConfigRejected tests that Server.Start enforces
// configuration validation before changing running state or opening network listeners (#659).
func TestServerStart_InvalidRemoteConfigRejected(t *testing.T) {
	_, store := setupTestServer(t)

	// 1. Unauthenticated non-loopback IP bind: Start must return error, server not running
	srvNonLoopback := NewServer(Config{
		Address:  "192.168.1.100:8080",
		ApiToken: "",
	}, store)
	assertStartError(t, srvNonLoopback, "unauthenticated bind to non-loopback address")

	// 2. Unauthenticated wildcard bind: Start must return error, stays not running, no listener opened
	freePort := getFreePort(t)
	wildcardAddr := fmt.Sprintf("0.0.0.0:%d", freePort)
	srvWildcard := NewServer(Config{
		Address:  wildcardAddr,
		ApiToken: "",
	}, store)
	assertStartError(t, srvWildcard, "unauthenticated bind")

	// Verify no listener is active on that port (real startup no-listen oracle)
	conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", freePort), 100*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatalf("expected no listener on %s, but connection succeeded", wildcardAddr)
	}

	// Verify port remains unbound and immediately available for another listener
	probeLn, bindErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", freePort))
	if bindErr != nil {
		t.Fatalf("expected port %d to remain free for binding, got err: %v", freePort, bindErr)
	}
	probeLn.Close()

	// 3. Whitespace-only token on non-loopback bind: Start must return error, stays not running
	srvWhitespace := NewServer(Config{
		Address:  "10.0.0.1:8080",
		ApiToken: "   \t\n  ",
	}, store)
	assertStartError(t, srvWhitespace, "invalid api_token")

	// Helper for valid server startup and connection acceptance
	assertStartAndAcceptsConn := func(srv *Server, addr string) {
		t.Helper()
		if err := srv.Start(); err != nil {
			t.Fatalf("expected server to start successfully, got: %v", err)
		}
		if !srv.IsRunning() {
			t.Fatal("expected server to be running after valid Start()")
		}

		time.Sleep(20 * time.Millisecond)
		activeConn, dialOkErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if dialOkErr != nil {
			t.Fatalf("expected listener to accept connection on %s, got: %v", addr, dialOkErr)
		}
		activeConn.Close()

		stopTestServer(t, srv)
	}

	// 4. Valid remote configuration with API token starts cleanly and stops cleanly
	remotePort := getFreePort(t)
	validRemoteAddr := fmt.Sprintf("127.0.0.1:%d", remotePort)
	srvValidRemote := NewServer(Config{
		Address:  validRemoteAddr,
		ApiToken: "valid-secret-token-123",
	}, store)
	assertStartAndAcceptsConn(srvValidRemote, validRemoteAddr)

	// 5. Valid loopback configuration without token starts cleanly and stops cleanly
	loopbackPort := getFreePort(t)
	loopbackAddr := fmt.Sprintf("127.0.0.1:%d", loopbackPort)
	cfgLoopback := DefaultConfig()
	cfgLoopback.Address = loopbackAddr
	srvLoopback := NewServer(cfgLoopback, store)
	assertStartAndAcceptsConn(srvLoopback, loopbackAddr)
}
