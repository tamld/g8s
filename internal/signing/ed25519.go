package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const (
	F0PrivateKeyFile = "f0-signing.key"
	F0PublicKeyFile  = "f0-signing.pub"
)

var (
	ErrInvalidSignature  = errors.New("signing: invalid or missing cryptographic signature")
	ErrF0KeyAccessDenied = errors.New("signing: worker tier F1/F2 cannot access or mint F0 signing key")
)

var (
	ephemeralOnce     sync.Once
	ephemeralPriv     ed25519.PrivateKey
	ephemeralPub      ed25519.PublicKey
	ephemeralInitErr  error
	diskSignerMu      sync.Mutex
	diskSignerByState = map[string]struct {
		priv ed25519.PrivateKey
		pub  ed25519.PublicKey
	}{}
)

func workerTierForbidden() bool {
	tier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER")))
	return tier == "F1" || tier == "F2"
}

// IsInMemorySQLitePath reports whether dbPath denotes an in-memory SQLite database.
func IsInMemorySQLitePath(dbPath string) bool {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || dbPath == ":memory:" {
		return true
	}
	return strings.Contains(dbPath, "mode=memory")
}

// CleanSQLiteFilePath strips file: URI prefixes and ?query suffixes from a SQLite DSN path.
func CleanSQLiteFilePath(dbPath string) string {
	dbPath = strings.TrimPrefix(strings.TrimSpace(dbPath), "file:")
	if idx := strings.Index(dbPath, "?"); idx >= 0 {
		dbPath = dbPath[:idx]
	}
	return dbPath
}

func ephemeralKeypair() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	ephemeralOnce.Do(func() {
		ephemeralPub, ephemeralPriv, ephemeralInitErr = ed25519.GenerateKey(rand.Reader)
	})
	return ephemeralPriv, ephemeralPub, ephemeralInitErr
}

func tightenDirPerm(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSticky != 0 || filepath.Clean(dir) == filepath.Clean(os.TempDir()) {
		return nil
	}
	perm := fi.Mode().Perm()
	if perm&0o077 != 0 {
		if err := os.Chmod(dir, perm&^0o077); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
	}
	return nil
}

func tightenFile0600(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if perm&0o177 != 0 {
		return os.Chmod(path, 0o600)
	}
	return nil
}

