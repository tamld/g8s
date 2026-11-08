package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/search"
)

const (
	gitCommitRequestsDir       = ".g8s-git-requests"
	gitCommitRequestPollPeriod = 2 * time.Second
	maxCommitMessageLines      = 200
)

// gitCommitHostOS is overridable in tests for the Windows gate.
var gitCommitHostOS = runtime.GOOS

// gitCommitRequestFile is the workdir-local commit request marker (#822 option 3).
type gitCommitRequestFile struct {
	V        int      `json:"v"`
	Op       string   `json:"op"`
	Message  string   `json:"message"`
	AddPaths []string `json:"add_paths"`
}

type gitCommitResultFile struct {
	OK     bool   `json:"ok"`
	Commit string `json:"commit,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// superviseGitCommitRequests polls worktree commit request markers while the
// provider child runs, then performs one final sweep after exit (#822).
func (s *Supervisor) superviseGitCommitRequests(
	ctx context.Context,
	worktreeDir, taskID string,
	req taskRequest,
	childDone <-chan struct{},
) {
	if strings.TrimSpace(worktreeDir) == "" {
		return
	}
	ticker := time.NewTicker(gitCommitRequestPollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.sweepGitCommitRequests(worktreeDir, taskID, req)
			return
		case <-childDone:
			s.sweepGitCommitRequests(worktreeDir, taskID, req)
			return
		case <-ticker.C:
			s.sweepGitCommitRequests(worktreeDir, taskID, req)
		}
	}
}

func (s *Supervisor) sweepGitCommitRequests(worktreeDir, taskID string, req taskRequest) {
	dir := gitCommitRequestsPath(worktreeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := s.clock()
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".result.json") {
			continue
		}
		reqPath := filepath.Join(dir, name)
		resPath := resultMarkerPath(reqPath)
		if _, statErr := os.Stat(resPath); statErr == nil {
			continue
		}
		raw, readErr := os.ReadFile(reqPath)
		if readErr != nil {
			writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_READ_FAILED: " + readErr.Error()})
			continue
		}
		s.processGitCommitRequest(worktreeDir, taskID, req, reqPath, resPath, raw, now)
	}
}

func gitCommitRequestsPath(worktreeDir string) string {
	return filepath.Join(worktreeDir, gitCommitRequestsDir)
}

func resultMarkerPath(requestPath string) string {
	base := strings.TrimSuffix(filepath.Base(requestPath), ".json")
	return filepath.Join(filepath.Dir(requestPath), base+".result.json")
}

func writeCommitResult(path string, res gitCommitResultFile) {
	payload, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: marshal commit result %s: %v\n", path, err)
		return
	}
	if werr := os.WriteFile(path, payload, 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: write commit result %s: %v\n", path, werr)
	}
}

func (s *Supervisor) processGitCommitRequest(
	worktreeDir, taskID string,
	req taskRequest,
	reqPath, resPath string,
	raw []byte,
	requestTime time.Time,
) {
	var marker gitCommitRequestFile
	if err := json.Unmarshal(raw, &marker); err != nil {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_INVALID_JSON: " + err.Error()})
		return
	}
	if marker.V != 1 {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_UNSUPPORTED_VERSION"})
		return
	}
	if marker.Op != "commit" {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_UNKNOWN_OP"})
		return
	}
	if code := validateHarnessCommitRequest(worktreeDir, req, marker); code != "" {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: code})
		return
	}
	allowedPaths, lookupErr := resolveReceiptAllowedPaths(req.ReceiptID)
	if lookupErr != nil {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: lookupErr.Error()})
		return
	}
	addArgs, scopeCode := resolveCommitAddPaths(worktreeDir, marker.AddPaths, allowedPaths)
	if scopeCode != "" {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: scopeCode})
		return
	}
	// Platform gate sits at the EXECUTION boundary: every request-schema,
	// permission, and scope error is platform-independent and must surface
	// everywhere (Windows CI asserts the full matrix); only git EXECUTION
	// is macOS/Linux-only per ADR-0041.
	if gitCommitHostOS == "windows" {
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: "E_COMMIT_REQUEST_UNSUPPORTED_OS"})
		return
	}
	actor := strings.TrimSpace(req.Provider)
	if actor == "" {
		actor = "unknown"
	}
	fullMessage := strings.TrimRight(marker.Message, "\n") + "\n\nReceipt: " + taskID + "\n"
	gitDate := requestTime.UTC().Format("2006-01-02T15:04:05-0700")
	identityName := fmt.Sprintf("g8s-worker %s %s", taskID, actor)
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME="+identityName,
		"GIT_COMMITTER_NAME="+identityName,
		"GIT_AUTHOR_EMAIL=noreply@g8s.local",
		"GIT_COMMITTER_EMAIL=noreply@g8s.local",
		"GIT_AUTHOR_DATE="+gitDate,
		"GIT_COMMITTER_DATE="+gitDate,
	)
	addCmd := exec.Command("git", append([]string{"-C", worktreeDir, "add", "--"}, addArgs...)...)
	addCmd.Env = gitEnv
	if addOut, addErr := addCmd.CombinedOutput(); addErr != nil {
		reason := fmt.Sprintf("E_COMMIT_GIT_ADD_FAILED: %v: %s", addErr, strings.TrimSpace(string(addOut)))
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: reason})
		return
	}
	commitCmd := exec.Command("git", "-C", worktreeDir, "commit", "-m", fullMessage)
	commitCmd.Env = gitEnv
	if commitOut, commitErr := commitCmd.CombinedOutput(); commitErr != nil {
		reason := fmt.Sprintf("E_COMMIT_GIT_COMMIT_FAILED: %v: %s", commitErr, strings.TrimSpace(string(commitOut)))
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: reason})
		return
	}
	revCmd := exec.Command("git", "-C", worktreeDir, "rev-parse", "HEAD")
	revCmd.Env = gitEnv
	shaOut, revErr := revCmd.Output()
	if revErr != nil {
		reason := fmt.Sprintf("E_COMMIT_GIT_REVPARSE_FAILED: %v", revErr)
		writeCommitResult(resPath, gitCommitResultFile{OK: false, Reason: reason})
		return
	}
	sha := strings.TrimSpace(string(shaOut))
	writeCommitResult(resPath, gitCommitResultFile{OK: true, Commit: sha})
}

func validateHarnessCommitRequest(worktreeDir string, req taskRequest, marker gitCommitRequestFile) string {
	if isDiffFirewallReadPermission(req.Permission) {
		return "E_COMMIT_REQUEST_FORBIDDEN_READ_ONLY"
	}
	if req.Permission != "workspace_write" {
		return "E_COMMIT_REQUEST_FORBIDDEN_READ_ONLY"
	}
	if !req.AllowWorkerCommit {
		return "E_COMMIT_REQUEST_NOT_ENABLED"
	}
	msg := strings.TrimSpace(marker.Message)
	if msg == "" {
		return "E_COMMIT_MESSAGE_EMPTY"
	}
	if strings.Count(marker.Message, "\n")+1 > maxCommitMessageLines {
		return "E_COMMIT_MESSAGE_TOO_LONG"
	}
	if len(marker.AddPaths) == 0 {
		return "E_COMMIT_ADD_PATHS_REQUIRED"
	}
	if strings.TrimSpace(worktreeDir) == "" {
		return "E_COMMIT_WORKTREE_MISSING"
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, ".git")); err != nil {
		return "E_COMMIT_WORKTREE_NOT_GIT"
	}
	return ""
}

func resolveCommitAddPaths(worktreeDir string, addPaths []string, allowedPaths []string) ([]string, string) {
	worktreeAbs, err := filepath.Abs(worktreeDir)
	if err != nil {
		return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
	}
	var gitAddArgs []string
	for _, raw := range addPaths {
		rel := strings.TrimSpace(raw)
		if rel == "" {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		if rel == "." {
			if !addPathInReceiptScope(".", allowedPaths) {
				return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
			}
			gitAddArgs = append(gitAddArgs, ".")
			continue
		}
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		clean := filepath.Clean(rel)
		if clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		joined := filepath.Join(worktreeDir, clean)
		joinedAbs, absErr := filepath.Abs(joined)
		if absErr != nil {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		if joinedAbs != worktreeAbs && !strings.HasPrefix(joinedAbs, worktreeAbs+string(filepath.Separator)) {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		relSlash := filepath.ToSlash(clean)
		if !addPathInReceiptScope(relSlash, allowedPaths) {
			return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
		}
		gitAddArgs = append(gitAddArgs, relSlash)
	}
	if len(gitAddArgs) == 0 {
		return nil, "E_COMMIT_PATH_OUT_OF_SCOPE"
	}
	return gitAddArgs, ""
}

func addPathInReceiptScope(rel string, allowedPaths []string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		for _, raw := range allowedPaths {
			p := strings.TrimSpace(raw)
			if p == "*" || p == "**" || p == "." {
				return true
			}
		}
	}
	return search.MatchesAllowedPaths(rel, allowedPaths)
}
