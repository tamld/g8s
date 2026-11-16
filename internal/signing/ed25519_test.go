package signing

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS != "windows" {
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("key file too permissive: %o", fi.Mode().Perm())
		}
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

func TestCleanSQLiteFilePath(t *testing.T) {
	if got := CleanSQLiteFilePath(" file:/tmp/state.db?mode=rw&_pragma=busy_timeout(5000) "); got != "/tmp/state.db" {
		t.Fatalf("unexpected cleaned URI path: %q", got)
	}
	if got := CleanSQLiteFilePath("plain/state.db"); got != "plain/state.db" {
		t.Fatalf("unexpected cleaned plain path: %q", got)
	}
}

func TestVerifyDomainAndStratumTokenEdgeCases(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	_, pub, err := EnsureOrLoadF0Signer("")
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyDomain(pub, "badprefix:deadbeef", "g8s.test", "a"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for bad prefix, got %v", err)
	}
	if err := VerifyDomain(pub, "ed25519:v1:zzzz", "g8s.test", "a"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for invalid hex, got %v", err)
	}
	if err := VerifyDomain(pub, "ed25519:v1:deadbeef", "g8s.test", "a"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for short sig, got %v", err)
	}

	if err := VerifyStratumToken(pub, "bad:S2:999:sig", "S2", 1); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for bad token prefix, got %v", err)
	}
	if err := VerifyStratumToken(pub, "st1:S2", "S2", 1); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for malformed parts, got %v", err)
	}
	if err := VerifyStratumToken(pub, "st1:S2:notanint:ed25519:v1:deadbeef", "S2", 1); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for non-int expiry, got %v", err)
	}
}

func TestEnsureOrLoadF0SignerAndVerifierBranches(t *testing.T) {
	t.Setenv("G8S_TIER", "F0")
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	priv1, pub1, err := EnsureOrLoadF0Signer(dir)
	if err != nil {
		t.Fatal(err)
	}
	privCached, pubCached, err := EnsureOrLoadF0Signer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(priv1) != string(privCached) || string(pub1) != string(pubCached) {
		t.Fatal("cached keypair mismatch")
	}

	if runtime.GOOS != "windows" {
		keyPath := filepath.Join(dir, F0PrivateKeyFile)
		pubPath := filepath.Join(dir, F0PublicKeyFile)
		if err := os.Chmod(keyPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(pubPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		delete(diskSignerByState, dir)
		if _, _, err := EnsureOrLoadF0Signer(dir); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{keyPath, pubPath} {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm()&0o177 != 0 {
				t.Fatalf("%s still too permissive: %o", p, fi.Mode().Perm())
			}
		}
	}

	badDir1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(badDir1, F0PrivateKeyFile), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureOrLoadF0Signer(badDir1); err == nil {
		t.Fatal("expected error for short private key hex")
	}

	badDir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(badDir2, F0PrivateKeyFile), []byte(strings.Repeat("z", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureOrLoadF0Signer(badDir2); err == nil {
		t.Fatal("expected error for invalid private key hex")
	}

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureOrLoadF0Signer(filepath.Join(blocker, "sub")); err == nil {
		t.Fatal("expected MkdirAll error when parent is a file")
	}

	for _, mem := range []string{"", ":memory:"} {
		pub, err := LoadF0Verifier(mem)
		if err != nil || len(pub) == 0 {
			t.Fatalf("LoadF0Verifier(%q) failed: pub=%d err=%v", mem, len(pub), err)
		}
	}

	t.Setenv("G8S_TIER", "F1")
	if _, err := LoadF0Verifier(t.TempDir()); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature when F1 loads missing pub, got %v", err)
	}

	t.Setenv("G8S_TIER", "F0")
	freshDir := t.TempDir()
	bootPub, err := LoadF0Verifier(freshDir)
	if err != nil || len(bootPub) == 0 {
		t.Fatalf("LoadF0Verifier bootstrap failed: %v", err)
	}

	badPub1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(badPub1, F0PublicKeyFile), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadF0Verifier(badPub1); err == nil {
		t.Fatal("expected error for short public key hex")
	}

	badPub2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(badPub2, F0PublicKeyFile), []byte(strings.Repeat("z", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadF0Verifier(badPub2); err == nil {
		t.Fatal("expected error for invalid public key hex")
	}
}
