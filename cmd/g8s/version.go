package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	offer "github.com/tamld/g8s"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/pathutil"
)

var (
	Version     = "0.21.0"
	Commit      = "unknown" // -ldflags "-X main.Commit=$(git rev-parse HEAD)"
	BuildTime   = "unknown" // -ldflags "-X main.BuildTime=$(date -u)"
	BuildSource = "unknown" // -ldflags "-X main.BuildSource=release|local-dev"
	VCSModified = "false"
)

// BuildManifest describes the complete binary provenance, runtime, and path manifest.
type BuildManifest struct {
	App            string `json:"app"`
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BuildTime      string `json:"build_time"`
	BuildSource    string `json:"build_source"`
	VCSModified    bool   `json:"vcs_modified"`
	GoVersion      string `json:"go_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Runtime        string `json:"runtime"`
	ZeroCGO        bool   `json:"zero_cgo"`
	BinaryPath     string `json:"binary_path"`
	ConfigDir      string `json:"config_dir"`
	StateDir       string `json:"state_dir"`
	DBPath         string `json:"db_path"`
	ManifestPath   string `json:"manifest_path,omitempty"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	ActiveDeltas   int    `json:"active_deltas,omitempty"`
	InstallHint    string `json:"install_hint,omitempty"`
}

type embeddedManifestInfo struct {
	Version        string
	ReleaseTag     string
	ReleaseDate    string
	ActiveDeltas   int
	ManifestSHA256 string
}

func readEmbeddedManifestInfo() embeddedManifestInfo {
	raw := offer.EmbeddedManifest()
	if len(raw) == 0 {
		return embeddedManifestInfo{}
	}
	sum := sha256.Sum256(raw)
	shaHex := hex.EncodeToString(sum[:])
	var parsed struct {
		Version       string `json:"version"`
		LatestRelease struct {
			Tag  string `json:"tag"`
			Date string `json:"date"`
		} `json:"latest_release"`
		Specifications []struct {
			ID string `json:"id"`
		} `json:"specifications"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return embeddedManifestInfo{ManifestSHA256: shaHex}
	}
	return embeddedManifestInfo{
		Version:        parsed.Version,
		ReleaseTag:     parsed.LatestRelease.Tag,
		ReleaseDate:    parsed.LatestRelease.Date,
		ActiveDeltas:   len(parsed.Specifications),
		ManifestSHA256: shaHex,
	}
}

// UpdateInfo holds details of a remote version check.
type UpdateInfo struct {
	Checked         bool   `json:"checked"`
	UpdateAvailable bool   `json:"update_available"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ReleaseInfo models the minimal subset of GitHub release metadata required.
type ReleaseInfo struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

// HTTPClient allows mock injection for testing update check logic offline.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

var (
	defaultHTTPClient HTTPClient = &http.Client{Timeout: 3 * time.Second}
	latestReleaseURL             = "https://api.github.com/repos/tamld/g8s/releases/latest"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	resolveBuildMetadata(info, ok)
}

// resolveBuildMetadata populates Commit, BuildTime, BuildSource, and VCSModified
// using the priority chain: ldflags -> vcs.* settings -> Go module info (go install) -> descriptive fallback.
func resolveBuildMetadata(info *debug.BuildInfo, ok bool) {
	if Commit != "unknown" && Commit != "" && BuildTime != "unknown" && BuildTime != "" {
		if BuildSource == "unknown" || BuildSource == "" {
			BuildSource = "release"
		}
		return
	}

	if ok && info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if Commit == "unknown" || Commit == "" {
					Commit = setting.Value
				}
			case "vcs.time":
				if BuildTime == "unknown" || BuildTime == "" {
					BuildTime = setting.Value
				}
			case "vcs.modified":
				VCSModified = setting.Value
			}
		}

		if Commit != "unknown" && Commit != "" {
			if VCSModified == "true" && !strings.HasSuffix(Commit, "-dirty") {
				Commit += "-dirty"
			}
			if BuildTime == "unknown" || BuildTime == "" {
				BuildTime = "vcs-tracked (no timestamp)"
			}
			if BuildSource == "unknown" || BuildSource == "" {
				BuildSource = "local-vcs"
			}
			return
		}

		// Module-mode `go install pkg@version` has no vcs.* settings, but populates Main.Version / Main.Sum.
		modVer := strings.TrimSpace(info.Main.Version)
		modSum := strings.TrimSpace(info.Main.Sum)
		if modVer != "" && modVer != "(devel)" {
			if modSum != "" {
				Commit = fmt.Sprintf("module:%s (%s)", modVer, modSum)
			} else {
				Commit = fmt.Sprintf("module:%s", modVer)
			}
			if BuildTime == "unknown" || BuildTime == "" {
				BuildTime = "go-install (module build; use 'g8s update' or scripts/install.{sh,ps1} for release binary)"
			}
			BuildSource = "go-install"
			return
		}
	}

	em := readEmbeddedManifestInfo()
	if Commit == "unknown" || Commit == "" {
		if em.ReleaseTag != "" && len(em.ManifestSHA256) >= 12 {
			Commit = fmt.Sprintf("source-archive:%s (manifest-sha256=%s)", em.ReleaseTag, em.ManifestSHA256[:12])
		} else {
			Commit = "untracked-local-build (run 'make local' or 'g8s update' for stamped commit)"
		}
	}
	if BuildTime == "unknown" || BuildTime == "" {
		if em.ReleaseDate != "" {
			BuildTime = fmt.Sprintf("%s (embedded-manifest; no ldflags/vcs metadata)", em.ReleaseDate)
		} else {
			BuildTime = "untracked-local-build (no ldflags/vcs metadata)"
		}
	}
	if BuildSource == "unknown" || BuildSource == "" {
		BuildSource = "local-untracked"
	}
}

