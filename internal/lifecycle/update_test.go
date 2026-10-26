package lifecycle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// fakeRelease serves a GitHub-shaped API plus asset downloads over TLS.
type fakeRelease struct {
	tag       string
	archives  map[string][]byte
	checksums string // overrides the computed manifest when non-empty
	noSums    bool
	status    int
	hits      map[string]int
}

func (f *fakeRelease) server(t *testing.T) *httptest.Server {
	t.Helper()
	f.hits = map[string]int{}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits[r.URL.Path]++
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		switch {
		case r.URL.Path == "/repos/tamld/g8s/releases/latest" || r.URL.Path == "/repos/tamld/g8s/releases/tags/"+f.tag:
			rel := Release{TagName: f.tag, HTMLURL: srv.URL + "/rel/" + f.tag}
			for name := range f.archives {
				rel.Assets = append(rel.Assets, Asset{Name: name, URL: srv.URL + "/dl/" + name})
			}
			if !f.noSums {
				rel.Assets = append(rel.Assets, Asset{Name: ChecksumsAsset, URL: srv.URL + "/dl/" + ChecksumsAsset})
			}
			_ = json.NewEncoder(w).Encode(rel)
		case r.URL.Path == "/dl/"+ChecksumsAsset:
			if f.checksums != "" {
				_, _ = w.Write([]byte(f.checksums))
				return
			}
			for name, data := range f.archives {
				fmt.Fprintf(w, "%s  %s\n", sha(data), name)
			}
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			data, ok := f.archives[strings.TrimPrefix(r.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTarget(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "g8s")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUpdateApplyTable(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho new\n")
	goodArchive := tarGz(t, map[string][]byte{"README.md": []byte("r"), "g8s": newBin})
	cases := []struct {
		name        string
		current     string
		version     string
		force       bool
		mutate      func(f *fakeRelease)
		smoke       func(string) error
		wantErr     error
		wantErrSub  string
		wantApplied bool
		wantContent string
		wantRB      bool
	}{
		{name: "newer release installs and keeps backup", current: "0.20.0", wantApplied: true, wantContent: string(newBin)},
		{name: "same version is a no-op", current: "0.21.0", wantContent: "old"},
		{name: "same version with force reinstalls", current: "0.21.0", force: true, wantApplied: true, wantContent: string(newBin)},
		{name: "pinned version resolves tag endpoint", current: "0.20.0", version: "0.21.0", wantApplied: true, wantContent: string(newBin)},
		{
			name: "checksum mismatch fails closed", current: "0.20.0",
			mutate:  func(f *fakeRelease) { f.checksums = strings.Repeat("0", 64) + "  g8s_0.21.0_linux_amd64.tar.gz\n" },
			wantErr: ErrChecksumMismatch, wantContent: "old",
		},
		{
			name: "missing checksums asset fails closed", current: "0.20.0",
			mutate:  func(f *fakeRelease) { f.noSums = true },
			wantErr: ErrChecksumMissing, wantContent: "old",
		},
		{
			name: "archive not listed in checksums fails closed", current: "0.20.0",
			mutate:  func(f *fakeRelease) { f.checksums = strings.Repeat("a", 64) + "  other.tar.gz\n" },
			wantErr: ErrChecksumMissing, wantContent: "old",
		},
		{
			name: "no asset for platform", current: "0.20.0",
			mutate: func(f *fakeRelease) {
				f.archives = map[string][]byte{"g8s_0.21.0_plan9_amd64.tar.gz": goodArchive}
			},
			wantErr: ErrAssetNotFound, wantContent: "old",
		},
		{
			name: "archive without binary fails", current: "0.20.0",
			mutate: func(f *fakeRelease) {
				f.archives = map[string][]byte{"g8s_0.21.0_linux_amd64.tar.gz": tarGz(t, map[string][]byte{"README.md": []byte("x")})}
			},
			wantErr: ErrBinaryNotInBundle, wantContent: "old",
		},
		{
			name: "smoke failure rolls back", current: "0.20.0",
			smoke:  func(string) error { return errors.New("boom") },
			wantRB: true, wantErrSub: "E_UPDATE_SMOKE_FAILED", wantContent: "old",
		},
		{
			name: "http error surfaces", current: "0.20.0",
			mutate:     func(f *fakeRelease) { f.status = http.StatusForbidden },
			wantErrSub: "unexpected HTTP status 403", wantContent: "old",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRelease{tag: "v0.21.0", archives: map[string][]byte{"g8s_0.21.0_linux_amd64.tar.gz": goodArchive}}
			if tc.mutate != nil {
				tc.mutate(f)
			}
			srv := f.server(t)
			target := newTarget(t, "old")
			u := &Updater{
				Client: srv.Client(), APIBase: srv.URL, CurrentVersion: tc.current,
				GOOS: "linux", GOARCH: "amd64", TargetPath: target, Smoke: tc.smoke, Token: "tkn",
			}
			res, err := u.Apply(context.Background(), tc.version, tc.force)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v want %v", err, tc.wantErr)
				}
			case tc.wantErrSub != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err=%v want substring %q", err, tc.wantErrSub)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
			}
			got, rerr := os.ReadFile(target)
			if rerr != nil {
				t.Fatalf("target missing after update: %v", rerr)
			}
			if string(got) != tc.wantContent {
				t.Errorf("target content=%q want %q", got, tc.wantContent)
			}
			if res != nil && res.Applied != tc.wantApplied {
				t.Errorf("Applied=%v want %v", res.Applied, tc.wantApplied)
			}
			if res != nil && res.RolledBack != tc.wantRB {
				t.Errorf("RolledBack=%v want %v", res.RolledBack, tc.wantRB)
			}
			if tc.wantApplied {
				bak, err := os.ReadFile(BackupPath(target))
				if err != nil || string(bak) != "old" {
					t.Errorf("backup=%q err=%v want old", bak, err)
				}
				if res.ActualSHA256 != sha(goodArchive) || res.ExpectedSHA256 != res.ActualSHA256 {
					t.Errorf("sha fields not populated: %+v", res)
				}
				info, _ := os.Stat(target)
				if info.Mode().Perm()&0o100 == 0 {
					t.Errorf("installed binary not executable: %v", info.Mode())
				}
			}
			if tc.version != "" && f.hits["/repos/tamld/g8s/releases/tags/v0.21.0"] == 0 {
				t.Errorf("pinned version did not hit tag endpoint: %v", f.hits)
			}
		})
	}
}

func TestUpdateCheckMakesNoChanges(t *testing.T) {
	f := &fakeRelease{tag: "v0.21.0", archives: map[string][]byte{"g8s_0.21.0_darwin_all.tar.gz": tarGz(t, map[string][]byte{"g8s": []byte("n")})}}
	srv := f.server(t)
	target := newTarget(t, "old")
	before, _ := os.ReadDir(filepath.Dir(target))
	u := &Updater{Client: srv.Client(), APIBase: srv.URL, CurrentVersion: "0.20.0", GOOS: "darwin", GOARCH: "arm64", TargetPath: target}
	res, _, err := u.Check(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpdateAvailable || !res.CheckOnly || res.Applied || res.Asset != "g8s_0.21.0_darwin_all.tar.gz" {
		t.Fatalf("unexpected check result: %+v", res)
	}
	after, _ := os.ReadDir(filepath.Dir(target))
	if len(before) != len(after) {
		t.Fatalf("check mutated target dir: before=%d after=%d", len(before), len(after))
	}
	for path := range f.hits {
		if strings.HasPrefix(path, "/dl/") {
			t.Errorf("--check downloaded %s", path)
		}
	}
	// Older remote than current: no update.
	u.CurrentVersion = "0.22.0"
	res, _, err = u.Check(context.Background(), "")
	if err != nil || res.UpdateAvailable {
		t.Fatalf("downgrade must not be offered without pin: %+v err=%v", res, err)
	}
}

func TestUpdateRejectsInsecureAPIBase(t *testing.T) {
	u := &Updater{APIBase: "http://example.invalid", CurrentVersion: "0.1.0"}
	if _, err := u.ResolveRelease(context.Background(), ""); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("err=%v want ErrInsecureURL", err)
	}
	if err := requireHTTPS("://bad"); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("err=%v want ErrInsecureURL", err)
	}
}

