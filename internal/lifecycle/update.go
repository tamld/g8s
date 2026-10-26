// Package lifecycle implements the DELTA-40 binary lifecycle surface: self-update from GitHub releases with fail-closed SHA-256 verification and atomic binary replacement, plus a dry-run-first uninstall planner.
//
// Every network call flows through the injectable HTTPDoer seam and every
// filesystem root is passed in explicitly, so the whole package is exercised
// offline by httptest servers and t.TempDir() fixtures.
package lifecycle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// DefaultRepo is the GitHub owner/name that publishes g8s releases.
	DefaultRepo = "tamld/g8s"
	// DefaultAPIBase is the GitHub REST API root.
	DefaultAPIBase = "https://api.github.com"
	// ChecksumsAsset is the goreleaser checksum manifest name.
	ChecksumsAsset = "checksums.txt"
	// DefaultMaxDownloadBytes bounds archive and checksum downloads (256 MiB).
	DefaultMaxDownloadBytes int64 = 256 << 20
)

// Sentinel errors let callers and tests branch on fail-closed causes.
var (
	ErrChecksumMismatch  = errors.New("E_UPDATE_CHECKSUM_MISMATCH: archive SHA-256 does not match checksums.txt")
	ErrChecksumMissing   = errors.New("E_UPDATE_CHECKSUM_MISSING: release has no verifiable checksum for the selected archive")
	ErrAssetNotFound     = errors.New("E_UPDATE_ASSET_NOT_FOUND: release has no archive for this OS/arch")
	ErrInsecureURL       = errors.New("E_UPDATE_INSECURE_URL: refusing non-https download URL")
	ErrDownloadTooLarge  = errors.New("E_UPDATE_DOWNLOAD_TOO_LARGE: download exceeds size limit")
	ErrBinaryNotInBundle = errors.New("E_UPDATE_BINARY_NOT_IN_ARCHIVE: archive does not contain a g8s binary")
)

