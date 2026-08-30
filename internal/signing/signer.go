// Package signing provides code signing and signature verification primitives
// for g8s binaries and installation packages across supported operating systems.
package signing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimestamper is the default RFC 3161 timestamp authority URL.
const DefaultTimestamper = "http://timestamp.digicert.com"

// Signer defines the contract for signing and verifying binaries.
type Signer interface {
	Sign(ctx context.Context, path string) error
	Verify(path string) (bool, error)
}

// Runner executes an external command under context/timeout controls.
// Production wires execRunner; tests inject mocks.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execRunner executes commands using os/exec with context cancellation.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w (output: %s)", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// SigntoolSigner signs and verifies Windows binaries using Microsoft signtool.exe.
type SigntoolSigner struct {
	CertPath     string
	CertPassword string
	Timestamper  string
	Runner       Runner
}

var _ Signer = (*SigntoolSigner)(nil)

// NewSigntoolSigner creates a SigntoolSigner with default runner and timestamper.
func NewSigntoolSigner(certPath, timestamper string) *SigntoolSigner {
	if timestamper == "" {
		timestamper = DefaultTimestamper
	}
	return &SigntoolSigner{
		CertPath:    certPath,
		Timestamper: timestamper,
		Runner:      execRunner{},
	}
}

func (s *SigntoolSigner) runner() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return execRunner{}
}

func (s *SigntoolSigner) timestamper() string {
	if s.Timestamper != "" {
		return s.Timestamper
	}
	return DefaultTimestamper
}

// Sign signs the binary or installer at the given path using signtool.
func (s *SigntoolSigner) Sign(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("target path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("target file stat: %w", err)
	}
	if strings.TrimSpace(s.CertPath) == "" {
		return errors.New("certificate path is required for signing")
	}
	if _, err := os.Stat(s.CertPath); err != nil {
		return fmt.Errorf("certificate file stat: %w", err)
	}

	args := []string{
		"sign",
		"/tr", s.timestamper(),
		"/td", "sha256",
		"/fd", "sha256",
		"/a",
		"/f", s.CertPath,
	}

	if s.CertPassword != "" {
		args = append(args, "/p", s.CertPassword)
	}

	args = append(args, path)

	_, err := s.runner().Run(ctx, "signtool", args...)
	if err != nil {
		return fmt.Errorf("signtool sign: %w", err)
	}
	return nil
}

// Verify verifies the digital signature of the binary at the given path using signtool.
func (s *SigntoolSigner) Verify(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("target path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return false, fmt.Errorf("target file stat: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"verify", "/pa", path}
	_, err := s.runner().Run(ctx, "signtool", args...)
	if err != nil {
		return false, fmt.Errorf("signtool verify: %w", err)
	}
	return true, nil
}