func TestUpdateDownloadLimit(t *testing.T) {
	f := &fakeRelease{tag: "v0.21.0", archives: map[string][]byte{"g8s_0.21.0_linux_amd64.tar.gz": bytes.Repeat([]byte("x"), 4096)}}
	srv := f.server(t)
	u := &Updater{Client: srv.Client(), APIBase: srv.URL, CurrentVersion: "0.20.0", GOOS: "linux", GOARCH: "amd64", TargetPath: newTarget(t, "old"), MaxDownloadBytes: 1024}
	if _, err := u.Apply(context.Background(), "", false); !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("err=%v want ErrDownloadTooLarge", err)
	}
}

func TestResolveReleaseDecodeErrors(t *testing.T) {
	for _, body := range []string{"not json", `{"tag_name":""}`} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		u := &Updater{Client: srv.Client(), APIBase: srv.URL}
		if _, err := u.ResolveRelease(context.Background(), ""); err == nil {
			t.Errorf("body %q: expected decode error", body)
		}
		srv.Close()
	}
}

func TestAssetCandidatesAndSelect(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         []string
	}{
		{"darwin", "arm64", []string{"g8s_1.2.3_darwin_all.tar.gz", "g8s_1.2.3_darwin_arm64.tar.gz"}},
		{"linux", "arm64", []string{"g8s_1.2.3_linux_arm64.tar.gz"}},
		{"windows", "amd64", []string{"g8s_1.2.3_windows_amd64.zip"}},
	}
	for _, tc := range cases {
		got := AssetCandidates("v1.2.3", tc.goos, tc.goarch)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s/%s: got %v want %v", tc.goos, tc.goarch, got, tc.want)
		}
	}
	rel := &Release{TagName: "v1.2.3", Assets: []Asset{{Name: "g8s_1.2.3_darwin_arm64.tar.gz"}}}
	a, err := SelectAsset(rel, "darwin", "arm64")
	if err != nil || a.Name != "g8s_1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("per-arch darwin fallback failed: %v %v", a, err)
	}
}