// HTTPDoer is the network seam; *http.Client satisfies it.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Asset is one downloadable file attached to a GitHub release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is the subset of GitHub release metadata the updater consumes.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// UpdateResult is the JSON payload emitted by `g8s update`.
type UpdateResult struct {
	CheckOnly       bool   `json:"check_only"`
	CurrentVersion  string `json:"current_version"`
	TargetVersion   string `json:"target_version"`
	UpdateAvailable bool   `json:"update_available"`
	Applied         bool   `json:"applied"`
	RolledBack      bool   `json:"rolled_back,omitempty"`
	Asset           string `json:"asset,omitempty"`
	AssetURL        string `json:"asset_url,omitempty"`
	ExpectedSHA256  string `json:"expected_sha256,omitempty"`
	ActualSHA256    string `json:"actual_sha256,omitempty"`
	TargetPath      string `json:"target_path,omitempty"`
	BackupPath      string `json:"backup_path,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	Message         string `json:"message,omitempty"`
}

// Updater resolves, downloads, verifies and installs g8s releases.
type Updater struct {
	Client         HTTPDoer
	APIBase        string
	Repo           string
	CurrentVersion string
	GOOS           string
	GOARCH         string
	// TargetPath is the binary to replace; empty means the resolved running executable.
	TargetPath string
	// MaxDownloadBytes bounds every download; zero means DefaultMaxDownloadBytes.
	MaxDownloadBytes int64
	// Token is an optional GitHub API token sent only to APIBase requests.
	Token string
	// Smoke, when set, runs against the freshly installed binary; a non-nil
	// error triggers an automatic rollback to the backup.
	Smoke func(binaryPath string) error
}

func (u *Updater) defaults() {
	if u.Client == nil {
		u.Client = http.DefaultClient
	}
	if u.APIBase == "" {
		u.APIBase = DefaultAPIBase
	}
	if u.Repo == "" {
		u.Repo = DefaultRepo
	}
	if u.GOOS == "" {
		u.GOOS = runtime.GOOS
	}
	if u.GOARCH == "" {
		u.GOARCH = runtime.GOARCH
	}
	if u.MaxDownloadBytes <= 0 {
		u.MaxDownloadBytes = DefaultMaxDownloadBytes
	}
}

// NormalizeTag returns the canonical "vX.Y.Z" form of a version string.
func NormalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// requireHTTPS fails closed on any URL that is not https.
func requireHTTPS(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrInsecureURL, raw, err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%w: %q", ErrInsecureURL, raw)
	}
	return nil
}

func (u *Updater) get(ctx context.Context, rawURL string, api bool) ([]byte, error) {
	if err := requireHTTPS(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", "g8s-cli/"+u.CurrentVersion)
	if api {
		req.Header.Set("Accept", "application/vnd.github+json")
		if u.Token != "" {
			req.Header.Set("Authorization", "Bearer "+u.Token)
		}
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected HTTP status %d", rawURL, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, u.MaxDownloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if int64(len(data)) > u.MaxDownloadBytes {
		return nil, fmt.Errorf("%w: %s > %d bytes", ErrDownloadTooLarge, rawURL, u.MaxDownloadBytes)
	}
	return data, nil
}

// ResolveRelease fetches the latest release, or the release tagged version when non-empty.
func (u *Updater) ResolveRelease(ctx context.Context, version string) (*Release, error) {
	u.defaults()
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(u.APIBase, "/"), u.Repo)
	if tag := NormalizeTag(version); tag != "" {
		endpoint = fmt.Sprintf("%s/repos/%s/releases/tags/%s", strings.TrimRight(u.APIBase, "/"), u.Repo, url.PathEscape(tag))
	}
	data, err := u.get(ctx, endpoint, true)
	if err != nil {
		return nil, fmt.Errorf("resolve release: %w", err)
	}
	var rel Release
	if err := json.Unmarshal(data, &rel); err != nil {
		return nil, fmt.Errorf("decode release metadata: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("decode release metadata: empty tag_name")
	}
	return &rel, nil
}

// AssetCandidates lists archive names in preference order for a platform.
// goreleaser publishes a universal darwin_all archive (replace: true), so
// darwin prefers it and falls back to per-arch names for older releases.
func AssetCandidates(tag, goos, goarch string) []string {
	ver := strings.TrimPrefix(NormalizeTag(tag), "v")
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	var names []string
	if goos == "darwin" {
		names = append(names, fmt.Sprintf("g8s_%s_darwin_all.%s", ver, ext))
	}
	names = append(names, fmt.Sprintf("g8s_%s_%s_%s.%s", ver, goos, goarch, ext))
	return names
}

// SelectAsset picks the archive for goos/goarch from a release.
func SelectAsset(rel *Release, goos, goarch string) (Asset, error) {
	byName := make(map[string]Asset, len(rel.Assets))
	for _, a := range rel.Assets {
		byName[a.Name] = a
	}
	for _, name := range AssetCandidates(rel.TagName, goos, goarch) {
		if a, ok := byName[name]; ok {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("%w: %s/%s in %s (tried %s)", ErrAssetNotFound, goos, goarch, rel.TagName, strings.Join(AssetCandidates(rel.TagName, goos, goarch), ", "))
}

// ParseChecksum extracts the SHA-256 for name from a sha256sum-format
// manifest. Malformed digests, a missing entry, or conflicting duplicate
// entries all fail closed.
func ParseChecksum(manifest []byte, name string) (string, error) {
	found := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		file := strings.TrimPrefix(fields[1], "*")
		if file != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("%w: malformed digest for %s", ErrChecksumMissing, name)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("%w: non-hex digest for %s", ErrChecksumMissing, name)
		}
		if found != "" && found != sum {
			return "", fmt.Errorf("%w: conflicting digests for %s", ErrChecksumMissing, name)
		}
		found = sum
	}
	if found == "" {
		return "", fmt.Errorf("%w: %s not listed in %s", ErrChecksumMissing, name, ChecksumsAsset)
	}
	return found, nil
}

// VerifySHA256 compares data against the expected hex digest in constant time.
func VerifySHA256(data []byte, expected string) (string, error) {
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(expected))) != 1 {
		return actual, fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expected, actual)
	}
	return actual, nil
}

// binaryName returns the executable name inside release archives.
func binaryName(goos string) string {
	if goos == "windows" {
		return "g8s.exe"
	}
	return "g8s"
}

// ExtractBinary returns the g8s executable bytes from a tar.gz or zip
// archive. Only a regular file whose base name is the g8s binary is read;
// paths are never joined onto the filesystem, so traversal entries are inert.
func ExtractBinary(archive []byte, assetName, goos string, limit int64) ([]byte, error) {
	want := binaryName(goos)
	if limit <= 0 {
		limit = DefaultMaxDownloadBytes
	}
	readLimited := func(r io.Reader) ([]byte, error) {
		data, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, ErrDownloadTooLarge
		}
		return data, nil
	}
	if strings.HasSuffix(assetName, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != want || !f.Mode().IsRegular() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
			}
			data, err := readLimited(rc)
			if cErr := rc.Close(); cErr != nil && err == nil {
				err = cErr
			}
			if err != nil {
				return nil, fmt.Errorf("read %s in zip: %w", f.Name, err)
			}
			return data, nil
		}
		return nil, ErrBinaryNotInBundle
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrBinaryNotInBundle
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != want {
			continue
		}
		data, err := readLimited(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s in tar: %w", hdr.Name, err)
		}
		return data, nil
	}
}

// CompareVersions orders two semver-ish versions ("v1.2.3", "1.2.3-rc1").
// It returns -1, 0, or 1; a pre-release sorts before its release.
func CompareVersions(a, b string) int {
	pa, preA := splitVersion(a)
	pb, preB := splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	case preA < preB:
		return -1
	default:
		return 1
	}
}

func splitVersion(v string) ([3]int, string) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}, "unparsable"
		}
		out[i] = n
	}
	return out, pre
}

// Check resolves the target release and reports whether it differs from the
// running version. It performs no filesystem writes.
func (u *Updater) Check(ctx context.Context, version string) (*UpdateResult, *Release, error) {
	u.defaults()
	rel, err := u.ResolveRelease(ctx, version)
	if err != nil {
		return nil, nil, err
	}
	res := &UpdateResult{
		CheckOnly:      true,
		CurrentVersion: u.CurrentVersion,
		TargetVersion:  rel.TagName,
		ReleaseURL:     rel.HTMLURL,
	}
	if version != "" {
		// An explicit pin is honoured as long as it differs from what runs now.
		res.UpdateAvailable = CompareVersions(rel.TagName, u.CurrentVersion) != 0
	} else {
		res.UpdateAvailable = CompareVersions(rel.TagName, u.CurrentVersion) > 0
	}
	if asset, err := SelectAsset(rel, u.GOOS, u.GOARCH); err == nil {
		res.Asset = asset.Name
		res.AssetURL = asset.URL
	}
	if res.UpdateAvailable {
		res.Message = fmt.Sprintf("update available: %s -> %s", u.CurrentVersion, rel.TagName)
	} else {
		res.Message = fmt.Sprintf("already at %s", u.CurrentVersion)
	}
	return res, rel, nil
}

// Apply downloads, verifies and installs the target release. force reinstalls
// even when the target equals the running version. Verification failures
// abort before the installed binary is touched.
func (u *Updater) Apply(ctx context.Context, version string, force bool) (*UpdateResult, error) {
	res, rel, err := u.Check(ctx, version)
	if err != nil {
		return nil, err
	}
	res.CheckOnly = false
	if !res.UpdateAvailable && !force {
		return res, nil
	}

	asset, err := SelectAsset(rel, u.GOOS, u.GOARCH)
	if err != nil {
		return res, err
	}
	var sumsAsset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == ChecksumsAsset {
			sumsAsset = &rel.Assets[i]
			break
		}
	}
	if sumsAsset == nil {
		return res, fmt.Errorf("%w: %s not attached to %s", ErrChecksumMissing, ChecksumsAsset, rel.TagName)
	}
	manifest, err := u.get(ctx, sumsAsset.URL, false)
	if err != nil {
		return res, fmt.Errorf("%w: download %s: %v", ErrChecksumMissing, ChecksumsAsset, err)
	}
	expected, err := ParseChecksum(manifest, asset.Name)
	if err != nil {
		return res, err
	}
	res.ExpectedSHA256 = expected

	archive, err := u.get(ctx, asset.URL, false)
	if err != nil {
		return res, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	actual, err := VerifySHA256(archive, expected)
	res.ActualSHA256 = actual
	if err != nil {
		return res, err
	}
	bin, err := ExtractBinary(archive, asset.Name, u.GOOS, u.MaxDownloadBytes)
	if err != nil {
		return res, err
	}

	target := u.TargetPath
	if target == "" {
		exe, err := os.Executable()
		if err != nil {
			return res, fmt.Errorf("resolve running executable: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		target = exe
	}
	res.TargetPath = target

	backup, err := ReplaceBinary(target, bin, u.GOOS)
	if err != nil {
		return res, err
	}
	res.BackupPath = backup
	res.Applied = true
	res.Message = fmt.Sprintf("updated %s -> %s", u.CurrentVersion, rel.TagName)

	if u.Smoke != nil {
		if smokeErr := u.Smoke(target); smokeErr != nil {
			if rbErr := Rollback(target, backup, u.GOOS); rbErr != nil {
				return res, fmt.Errorf("smoke test failed (%v) and rollback failed: %w", smokeErr, rbErr)
			}
			res.Applied = false
			res.RolledBack = true
			res.Message = "smoke test failed; restored previous binary"
			return res, fmt.Errorf("E_UPDATE_SMOKE_FAILED: new binary failed smoke test, rolled back: %w", smokeErr)
		}
	}
	return res, nil
}

// BackupPath returns the rollback copy location for a target binary.
func BackupPath(target string) string { return target + ".bak" }

// ReplaceBinary atomically swaps target for data and keeps the previous
// binary at BackupPath(target). The new bytes are staged in a temp file in
// the same directory so the final step is a same-filesystem rename.
//
// POSIX: the backup is a hard link (copy fallback) and rename(2) replaces the
// target atomically, so the path never disappears. Windows: a running .exe
// cannot be overwritten or deleted but can be renamed, so the target is
// renamed aside to the backup first and restored if the final rename fails.
func ReplaceBinary(target string, data []byte, goos string) (string, error) {
	dir := filepath.Dir(target)
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("stat target %s: %w", target, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("target %s is not a regular file", target)
	}
	tmp, err := os.CreateTemp(dir, ".g8s-update-*")
	if err != nil {
		return "", fmt.Errorf("stage update in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		if cErr := tmp.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] lifecycle: close staged binary %s: %v\n", tmpPath, cErr)
		}
		cleanup()
		return "", fmt.Errorf("write staged binary %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		if cErr := tmp.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] lifecycle: close staged binary %s: %v\n", tmpPath, cErr)
		}
		cleanup()
		return "", fmt.Errorf("sync staged binary %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("close staged binary %s: %w", tmpPath, err)
	}
	// #nosec G302 -- an installed CLI binary must be executable by its owner and group/other.
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		cleanup()
		return "", fmt.Errorf("chmod staged binary %s: %w", tmpPath, err)
	}

	backup := BackupPath(target)
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		cleanup()
		return "", fmt.Errorf("remove stale backup %s: %w", backup, err)
	}

	if goos == "windows" {
		if err := os.Rename(target, backup); err != nil {
			cleanup()
			return "", fmt.Errorf("rename running binary aside %s -> %s: %w", target, backup, err)
		}
		if err := os.Rename(tmpPath, target); err != nil {
			if rbErr := os.Rename(backup, target); rbErr != nil {
				fmt.Fprintf(os.Stderr, "[warn] lifecycle: restore %s from %s: %v\n", target, backup, rbErr)
			}
			cleanup()
			return "", fmt.Errorf("install new binary %s: %w", target, err)
		}
		return backup, nil
	}

	if err := os.Link(target, backup); err != nil {
		if cpErr := copyFile(target, backup, info.Mode().Perm()); cpErr != nil {
			cleanup()
			return "", fmt.Errorf("create backup %s: link: %v; copy: %w", backup, err, cpErr)
		}
	}
	if err := os.Rename(tmpPath, target); err != nil {
		cleanup()
		return "", fmt.Errorf("install new binary %s: %w", target, err)
	}
	return backup, nil
}

// Rollback restores target from backup.
func Rollback(target, backup, goos string) error {
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup %s unavailable: %w", backup, err)
	}
	if goos == "windows" {
		aside := target + ".failed"
		if err := os.Remove(aside); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", aside, err)
		}
		if err := os.Rename(target, aside); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("move failed binary aside %s: %w", target, err)
		}
	}
	if err := os.Rename(backup, target); err != nil {
		return fmt.Errorf("restore %s from %s: %w", target, backup, err)
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		if cErr := out.Close(); cErr != nil {
			fmt.Fprintf(os.Stderr, "[warn] lifecycle: close %s: %v\n", dst, cErr)
		}
		return err
	}
	return out.Close()
}
