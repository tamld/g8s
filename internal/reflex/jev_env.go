package reflex

import (
	"os"
	"path/filepath"
)

func dotEnvSearchDirs() []string {
	candidates := []string{".", "..", "../.."}
	anyGit := false
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			anyGit = true
			break
		}
	}
	if !anyGit {
		return []string{"."}
	}
	var dirs []string
	for _, d := range candidates {
		dirs = append(dirs, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
	}
	return dirs
}
