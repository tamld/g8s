package signing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Unsetenv("G8S_TIER")
	os.Exit(m.Run())
}

func TestDomainSignVerify(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	priv, pub, err := EnsureOrLoadF0Signer("")
	if err != nil {
		t.Fatal(err)
	}
	sig := SignDomain(priv, "g8s.test", "a", "b")
	if err := VerifyDomain(pub, sig, "g8s.test", "a", "b"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyDomain(pub, sig, "g8s.test", "a", "c"); err == nil {
		t.Fatal("expected tamper failure")
	}
}

func TestStratumToken(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	priv, pub, err := EnsureOrLoadF0Signer("")
	if err != nil {
		t.Fatal(err)
	}
	tok := SignStratumToken(priv, "S2", 9_999_999_999)
	if err := VerifyStratumToken(pub, tok, "S2", 100); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if err := VerifyStratumToken(pub, tok, "S3-M", 100); err == nil {
		t.Fatal("wrong stratum must fail")
	}
	if err := VerifyStratumToken(pub, tok, "S2", 9_999_999_999+1); err == nil {
		t.Fatal("expired token must fail")
	}
}

func TestF0KeyDiskRoundTrip(t *testing.T) {
	t.Setenv("G8S_TIER", "F0")
	dir := t.TempDir()
	priv1, pub1, err := EnsureOrLoadF0Signer(dir)
	if err != nil {
		t.Fatal(err)
	}
	delete(diskSignerByState, dir)
	priv2, pub2, err := EnsureOrLoadF0Signer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(priv1) != string(priv2) || string(pub1) != string(pub2) {
		t.Fatal("key not stable across reload")
	}
	pubOnly, err := LoadF0Verifier(dir)
	if err != nil || string(pubOnly) != string(pub1) {
		t.Fatalf("LoadF0Verifier: %v", err)
	}
	keyPath := filepath.Join(dir, F0PrivateKeyFile)
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key file too permissive: %o", fi.Mode().Perm())
	}
}

func TestWorkerCannotMintF0Key(t *testing.T) {
	t.Setenv("G8S_TIER", "F1")
	_, _, err := EnsureOrLoadF0Signer(t.TempDir())
	if err != ErrF0KeyAccessDenied {
		t.Fatalf("got %v", err)
	}
}

func TestIsInMemorySQLitePath(t *testing.T) {
	for _, p := range []string{"", ":memory:", "file:x?mode=memory", "dir/state.db"} {
		want := p == "" || p == ":memory:" || strings.Contains(p, "mode=memory")
		if got := IsInMemorySQLitePath(p); got != want {
			t.Fatalf("%q: got %v want %v", p, got, want)
		}
	}
}
