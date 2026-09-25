// Package server implements the g8s daemon mode with HTTP API server.
package server

import (
	"net"
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
}

// DefaultConfig returns a sensible default configuration.
//
// Security defaults (#348): the daemon binds loopback only and CORS is
// disabled unless explicitly enabled — the HTTP API is unauthenticated, so
// a wildcard bind plus permissive CORS exposed the control plane to the
// local network by default. External binds require explicit opt-in via
// config/flag.
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
	return nil
}

// ParseAddress parses the address string into host and port.
func ParseAddress(addr string) (host string, port string, err error) {
	return net.SplitHostPort(addr)
}
