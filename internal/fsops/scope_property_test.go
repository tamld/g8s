package fsops

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"

	"github.com/tamld/g8s/internal/search"
)

const scopePropMaxCases = 200

func scopePropRand() *rand.Rand {
	return rand.New(rand.NewSource(42))
}

func genRelPath(r *rand.Rand) string {
	nSeg := 1 + r.Intn(3)
	parts := make([]string, nSeg)
	for i := range parts {
		n := 1 + r.Intn(6)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte('a' + r.Intn(26))
		}
		parts[i] = string(b)
	}
	exts := []string{".go", ".txt", ".md", ""}
	return strings.Join(parts, "/") + exts[r.Intn(len(exts))]
}

func touchRelFile(scopeRoot, rel string) error {
	full := filepath.Join(scopeRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte("x"), 0o644)
}

func scopedRel(seed uint16, r *rand.Rand) string {
	return fmt.Sprintf("case%d/%s", seed, genRelPath(r))
}

// P1: paths matching an allowed glob pass VerifyScope when the file exists in scope.
func TestVerifyScopePropertyMatchingGlobAllowed(t *testing.T) {
	scopeRoot := t.TempDir()
	r := scopePropRand()

	cfg := &quick.Config{MaxCount: scopePropMaxCases, Rand: r}
	err := quick.Check(func(seed uint16) bool {
		rel := scopedRel(seed, r)
		if err := touchRelFile(scopeRoot, rel); err != nil {
			t.Fatalf("setup file: %v", err)
		}
		allowed := []string{"**"}
		if !search.MatchesAllowedPaths(rel, allowed) {
			return true
		}
		got, vErr := VerifyScope(filepath.Join(scopeRoot, rel), scopeRoot, allowed)
		if vErr != nil {
			t.Fatalf("P1: rel=%q allowed=%v: %v", rel, allowed, vErr)
		}
		if got == "" || strings.HasPrefix(got, "../") {
			t.Fatalf("P1: bad relative path %q for %q", got, rel)
		}
		_ = seed
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}

// P2: paths outside every allowed glob are denied.
func TestVerifyScopePropertyNonMatchingGlobDenied(t *testing.T) {
	scopeRoot := t.TempDir()
	r := scopePropRand()

	cfg := &quick.Config{MaxCount: scopePropMaxCases, Rand: r}
	err := quick.Check(func(seed uint16) bool {
		rel := scopedRel(seed, r)
		if err := touchRelFile(scopeRoot, rel); err != nil {
			t.Fatalf("setup file: %v", err)
		}
		allowed := []string{"only-prefix/" + strings.Split(rel, "/")[0] + "/*.zzz"}
		if search.MatchesAllowedPaths(rel, allowed) {
			return true
		}
		_, vErr := VerifyScope(filepath.Join(scopeRoot, rel), scopeRoot, allowed)
		if vErr == nil || !strings.Contains(vErr.Error(), "outside receipt allowed_paths") {
			t.Fatalf("P2: expected denial for rel=%q allowed=%v, got %v", rel, allowed, vErr)
		}
		_ = seed
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}

// P3: relative and absolute inputs normalize to the same successful relative path.
func TestVerifyScopePropertyRelativeAbsoluteConsistency(t *testing.T) {
	scopeRoot := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(scopeRoot); err != nil {
		t.Fatal(err)
	}
	r := scopePropRand()

	cfg := &quick.Config{MaxCount: scopePropMaxCases, Rand: r}
	if checkErr := quick.Check(func(seed uint16) bool {
		rel := scopedRel(seed, r)
		if err := touchRelFile(scopeRoot, rel); err != nil {
			t.Fatalf("setup file: %v", err)
		}
		abs := filepath.Join(scopeRoot, filepath.FromSlash(rel))
		relIn, err := filepath.Rel(scopeRoot, abs)
		if err != nil {
			return true
		}
		fromRel, errRel := VerifyScope(relIn, scopeRoot, nil)
		fromAbs, errAbs := VerifyScope(abs, scopeRoot, nil)
		if (errRel == nil) != (errAbs == nil) {
			t.Fatalf("P3: rel/abs error mismatch rel=%q: relErr=%v absErr=%v", rel, errRel, errAbs)
		}
		if errRel != nil {
			return true
		}
		if fromRel != fromAbs {
			t.Fatalf("P3: rel=%q fromRel=%q fromAbs=%q", rel, fromRel, fromAbs)
		}
		_ = seed
		return true
	}, cfg); checkErr != nil {
		t.Fatal(checkErr)
	}
}
