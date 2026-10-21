package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/fsops"
	"github.com/tamld/g8s/internal/receipt"
)

func resolveReceiptAllowedPaths(cmdName, flagReceiptID, flagScopeRoot string, isMutation, jsonl bool) (string, []string) {
	explicitReceipt := strings.TrimSpace(flagReceiptID) != ""
	receiptID, scopeRoot, err := fsops.ResolveDelegationContext(flagReceiptID, flagScopeRoot, isMutation)
	if err != nil {
		exitRuntime(cmdName, "", "", cli.CodeHarness, err, "Delegation boundary violation", jsonl)
	}

	if receiptID != "" && (isMutation || explicitReceipt) {
		rcDbPath, err := receiptDatabasePath()
		if err != nil {
			exitRuntime(cmdName, "", "", cli.CodeDenied, fmt.Errorf("locate receipt database: %w", err), "Ensure G8S_STATE_DIR is set properly", jsonl)
		}
		receipts, err := receipt.NewReceiptManager(rcDbPath, nil)
		if err != nil {
			exitRuntime(cmdName, "", "", cli.CodeDenied, fmt.Errorf("open receipt database: %w", err), "Ensure receipt database is accessible", jsonl)
		}
		defer receipts.Close()

		rec, err := receipts.VerifyReceipt(receiptID)
		if err != nil {
			exitRuntime(cmdName, "", "", cli.CodeDenied, fmt.Errorf("verify receipt %s: %w", receiptID, err), "Provide a valid unconsumed receipt ID", jsonl)
		}
		if rec.Consumed {
			exitRuntime(cmdName, "", "", cli.CodeDenied, fmt.Errorf("receipt %s already consumed", receiptID), "Provide a fresh write receipt", jsonl)
		}
		return scopeRoot, rec.AllowedPaths
	}

	return scopeRoot, nil
}

func parseLinesFlag(linesStr string) (int, int, error) {
	linesStr = strings.TrimSpace(linesStr)
	if linesStr == "" {
		return 0, 0, nil
	}
	parts := strings.Split(linesStr, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid --lines format %q: expected M:N, M:, or :N", linesStr)
	}
	var start, end int
	if strings.TrimSpace(parts[0]) != "" {
		s, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start line in %q: %w", linesStr, err)
		}
		start = s
	}
	if strings.TrimSpace(parts[1]) != "" {
		e, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end line in %q: %w", linesStr, err)
		}
		end = e
	}
	return start, end, nil
}