func TestParseChecksumTable(t *testing.T) {
	good := strings.Repeat("ab", 32)
	cases := []struct {
		name, manifest string
		want           string
		wantErr        bool
	}{
		{"plain", good + "  f.tar.gz\n", good, false},
		{"binary marker", good + " *f.tar.gz\n", good, false},
		{"uppercase normalised", strings.ToUpper(good) + "  f.tar.gz\n", good, false},
		{"duplicate identical ok", good + "  f.tar.gz\n" + good + "  f.tar.gz\n", good, false},
		{"conflicting duplicate", good + "  f.tar.gz\n" + strings.Repeat("cd", 32) + "  f.tar.gz\n", "", true},
		{"short digest", "abcd  f.tar.gz\n", "", true},
		{"non hex", strings.Repeat("zz", 32) + "  f.tar.gz\n", "", true},
		{"absent", good + "  other\n", "", true},
		{"garbage lines ignored", "junk\n\n" + good + "  f.tar.gz\n", good, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChecksum([]byte(tc.manifest), "f.tar.gz")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExtractBinaryTable(t *testing.T) {
	bin := []byte("BIN")
	cases := []struct {
		name, asset, goos string
		archive           []byte
		want              string
		wantErr           bool
	}{
		{"tar nested dir", "x.tar.gz", "linux", tarGz(t, map[string][]byte{"dist/g8s": bin}), "BIN", false},
		{"zip windows exe", "x.zip", "windows", zipArchive(t, map[string][]byte{"g8s.exe": bin, "README.md": []byte("r")}), "BIN", false},
		{"zip traversal name is inert", "x.zip", "windows", zipArchive(t, map[string][]byte{"../../g8s.exe": bin}), "BIN", false},
		{"zip missing exe", "x.zip", "windows", zipArchive(t, map[string][]byte{"g8s": bin}), "", true},
		{"corrupt gzip", "x.tar.gz", "linux", []byte("nope"), "", true},
		{"corrupt zip", "x.zip", "windows", []byte("nope"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractBinary(tc.archive, tc.asset, tc.goos, 0)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if string(got) != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
	if _, err := ExtractBinary(tarGz(t, map[string][]byte{"g8s": bytes.Repeat([]byte("x"), 100)}), "x.tar.gz", "linux", 10); !errors.Is(err, ErrDownloadTooLarge) {
		t.Errorf("tar size limit: err=%v", err)
	}
	if _, err := ExtractBinary(zipArchive(t, map[string][]byte{"g8s.exe": bytes.Repeat([]byte("x"), 100)}), "x.zip", "windows", 10); !errors.Is(err, ErrDownloadTooLarge) {
		t.Errorf("zip size limit: err=%v", err)
	}
}

func TestCompareVersionsTable(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "1.2.3", 0},
		{"0.21.0", "0.20.9", 1},
		{"0.9.0", "0.10.0", -1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-rc2", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc1", 0},
		{"1.2", "1.2.0", 0},
		{"dev", "0.1.0", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if NormalizeTag(" ") != "" || NormalizeTag("1.0.0") != "v1.0.0" || NormalizeTag("v2") != "v2" {
		t.Error("NormalizeTag mismatch")
	}
}

func TestReplaceBinaryAndRollbackTable(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			target := newTarget(t, "old")
			if err := os.WriteFile(BackupPath(target), []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}
			backup, err := ReplaceBinary(target, []byte("new"), goos)
			if err != nil {
				t.Fatalf("ReplaceBinary: %v", err)
			}
			if b, _ := os.ReadFile(target); string(b) != "new" {
				t.Fatalf("target=%q want new", b)
			}
			if b, _ := os.ReadFile(backup); string(b) != "old" {
				t.Fatalf("backup=%q want old", b)
			}
			entries, _ := os.ReadDir(filepath.Dir(target))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".g8s-update-") {
					t.Errorf("staging file leaked: %s", e.Name())
				}
			}
			if err := Rollback(target, backup, goos); err != nil {
				t.Fatalf("Rollback: %v", err)
			}
			if b, _ := os.ReadFile(target); string(b) != "old" {
				t.Fatalf("after rollback target=%q want old", b)
			}
			if err := Rollback(target, backup, goos); err == nil {
				t.Error("second rollback without backup must fail")
			}
		})
	}
	if _, err := ReplaceBinary(filepath.Join(t.TempDir(), "missing"), []byte("x"), "linux"); err == nil {
		t.Error("missing target must fail")
	}
	if _, err := ReplaceBinary(t.TempDir(), []byte("x"), "linux"); err == nil {
		t.Error("directory target must fail")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(src, dst, 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" {
		t.Fatalf("dst=%q", b)
	}
	if err := copyFile(src, dst, 0o600); err == nil {
		t.Error("O_EXCL copy over existing file must fail")
	}
	if err := copyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "x"), 0o600); err == nil {
		t.Error("missing source must fail")
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("payload")
	if _, err := VerifySHA256(data, strings.ToUpper(sha(data))); err != nil {
		t.Fatalf("uppercase expected digest should verify: %v", err)
	}
	if _, err := VerifySHA256(data, sha([]byte("other"))); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err=%v want mismatch", err)
	}
}
