package fsops

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"

	"github.com/tamld/g8s/internal/dispatch"
)

const sanitizePropMaxCases = 200

func sanitizePropRand() *rand.Rand {
	return rand.New(rand.NewSource(43))
}

func genSecretPayload(r *rand.Rand) (input, secret string) {
	secretChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$"
	n := 8 + r.Intn(16)
	b := make([]byte, n)
	for i := range b {
		b[i] = secretChars[r.Intn(len(secretChars))]
	}
	secret = string(b)
	kind := r.Intn(4)
	switch kind {
	case 0:
		input = fmt.Sprintf("password=%s", secret)
	case 1:
		input = fmt.Sprintf("api_key: %s", secret)
	case 2:
		input = fmt.Sprintf(`token="%s"`, secret)
	default:
		input = fmt.Sprintf("postgresql://user:%s@db.example.com/app", secret)
	}
	return input, secret
}

func TestSanitizeOutputPropertySecretNeverLeaks(t *testing.T) {
	r := sanitizePropRand()
	cfg := &quick.Config{MaxCount: sanitizePropMaxCases, Rand: r}
	err := quick.Check(func(seed uint16) bool {
		input, secret := genSecretPayload(r)
		if len(secret) < 8 {
			return true
		}
		out := dispatch.SanitizeOutput(input)
		if strings.Contains(out, secret) {
			t.Fatalf("secret substring leaked: secret=%q input=%q out=%q", secret, input, out)
		}
		_ = seed
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeOutputPropertyIdempotent(t *testing.T) {
	r := sanitizePropRand()
	cfg := &quick.Config{MaxCount: sanitizePropMaxCases, Rand: r}
	err := quick.Check(func(seed uint16) bool {
		input, _ := genSecretPayload(r)
		plain := fmt.Sprintf("prefix %s suffix", input)
		once := dispatch.SanitizeOutput(plain)
		twice := dispatch.SanitizeOutput(once)
		if once != twice {
			t.Fatalf("idempotence: once=%q twice=%q", once, twice)
		}
		_ = seed
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeOutputPropertyIdempotentPublicNoise(t *testing.T) {
	r := sanitizePropRand()
	cfg := &quick.Config{MaxCount: sanitizePropMaxCases, Rand: r}
	err := quick.Check(func(n uint8) bool {
		base := fmt.Sprintf("token = False retries=%d timeout=None", n%3)
		s1 := dispatch.SanitizeOutput(base)
		s2 := dispatch.SanitizeOutput(s1)
		if s1 != s2 {
			t.Fatalf("public idempotence: %q -> %q -> %q", base, s1, s2)
		}
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}
