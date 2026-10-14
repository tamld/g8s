// Package search implements a native cross-platform file discovery and content search engine.
package search

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configures a file discovery or content search execution.
type Options struct {
	Roots         []string
	ScopeRoot     string
	Pattern       string
	FixedStrings  bool
	IgnoreCase    bool
	Globs         []string
	FilesOnly     bool
	IncludeHidden bool
	NoIgnore      bool
	AllowedPaths  []string
	MaxResults    int
}

// Match represents a single line match inside a searched file.
type Match struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

// Result contains the deterministic output of Execute.
type Result struct {
	Matches       []Match  `json:"matches,omitempty"`
	Files         []string `json:"files,omitempty"`
	FilesSearched int      `json:"files_searched"`
	DurationMs    int64    `json:"duration_ms"`
	Engine        string   `json:"engine"`
}

// Execute performs the search operation based on the provided Options.
func Execute(ctx context.Context, opts Options) (*Result, error) {
	start := time.Now()

	roots := opts.Roots
	if len(roots) == 0 {
		if opts.ScopeRoot != "" {
			roots = []string{opts.ScopeRoot}
		} else {
			roots = []string{"."}
		}
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 1000
	}

	var re *regexp.Regexp
	if !opts.FilesOnly {
		if opts.Pattern == "" {
			return nil, fmt.Errorf("search pattern is required unless --files is set")
		}
		if !opts.FixedStrings {
			pattern := opts.Pattern
			if opts.IgnoreCase {
				pattern = "(?i)" + pattern
			}
			var err error
			re, err = regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex pattern: %w", err)
			}
		}
	}

	defaultIgnoredDirs := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".venv":        true,
		"dist":         true,
		"coverage":     true,
	}

	var candidateFiles []string
	seenFiles := make(map[string]bool)

	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		scopeBase := root
		if opts.ScopeRoot != "" {
			scopeBase = opts.ScopeRoot
		}

		var ignorePatterns []string
		if !opts.NoIgnore {
			for _, ignoreName := range []string{".gitignore", ".ignore"} {
				ignorePath := filepath.Join(root, ignoreName)
				if data, err := os.ReadFile(ignorePath); err == nil {
					scanner := bufio.NewScanner(bytes.NewReader(data))
					for scanner.Scan() {
						line := strings.TrimSpace(scanner.Text())
						if line == "" || strings.HasPrefix(line, "#") {
							continue
						}
						ignorePatterns = append(ignorePatterns, line)
					}
				}
			}
		}

		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			relSlash := filepath.ToSlash(filepath.Clean(rel))
			base := d.Name()

			if d.IsDir() {
				if relSlash == "." {
					return nil
				}
				if !opts.NoIgnore {
					if base == ".git" {
						return filepath.SkipDir
					}
					if !opts.IncludeHidden && strings.HasPrefix(base, ".") {
						return filepath.SkipDir
					}
					if defaultIgnoredDirs[base] {
						return filepath.SkipDir
					}
					if isIgnored(relSlash, base, ignorePatterns) {
						return filepath.SkipDir
					}
				}
				return nil
			}

			if !d.Type().IsRegular() {
				return nil
			}

			if !opts.NoIgnore {
				if !opts.IncludeHidden && strings.HasPrefix(base, ".") {
					return nil
				}
				if isIgnored(relSlash, base, ignorePatterns) {
					return nil
				}
			}

			if !MatchesGlobs(relSlash, base, opts.Globs) {
				return nil
			}

			if len(opts.AllowedPaths) > 0 {
				scopeRel, relErr := filepath.Rel(scopeBase, path)
				if relErr != nil {
					return nil
				}
				scopeRelSlash := filepath.ToSlash(filepath.Clean(scopeRel))
				if !MatchesAllowedPaths(scopeRelSlash, opts.AllowedPaths) {
					return nil
				}
			}

			displayPath := filepath.ToSlash(path)
			if !seenFiles[displayPath] {
				seenFiles[displayPath] = true
				candidateFiles = append(candidateFiles, displayPath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if opts.FilesOnly {
		sort.Strings(candidateFiles)
		filesSearched := len(candidateFiles)
		if len(candidateFiles) > maxResults {
			candidateFiles = candidateFiles[:maxResults]
		}
		return &Result{
			Files:         candidateFiles,
			FilesSearched: filesSearched,
			DurationMs:    time.Since(start).Milliseconds(),
			Engine:        "native",
		}, nil
	}

	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}

	fileChan := make(chan string, len(candidateFiles))
	for _, f := range candidateFiles {
		fileChan <- f
	}
	close(fileChan)

	var wg sync.WaitGroup
	resChan := make(chan []Match, len(candidateFiles))

	lowerPattern := opts.Pattern
	if opts.IgnoreCase {
		lowerPattern = strings.ToLower(opts.Pattern)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1024*1024)

			for filePath := range fileChan {
				if ctx.Err() != nil {
					return
				}
				fileMatches := scanFile(filePath, opts, lowerPattern, re, buf)
				if len(fileMatches) > 0 {
					resChan <- fileMatches
				}
			}
		}()
	}

	wg.Wait()
	close(resChan)

	var allMatches []Match
	for matches := range resChan {
		allMatches = append(allMatches, matches...)
	}

	sort.Slice(allMatches, func(i, j int) bool {
		if allMatches[i].File != allMatches[j].File {
			return allMatches[i].File < allMatches[j].File
		}
		if allMatches[i].Line != allMatches[j].Line {
			return allMatches[i].Line < allMatches[j].Line
		}
		return allMatches[i].Column < allMatches[j].Column
	})

	if len(allMatches) > maxResults {
		allMatches = allMatches[:maxResults]
	}

	return &Result{
		Matches:       allMatches,
		FilesSearched: len(candidateFiles),
		DurationMs:    time.Since(start).Milliseconds(),
		Engine:        "native",
	}, nil
}

