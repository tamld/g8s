package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/lockfile"
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
	TaskID            string   `json:"task_id"`
	DeliverableDir    string   `json:"deliverable_dir"`
	Applied           []string `json:"applied"`
	Deleted           []string `json:"deleted,omitempty"`
	Skipped           []string `json:"skipped"`
	ReceiptID         string   `json:"receipt_id"`
	HeadOID           string   `json:"head_oid,omitempty"`
	PrunedWorktree    bool     `json:"pruned_worktree,omitempty"`
	OverrideReason    string   `json:"override_reason,omitempty"`
	VerificationState string   `json:"verification_state,omitempty"`
}

type deliverRenameOp struct {
	oldPath string
	newPath string
}

type deliverCopyOp struct {
	newPath string
}

func runDeliver(args []string) {
	fs := flag.NewFlagSet("deliver", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	dryRun := fs.Bool("dry-run", false, "perform validation and list files without copying")
	pruneWorktree := fs.Bool("prune-worktree", false, "remove the isolated git worktree and wt/<task-id> branch after successful delivery")
	taskIDFlag := fs.String("task-id", "", "task ID to deliver")
	taskAliasFlag := fs.String("task", "", "task ID to deliver (canonical alias for --task-id)")
	worktreeFlag := fs.String("worktree", "", "explicit preserved worktree directory (#427 deliverable survival)")
	receiptIDFlag := fs.String("receipt-id", "", "explicit receipt ID override for delivering preserved worktrees")
	receiptAliasFlag := fs.String("receipt", "", "explicit receipt ID override (canonical alias for --receipt-id)")
	expectedHeadOID := fs.String("expected-head-oid", "", "optional expected git HEAD SHA in target workspace (CAS STALE_HEAD guard; DELTA-34)")
	allowUnverifiedOverride := fs.Bool("allow-unverified-override", false, "allow F0 override to deliver unverified or rejected tasks (requires --override-reason)")
	overrideReason := fs.String("override-reason", "", "mandatory audit rationale when --allow-unverified-override is used")

	// Allow flags to appear before or after positional task-id
	var positional []string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			if (arg == "--task-id" || arg == "-task-id" || arg == "--task" || arg == "-task" || arg == "--worktree" || arg == "-worktree" || arg == "--receipt-id" || arg == "-receipt-id" || arg == "--receipt" || arg == "-receipt" || arg == "--expected-head-oid" || arg == "-expected-head-oid" || arg == "--override-reason" || arg == "-override-reason" || arg == "--actor" || arg == "-actor" || arg == "--trace-id" || arg == "-trace-id") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
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
	if taskID == "" {
		taskID = *taskAliasFlag
	}
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if *receiptIDFlag == "" && *receiptAliasFlag != "" {
		*receiptIDFlag = *receiptAliasFlag
	}
	if taskID == "" {
		exitUsage("deliver", "", *traceID, "usage: g8s deliver <task-id> [--worktree <dir>] [--prune-worktree] [--dry-run]", "Provide a task ID", *jsonl)
	}
	if *allowUnverifiedOverride && strings.TrimSpace(*overrideReason) == "" {
		exitUsage("deliver", "", *traceID, "--allow-unverified-override requires a non-empty --override-reason", "Provide an audit rationale for the override", *jsonl)
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

	verifiedOK := task.State == controlplane.StateSupervisorAccepted || task.State == controlplane.StateSucceeded
	isPreservedWorktreeRecovery := strings.TrimSpace(*worktreeFlag) != "" && task.State != controlplane.StateSupervisorRejected
	var recordedOverrideReason string
	var recordedVerificationState string
	if !verifiedOK {
		if *allowUnverifiedOverride {
			recordedOverrideReason = strings.TrimSpace(*overrideReason)
			recordedVerificationState = task.State
			fmt.Fprintf(os.Stderr, "warning: delivering task %s in state %s via --allow-unverified-override (reason: %s)\n",
				taskID, task.State, recordedOverrideReason)
		} else if isPreservedWorktreeRecovery {
			// #427 preserved-worktree recovery
		} else {
			exitRuntime("deliver", "", *traceID, cli.CodeDenied,
				fmt.Errorf("task %s is in state %s; must be SUPERVISOR_ACCEPTED before deliver (run 'g8s verify --task %s' or pass --allow-unverified-override --override-reason)", taskID, task.State, taskID),
				"", *jsonl)
		}
	}

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
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("resolve absolute workspace path: %w", err), "", *jsonl)
	}

	lockKey := filepath.Clean(cwd)
	if eval, evalErr := filepath.EvalSymlinks(lockKey); evalErr == nil {
		lockKey = eval
	}
	sum := sha256.Sum256([]byte(lockKey))
	lockPath := filepath.Join(filepath.Dir(dbPath), fmt.Sprintf("deliver-%x.lock", sum[:8]))
	wsLock, err := lockfile.TryLock(lockPath)
	if err != nil {
		if errors.Is(err, lockfile.ErrContended) {
			exitRuntime("deliver", "", *traceID, cli.CodeConflict,
				fmt.Errorf("deliver workspace lock held: %s", lockPath),
				"Another deliver is in progress for this workspace; retry when it completes", *jsonl)
		}
		exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("acquire deliver workspace lock %s: %w", lockPath, err), "", *jsonl)
	}
	defer func() {
		if relErr := wsLock.Release(); relErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release deliver workspace lock %s: %v\n", lockPath, relErr)
		}
	}()

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

	// 3. In the pointer dir, run `git status --porcelain -z`
	cmd := exec.Command("git", "status", "--porcelain", "-z")
	cmd.Dir = deliverableDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
			fmt.Errorf("git status --porcelain failed in %s: %w (%s)", deliverableDir, err, strings.TrimSpace(string(out))),
			"", *jsonl)
	}

	var rawCandidates []string
	var deletedCandidates []string
	var renameOps []deliverRenameOp
	var copyOps []deliverCopyOp
	seenDeleted := make(map[string]bool)
	skipped := []string{}
	seenSkipped := make(map[string]bool)

	entries := bytes.Split(out, []byte{0})
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) == 0 {
			continue
		}
		if len(entry) < 4 {
			continue
		}
		x := entry[0]
		y := entry[1]
		rawPath := string(entry[3:])

		if deliverStatusConflict(x, y) {
			cleanTarget := filepath.ToSlash(filepath.Clean(rawPath))
			exitRuntime("deliver", "", *traceID, cli.CodeConflict,
				fmt.Errorf("conflict status %c%c on %s blocks deliver", x, y, cleanTarget),
				"Resolve merge/type/unmerged conflicts in the deliverable worktree before delivering", *jsonl)
		}

		if x == 'R' || y == 'R' {
			newPath := string(entry[3:])
			i++
			if i >= len(entries) {
				exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
					fmt.Errorf("truncated rename entry in git status for %s", newPath), "", *jsonl)
			}
			oldPath := string(entries[i])
			renameOps = append(renameOps, deliverRenameOp{
				oldPath: filepath.ToSlash(filepath.Clean(oldPath)),
				newPath: filepath.ToSlash(filepath.Clean(newPath)),
			})
			continue
		}

		if x == 'C' || y == 'C' {
			newPath := string(entry[3:])
			i++
			if i >= len(entries) {
				exitRuntime("deliver", "", *traceID, cli.CodeRuntime,
					fmt.Errorf("truncated copy entry in git status for %s", newPath), "", *jsonl)
			}
			_ = entries[i]
			copyOps = append(copyOps, deliverCopyOp{
				newPath: filepath.ToSlash(filepath.Clean(newPath)),
			})
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

	matchReceiptPath := func(file string) bool {
		return deliverPathMatchesScope(file, rc.AllowedPaths)
	}

	for _, op := range renameOps {
		if !deliverPathSafe(op.oldPath) || !deliverPathSafe(op.newPath) {
			exitRuntime("deliver", "", *traceID, cli.CodeDenied,
				fmt.Errorf("unsafe rename path(s) %s -> %s", op.oldPath, op.newPath),
				"All paths must be repo-relative and outside .git/", *jsonl)
		}
		if !matchReceiptPath(op.oldPath) || !matchReceiptPath(op.newPath) {
			exitRuntime("deliver", "", *traceID, cli.CodeDenied,
				fmt.Errorf("rename path(s) %s -> %s not covered by receipt %s", op.oldPath, op.newPath, receiptID),
				"All modified files must be covered by receipt allowed_paths", *jsonl)
		}
	}
	for _, op := range copyOps {
		if !deliverPathSafe(op.newPath) {
			exitRuntime("deliver", "", *traceID, cli.CodeDenied,
				fmt.Errorf("unsafe copy path %s", op.newPath),
				"All paths must be repo-relative and outside .git/", *jsonl)
		}
		if !matchReceiptPath(op.newPath) {
			exitRuntime("deliver", "", *traceID, cli.CodeDenied,
				fmt.Errorf("copy path %s not covered by receipt %s", op.newPath, receiptID),
				"All modified files must be covered by receipt allowed_paths", *jsonl)
		}
	}

	var filteredCandidates []string
	var offending []string
	for _, file := range candidates {
		if deliverPathMatchesScope(file, rc.AllowedPaths) {
			filteredCandidates = append(filteredCandidates, file)
			continue
		}
		if isEphemeralCLIAgentArtifact(file) {
			continue
		}
		offending = append(offending, file)
	}
	candidates = filteredCandidates
	for _, file := range deletedCandidates {
		if !deliverPathMatchesScope(file, rc.AllowedPaths) {
			offending = append(offending, file)
		}
	}
	if len(offending) > 0 {
		recordDeliverTaskTelemetry(task, len(candidates)+len(deletedCandidates), len(rc.AllowedPaths), len(offending), false)
		for _, off := range offending {
			fmt.Fprintf(os.Stderr, "out of scope path: %s\n", off)
		}
		exitRuntime("deliver", "", *traceID, cli.CodeDenied,
			fmt.Errorf("out-of-scope path(s) not covered by receipt %s: %s", receiptID, strings.Join(offending, ", ")),
			"All modified files must be covered by receipt allowed_paths", *jsonl)
	}

	// 5. Apply — DELTA-34: CAS Head Check (--expected-head-oid / STALE_HEAD protection)
	var currentHeadOID string
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = cwd
	if headOut, headErr := headCmd.Output(); headErr == nil {
		currentHeadOID = strings.TrimSpace(string(headOut))
	}
	var wtHeadOID string
	wtHeadCmd := exec.Command("git", "rev-parse", "HEAD")
	wtHeadCmd.Dir = deliverableDir
	if wtHeadOut, wtHeadErr := wtHeadCmd.Output(); wtHeadErr == nil {
		wtHeadOID = strings.TrimSpace(string(wtHeadOut))
	}
	wantHeadOID := strings.TrimSpace(*expectedHeadOID)
	if wantHeadOID == "" {
		wantHeadOID = strings.TrimSpace(os.Getenv("G8S_EXPECTED_HEAD_OID"))
	}
	if wantHeadOID != "" {
		if currentHeadOID == "" {
			exitRuntime("deliver", "stale-head", *traceID, cli.CodeConflict,
				fmt.Errorf("STALE_HEAD: unable to resolve git HEAD in %s while verifying --expected-head-oid %s", cwd, wantHeadOID),
				"Ensure the target workspace is a valid git repository with at least one commit", *jsonl)
		}
		if !strings.EqualFold(currentHeadOID, wantHeadOID) && !strings.HasPrefix(strings.ToLower(currentHeadOID), strings.ToLower(wantHeadOID)) {
			exitRuntime("deliver", "stale-head", *traceID, cli.CodeConflict,
				fmt.Errorf("STALE_HEAD: expected target HEAD %s, got %s", wantHeadOID, currentHeadOID),
				"Re-verify or rebase against the current target HEAD before delivering", *jsonl)
		}
	} else {
		baseSHA := extractDeliverBaseSHA(task.Request, task.Result)
		if baseSHA != "" && currentHeadOID != "" && !strings.EqualFold(currentHeadOID, baseSHA) {
			exitRuntime("deliver", "stale-head", *traceID, cli.CodeConflict,
				fmt.Errorf("STALE_HEAD: workspace HEAD %s differs from task base_sha %s", currentHeadOID, baseSHA),
				"Pass --expected-head-oid to acknowledge the current target HEAD before delivering", *jsonl)
		} else if currentHeadOID != "" && wtHeadOID != "" && !strings.EqualFold(currentHeadOID, wtHeadOID) {
			mbCmd := exec.Command("git", "merge-base", "HEAD", wtHeadOID)
			mbCmd.Dir = cwd
			if mbOut, mbErr := mbCmd.Output(); mbErr == nil {
				mergeBase := strings.TrimSpace(string(mbOut))
				if mergeBase != "" && !strings.EqualFold(mergeBase, currentHeadOID) {
					exitRuntime("deliver", "stale-head", *traceID, cli.CodeConflict,
						fmt.Errorf("STALE_HEAD: workspace HEAD %s diverged from worktree base commit %s (merge-base %s)", currentHeadOID, wtHeadOID, mergeBase),
						"Pass --expected-head-oid to acknowledge the current target HEAD before delivering", *jsonl)
				}
			}
		}
	}

	var toApply []string
	seenToApply := make(map[string]bool)
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
		if !seenToApply[rel] {
			seenToApply[rel] = true
			toApply = append(toApply, rel)
		}
	}

	for _, op := range copyOps {
		destPath := filepath.Join(cwd, filepath.FromSlash(op.newPath))
		if fi, err := os.Lstat(destPath); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			if !seenSkipped[op.newPath] {
				seenSkipped[op.newPath] = true
				skipped = append(skipped, op.newPath)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s: destination is a symlink\n", op.newPath)
			continue
		}
		if !seenToApply[op.newPath] {
			seenToApply[op.newPath] = true
			toApply = append(toApply, op.newPath)
		}
	}

	var validRenameOps []deliverRenameOp
	for _, op := range renameOps {
		newDest := filepath.Join(cwd, filepath.FromSlash(op.newPath))
		oldDest := filepath.Join(cwd, filepath.FromSlash(op.oldPath))
		newSymlink := false
		if fi, err := os.Lstat(newDest); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			newSymlink = true
		}
		oldSymlink := false
		if fi, err := os.Lstat(oldDest); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
			oldSymlink = true
		}
		if newSymlink || oldSymlink {
			if !seenSkipped[op.newPath] {
				seenSkipped[op.newPath] = true
				skipped = append(skipped, op.newPath)
			}
			fmt.Fprintf(os.Stderr, "warning: skipping %s -> %s: destination or source is a symlink\n", op.oldPath, op.newPath)
			continue
		}
		validRenameOps = append(validRenameOps, op)
		if !seenToApply[op.newPath] {
			seenToApply[op.newPath] = true
			toApply = append(toApply, op.newPath)
		}
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
		actorName := strings.TrimSpace(*actor)
		if actorName == "" {
			actorName = "f0-supervisor"
		}
		if derr := store.ClaimDelivery(context.Background(), taskID, actorName, currentHeadOID); derr != nil {
			if errors.Is(derr, controlplane.ErrTaskAlreadyDelivered) {
				exitRuntime("deliver", "", *traceID, cli.CodeConflict, derr,
					"Task has already been delivered (exactly-one delivery fence)", *jsonl)
			}
			exitRuntime("deliver", "", *traceID, cli.CodeRuntime, derr,
				"Failed to acquire task delivery fence", *jsonl)
		}
		releaseClaimIfUnlanded := func() {
			if len(applied) == 0 {
				store.ReleaseDeliveryClaim(context.Background(), taskID)
			}
		}

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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
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
				releaseClaimIfUnlanded()
				landedStr := "none"
				if len(applied) > 0 {
					landedStr = strings.Join(applied, ", ")
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO,
					fmt.Errorf("rename applied file %s: %w (files landed: %s)", s.rel, err, landedStr), "", *jsonl)
			}
			applied = append(applied, s.rel)
		}

		for _, op := range validRenameOps {
			newDest := filepath.Join(cwd, filepath.FromSlash(op.newPath))
			oldDest := filepath.Join(cwd, filepath.FromSlash(op.oldPath))
			oldInfo, oldErr := os.Lstat(oldDest)
			if oldErr != nil {
				if os.IsNotExist(oldErr) {
					continue
				}
				exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("stat old path %s after rename: %w", op.oldPath, oldErr), "", *jsonl)
			}
			newInfo, newErr := os.Lstat(newDest)
			if newErr == nil && os.SameFile(newInfo, oldInfo) {
				continue
			}
			if err := os.Remove(oldDest); err != nil && !os.IsNotExist(err) {
				exitRuntime("deliver", "", *traceID, cli.CodeIO, fmt.Errorf("remove old path %s after rename: %w", op.oldPath, err), "", *jsonl)
			}
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

	recordDeliverTaskTelemetry(task, len(applied)+len(deleted), len(rc.AllowedPaths), 0, true)

	data := deliverData{
		TaskID:            taskID,
		DeliverableDir:    deliverableDir,
		Applied:           applied,
		Deleted:           deleted,
		Skipped:           skipped,
		ReceiptID:         receiptID,
		HeadOID:           currentHeadOID,
		PrunedWorktree:    pruned,
		OverrideReason:    recordedOverrideReason,
		VerificationState: recordedVerificationState,
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

func deliverStatusConflict(x, y byte) bool {
	if x == 'T' || y == 'T' || x == 'U' || y == 'U' {
		return true
	}
	switch string([]byte{x, y}) {
	case "AA", "DD", "AU", "UA", "DU", "UD":
		return true
	}
	return false
}

func deliverPathSafe(relSlash string) bool {
	p := filepath.ToSlash(filepath.Clean(relSlash))
	if p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || deliverIsWindowsDrive(p) {
		return false
	}
	if p == ".git" || strings.HasPrefix(p, ".git/") || strings.Contains(p, "/.git/") {
		return false
	}
	return true
}

func extractDeliverBaseSHA(requestJSON, resultJSON []byte) string {
	for _, raw := range [][]byte{requestJSON, resultJSON} {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if v, ok := m["base_sha"].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
		if d, ok := m["deliverable"].(map[string]any); ok {
			if v, ok := d["base_sha"].(string); ok {
				if s := strings.TrimSpace(v); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func isEphemeralCLIAgentArtifact(relSlash string) bool {
	p := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relSlash)), "./")
	for _, prefix := range []string{".omo", ".opencode", ".commandcode", ".qwen", ".cursor"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
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
