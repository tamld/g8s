package diffintel

import (
	"path/filepath"
	"strings"
)

var defaultNoiseFileNames = map[string]struct{}{
	"go.sum":            {},
	"package-lock.json": {},
	"pnpm-lock.yaml":    {},
	"yarn.lock":         {},
	"cargo.lock":        {},
	"composer.lock":     {},
	"gemfile.lock":      {},
	"poetry.lock":       {},
	"mix.lock":          {},
	"flake.lock":        {},
}

var defaultNoiseExtensions = map[string]struct{}{
	".min.js":  {},
	".min.css": {},
	".map":     {},
	".svg":     {},
	".png":     {},
	".jpg":     {},
	".jpeg":    {},
	".gif":     {},
	".ico":     {},
	".wasm":    {},
	".pdf":     {},
	".zip":     {},
	".gz":      {},
	".tar":     {},
}

// IsNoiseFile reports whether a given file path is generated noise or a lockfile
// that should not consume LLM review tokens.
func IsNoiseFile(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	base := strings.ToLower(filepath.Base(clean))

	// Check exact filename matches
	if _, ok := defaultNoiseFileNames[base]; ok {
		return true
	}

	// Check vendored prefixes
	if strings.HasPrefix(clean, "vendor/") || strings.Contains(clean, "/vendor/") ||
		strings.HasPrefix(clean, "node_modules/") || strings.Contains(clean, "/node_modules/") {
		return true
	}

	// Check generated suffixes
	if strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_generated.go") ||
		strings.HasSuffix(base, ".gen.go") {
		return true
	}

	// Check extension matches
	for ext := range defaultNoiseExtensions {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}

	return false
}

// PruneNoise filters out files from FileDiff list that are identified as noise or binary files.
func PruneNoise(diffs []FileDiff) (pruned []FileDiff, noiseCount int) {
	pruned = make([]FileDiff, 0, len(diffs))
	for _, d := range diffs {
		path := d.NewPath
		if path == "" || path == "/dev/null" {
			path = d.OldPath
		}
		if d.IsBinary || IsNoiseFile(path) {
			noiseCount++
			continue
		}
		pruned = append(pruned, d)
	}
	return pruned, noiseCount
}