func collectBuildManifest() BuildManifest {
	exePath, err := os.Executable()
	if err != nil {
		exePath = os.Args[0]
	} else if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	cfgDir := pathutil.ConfigDirForOS(runtime.GOOS, os.Getenv, "")
	stateDir := pathutil.DefaultStateDir()
	dbPath := pathutil.DefaultDatabasePath()
	em := readEmbeddedManifestInfo()

	manifestPath := ""
	if _, err := os.Stat("manifest.json"); err == nil {
		if abs, err := filepath.Abs("manifest.json"); err == nil {
			manifestPath = abs
		} else {
			manifestPath = "manifest.json"
		}
	} else if len(em.ManifestSHA256) >= 12 {
		manifestPath = fmt.Sprintf("embedded://manifest.json (%s, %d deltas, sha256=%s)", em.ReleaseTag, em.ActiveDeltas, em.ManifestSHA256[:12])
	}

	hint := ""
	if BuildSource == "go-install" || BuildSource == "local-untracked" {
		hint = "Run 'g8s update' or install via scripts/install.sh (macOS/Linux) / scripts/install.ps1 (Windows) for official SHA-256 verified release binaries."
	}

	return BuildManifest{
		App:            AppName,
		Version:        Version,
		Commit:         Commit,
		BuildTime:      BuildTime,
		BuildSource:    BuildSource,
		VCSModified:    VCSModified == "true",
		GoVersion:      runtime.Version(),
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Runtime:        "pure-go",
		ZeroCGO:        true,
		BinaryPath:     exePath,
		ConfigDir:      cfgDir,
		StateDir:       stateDir,
		DBPath:         dbPath,
		ManifestPath:   manifestPath,
		ManifestSHA256: em.ManifestSHA256,
		ActiveDeltas:   em.ActiveDeltas,
		InstallHint:    hint,
	}
}

