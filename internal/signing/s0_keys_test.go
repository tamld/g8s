package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestS0ApprovalSignVerify(t *testing.T) {
	s0Pub, s0Priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey s0: %v", err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey other: %v", err)
	}

	fields := []string{"human:tamld", "S0", "nonce-1234567890abcd", "internal/harness/trust_root.go"}
	sig := SignS0Approval(s0Priv, fields...)

	if err := VerifyS0Approval(s0Pub, sig, fields...); err != nil {
		t.Fatalf("VerifyS0Approval failed on valid signature: %v", err)
	}

	// Tampered field must fail.
	tampered := []string{"human:tamld", "S0", "nonce-1234567890abcd", "internal/sandbox/sandbox.go"}
	if err := VerifyS0Approval(s0Pub, sig, tampered...); err == nil {
		t.Fatal("expected tampered field verification failure")
	}

	// Wrong public key must fail.
	if err := VerifyS0Approval(otherPub, sig, fields...); err == nil {
		t.Fatal("expected wrong public key verification failure")
	}

	// Short/nil public key must fail without panicking.
	if err := VerifyS0Approval(nil, sig, fields...); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for nil pubkey, got %v", err)
	}

	// Cross-domain signature reuse must fail.
	otherDomainSig := SignDomain(s0Priv, "g8s.other_domain.v1", fields...)
	if err := VerifyS0Approval(s0Pub, otherDomainSig, fields...); err == nil {
		t.Fatal("expected cross-domain signature rejection")
	}
}

func TestVerifyDisjointS0Key(t *testing.T) {
	s0Pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey s0: %v", err)
	}
	f0Pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey f0: %v", err)
	}

	if err := VerifyDisjointS0Key(s0Pub, f0Pub); err != nil {
		t.Fatalf("distinct S0/F0 keys failed: %v", err)
	}
	if err := VerifyDisjointS0Key(s0Pub, nil); err != nil {
		t.Fatalf("distinct S0 key with empty F0 key failed: %v", err)
	}
	if err := VerifyDisjointS0Key(s0Pub, append(ed25519.PublicKey(nil), s0Pub...)); !errors.Is(err, ErrS0KeyMatchesF0Key) {
		t.Fatalf("expected ErrS0KeyMatchesF0Key, got %v", err)
	}
	if err := VerifyDisjointS0Key(ed25519.PublicKey{1, 2, 3}, f0Pub); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for malformed S0 key, got %v", err)
	}
}

func TestParseAndLoadPublicKeyFile(t *testing.T) {
	s0Pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubHex := hex.EncodeToString(s0Pub)

	parsed, err := ParsePublicKeyHex("  " + pubHex + "\n")
	if err != nil {
		t.Fatalf("ParsePublicKeyHex: %v", err)
	}
	if string(parsed) != string(s0Pub) {
		t.Fatalf("parsed key mismatch")
	}

	dir := t.TempDir()
	pubPath := filepath.Join(dir, S0PublicKeyFile)
	if err := os.WriteFile(pubPath, []byte(pubHex+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, err := LoadPublicKeyFile(pubPath)
	if err != nil {
		t.Fatalf("LoadPublicKeyFile valid: %v", err)
	}
	if string(loaded) != string(s0Pub) {
		t.Fatalf("loaded key mismatch")
	}

	badPath := filepath.Join(dir, "bad.pub")
	if err := os.WriteFile(badPath, []byte("not-hex"), 0o600); err != nil {
		t.Fatalf("WriteFile bad: %v", err)
	}
	if _, err := LoadPublicKeyFile(badPath); err == nil {
		t.Fatal("expected error for invalid hex pubkey file")
	}

	missingPath := filepath.Join(dir, "missing.pub")
	if _, err := LoadPublicKeyFile(missingPath); err == nil {
		t.Fatal("expected error for missing pubkey file")
	}
	if _, statErr := os.Stat(missingPath); !os.IsNotExist(statErr) {
		t.Fatalf("LoadPublicKeyFile must never mint missing file; stat err=%v", statErr)
	}
}
