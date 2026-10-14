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

type Options struct {
	Roots         []string
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

type Match struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

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
		roots = []string{"."}
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

		// Parse root .gitignore and .ignore if not NoIgnore
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
			relSlash := filepath.ToSlash(rel)
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
				} else if base == ".git" {
					// Even with NoIgnore, standard behavior skips .git unless specifically handled,
					// but requirement states: "Skip .git directory always unless opts.NoIgnore is true."
				}
				return nil
			}

			// Regular files
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

			if len(opts.AllowedPaths) > 0 && !MatchesAllowedPaths(relSlash, opts.AllowedPaths) {
				return nil
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

	type fileResult struct {
		matches []Match
		err     error
	}

	fileChan := make(chan string, len(candidateFiles))
	for _, f := range candidateFiles {
		fileChan <- f
	}
	close(fileChan)

	var wg sync.WaitGroup
	resChan := make(chan fileResult, len(candidateFiles))

	lowerPattern := opts.Pattern
	if opts.IgnoreCase {
		lowerPattern = strings.ToLower(opts.Pattern)
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1024*1024) // 1MB buffer

			for filePath := range fileChan {
				if ctx.Err() != nil {
					return
				}

				f, err := os.Open(filePath)
				if err != nil {
					continue
				}

				header := make([]byte, 512)
				n, readErr := f.Read(header)
				if readErr != nil && readErr != io.EOF {
					if closeErr := f.Close(); closeErr != nil {
						// log/handle if needed
					}
					continue
				}
				if n > 0 && bytes.IndexByte(header[:n], 0) >= 0 {
					// Binary file, skip
					if closeErr := f.Close(); closeErr != nil {
						// handle close error
					}
					continue
				}

				// Reset file offset
				if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
					if closeErr := f.Close(); closeErr != nil {
						// handle close error
					}
					continue
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
						searchPat := lowerPattern
						if opts.IgnoreCase {
							target = strings.ToLower(lineText)
						}
						colIdx := strings.Index(target, searchPat)
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

				if closeErr := f.Close(); closeErr != nil {
					// handle close error
				}

				if len(fileMatches) > 0 {
					resChan <- fileResult{matches: fileMatches}
				}
			}
		}()
	}

	wg.Wait()
	close(resChan)

	var allMatches []Match
	for res := range resChan {
		allMatches = append(allMatches, res.matches...)
	}

	// Sort results deterministically by File, Line, Column
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
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	if pattern == relPath || pattern == base {
		return true
	}

	// Basename match
	if matched, _ := filepath.Match(pattern, base); matched {
		return true
	}

	// Full path match
	if matched, _ := filepath.Match(pattern, relPath); matched {
		return true
	}

	// Globstar ** match
	if strings.Contains(pattern, "**") {
		return matchGlobstar(pattern, relPath)
	}

	return false
}

func matchGlobstar(pattern, path string) bool {
	parts := strings.Split(pattern, "**")
	if len(parts) == 2 {
		prefix := parts[0]
		suffix := parts[1]
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix) {
			return true
		}
	}
	// Fallback recursive wildcard regex
	rePattern := "^" + regexp.QuoteMeta(pattern) + "$"
	rePattern = strings.ReplaceAll(rePattern, `\*\*`, `.*`)
	rePattern = strings.ReplaceAll(rePattern, `\*`, `[^/]*`)
	rePattern = strings.ReplaceAll(rePattern, `\?`, `[^/]`)
	if matched, _ := regexp.MatchString(rePattern, path); matched {
		return true
	}
	return false
}

// MatchesAllowedPaths checks whether relPath is in allowed paths.
func MatchesAllowedPaths(relPath string, allowedPaths []string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, ap := range allowedPaths {
		ap = filepath.ToSlash(strings.TrimSpace(ap))
		if ap == "" {
			continue
		}
		if ap == relPath {
			return true
		}

		cleanAp := strings.TrimSuffix(ap, "/**")
		cleanAp = strings.TrimSuffix(cleanAp, "/*")
		cleanAp = strings.TrimSuffix(cleanAp, "/")

		if relPath == cleanAp || strings.HasPrefix(relPath, cleanAp+"/") {
			return true
		}

		if matchPattern(ap, relPath, filepath.Base(relPath)) {
			return true
		}
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
