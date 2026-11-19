package signing

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
)

const (
	// S0PublicKeyFile stores the hex-encoded Ed25519 public key belonging to the S0 Human Principal.
	S0PublicKeyFile = "s0-human.pub"
	// S0PrivateKeyFile denotes the out-of-band human signing key filename protected as a TCB boundary.
	S0PrivateKeyFile = "s0-human.key"
	// S0ApprovalDomain domain-separates S0 TCB approval attestations from F0 worker/receipt signatures.
	S0ApprovalDomain = "g8s.s0_tcb_approval.v1"
)

// ErrS0KeyMatchesF0Key is returned when the pinned S0 human verification key collides with F0's key.
var ErrS0KeyMatchesF0Key = errors.New("signing: S0 human public key must be distinct from F0 coordinator public key")

// ParsePublicKeyHex parses a 64-character hex-encoded Ed25519 public key.
func ParsePublicKeyHex(s string) (ed25519.PublicKey, error) {
	return parsePublicKeyHex(s)
}

// LoadPublicKeyFile reads a hex-encoded Ed25519 public key from path without creating keys on miss.
func LoadPublicKeyFile(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signing: read public key file %q: %w", path, err)
	}
	return parsePublicKeyHex(string(data))
}

// VerifyDisjointS0Key enforces that s0Pub is a valid Ed25519 public key distinct from f0Pub.
func VerifyDisjointS0Key(s0Pub, f0Pub ed25519.PublicKey) error {
	if len(s0Pub) != ed25519.PublicKeySize {
		return ErrInvalidSignature
	}
	if len(f0Pub) == ed25519.PublicKeySize && subtle.ConstantTimeCompare(s0Pub, f0Pub) == 1 {
		return ErrS0KeyMatchesF0Key
	}
	return nil
}

// SignS0Approval signs canonical length-prefixed S0 TCB approval fields under S0ApprovalDomain.
func SignS0Approval(s0Priv ed25519.PrivateKey, fields ...string) string {
	return SignDomain(s0Priv, S0ApprovalDomain, fields...)
}

// VerifyS0Approval verifies an S0 TCB approval signature against s0Pub under S0ApprovalDomain.
func VerifyS0Approval(s0Pub ed25519.PublicKey, sig string, fields ...string) error {
	if len(s0Pub) != ed25519.PublicKeySize {
		return ErrInvalidSignature
	}
	return VerifyDomain(s0Pub, sig, S0ApprovalDomain, fields...)
}
