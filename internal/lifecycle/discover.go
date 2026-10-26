package lifecycle

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/initwiz"
	"github.com/tamld/g8s/internal/pathutil"
)

// DefaultMCPConfigs returns every IDE MCP config location `g8s init` writes,
// reusing initwiz.DetectIDEs so install and uninstall share one path table.
func DefaultMCPConfigs(home string) []MCPConfig {
	ides, err := initwiz.DetectIDEs(home)
	if err != nil {
		return nil
	}
	out := make([]MCPConfig, 0, len(ides))
	for _, ide := range ides {
		out = append(out, MCPConfig{
			IDE:       ide.ID,
			Path:      ide.ConfigPath,
			Dedicated: ide.ID == initwiz.IDEAntigravity,
		})
	}
	return out
}

// DefaultStateDirs returns the current user's g8s-owned directories using the
// same pathutil resolvers as `g8s doctor --detect-paths`, plus the XDG paths
// `g8s init` writes and any session-isolated `g8s-sess-*` state siblings.
func DefaultStateDirs(home string) []string {
	dirs := []string{
		pathutil.DefaultStateDir(),
		pathutil.DefaultDataDir(),
		pathutil.DefaultConfigDir(),
		pathutil.DefaultCacheDir(),
		pathutil.DefaultLogsDir(),
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "state", "g8s"),
			filepath.Join(home, ".config", "g8s"),
		)
		stateRoot := filepath.Join(home, ".local", "state")
		if entries, err := os.ReadDir(stateRoot); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "g8s-sess-") {
					dirs = append(dirs, filepath.Join(stateRoot, e.Name()))
				}
			}
		}
	}
	return dirs
}