// EnsureOrLoadF0Signer loads or creates the F0 ed25519 keypair under stateDir.
func EnsureOrLoadF0Signer(stateDir string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if workerTierForbidden() {
		return nil, nil, ErrF0KeyAccessDenied
	}
	if stateDir == "" || IsInMemorySQLitePath(stateDir) {
		return ephemeralKeypair()
	}

	diskSignerMu.Lock()
	defer diskSignerMu.Unlock()
	if cached, ok := diskSignerByState[stateDir]; ok {
		return cached.priv, cached.pub, nil
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := tightenDirPerm(stateDir); err != nil {
		return nil, nil, err
	}

	keyPath := filepath.Join(stateDir, F0PrivateKeyFile)
	pubPath := filepath.Join(stateDir, F0PublicKeyFile)

	if data, err := os.ReadFile(keyPath); err == nil {
		priv, pub, err := parsePrivateKeyHex(string(data))
		if err != nil {
			return nil, nil, err
		}
		if err := tightenFile0600(keyPath); err != nil {
			return nil, nil, err
		}
		if _, statErr := os.Stat(pubPath); statErr == nil {
			if err := tightenFile0600(pubPath); err != nil {
				return nil, nil, err
			}
		}
		diskSignerByState[stateDir] = struct {
			priv ed25519.PrivateKey
			pub  ed25519.PublicKey
		}{priv: priv, pub: pub}
		return priv, pub, nil
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	privHex := hex.EncodeToString(priv)
	pubHex := hex.EncodeToString(pub)

	tmpKey := keyPath + ".tmp." + randomHex(8)
	f, err := os.OpenFile(tmpKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			os.Remove(tmpKey)
			if data, readErr := os.ReadFile(keyPath); readErr == nil {
				priv, pub, parseErr := parsePrivateKeyHex(string(data))
				if parseErr != nil {
					return nil, nil, parseErr
				}
				diskSignerByState[stateDir] = struct {
					priv ed25519.PrivateKey
					pub  ed25519.PublicKey
				}{priv: priv, pub: pub}
				return priv, pub, nil
			}
		}
		return nil, nil, err
	}
	if _, err := io.WriteString(f, privHex); err != nil {
		f.Close()
		os.Remove(tmpKey)
		return nil, nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpKey)
		return nil, nil, err
	}
	if err := os.Rename(tmpKey, keyPath); err != nil {
		os.Remove(tmpKey)
		if data, readErr := os.ReadFile(keyPath); readErr == nil {
			priv, pub, parseErr := parsePrivateKeyHex(string(data))
			if parseErr != nil {
				return nil, nil, parseErr
			}
			diskSignerByState[stateDir] = struct {
				priv ed25519.PrivateKey
				pub  ed25519.PublicKey
			}{priv: priv, pub: pub}
			return priv, pub, nil
		}
		return nil, nil, err
	}

	tmpPub := pubPath + ".tmp." + randomHex(8)
	pf, err := os.OpenFile(tmpPub, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if _, err := io.WriteString(pf, pubHex); err != nil {
		pf.Close()
		os.Remove(tmpPub)
		return nil, nil, err
	}
	if err := pf.Close(); err != nil {
		os.Remove(tmpPub)
		return nil, nil, err
	}
	if err := os.Rename(tmpPub, pubPath); err != nil {
		return nil, nil, err
	}

	if err := tightenFile0600(keyPath); err != nil {
		return nil, nil, err
	}
	if err := tightenFile0600(pubPath); err != nil {
		return nil, nil, err
	}

	diskSignerByState[stateDir] = struct {
		priv ed25519.PrivateKey
		pub  ed25519.PublicKey
	}{priv: priv, pub: pub}
	return priv, pub, nil
}

func parsePrivateKeyHex(s string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if len(s) != 128 {
		return nil, nil, fmt.Errorf("signing: private key hex must be 128 chars, got %d", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("signing: invalid private key hex: %w", err)
	}
	priv := ed25519.PrivateKey(raw)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, ErrInvalidSignature
	}
	return priv, pub, nil
}

func parsePublicKeyHex(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return nil, fmt.Errorf("signing: public key hex must be 64 chars, got %d", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("signing: invalid public key hex: %w", err)
	}
	return ed25519.PublicKey(raw), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// LoadF0Verifier loads the F0 public verification key from stateDir.
func LoadF0Verifier(stateDir string) (ed25519.PublicKey, error) {
	if stateDir == "" || IsInMemorySQLitePath(stateDir) {
		_, pub, err := EnsureOrLoadF0Signer("")
		return pub, err
	}

	pubPath := filepath.Join(stateDir, F0PublicKeyFile)
	if data, err := os.ReadFile(pubPath); err == nil {
		return parsePublicKeyHex(string(data))
	}

	if workerTierForbidden() {
		return nil, ErrInvalidSignature
	}
	_, pub, err := EnsureOrLoadF0Signer(stateDir)
	return pub, err
}

func domainBuffer(domain string, fields []string) []byte {
	var buf []byte
	buf = append(buf, domain...)
	buf = append(buf, 0)
	for _, f := range fields {
		part := fmt.Sprintf("%d:%s\x00", len(f), f)
		buf = append(buf, part...)
	}
	return buf
}

// SignDomain signs canonical length-prefixed domain fields and returns an ed25519:v1 tag.
func SignDomain(priv ed25519.PrivateKey, domain string, fields ...string) string {
	sig := ed25519.Sign(priv, domainBuffer(domain, fields))
	return "ed25519:v1:" + hex.EncodeToString(sig)
}

// VerifyDomain verifies a SignDomain-produced signature.
func VerifyDomain(pub ed25519.PublicKey, sig string, domain string, fields ...string) error {
	if !strings.HasPrefix(sig, "ed25519:v1:") {
		return ErrInvalidSignature
	}
	hexSig := strings.TrimPrefix(sig, "ed25519:v1:")
	raw, err := hex.DecodeString(hexSig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(pub, domainBuffer(domain, fields), raw) {
		return ErrInvalidSignature
	}
	return nil
}

// SignStratumToken mints a signed stratum capability token.
func SignStratumToken(priv ed25519.PrivateKey, stratum string, expiresUnix int64) string {
	domainSig := SignDomain(priv, "g8s.stratum_token.v1", stratum, strconv.FormatInt(expiresUnix, 10))
	return fmt.Sprintf("st1:%s:%d:%s", stratum, expiresUnix, domainSig)
}

// VerifyStratumToken validates a stratum capability token.
func VerifyStratumToken(pub ed25519.PublicKey, token string, wantStratum string, nowUnix int64) error {
	if !strings.HasPrefix(token, "st1:") {
		return ErrInvalidSignature
	}
	rest := strings.TrimPrefix(token, "st1:")
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return ErrInvalidSignature
	}
	gotStratum, expiresStr, sig := parts[0], parts[1], parts[2]
	if gotStratum != wantStratum {
		return ErrInvalidSignature
	}
	expiresUnix, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	if nowUnix > expiresUnix {
		return ErrInvalidSignature
	}
	return VerifyDomain(pub, sig, "g8s.stratum_token.v1", gotStratum, expiresStr)
}
