package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
)

var (
	Version   = "0.9.0"
	Commit    = "unknown" // -ldflags "-X main.Commit=$(git rev-parse HEAD)"
	BuildTime = "unknown" // -ldflags "-X main.BuildTime=$(date -u)"
)

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
	if Commit == "unknown" || BuildTime == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && Commit == "unknown" {
					Commit = setting.Value
				}
				if setting.Key == "vcs.time" && BuildTime == "unknown" {
					BuildTime = setting.Value
				}
			}
		}
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

	if *jsonMode || *jsonl {
		data := map[string]any{
			"app":        AppName,
			"version":    Version,
			"commit":     Commit,
			"build_time": BuildTime,
			"zero_cgo":   true,
			"runtime":    "pure-go",
			"actor":      *actor,
		}
		if updateInfo != nil {
			data["check_update"] = updateInfo
		}
		env := cli.NewEnvelope("version", "version", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	fmt.Printf("g8s version %s\n", Version)
	fmt.Printf("  commit: %s\n", Commit)
	fmt.Printf("  built:  %s\n", BuildTime)

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