// runVersion emits application build and version metadata.
func runVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	checkUpdate := fs.Bool("check-update", false, "check GitHub releases for newer version")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("version", "", *traceID, err.Error(), "", *jsonl)
	}

	var updateInfo *UpdateInfo
	if *checkUpdate {
		info := checkLatestRelease(context.Background(), defaultHTTPClient, latestReleaseURL, Version)
		updateInfo = &info
	}

	bm := collectBuildManifest()

	if *jsonMode || *jsonl {
		data := map[string]any{
			"app":            bm.App,
			"version":        bm.Version,
			"commit":         bm.Commit,
			"build_time":     bm.BuildTime,
			"build_source":   bm.BuildSource,
			"vcs_modified":   bm.VCSModified,
			"go_version":     bm.GoVersion,
			"os":             bm.OS,
			"arch":           bm.Arch,
			"zero_cgo":       bm.ZeroCGO,
			"runtime":        bm.Runtime,
			"binary_path":    bm.BinaryPath,
			"config_dir":     bm.ConfigDir,
			"state_dir":      bm.StateDir,
			"db_path":        bm.DBPath,
			"build_manifest": bm,
			"actor":          *actor,
		}
		if bm.ManifestPath != "" {
			data["manifest_path"] = bm.ManifestPath
		}
		if bm.InstallHint != "" {
			data["install_hint"] = bm.InstallHint
		}
		if updateInfo != nil {
			data["check_update"] = updateInfo
		}
		env := cli.NewEnvelope("version", "version", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Printf("g8s version %s\n", bm.Version)
	fmt.Printf("  commit:       %s\n", bm.Commit)
	fmt.Printf("  built:        %s\n", bm.BuildTime)
	fmt.Printf("  build_source: %s\n", bm.BuildSource)
	fmt.Printf("  runtime:      %s %s/%s (%s, cgo=0)\n", bm.GoVersion, bm.OS, bm.Arch, bm.Runtime)
	fmt.Printf("  binary_path:  %s\n", bm.BinaryPath)
	fmt.Printf("  config_dir:   %s\n", bm.ConfigDir)
	fmt.Printf("  state_dir:    %s\n", bm.StateDir)
	fmt.Printf("  db_path:      %s\n", bm.DBPath)
	if bm.ManifestPath != "" {
		fmt.Printf("  manifest:     %s\n", bm.ManifestPath)
	}
	if bm.InstallHint != "" {
		fmt.Printf("  hint:         %s\n", bm.InstallHint)
	}

	if updateInfo != nil {
		fmt.Println()
		if updateInfo.Error != "" {
			fmt.Printf("Warning: unable to check for updates: %s\n", updateInfo.Error)
		} else if updateInfo.UpdateAvailable {
			fmt.Printf("A new version of g8s is available: %s -> %s\n", Version, updateInfo.LatestVersion)
			if updateInfo.ReleaseURL != "" {
				fmt.Printf("Release notes & downloads: %s\n", updateInfo.ReleaseURL)
			}
		} else {
			fmt.Printf("You are running the latest version (%s).\n", Version)
		}
	}
}

// checkLatestRelease queries the GitHub release endpoint and compares versions.
func checkLatestRelease(ctx context.Context, client HTTPClient, url string, currentVersion string) UpdateInfo {
	info := UpdateInfo{
		Checked:        true,
		CurrentVersion: currentVersion,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("User-Agent", "g8s-cli/"+currentVersion)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		info.Error = fmt.Sprintf("github API returned HTTP %d", resp.StatusCode)
		return info
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Error = fmt.Sprintf("decode release payload: %v", err)
		return info
	}

	cleanLatest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	info.LatestVersion = cleanLatest
	info.ReleaseURL = rel.HTMLURL
	info.PublishedAt = rel.PublishedAt

	if compareSemVer(cleanLatest, currentVersion) > 0 {
		info.UpdateAvailable = true
	}
	return info
}

// compareSemVer compares two semver strings (with or without leading 'v').
// Returns:
//
//	 1 if v1 > v2
//	-1 if v1 < v2
//	 0 if v1 == v2
func compareSemVer(v1, v2 string) int {
	v1 = strings.TrimPrefix(strings.TrimSpace(v1), "v")
	v2 = strings.TrimPrefix(strings.TrimSpace(v2), "v")

	v1Core, v1Pre := splitPreRelease(v1)
	v2Core, v2Pre := splitPreRelease(v2)

	p1 := parseSemVerParts(v1Core)
	p2 := parseSemVerParts(v2Core)

	for i := 0; i < 3; i++ {
		if p1[i] > p2[i] {
			return 1
		}
		if p1[i] < p2[i] {
			return -1
		}
	}

	// Non-prerelease is newer than prerelease for equal core versions (e.g. 0.8.0 > 0.8.0-rc.1)
	if v1Pre == "" && v2Pre != "" {
		return 1
	}
	if v1Pre != "" && v2Pre == "" {
		return -1
	}
	if v1Pre > v2Pre {
		return 1
	}
	if v1Pre < v2Pre {
		return -1
	}
	return 0
}

func splitPreRelease(v string) (string, string) {
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		return v[:idx], v[idx+1:]
	}
	return v, ""
}

func parseSemVerParts(core string) [3]int {
	var parts [3]int
	segs := strings.Split(core, ".")
	for i := 0; i < len(segs) && i < 3; i++ {
		n, _ := strconv.Atoi(segs[i])
		parts[i] = n
	}
	return parts
}
