package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/pathutil"
)

// MigrateItem represents one file or directory migration record.
type MigrateItem struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Status      string `json:"status"` // migrated, would_migrate, skipped, failed
	BytesMoved  int64  `json:"bytes_moved"`
	Error       string `json:"error,omitempty"`
}

// MigrateReport aggregates migration operations.
type MigrateReport struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	DryRun     bool          `json:"dry_run"`
	TotalFiles int           `json:"total_files"`
	TotalBytes int64         `json:"total_bytes"`
	Items      []MigrateItem `json:"items"`
}

// MigrateData scans the source path for legacy g8s files/directories and migrates them to target.
func MigrateData(from, to string, dryRun, force bool) (*MigrateReport, error) {
	if from == "" {
		from = "."
	}
	if to == "" {
		to = pathutil.DefaultDataDir()
	}

	absFrom, err := filepath.Abs(from)
	if err != nil {
		absFrom = from
	}
	absTo, err := filepath.Abs(to)
	if err != nil {
		absTo = to
	}

	report := &MigrateReport{
		From:   absFrom,
		To:     absTo,
		DryRun: dryRun,
		Items:  make([]MigrateItem, 0),
	}

	candidates := []string{
		"g8s.db",
		"g8s.db-wal",
		"g8s.db-shm",
		".g8s",
		".heartbeat",
		"evidence",
		"g8s.yaml",
		"config.json",
		".cleanup-audit.jsonl",
	}

	for _, cand := range candidates {
		srcPath := filepath.Join(absFrom, cand)
		info, err := os.Stat(srcPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			report.Items = append(report.Items, MigrateItem{
				Source:      srcPath,
				Destination: filepath.Join(absTo, cand),
				Status:      "failed",
				Error:       err.Error(),
			})
			continue
		}

		if info.IsDir() {
			items, filesCount, bytesCount, err := migrateDirectory(srcPath, filepath.Join(absTo, cand), dryRun, force)
			report.Items = append(report.Items, items...)
			report.TotalFiles += filesCount
			report.TotalBytes += bytesCount
			if err != nil {
				// directory migration error recorded in items
			}
		} else {
			destPath := filepath.Join(absTo, cand)
			item := migrateFile(srcPath, destPath, info.Size(), dryRun, force)
			report.Items = append(report.Items, item)
			if item.Status == "migrated" || item.Status == "would_migrate" {
				report.TotalFiles++
				report.TotalBytes += item.BytesMoved
			}
		}
	}

	return report, nil
}

func migrateFile(src, dest string, size int64, dryRun, force bool) MigrateItem {
	item := MigrateItem{
		Source:      src,
		Destination: dest,
		BytesMoved:  size,
	}

	if _, err := os.Stat(dest); err == nil && !force {
		item.Status = "skipped"
		item.Error = "destination file already exists (use --force to overwrite)"
		item.BytesMoved = 0
		return item
	}

	if dryRun {
		item.Status = "would_migrate"
		return item
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("create destination dir: %v", err)
		item.BytesMoved = 0
		return item
	}

	srcFile, err := os.Open(src)
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("open source: %v", err)
		item.BytesMoved = 0
		return item
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("open destination: %v", err)
		item.BytesMoved = 0
		return item
	}
	defer destFile.Close()

	written, err := io.Copy(destFile, srcFile)
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("copy data: %v", err)
		item.BytesMoved = 0
		return item
	}

	item.BytesMoved = written
	item.Status = "migrated"
	return item
}

func migrateDirectory(srcDir, destDir string, dryRun, force bool) ([]MigrateItem, int, int64, error) {
	var items []MigrateItem
	var filesCount int
	var bytesCount int64

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		targetFile := filepath.Join(destDir, rel)
		item := migrateFile(path, targetFile, info.Size(), dryRun, force)
		items = append(items, item)
		if item.Status == "migrated" || item.Status == "would_migrate" {
			filesCount++
			bytesCount += item.BytesMoved
		}
		return nil
	})

	return items, filesCount, bytesCount, err
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	fromFlag := fs.String("from", ".", "source directory containing cwd-relative g8s files")
	toFlag := fs.String("to", "", "destination directory (defaults to canonical data directory)")
	dryRunFlag := fs.Bool("dry-run", false, "simulate migration without copying files")
	forceFlag := fs.Bool("force", false, "overwrite files at destination if they already exist")

	if err := fs.Parse(args); err != nil {
		exitUsage("migrate", "", *traceID, err.Error(), "", *jsonl)
	}

	toPath := *toFlag
	if toPath == "" {
		toPath = pathutil.DefaultDataDir()
	}

	report, err := MigrateData(*fromFlag, toPath, *dryRunFlag, *forceFlag)
	if err != nil {
		exitRuntime("migrate", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("migrate_report", "migrate", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	pterm.DefaultHeader.WithFullWidth().Println("g8s Data Migration")
	fmt.Printf("Source:      %s\n", report.From)
	fmt.Printf("Destination: %s\n", report.To)
	fmt.Printf("Dry Run:     %t\n", report.DryRun)
	fmt.Printf("Total Files: %d (%d bytes)\n\n", report.TotalFiles, report.TotalBytes)

	if len(report.Items) == 0 {
		pterm.Info.Println("No legacy g8s files found in source directory.")
		return
	}

	var td pterm.TableData
	td = append(td, []string{"Source", "Destination", "Status", "Bytes", "Detail"})
	for _, it := range report.Items {
		var statusStr string
		switch it.Status {
		case "migrated", "would_migrate":
			statusStr = pterm.Green(it.Status)
		case "skipped":
			statusStr = pterm.Yellow(it.Status)
		default:
			statusStr = pterm.Red(it.Status)
		}
		td = append(td, []string{
			filepath.Base(it.Source),
			it.Destination,
			statusStr,
			fmt.Sprintf("%d", it.BytesMoved),
			it.Error,
		})
	}
	pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	if !report.DryRun {
		pterm.Success.Printf("Successfully migrated %d file(s) to %s\n", report.TotalFiles, report.To)
	}
}
