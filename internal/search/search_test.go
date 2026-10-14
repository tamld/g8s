package search_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tamld/g8s/internal/search"
)

func TestSearchExecute(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Fixed-string and case-insensitive literal search files
	f1 := filepath.Join(tempDir, "file1.txt")
	require.NoError(t, os.WriteFile(f1, []byte("Hello World\nAnother line with hello world\nThird line"), 0o644))

	// 2. Regex test file
	f2 := filepath.Join(tempDir, "file2.txt")
	require.NoError(t, os.WriteFile(f2, []byte("alpha 123\nbeta 456\ngamma 789"), 0o644))

	// 3. Glob filtering files
	go1 := filepath.Join(tempDir, "main.go")
	require.NoError(t, os.WriteFile(go1, []byte("package main\n// Hello go"), 0o644))
	goTest := filepath.Join(tempDir, "main_test.go")
	require.NoError(t, os.WriteFile(goTest, []byte("package main\n// Hello go test"), 0o644))

	// 4. .gitignore, node_modules, and hidden files
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, ".gitignore"), []byte("ignored_dir/\n*.ignored\n# comment line\n"), 0o644))
	ignoredDir := filepath.Join(tempDir, "ignored_dir")
	require.NoError(t, os.MkdirAll(ignoredDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ignoredDir, "foo.txt"), []byte("Hello in ignored dir"), 0o644))

	ignoredFile := filepath.Join(tempDir, "test.ignored")
	require.NoError(t, os.WriteFile(ignoredFile, []byte("Hello in ignored file"), 0o644))

	nodeModules := filepath.Join(tempDir, "node_modules")
	require.NoError(t, os.MkdirAll(nodeModules, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nodeModules, "pkg.js"), []byte("Hello in node_modules"), 0o644))

	hiddenDir := filepath.Join(tempDir, ".hidden")
	require.NoError(t, os.MkdirAll(hiddenDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hiddenDir, "secret.txt"), []byte("Hello in hidden dir"), 0o644))

	// 5. Binary file with NUL byte
	binFile := filepath.Join(tempDir, "binary.bin")
	require.NoError(t, os.WriteFile(binFile, []byte("Hello\x00World binary content"), 0o644))

	// 7. AllowedPaths files
	allowedDir := filepath.Join(tempDir, "pkg", "allowed")
	require.NoError(t, os.MkdirAll(allowedDir, 0o755))
	allowedFile := filepath.Join(allowedDir, "target.go")
	require.NoError(t, os.WriteFile(allowedFile, []byte("// Target Hello code"), 0o644))

	disallowedDir := filepath.Join(tempDir, "pkg", "disallowed")
	require.NoError(t, os.MkdirAll(disallowedDir, 0o755))
	disallowedFile := filepath.Join(disallowedDir, "other.go")
	require.NoError(t, os.WriteFile(disallowedFile, []byte("// Target Hello code disallowed"), 0o644))

	ctx := context.Background()

	// Scenario 1: Fixed-string and case-insensitive literal search
	t.Run("Fixed-string and case-insensitive", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:        []string{tempDir},
			Pattern:      "HELLO",
			FixedStrings: true,
			IgnoreCase:   true,
		})
		require.NoError(t, err)
		assert.Equal(t, "native", res.Engine)
		assert.NotEmpty(t, res.Matches)

		foundHello := false
		for _, m := range res.Matches {
			if filepath.ToSlash(m.File) == filepath.ToSlash(f1) {
				foundHello = true
				assert.Equal(t, 1, m.Line)
				assert.Equal(t, 1, m.Column)
				assert.Equal(t, "Hello World", m.Text)
				break
			}
		}
		assert.True(t, foundHello)
	})

	// Scenario 2: Regex search + invalid regex error handling
	t.Run("Regex search and invalid error handling", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:   []string{tempDir},
			Pattern: `\w+\s+\d{3}`,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Matches)

		_, errInvalid := search.Execute(ctx, search.Options{
			Roots:   []string{tempDir},
			Pattern: `[unclosed-regex`,
		})
		require.Error(t, errInvalid)
	})

	// Scenario 3: Glob filtering
	t.Run("Glob filtering (*.go, !*_test.go)", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:   []string{tempDir},
			Pattern: "Hello",
			Globs:   []string{"*.go", "!*_test.go"},
		})
		require.NoError(t, err)
		for _, m := range res.Matches {
			assert.Contains(t, m.File, ".go")
			assert.NotContains(t, m.File, "_test.go")
		}
	})

	// Scenario 4: .gitignore, node_modules, and hidden file skipping vs IncludeHidden / NoIgnore
	t.Run("Gitignore, node_modules, and hidden skipping", func(t *testing.T) {
		// By default should skip node_modules, hidden, and .gitignore patterns
		res, err := search.Execute(ctx, search.Options{
			Roots:   []string{tempDir},
			Pattern: "Hello",
		})
		require.NoError(t, err)
		for _, m := range res.Matches {
			assert.NotContains(t, m.File, "node_modules")
			assert.NotContains(t, m.File, ".hidden")
			assert.NotContains(t, m.File, "ignored_dir")
			assert.NotContains(t, m.File, ".ignored")
		}

		// With NoIgnore: true and IncludeHidden: true
		resAll, err := search.Execute(ctx, search.Options{
			Roots:         []string{tempDir},
			Pattern:       "Hello",
			NoIgnore:      true,
			IncludeHidden: true,
		})
		require.NoError(t, err)
		foundHidden := false
		foundNodeModules := false
		for _, m := range resAll.Matches {
			if filepath.ToSlash(m.File) == filepath.ToSlash(filepath.Join(hiddenDir, "secret.txt")) {
				foundHidden = true
			}
			if filepath.ToSlash(m.File) == filepath.ToSlash(filepath.Join(nodeModules, "pkg.js")) {
				foundNodeModules = true
			}
		}
		assert.True(t, foundHidden)
		assert.True(t, foundNodeModules)
	})

	// Scenario 5: Binary file with NUL byte skipped automatically
	t.Run("Binary file skipped automatically", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:        []string{tempDir},
			Pattern:      "World",
			FixedStrings: true,
		})
		require.NoError(t, err)
		for _, m := range res.Matches {
			assert.NotContains(t, m.File, "binary.bin")
		}
	})

	// Scenario 6: FilesOnly: true deterministic sorted file discovery
	t.Run("FilesOnly discovery mode", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:     []string{tempDir},
			FilesOnly: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "native", res.Engine)
		assert.NotEmpty(t, res.Files)
		assert.Empty(t, res.Matches)

		for i := 1; i < len(res.Files); i++ {
			assert.True(t, res.Files[i-1] <= res.Files[i], "Files should be sorted deterministically")
		}
	})

	// Scenario 7: AllowedPaths scope filtering
	t.Run("AllowedPaths scope filtering", func(t *testing.T) {
		res, err := search.Execute(ctx, search.Options{
			Roots:        []string{tempDir},
			Pattern:      "Target",
			AllowedPaths: []string{"pkg/allowed/**"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Matches)
		for _, m := range res.Matches {
			assert.Contains(t, filepath.ToSlash(m.File), "pkg/allowed")
			assert.NotContains(t, filepath.ToSlash(m.File), "pkg/disallowed")
		}
	})
}
