// Package server implements the g8s daemon mode with HTTP API server.
package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config holds the server configuration.
type Config struct {
	// Address to listen on (e.g., ":8080" or "127.0.0.1:8080").
	Address string `json:"address" yaml:"address"`

	// ReadHeaderTimeout is the amount of time allowed to read request headers.
	ReadHeaderTimeout time.Duration `json:"read_header_timeout" yaml:"read_header_timeout"`

	// ReadTimeout is the maximum duration for reading the entire request.
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout"`

	// WriteTimeout is the maximum duration before timing out writes.
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout"`

	// IdleTimeout is the maximum amount of time to wait for the next request.
	IdleTimeout time.Duration `json:"idle_timeout" yaml:"idle_timeout"`

	// EnableCORS enables Cross-Origin Resource Sharing.
	EnableCORS bool `json:"enable_cors" yaml:"enable_cors"`

	// AllowedOrigins is the list of allowed origins for CORS.
	AllowedOrigins []string `json:"allowed_origins" yaml:"allowed_origins"`

	// EnableMetrics enables the /metrics endpoint.
	EnableMetrics bool `json:"enable_metrics" yaml:"enable_metrics"`

	// EnableHealthz enables the /healthz endpoint.
	EnableHealthz bool `json:"enable_healthz" yaml:"enable_healthz"`

	// ApiToken enables bearer-token auth on /api/v1/* and /openapi* when
	// non-empty (#371-class controlled access): clients must send
	// `Authorization: Bearer <token>`. healthz/readyz/metrics stay open for
	// liveness probes. Empty = auth disabled (loopback-only deployments).
	ApiToken string `json:"api_token" yaml:"api_token"`
}

// DefaultConfig returns a sensible default configuration.
//
// Security defaults (#348, #659): the daemon binds loopback only and CORS is
// disabled unless explicitly enabled — the HTTP API is unauthenticated, so
// a wildcard bind plus permissive CORS exposed the control plane to the
// local network by default. External binds require explicit opt-in via
// config/flag and mandatory API token authentication.
func DefaultConfig() Config {
	return Config{
		Address:           "127.0.0.1:8080",
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		EnableCORS:        false,
		AllowedOrigins:    nil,
		EnableMetrics:     true,
		EnableHealthz:     true,
	}
}

// Validate validates the configuration.
//
// Security invariants (#348, #371, #659):
// 1. Literal IPv4 and IPv6 loopback addresses (127.0.0.0/8, ::1) are allowed without an API token.
// 2. Non-loopback addresses, wildcard binds (0.0.0.0, ::, empty host ":port"), and hostnames require a valid, non-whitespace API token.
// 3. Whitespace-only API tokens are strictly rejected.
// 4. Malformed addresses and invalid hostnames/ports are rejected conservatively.
func (c *Config) Validate() error {
	if c.Address == "" {
		c.Address = "127.0.0.1:8080"
	}
	if c.ReadHeaderTimeout <= 0 {
		c.ReadHeaderTimeout = 10 * time.Second
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 30 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 30 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 120 * time.Second
	}

	host, portStr, err := net.SplitHostPort(c.Address)
	if err != nil {
		return fmt.Errorf("invalid server address %q: %w", c.Address, err)
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port < 1 {
		return fmt.Errorf("invalid port %q in address %q: port must be between 1 and 65535", portStr, c.Address)
	}

	isLiteralLoopback := false
	if host != "" {
		if ip := net.ParseIP(host); ip != nil {
			isLiteralLoopback = ip.IsLoopback()
		} else {
			if !isValidHostname(host) {
				return fmt.Errorf("invalid host %q in address %q: must be a valid IP address or hostname", host, c.Address)
			}
			// Hostnames are conservatively treated as non-loopback; authentication is required.
			isLiteralLoopback = false
		}
	}

	if c.ApiToken != "" && strings.TrimSpace(c.ApiToken) == "" {
		return fmt.Errorf("invalid api_token: token cannot be whitespace-only")
	}

	if !isLiteralLoopback && strings.TrimSpace(c.ApiToken) == "" {
		return fmt.Errorf("unauthenticated bind to non-loopback address %q: api_token is required for non-loopback, wildcard, or hostname binds", c.Address)
	}

	return nil
}

// isValidHostname validates hostname syntax according to RFC 1123 / RFC 952.
func isValidHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		first := label[0]
		last := label[len(label)-1]
		if !isAlphanumeric(first) || !isAlphanumeric(last) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			c := label[i]
			if !isAlphanumeric(c) && c != '-' {
				return false
			}
		}
	}
	return true
}

func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ParseAddress parses the address string into host and port.
func ParseAddress(addr string) (host string, port string, err error) {
	return net.SplitHostPort(addr)
}
