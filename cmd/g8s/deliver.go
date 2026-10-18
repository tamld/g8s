package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
)

type deliverablePointer struct {
	Mode string `json:"mode"`
	Dir  string `json:"dir"`
}

type deliverableResult struct {
	Deliverable *deliverablePointer `json:"deliverable"`
}

type deliverData struct {
	TaskID         string   `json:"task_id"`
	DeliverableDir string   `json:"deliverable_dir"`
	Applied        []string `json:"applied"`
	Deleted        []string `json:"deleted,omitempty"`
	Skipped        []string `json:"skipped"`
	ReceiptID      string   `json:"receipt_id"`
	PrunedWorktree bool     `json:"pruned_worktree,omitempty"`
}

func runDeliver(args []string) {
	fs := flag.NewFlagSet("deliver", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	dryRun := fs.Bool("dry-run", false, "perform validation and list files without copying")
	pruneWorktree := fs.Bool("prune-worktree", false, "remove the isolated git worktree and wt/<task-id> branch after successful delivery")
	taskIDFlag := fs.String("task-id", "", "task ID to deliver")
	worktreeFlag := fs.String("worktree", "", "explicit preserved worktree directory (#427 deliverable survival)")
	receiptIDFlag := fs.String("receipt-id", "", "explicit receipt ID override for delivering preserved worktrees")

	// Allow flags to appear before or after positional task-id
	var positional []string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			if (arg == "--task-id" || arg == "-task-id" || arg == "--worktree" || arg == "-worktree" || arg == "--receipt-id" || arg == "-receipt-id" || arg == "--actor" || arg == "-actor" || arg == "--trace-id" || arg == "-trace-id") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	parseArgs := append(flagArgs, positional...)

	if err := fs.Parse(parseArgs); err != nil {
		exitUsage("deliver", "", *traceID, err.Error(), "", *jsonl)
	}

	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("deliver", "", *traceID, "usage: g8s deliver <task-id> [--worktree <dir>] [--prune-worktree] [--dry-run]", "Provide a task ID", *jsonl)
	}

	// 1. Resolve the task from the control plane
	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeIO, err, "Failed to resolve database path", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeRuntime, err, "Failed to open control plane database", *jsonl)
	}
	defer store.Close()

	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if task == nil {
		exitRuntime("deliver", "", *traceID, cli.CodeNotFound, fmt.Errorf("unknown task: %s", taskID), "Verify the task ID with 'g8s tasks'", *jsonl)
	}

	// 2. Read the task's result JSON (or explicit --worktree override for #427 preserved worktrees)
	var res deliverableResult
	if len(task.Result) > 0 {
		if err := json.Unmarshal(task.Result, &res); err != nil {
			exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
				fmt.Errorf("corrupt result JSON for task %s: %w", taskID, err),
				"Task result is not valid JSON", *jsonl)
		}
	}
	deliverableDir := strings.TrimSpace(*worktreeFlag)
	if deliverableDir == "" && res.Deliverable != nil && res.Deliverable.Mode == "worktree" {
		deliverableDir = strings.TrimSpace(res.Deliverable.Dir)
	}
	if deliverableDir == "" {
		exitUsage("deliver", "", *traceID,
			fmt.Sprintf("no deliverable pointer for %s (attempt was not worktree-isolated, failed, or predates the pointer contract)", taskID),
			"Pass --worktree <dir> to deliver a preserved worktree after supervisor inspection", *jsonl)
	}

	// 3. In the pointer dir, run `git status --porcelain`
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = deliverableDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
			fmt.Errorf("git status --porcelain failed in %s: %w (%s)", deliverableDir, err, strings.TrimSpace(string(out))),
			"", *jsonl)
	}

	var rawCandidates []string
	var deletedCandidates []string
	seenDeleted := make(map[string]bool)
	skipped := []string{}
	seenSkipped := make(map[string]bool)

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}
		x := line[0]
		y := line[1]
		rawPath := strings.TrimSpace(line[3:])
		if len(rawPath) >= 2 && rawPath[0] == '"' && rawPath[len(rawPath)-1] == '"' {
			if unquoted, uerr := strconv.Unquote(rawPath); uerr == nil {
				rawPath = unquoted
			}
		}

		if x == 'R' || y == 'R' {
			parts := strings.Split(rawPath, " -> ")
			target := strings.TrimSpace(parts[len(parts)-1])
			if len(target) >= 2 && target[0] == '"' && target[len(target)-1] == '"' {
				if unquoted, uerr := strconv.Unquote(target); uerr == nil {
					target = unquoted
				}
			}
			cleanTarget := filepath.ToSlash(filepath.Clean(target))
			if !seenSkipped[cleanTarget] {
				seenSkipped[cleanTarget] = true
				skipped = append(skipped, cleanTarget)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: copy-apply does not model rename in v1\n", rawPath)
			continue
		}

		if x == 'C' || y == 'C' {
			parts := strings.Split(rawPath, " -> ")
			target := strings.TrimSpace(parts[len(parts)-1])
			if len(target) >= 2 && target[0] == '"' && target[len(target)-1] == '"' {
				if unquoted, uerr := strconv.Unquote(target); uerr == nil {
					target = unquoted
				}
			}
			cleanTarget := filepath.ToSlash(filepath.Clean(target))
			if !seenSkipped[cleanTarget] {
				seenSkipped[cleanTarget] = true
				skipped = append(skipped, cleanTarget)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: copy-apply does not model copy in v1\n", rawPath)
			continue
		}

		if x == 'D' || y == 'D' {
			cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
			if !seenDeleted[cleanTarget] {
				seenDeleted[cleanTarget] = true
				deletedCandidates = append(deletedCandidates, cleanTarget)
			}
			continue
		}

		if x == 'T' || y == 'T' {
			cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
			if !seenSkipped[cleanTarget] {
				seenSkipped[cleanTarget] = true
				skipped = append(skipped, cleanTarget)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: copy-apply does not model typechange in v1\n", rawPath)
			continue
		}

		if x == 'U' || y == 'U' {
			cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
			if !seenSkipped[cleanTarget] {
				seenSkipped[cleanTarget] = true
				skipped = append(skipped, cleanTarget)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: copy-apply does not model unmerged in v1\n", rawPath)
			continue
		}

		if (x == '?' && y == '?') || x == 'M' || y == 'M' || x == 'A' || y == 'A' {
			cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
			rawCandidates = append(rawCandidates, cleanTarget)
			continue
		}

		cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
		if !seenSkipped[cleanTarget] {
			seenSkipped[cleanTarget] = true
			skipped = append(skipped, cleanTarget)
		}
		fmt.Fprintf(os.Stderr, "warning: skipping %s: copy-apply does not model status %c%c in v1\n", rawPath, x, y)
	}

	candidates := []string{}
	seenCandidates := make(map[string]bool)
	for _, target := range rawCandidates {
		fullPath := filepath.Join(deliverableDir, filepath.FromSlash(target))
		fi, statErr := os.Stat(fullPath)
		if statErr == nil && fi.IsDir() {
			_ = filepath.Walk(fullPath, func(p string, info os.FileInfo, wErr error) error {
				if wErr != nil || info.IsDir() {
					return nil
				}
				rel, rErr := filepath.Rel(deliverableDir, p)
				if rErr == nil {
					cleanRel := filepath.ToSlash(filepath.Clean(rel))
					if !seenCandidates[cleanRel] {
						seenCandidates[cleanRel] = true
						candidates = append(candidates, cleanRel)
					}
				}
				return nil
			})
		} else {
			if !seenCandidates[target] {
				seenCandidates[target] = true
				candidates = append(candidates, target)
			}
		}
	}

	// 4. Receipt gate (the enforcement moment)
	var payload struct {
		ReceiptID string   `json:"receipt_id"`
		AddDirs   []string `json:"add_dirs"`
	}
	var wrapper struct {
		Payload struct {
			ReceiptID string   `json:"receipt_id"`
			AddDirs   []string `json:"add_dirs"`
		} `json:"payload"`
	}
	if len(task.Request) > 0 {
		if err := json.Unmarshal(task.Request, &payload); err != nil {
			exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
				fmt.Errorf("corrupt request payload JSON for task %s: %w", taskID, err),
				"Task request payload is not valid JSON", *jsonl)
		}
		_ = json.Unmarshal(task.Request, &wrapper)
	}
	receiptID := strings.TrimSpace(payload.ReceiptID)
	if receiptID == "" {
		receiptID = strings.TrimSpace(wrapper.Payload.ReceiptID)
	}
	if strings.TrimSpace(*receiptIDFlag) != "" {
		receiptID = strings.TrimSpace(*receiptIDFlag)
	}
	if receiptID == "" {
		exitRuntime("deliver", "", *traceID, cli.CodeDenied,
			fmt.Errorf("task %s has missing or empty receipt_id in payload", taskID),
			"A valid write receipt is required to deliver workspace changes", *jsonl)
	}

	rcDbPath, err := receiptDatabasePath()
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeIO, err, "Failed to resolve receipts database path", *jsonl)
	}
	receipts, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeRuntime, err, "Failed to open receipts manager", *jsonl)
	}
	defer receipts.Close()

	rc, err := receipts.VerifyReceipt(receiptID)
	if err != nil {
		var expErr *receipt.ExpiredError
		if errors.As(err, &expErr) {
			consumedList, listErr := receipts.ListReceipts(taskID, time.Time{}, "consumed")
			if listErr == nil {
				for _, item := range consumedList {
					if item != nil && item.ReceiptID == receiptID && item.Consumed && item.ConsumerTaskID != nil && *item.ConsumerTaskID == taskID {
						rc = item
						err = nil
						break
					}
				}
			}
		}
	}
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeDenied,
			fmt.Errorf("load receipt %s: %w", receiptID, err),
			"A valid write receipt is required to deliver workspace changes", *jsonl)
	}

	var offending []string
	for _, file := range candidates {
		if !deliverPathMatchesScope(file, rc.AllowedPaths) {
			offending = append(offending, file)
		}
	}
	for _, file := range deletedCandidates {
		if !deliverPathMatchesScope(file, rc.AllowedPaths) {
			offending = append(offending, file)
		}
	}
	if len(offending) > 0 {
		for _, off := range offending {
			fmt.Fprintf(os.Stderr, "out of scope path: %s\n", off)
		}
		exitRuntime("deliver", "", *traceID, cli.CodeDenied,
			fmt.Errorf("out-of-scope path(s) not covered by receipt %s: %s", receiptID, strings.Join(offending, ", ")),
			"All modified files must be covered by receipt allowed_paths", *jsonl)
	}

	// 5. Apply
	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("resolve current working directory: %w", err), "", *jsonl)
	}
	addDirs := payload.AddDirs
	if len(addDirs) == 0 {
		addDirs = wrapper.Payload.AddDirs
	}
	if len(addDirs) > 0 && strings.TrimSpace(addDirs[0]) != "" {
		if fi, statErr := os.Stat(strings.TrimSpace(addDirs[0])); statErr == nil && fi.IsDir() {
			cwd = strings.TrimSpace(addDirs[0])
		}
	}

	var toApply []string
	for _, rel := range candidates {
		destPath := filepath.Join(cwd, filepath.FromSlash(rel))
		if fi, err := os.Lstat(destPath); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			if !seenSkipped[rel] {
				seenSkipped[rel] = true
				skipped = append(skipped, rel)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: destination is a symlink\n", rel)
			continue
		}
		toApply = append(toApply, rel)
	}

	var toDelete []string
	for _, rel := range deletedCandidates {
		destPath := filepath.Join(cwd, filepath.FromSlash(rel))
		if fi, err := os.Lstat(destPath); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			if !seenSkipped[rel] {
				seenSkipped[rel] = true
				skipped = append(skipped, rel)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: destination is a symlink\n", rel)
			continue
		}
		toDelete = append(toDelete, rel)
	}

	applied := []string{}
	var deleted []string
	if !*dryRun {
		type stagedFile struct {
			tmpPath  string
			destPath string
			rel      string
		}
		var staged []stagedFile
		var createdTemps []string

		cleanupTemps := func() {
			for _, tmp := range createdTemps {
				_ = os.Remove(tmp)
			}
			createdTemps = nil
		}

		for _, rel := range toApply {
			srcPath := filepath.Join(deliverableDir, filepath.FromSlash(rel))
			destPath := filepath.Join(cwd, filepath.FromSlash(rel))

			srcInfo, err := os.Stat(srcPath)
			if err != nil {
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("stat deliverable file %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}

			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("create parent directory for %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}

			content, err := os.ReadFile(srcPath)
			if err != nil {
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("read deliverable file %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}

			mode := srcInfo.Mode().Perm()
			if mode == 0 {
				mode = 0o644
			}

			tmpFile, err := os.CreateTemp(destDir, ".g8s-deliver-*")
			if err != nil {
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("create temp file for %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}
			tmpPath := tmpFile.Name()
			createdTemps = append(createdTemps, tmpPath)

			if _, err := tmpFile.Write(content); err != nil {
				tmpFile.Close()
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("write temp file for %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}
			_ = tmpFile.Chmod(mode)
			if err := tmpFile.Close(); err != nil {
				cleanupTemps()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("close temp file for %s: %w (files landed: %s)", rel, err, landedStr), "", *jsonl)
			}

			staged = append(staged, stagedFile{tmpPath: tmpPath, destPath: destPath, rel: rel})
		}

		for i, s := range staged {
			if err := os.Rename(s.tmpPath, s.destPath); err != nil {
				for j := i; j < len(staged); j++ {
					_ = os.Remove(staged[j].tmpPath)
				}
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("rename applied file %s: %w (files landed: %s)", s.rel, err, landedStr), "", *jsonl)
			}
			applied = append(applied, s.rel)
		}

		for _, rel := range toDelete {
			destPath := filepath.Join(cwd, filepath.FromSlash(rel))
			if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
				exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("remove deleted file %s: %w", rel, err), "", *jsonl)
			}
			deleted = append(deleted, rel)
		}
	} else {
		applied = append(applied, toApply...)
		deleted = append(deleted, toDelete...)
	}

	pruned := false
	if *pruneWorktree && !*dryRun {
		rmCmd := exec.Command("git", "worktree", "remove", "--force", "--", deliverableDir)
		rmCmd.Dir = cwd
		if out, rmErr := rmCmd.CombinedOutput(); rmErr == nil {
			pruned = true
		} else {
			fmt.Fprintf(os.Stderr, "warning: failed to remove worktree %s: %v (%s)\n", deliverableDir, rmErr, strings.TrimSpace(string(out)))
		}
		brCmd := exec.Command("git", "branch", "-D", "--", "wt/"+taskID)
		brCmd.Dir = cwd
		_, _ = brCmd.CombinedOutput()
	}

	data := deliverData{
		TaskID:         taskID,
		DeliverableDir: deliverableDir,
		Applied:        applied,
		Deleted:        deleted,
		Skipped:        skipped,
		ReceiptID:      receiptID,
		PrunedWorktree: pruned,
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("deliver", "deliver", "", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("deliver", "", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	for _, rel := range applied {
		if *dryRun {
			fmt.Printf("would apply: %s\n", rel)
		} else {
			fmt.Printf("applied: %s\n", rel)
		}
	}
	for _, rel := range deleted {
		if *dryRun {
			fmt.Printf("would delete: %s\n", rel)
		} else {
			fmt.Printf("deleted: %s\n", rel)
		}
	}
	for _, rel := range skipped {
		fmt.Printf("skipped: %s\n", rel)
	}
}

func deliverPathMatchesScope(cleanFile string, allowed []string) bool {
	if cleanFile == ".." || strings.HasPrefix(cleanFile, "../") || strings.HasPrefix(cleanFile, "/") || deliverIsWindowsDrive(cleanFile) {
		return false
	}
	if cleanFile == "." {
		return false
	}
	for _, pat := range allowed {
		if deliverPathMatchesPattern(cleanFile, pat) {
			return true
		}
	}
	return false
}

func deliverIsWindowsDrive(p string) bool {
	if len(p) >= 2 && p[1] == ':' {
		drive := p[0]
		return (drive >= 'a' && drive <= 'z') || (drive >= 'A' && drive <= 'Z')
	}
	return false
}

func deliverPathMatchesPattern(cleanFile, pattern string) bool {
	cleanPattern := filepath.ToSlash(filepath.Clean(pattern))
	if cleanPattern == "*" || cleanPattern == "**" {
		return true
	}
	if cleanFile == cleanPattern {
		return true
	}
	if strings.Contains(cleanPattern, "**") {
		return deliverGlobstarMatch(
			strings.Split(cleanFile, "/"),
			strings.Split(cleanPattern, "/"),
		)
	}
	if strings.HasSuffix(pattern, "/*") || strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimSuffix(cleanPattern, "*")
		if !strings.HasSuffix(dirPrefix, "/") {
			dirPrefix += "/"
		}
		if strings.HasPrefix(cleanFile, dirPrefix) {
			return true
		}
	} else if !strings.Contains(cleanPattern, "*") {
		if strings.HasPrefix(cleanFile, cleanPattern+"/") {
			return true
		}
	}
	m, _ := filepath.Match(cleanPattern, cleanFile)
	return m
}

func deliverGlobstarMatch(fileSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(fileSegs) == 0
	}
	if patSegs[0] == "**" {
		for i := 0; i <= len(fileSegs); i++ {
			if deliverGlobstarMatch(fileSegs[i:], patSegs[1:]) {
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
	return deliverGlobstarMatch(fileSegs[1:], patSegs[1:])
}