func scanFile(filePath string, opts Options, lowerPattern string, re *regexp.Regexp, buf []byte) []Match {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	header := make([]byte, 512)
	n, readErr := f.Read(header)
	if readErr != nil && readErr != io.EOF {
		return nil
	}
	if n > 0 && bytes.IndexByte(header[:n], 0) >= 0 {
		return nil
	}

	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(buf, 1024*1024)

	var fileMatches []Match
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		lineText := scanner.Text()

		if opts.FixedStrings {
			target := lineText
			if opts.IgnoreCase {
				target = strings.ToLower(lineText)
			}
			colIdx := strings.Index(target, lowerPattern)
			if colIdx >= 0 {
				fileMatches = append(fileMatches, Match{
					File:   filePath,
					Line:   lineNum,
					Column: colIdx + 1,
					Text:   lineText,
				})
			}
		} else if re != nil {
			loc := re.FindStringIndex(lineText)
			if loc != nil {
				fileMatches = append(fileMatches, Match{
					File:   filePath,
					Line:   lineNum,
					Column: loc[0] + 1,
					Text:   lineText,
				})
			}
		}
	}

	return fileMatches
}

// MatchesGlobs evaluates positive and negative glob patterns against path and basename.
func MatchesGlobs(relPath, base string, globs []string) bool {
	if len(globs) == 0 {
		return true
	}

	hasPositive := false
	matchedPositive := false

	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if strings.HasPrefix(g, "!") {
			negPattern := strings.TrimPrefix(g, "!")
			if matchPattern(negPattern, relPath, base) {
				return false
			}
		} else {
			hasPositive = true
			if matchPattern(g, relPath, base) {
				matchedPositive = true
			}
		}
	}

	if hasPositive && !matchedPositive {
		return false
	}
	return true
}

func matchPattern(pattern, relPath, base string) bool {
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	relPath = filepath.ToSlash(filepath.Clean(relPath))

	if cleanPattern == "*" || cleanPattern == "**" {
		return true
	}
	if cleanPattern == relPath || cleanPattern == base {
		return true
	}
	if matched, _ := filepath.Match(cleanPattern, base); matched {
		return true
	}
	if matched, _ := filepath.Match(cleanPattern, relPath); matched {
		return true
	}
	if strings.Contains(cleanPattern, "**") {
		return matchGlobstarSegs(
			strings.Split(relPath, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	return false
}

func matchGlobstarSegs(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if matchGlobstarSegs(fileSegs[i:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(fileSegs) == 0 {
		return false
	}
	m, _ := filepath.Match(patSegs[0], fileSegs[0])
	if !m {
		return false
	}
	return matchGlobstarSegs(fileSegs[1:], patSegs[1:])
}

// MatchesAllowedPaths checks whether relPath is within the receipt's allowed paths.
// Rejects path traversal ("..", "../") and absolute paths.
func MatchesAllowedPaths(relPath string, allowedPaths []string) bool {
	cleanFile := filepath.ToSlash(filepath.Clean(relPath))
	if cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, "../") || strings.HasPrefix(cleanFile, "/") || isWindowsDrivePath(cleanFile) {
		return false
	}

	for _, rawPat := range allowedPaths {
		pat := strings.TrimSpace(rawPat)
		if pat == "" {
			continue
		}
		cleanPat := filepath.ToSlash(filepath.Clean(pat))
		if cleanPat == "*" || cleanPat == "**" || cleanFile == cleanPat {
			return true
		}
		if strings.Contains(cleanPat, "**") {
			if matchGlobstarSegs(strings.Split(cleanFile, "/"), strings.Split(cleanPat, "/")) {
				return true
			}
			continue
		}
		if strings.HasSuffix(pat, "/*") || strings.HasSuffix(pat, "/") {
			dirPrefix := strings.TrimSuffix(cleanPat, "*")
			if !strings.HasSuffix(dirPrefix, "/") {
				dirPrefix += "/"
			}
			if strings.HasPrefix(cleanFile, dirPrefix) {
				return true
			}
		} else if !strings.Contains(cleanPat, "*") {
			if strings.HasPrefix(cleanFile, cleanPat+"/") {
				return true
			}
		}
		if m, _ := filepath.Match(cleanPat, cleanFile); m {
			return true
		}
	}
	return false
}

func isWindowsDrivePath(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		d := p[0]
		return (d >= 'a' && d <= 'z') || (d >= 'A' && d <= 'Z')
	}
	return false
}

func isIgnored(relPath, base string, ignorePatterns []string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, p := range ignorePatterns {
		p = strings.TrimSpace(p)
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		if p == base || p == relPath || strings.HasPrefix(relPath, p+"/") {
			return true
		}
		if matchPattern(p, relPath, base) {
			return true
		}
	}
	return false
}
