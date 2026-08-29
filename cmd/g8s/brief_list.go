package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func runBriefList(args []string) {
	fs := flag.NewFlagSet("brief-list", flag.ExitOnError)
	statusFlag := fs.String("status", "", "filter by status (active, consumed, expired, all)")
	activeFlag := fs.Bool("active", false, "filter by active briefs")
	consumedFlag := fs.Bool("consumed", false, "filter by consumed briefs")
	expiredFlag := fs.Bool("expired", false, "filter by expired briefs")
	allFlag := fs.Bool("all", false, "list all briefs without filtering")
	limit := fs.Int("limit", 50, "maximum number of briefs to return (1..200)")
	jsonMode := fs.Bool("json", false, "output as machine-readable JSON")
	failIf(fs.Parse(args))

	status := strings.TrimSpace(strings.ToLower(*statusFlag))
	if *activeFlag {
		status = brief.StatusActive
	} else if *consumedFlag {
		status = brief.StatusConsumed
	} else if *expiredFlag {
		status = brief.StatusExpired
	} else if *allFlag {
		status = "all"
	}
	if status == "" {
		status = brief.StatusActive
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	failIf(executeBriefList(os.Stdout, store, status, *limit, *jsonMode))
}

func executeBriefList(w io.Writer, store *controlplane.Store, status string, limit int, jsonMode bool) error {
	briefs, err := brief.List(store, status)
	if err != nil {
		return err
	}

	if limit > 0 && len(briefs) > limit {
		briefs = briefs[:limit]
	}

	if jsonMode {
		out, err := json.MarshalIndent(briefs, "", "  ")
		if err != nil {
			return fmt.Errorf("format json: %w", err)
		}
		fmt.Fprintln(w, string(out))
		return nil
	}

	if len(briefs) == 0 {
		fmt.Fprintf(w, "No %s briefs found.\n", status)
		return nil
	}

	var td pterm.TableData
	td = append(td, []string{"ID", "Title", "Issued By", "Status", "Issued At", "Expires At"})
	for _, b := range briefs {
		var statusStr string
		switch b.Status {
		case brief.StatusActive:
			statusStr = pterm.Green(b.Status)
		case brief.StatusConsumed:
			statusStr = pterm.Blue(b.Status)
		case brief.StatusExpired:
			statusStr = pterm.Red(b.Status)
		default:
			statusStr = b.Status
		}
		td = append(td, []string{
			b.ID,
			b.Title,
			b.IssuedBy,
			statusStr,
			b.IssuedAt.Format("2006-01-02 15:04:05"),
			b.ExpiresAt.Format("2006-01-02 15:04:05"),
		})
	}
	return pterm.DefaultTable.WithHasHeader().WithData(td).WithWriter(w).Render()
}
