package orchestrator

import (
	"os/exec"
	"strings"
)

// gitDiffNameOnly runs `git diff --name-only <base>..HEAD` inside the
// worktree's git dir, returning the list of files the worker touched.
// Empty list on a fresh worktree (no commits yet) is normal.
func gitDiffNameOnly(repo, baseSHA string) ([]string, string) {
	cmd := exec.Command("git", "diff", "--name-only", baseSHA+"..HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, ""
	}
	files := splitNonEmpty(string(out), '\n')
	if len(files) == 0 {
		return files, baseSHA
	}
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = repo
	shaOut, shaErr := shaCmd.Output()
	sha := baseSHA
	if shaErr == nil {
		sha = strings.TrimSpace(string(shaOut))
	}
	return files, sha
}

// diffScope reports paths the worker touched that are NOT in the allowed
// list. Empty result = worker stayed in scope.
func diffScope(modified, allowed []string) []string {
	if len(modified) == 0 {
		return nil
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allow[a] = struct{}{}
	}
	var out []string
	for _, m := range modified {
		if _, ok := allow[m]; !ok {
			out = append(out, m)
		}
	}
	return out
}

func splitNonEmpty(s string, sep byte) []string {
	raw := strings.Split(s, string(sep))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if t := strings.TrimSpace(r); t != "" {
			out = append(out, t)
		}
	}
	return out
}
