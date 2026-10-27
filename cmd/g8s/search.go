package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/receipt"
	"github.com/tamld/g8s/internal/search"
)

type searchData struct {
	Matches       []search.Match `json:"matches,omitempty"`
	Files         []string       `json:"files,omitempty"`
	FilesSearched int            `json:"files_searched"`
	DurationMs    int64          `json:"duration_ms"`
	Engine        string         `json:"engine"`
	ReceiptID     string         `json:"receipt_id,omitempty"`
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor

	fixedStrings := fs.Bool("F", false, "treat pattern as a literal string")
	fixedStringsLong := fs.Bool("fixed-strings", false, "treat pattern as a literal string")
	ignoreCase := fs.Bool("i", false, "case-insensitive search")
	ignoreCaseLong := fs.Bool("ignore-case", false, "case-insensitive search")
	quiet := fs.Bool("q", false, "quiet mode; exit 0 on match, 1 on no match")
	quietLong := fs.Bool("quiet", false, "quiet mode; exit 0 on match, 1 on no match")
	filesOnly := fs.Bool("files", false, "print matching files without searching contents")
	includeHidden := fs.Bool("hidden", false, "search hidden files and directories")
	noIgnore := fs.Bool("no-ignore", false, "do not respect .gitignore or default ignore rules")
	noIgnoreShort := fs.Bool("u", false, "do not respect .gitignore or default ignore rules")
	scopeRoot := fs.String("scope-root", "", "base directory for search and receipt scope evaluation")
	receiptID := fs.String("receipt-id", "", "restrict search/files to paths allowed by WriteReceipt")
	fs.StringVar(receiptID, "receipt", "", "alias for --receipt-id")
	maxResults := fs.Int("max-results", 1000, "maximum matches or files to return")
	var globs pathFlags
	fs.Var(&globs, "g", "include or exclude files matching glob")
	fs.Var(&globs, "glob", "include or exclude files matching glob")
	fs.Var(&globs, "globs", "include or exclude files matching glob (alias for --glob)")

	lineNumber := fs.Bool("n", true, "print 1-based line numbers (enabled by default)")
	lineNumberLong := fs.Bool("line-number", true, "print 1-based line numbers (enabled by default)")
	filesWithMatches := fs.Bool("l", false, "print only unique paths of files with matches")
	filesWithMatchesLong := fs.Bool("files-with-matches", false, "print only unique paths of files with matches")
	countMode := fs.Bool("c", false, "print count of matching lines per file")
	countModeLong := fs.Bool("count", false, "print count of matching lines per file")
	contextLines := fs.Int("C", 0, "context lines (accepted for rg/grep compatibility)")
	afterLines := fs.Int("A", 0, "context lines (accepted for rg/grep compatibility)")
	beforeLines := fs.Int("B", 0, "context lines (accepted for rg/grep compatibility)")
	_ = lineNumber
	_ = lineNumberLong
	_ = contextLines
	_ = afterLines
	_ = beforeLines

	valueFlags := map[string]bool{
		"-g":            true,
		"--g":           true,
		"-glob":         true,
		"--glob":        true,
		"-globs":        true,
		"--globs":       true,
		"-scope-root":   true,
		"--scope-root":  true,
		"-receipt-id":   true,
		"--receipt-id":  true,
		"-receipt":      true,
		"--receipt":     true,
		"-max-results":  true,
		"--max-results": true,
		"-actor":        true,
		"--actor":       true,
		"-trace-id":     true,
		"--trace-id":    true,
		"-C":            true,
		"--C":           true,
		"-A":            true,
		"--A":           true,
		"-B":            true,
		"--B":           true,
	}

	var flagArgs []string
	var posArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			name := arg
			if idx := strings.Index(name, "="); idx != -1 {
				flagArgs = append(flagArgs, arg)
				continue
			}
			if valueFlags[name] {
				flagArgs = append(flagArgs, arg)
				if i+1 < len(args) {
					i++
					flagArgs = append(flagArgs, args[i])
				}
				continue
			}
			flagArgs = append(flagArgs, arg)
			continue
		}
		posArgs = append(posArgs, arg)
	}

	if err := fs.Parse(append(flagArgs, posArgs...)); err != nil {
		exitUsage("search", "", *traceID, err.Error(), "Check g8s search flags", *jsonl)
	}

	var pattern string
	var roots []string

	if *filesOnly {
		roots = fs.Args()
	} else {
		if fs.NArg() == 0 {
			exitUsage("search", "", *traceID, "usage: g8s search [flags] <pattern> [path...]", "Provide a search pattern or pass --files", *jsonl)
		}
		pattern = fs.Arg(0)
		roots = fs.Args()[1:]
	}

	hydrateTaskAddDirsEnv()
	explicitReceipt := strings.TrimSpace(*receiptID) != ""
	resReceiptID, resScopeRoot, err := fsops.ResolveDelegationContext(*receiptID, *scopeRoot, false)
	if err != nil {
		exitRuntime("search", "", *traceID, cli.CodeHarness, err, "Delegation boundary violation", *jsonl)
	}
	*receiptID = resReceiptID
	envTier := strings.TrimSpace(os.Getenv("G8S_TIER"))
	isDelegatedTier := strings.EqualFold(envTier, "F1") || strings.EqualFold(envTier, "F2")
	if *scopeRoot == "" && (resScopeRoot != "." || isDelegatedTier || strings.TrimSpace(os.Getenv("G8S_SCOPE_ROOT")) != "") {
		*scopeRoot = resScopeRoot
	}

	if len(roots) == 0 {
		if *scopeRoot != "" {
			roots = []string{*scopeRoot}
		} else {
			roots = []string{"."}
		}
	}

	var allowedPaths []string
	cleanReceiptID := strings.TrimSpace(*receiptID)
	if cleanReceiptID != "" && explicitReceipt {
		rcDbPath, err := receiptDatabasePath()
		if err != nil {
			exitRuntime("search", "", *traceID, cli.CodeDenied, fmt.Errorf("locate receipt database: %w", err), "Ensure G8S_STATE_DIR is set properly", *jsonl)
		}
		receipts, err := receipt.NewReceiptManager(rcDbPath, nil)
		if err != nil {
			exitRuntime("search", "", *traceID, cli.CodeDenied, fmt.Errorf("open receipt database: %w", err), "Ensure receipt database is accessible", *jsonl)
		}
		defer receipts.Close()

		rc, err := receipts.VerifyReceipt(cleanReceiptID)
		if err != nil {
			exitRuntime("search", "", *traceID, cli.CodeDenied, fmt.Errorf("verify receipt %s: %w", cleanReceiptID, err), "Provide a valid unconsumed receipt ID", *jsonl)
		}
		if rc.Consumed {
			exitRuntime("search", "", *traceID, cli.CodeDenied, fmt.Errorf("receipt %s already consumed", cleanReceiptID), "Provide a fresh write receipt", *jsonl)
		}
		allowedPaths = rc.AllowedPaths
	}

	opts := search.Options{
		Pattern:       pattern,
		Roots:         roots,
		ScopeRoot:     *scopeRoot,
		FixedStrings:  *fixedStrings || *fixedStringsLong,
		IgnoreCase:    *ignoreCase || *ignoreCaseLong,
		FilesOnly:     *filesOnly,
		IncludeHidden: *includeHidden,
		NoIgnore:      *noIgnore || *noIgnoreShort,
		Globs:         globs,
		MaxResults:    *maxResults,
		AllowedPaths:  allowedPaths,
	}

	res, err := search.Execute(context.Background(), opts)
	if err != nil {
		exitUsage("search", "", *traceID, err.Error(), "", *jsonl)
	}

	isFilesWithMatches := *filesWithMatches || *filesWithMatchesLong
	isCountMode := *countMode || *countModeLong

	if isFilesWithMatches && !*filesOnly {
		var uniqueFiles []string
		seen := make(map[string]bool)
		for _, m := range res.Matches {
			if !seen[m.File] {
				seen[m.File] = true
				uniqueFiles = append(uniqueFiles, m.File)
			}
		}
		res.Files = uniqueFiles
	}

	isQuiet := *quiet || *quietLong
	if isQuiet {
		if (*filesOnly && len(res.Files) > 0) || (!*filesOnly && len(res.Matches) > 0) {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if *jsonMode || *jsonl || cli.DefaultSelector() != "" {
		data := searchData{
			Matches:       res.Matches,
			Files:         res.Files,
			FilesSearched: res.FilesSearched,
			DurationMs:    res.DurationMs,
			Engine:        res.Engine,
			ReceiptID:     cleanReceiptID,
		}
		env := cli.NewEnvelope("search", "search", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	if *filesOnly {
		for _, f := range res.Files {
			fmt.Println(f)
		}
		if len(res.Files) == 0 {
			os.Exit(1)
		}
		return
	}

	if isFilesWithMatches {
		for _, f := range res.Files {
			fmt.Println(f)
		}
		if len(res.Files) == 0 {
			os.Exit(1)
		}
		return
	}

	if isCountMode {
		var fileOrder []string
		counts := make(map[string]int)
		for _, m := range res.Matches {
			if _, exists := counts[m.File]; !exists {
				fileOrder = append(fileOrder, m.File)
			}
			counts[m.File]++
		}
		for _, f := range fileOrder {
			fmt.Printf("%s:%d\n", f, counts[f])
		}
		if len(fileOrder) == 0 {
			os.Exit(1)
		}
		return
	}

	for _, m := range res.Matches {
		fmt.Printf("%s:%d:%d:%s\n", m.File, m.Line, m.Column, m.Text)
	}
	if len(res.Matches) == 0 {
		os.Exit(1)
	}
}