func runRead(args []string) {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor

	linesFlag := fs.String("lines", "", "line range to read (e.g. 10:50, 10:, :50)")
	startLineFlag := fs.Int("start-line", 0, "1-indexed starting line number")
	endLineFlag := fs.Int("end-line", 0, "1-indexed ending line number")
	maxBytesFlag := fs.Int("max-bytes", 0, "maximum bytes to return before truncation")
	scopeRoot := fs.String("scope-root", "", "base directory for read scope evaluation")
	receiptID := fs.String("receipt-id", "", "write receipt ID for delegation boundary validation")

	valueFlags := map[string]bool{
		"-lines":       true,
		"--lines":      true,
		"-start-line":  true,
		"--start-line": true,
		"-end-line":    true,
		"--end-line":   true,
		"-max-bytes":   true,
		"--max-bytes":  true,
		"-scope-root":  true,
		"--scope-root": true,
		"-receipt-id":  true,
		"--receipt-id": true,
		"-actor":       true,
		"--actor":      true,
		"-trace-id":    true,
		"--trace-id":   true,
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
			if idx := strings.Index(arg, "="); idx != -1 {
				flagArgs = append(flagArgs, arg)
				continue
			}
			if valueFlags[arg] {
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
		exitUsage("read", "", *traceID, err.Error(), "Check g8s read flags", *jsonl)
	}

	if fs.NArg() == 0 {
		exitUsage("read", "", *traceID, "usage: g8s read [flags] <file>", "Provide target file path", *jsonl)
	}
	targetPath := fs.Arg(0)

	startLine := *startLineFlag
	endLine := *endLineFlag
	if *linesFlag != "" {
		lStart, lEnd, err := parseLinesFlag(*linesFlag)
		if err != nil {
			exitUsage("read", "", *traceID, err.Error(), "Provide valid --lines range", *jsonl)
		}
		if lStart > 0 {
			startLine = lStart
		}
		if lEnd > 0 {
			endLine = lEnd
		}
	}

	scope, allowedPaths := resolveReceiptAllowedPaths("read", *receiptID, *scopeRoot, false, *jsonl)

	opts := fsops.ReadOptions{
		Path:         targetPath,
		ScopeRoot:    scope,
		StartLine:    startLine,
		EndLine:      endLine,
		MaxBytes:     *maxBytesFlag,
		AllowedPaths: allowedPaths,
	}

	res, err := fsops.Read(context.Background(), opts)
	if err != nil {
		exitRuntime("read", "", *traceID, cli.CodeIO, err, "Failed to read file", *jsonl)
	}

	if *jsonMode || *jsonl || cli.DefaultSelector() != "" {
		env := cli.NewEnvelope("read", "read", "", res)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	for _, l := range res.Lines {
		fmt.Printf("%d: %s\n", l.Line, l.Text)
	}
}

func runEdit(args []string) {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	actor := fs.String("actor", "", "caller identity for audit logging")
	traceID := fs.String("trace-id", "", "correlation ID across processes")
	jsonl := fs.Bool("jsonl", false, "output single-line JSONL response envelope")
	jsonMode := fs.Bool("json", true, "output JSON response envelope (default true)")
	_ = actor

	oldStr := fs.String("old", "", "exact string to replace in the target file")
	newStr := fs.String("new", "", "replacement string")
	allFlag := fs.Bool("all", false, "replace all occurrences instead of only a single occurrence")
	ifSHA := fs.String("if-sha256", "", "precondition SHA-256 hash expected before editing")
	scopeRoot := fs.String("scope-root", "", "base directory for edit scope evaluation")
	receiptID := fs.String("receipt-id", "", "write receipt ID for delegation boundary validation")

	valueFlags := map[string]bool{
		"-old":         true,
		"--old":        true,
		"-new":         true,
		"--new":        true,
		"-if-sha256":   true,
		"--if-sha256":  true,
		"-scope-root":  true,
		"--scope-root": true,
		"-receipt-id":  true,
		"--receipt-id": true,
		"-actor":       true,
		"--actor":      true,
		"-trace-id":    true,
		"--trace-id":   true,
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
			if idx := strings.Index(arg, "="); idx != -1 {
				flagArgs = append(flagArgs, arg)
				continue
			}
			if valueFlags[arg] {
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
		exitUsage("edit", "", *traceID, err.Error(), "Check g8s edit flags", *jsonl)
	}

	if fs.NArg() == 0 {
		exitUsage("edit", "", *traceID, "usage: g8s edit [flags] <file>", "Provide target file path", *jsonl)
	}
	targetPath := fs.Arg(0)
	if *oldStr == "" {
		exitUsage("edit", "", *traceID, "--old string must not be empty", "Specify --old <string> to replace", *jsonl)
	}

	scope, allowedPaths := resolveReceiptAllowedPaths("edit", *receiptID, *scopeRoot, true, *jsonl)

	opts := fsops.EditOptions{
		Path:         targetPath,
		ScopeRoot:    scope,
		OldString:    *oldStr,
		NewString:    *newStr,
		ReplaceAll:   *allFlag,
		IfSHA256:     *ifSHA,
		AllowedPaths: allowedPaths,
	}

	res, err := fsops.Edit(context.Background(), opts)
	if err != nil {
		exitRuntime("edit", "", *traceID, cli.CodeIO, err, "Failed to edit file", *jsonl)
	}

	_ = jsonMode
	env := cli.NewEnvelope("edit", "edit", "", res)
	env.TraceID = *traceID
	_ = cli.WriteResponse(os.Stdout, env, *jsonl)
}

func runSelect(args []string) {
	fs := flag.NewFlagSet("select", flag.ExitOnError)
	fileFlag := fs.String("file", "", "path to raw JSON file (defaults to reading stdin)")

	valueFlags := map[string]bool{
		"-file":  true,
		"--file": true,
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
			if idx := strings.Index(arg, "="); idx != -1 {
				flagArgs = append(flagArgs, arg)
				continue
			}
			if valueFlags[arg] {
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
		exitUsage("select", "", "", err.Error(), "Check g8s select flags", false)
	}

	if fs.NArg() == 0 {
		exitUsage("select", "", "", "usage: g8s select [--file <path>] <expr>", "Provide a selector path expression", false)
	}
	expr := fs.Arg(0)

	var raw []byte
	if *fileFlag != "" {
		data, err := os.ReadFile(*fileFlag)
		if err != nil {
			exitRuntime("select", "", "", cli.CodeIO, err, "Failed to read input JSON file", false)
		}
		raw = data
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			exitRuntime("select", "", "", cli.CodeIO, err, "Failed to read JSON from standard input", false)
		}
		raw = data
	}

	if len(strings.TrimSpace(string(raw))) == 0 {
		exitRuntime("select", "", "", cli.CodeInvalid, fmt.Errorf("empty JSON input"), "Provide non-empty JSON", false)
	}

	out, err := cli.SelectPath(raw, expr)
	if err != nil {
		exitRuntime("select", "", "", cli.CodeInvalid, err, "Invalid JSON or selector path", false)
	}
	fmt.Println(out)
}
