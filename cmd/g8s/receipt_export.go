package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
)

// bytesDigest computes sha256 hex digest of given bytes.
func bytesDigest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sanitizeCLISanitizedError strips absolute filesystem paths from errors.
func sanitizeCLIExportError(op, target string, err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		cat := "io_error"
		switch pathErr.Op {
		case "mkdir":
			cat = "mkdir_failed"
		case "open", "write":
			cat = "write_failed"
		case "stat":
			cat = "stat_failed"
		case "read":
			cat = "read_failed"
		}
		return cat, fmt.Sprintf("%s %s: %v", pathErr.Op, target, pathErr.Err)
	}
	raw := err.Error()
	re := regexp.MustCompile(`(/[a-zA-Z0-9_\-\.]+)+`)
	clean := re.ReplaceAllString(raw, "[path]")
	return "io_error", fmt.Sprintf("%s %s: %s", op, target, clean)
}

// runReceiptExport provides an operator-invokable retry for receipt export (DELTA-31).
// It performs export-only recovery without claiming tasks, acquiring leases, or invoking models.
func runReceiptExport(args []string) {
	fs := flag.NewFlagSet("receipt-export", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor

	taskIDFlag := fs.String("task", "", "task ID to export/retry receipt for")
	fs.StringVar(taskIDFlag, "task-id", "", "alias for --task")
	runDirFlag := fs.String("run-dir", "", "path to attempt run directory (optional)")
	fs.StringVar(runDirFlag, "attempt-dir", "", "alias for --run-dir")
	evidenceDirFlag := fs.String("evidence-dir", "", "path to evidence lake directory (defaults to G8S_EVIDENCE_DIR or standard location)")
	requireLocalFlag := fs.Bool("require-local", true, "require attempt-local export to succeed")
	requireCentralFlag := fs.Bool("require-central", false, "require centralized Evidence Lake export to succeed")

	if err := fs.Parse(args); err != nil {
		exitUsage("receipt", "export", *traceID, err.Error(), "", *jsonl)
	}

	taskID := *taskIDFlag
	if taskID == "" && fs.NArg() > 0 {
		taskID = fs.Arg(0)
	}
	if taskID == "" {
		exitUsage("receipt", "export", *traceID, "usage: g8s receipt-export <task-id> [--run-dir <dir>] [--evidence-dir <dir>]", "Provide a task ID", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	if task == nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeNotFound, fmt.Errorf("unknown task: %s", taskID), "Verify the task ID with 'g8s tasks'", *jsonl)
	}

	// Canonical export snapshot excludes mutable receipt_export and pins UpdatedAt to CompletedAt.
	snapBytes, err := controlplane.CanonicalReceiptSnapshot(task)
	if err != nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeRuntime, fmt.Errorf("canonical snapshot: %w", err), "", *jsonl)
	}
	hash := sha256.Sum256(snapBytes)
	expectedDigest := hex.EncodeToString(hash[:])

	now := float64(time.Now().UnixNano()) / 1e9
	status := controlplane.ReceiptExportStatus{
		UpdatedAt: now,
	}
	if task.ReceiptExport != nil {
		status = *task.ReceiptExport
		status.UpdatedAt = now
	}

	// Resolve local run directory if not explicitly provided
	runDir := *runDirFlag
	if runDir == "" && status.Local.Path != "" && status.Local.Status == controlplane.ExportStatusSucceeded {
		// Retain existing valid local status if not targeting a new directory
	} else if runDir != "" {
		receiptPath := filepath.Join(runDir, "receipt.json")
		validExisting := false
		if fi, err := os.Stat(receiptPath); err == nil && !fi.IsDir() {
			data, rerr := os.ReadFile(receiptPath)
			if rerr == nil && bytesDigest(data) == expectedDigest {
				validExisting = true
			}
		}

		if validExisting {
			// Verifies and preserves successful file
			status.Local = controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
				Time:   now,
				Digest: expectedDigest,
				Path:   "receipt.json",
			}
		} else {
			// Writes only failed/missing artifacts
			if err := os.MkdirAll(runDir, 0o700); err != nil {
				cat, sErr := sanitizeCLIExportError("mkdir", "run_dir", err)
				status.Local = controlplane.ExportDestinationStatus{
					Status:   controlplane.ExportStatusFailed,
					Time:     now,
					Category: cat,
					Error:    sErr,
					Path:     "receipt.json",
				}
			} else if err := os.WriteFile(receiptPath, snapBytes, 0o600); err != nil {
				cat, sErr := sanitizeCLIExportError("write", "receipt.json", err)
				status.Local = controlplane.ExportDestinationStatus{
					Status:   controlplane.ExportStatusFailed,
					Time:     now,
					Category: cat,
					Error:    sErr,
					Path:     "receipt.json",
				}
			} else {
				status.Local = controlplane.ExportDestinationStatus{
					Status: controlplane.ExportStatusSucceeded,
					Time:   now,
					Digest: expectedDigest,
					Path:   "receipt.json",
				}
			}
		}
	} else if status.Local.Status == "" {
		status.Local = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
	}

	// Resolve evidence directory
	evidenceDir := *evidenceDirFlag
	if evidenceDir == "" {
		evidenceDir = os.Getenv("G8S_EVIDENCE_DIR")
	}
	if evidenceDir == "" && *requireCentralFlag {
		evidenceDir = pathutil.DefaultEvidenceDir()
	}

	if evidenceDir == "" {
		status.Central = controlplane.ExportDestinationStatus{
			Status:   controlplane.ExportStatusDisabled,
			Time:     now,
			Category: "disabled",
		}
	} else {
		taskEvidenceDir := filepath.Join(evidenceDir, task.TaskID)
		evReceiptPath := filepath.Join(taskEvidenceDir, "receipt.json")
		validExisting := false
		if fi, err := os.Stat(evReceiptPath); err == nil && !fi.IsDir() {
			data, rerr := os.ReadFile(evReceiptPath)
			if rerr == nil && bytesDigest(data) == expectedDigest {
				validExisting = true
			}
		}

		if validExisting {
			// Verifies and preserves successful file
			if task.Attempts > 0 {
				attemptPath := filepath.Join(taskEvidenceDir, fmt.Sprintf("attempt_%d.json", task.Attempts))
				if _, err := os.Stat(attemptPath); err != nil {
					if aerr := os.WriteFile(attemptPath, snapBytes, 0o600); aerr != nil {
						_, sErr := sanitizeCLIExportError("write", fmt.Sprintf("attempt_%d.json", task.Attempts), aerr)
						fmt.Fprintf(os.Stderr, "[warn] receipt-export: %s\n", sErr)
					}
				}
			}
			status.Central = controlplane.ExportDestinationStatus{
				Status: controlplane.ExportStatusSucceeded,
				Time:   now,
				Digest: expectedDigest,
				Path:   filepath.Join(task.TaskID, "receipt.json"),
			}
		} else {
			// Writes only failed/missing artifacts
			if merr := os.MkdirAll(taskEvidenceDir, 0o700); merr != nil {
				cat, sErr := sanitizeCLIExportError("mkdir", "evidence_dir", merr)
				status.Central = controlplane.ExportDestinationStatus{
					Status:   controlplane.ExportStatusFailed,
					Time:     now,
					Category: cat,
					Error:    sErr,
					Path:     filepath.Join(task.TaskID, "receipt.json"),
				}
			} else if werr := os.WriteFile(evReceiptPath, snapBytes, 0o600); werr != nil {
				cat, sErr := sanitizeCLIExportError("write", "receipt.json", werr)
				status.Central = controlplane.ExportDestinationStatus{
					Status:   controlplane.ExportStatusFailed,
					Time:     now,
					Category: cat,
					Error:    sErr,
					Path:     filepath.Join(task.TaskID, "receipt.json"),
				}
			} else {
				if task.Attempts > 0 {
					attemptPath := filepath.Join(taskEvidenceDir, fmt.Sprintf("attempt_%d.json", task.Attempts))
					if aerr := os.WriteFile(attemptPath, snapBytes, 0o600); aerr != nil {
						_, sErr := sanitizeCLIExportError("write", fmt.Sprintf("attempt_%d.json", task.Attempts), aerr)
						fmt.Fprintf(os.Stderr, "[warn] receipt-export: %s\n", sErr)
					}
				}
				status.Central = controlplane.ExportDestinationStatus{
					Status: controlplane.ExportStatusSucceeded,
					Time:   now,
					Digest: expectedDigest,
					Path:   filepath.Join(task.TaskID, "receipt.json"),
				}
			}
		}
	}

	// Calculate overall success and fully evidenced
	localOk := status.Local.Status == controlplane.ExportStatusSucceeded || status.Local.Status == controlplane.ExportStatusDisabled
	centralOk := status.Central.Status == controlplane.ExportStatusSucceeded || status.Central.Status == controlplane.ExportStatusDisabled
	status.Success = localOk && centralOk
	status.FullyEvidenced = (status.Local.Status == controlplane.ExportStatusSucceeded) && (evidenceDir == "" || status.Central.Status == controlplane.ExportStatusSucceeded)

	// Persist outcome into control plane
	if err := store.RecordReceiptExport(ctx, task.TaskID, status); err != nil {
		exitRuntime("receipt", "export", *traceID, cli.CodeRuntime, fmt.Errorf("record receipt export: %w", err), "", *jsonl)
	}

	// Format response
	env := cli.NewEnvelope("receipt", "export", "", status)
	env.TraceID = *traceID
	if *jsonl || *jsonMode {
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("receipt", "export", *traceID, cli.CodeIO, err, "", *jsonl)
		}
	} else {
		fmt.Printf("Receipt export status for task %s:\n", task.TaskID)
		fmt.Printf("  Local:   %s", status.Local.Status)
		if status.Local.Digest != "" {
			fmt.Printf(" (digest: %s...)", status.Local.Digest[:12])
		}
		if status.Local.Error != "" {
			fmt.Printf(" [error: %s]", status.Local.Error)
		}
		fmt.Println()

		fmt.Printf("  Central: %s", status.Central.Status)
		if status.Central.Digest != "" {
			fmt.Printf(" (digest: %s...)", status.Central.Digest[:12])
		}
		if status.Central.Error != "" {
			fmt.Printf(" [error: %s]", status.Central.Error)
		}
		fmt.Println()
		fmt.Printf("  Success: %v, Fully Evidenced: %v\n", status.Success, status.FullyEvidenced)
	}

	// Check required vs optional export semantics
	if *requireLocalFlag && status.Local.Status != controlplane.ExportStatusSucceeded {
		os.Exit(1)
	}
	if *requireCentralFlag && status.Central.Status != controlplane.ExportStatusSucceeded {
		os.Exit(1)
	}
}
