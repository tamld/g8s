package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/reflex"
)

func main() {
	if err := runJevTriage(os.Args[1:], os.Stdout); err != nil {
		os.Exit(1)
	}
}

func runJevTriage(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("jevtriage", flag.ContinueOnError)
	taskID := fs.String("task", "m", "")
	files := fs.String("files", "", "")
	summary := fs.String("summary", "", "")
	allowed := fs.String("allowed", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	gate := reflex.NewReflexGate()
	req := reflex.TriageRequest{TaskID: *taskID, FilesModified: split(*files), DiffSummary: *summary, AllowedPaths: split(*allowed)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, err := gate.TriageMutation(ctx, req)
	if err != nil {
		return err
	}
	out, err := json.Marshal(map[string]any{"action": v.Action, "risk": v.RiskScore, "breach": v.BreachProb, "confidence": v.Confidence})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(out))
	return err
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	p := strings.Split(s, ",")
	for i := range p {
		p[i] = strings.TrimSpace(p[i])
	}
	return p
}
