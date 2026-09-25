package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/reflex"
)

func main() {
	taskID := flag.String("task", "supervisor-mutation", "mutation task id")
	files := flag.String("files", "", "comma-separated files")
	summary := flag.String("summary", "", "one-line diff summary")
	allowed := flag.String("allowed", "", "comma-separated allowed globs")
	flag.Parse()
	if *summary == "" {
		os.Exit(2)
	}
	gate := reflex.NewReflexGate()
	req := reflex.TriageRequest{TaskID: *taskID, FilesModified: split(*files), DiffSummary: *summary, AllowedPaths: split(*allowed)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	verdict, err := gate.TriageMutation(ctx, req)
	if err != nil {
		os.Exit(1)
	}
	out, _ := json.Marshal(verdict)
	fmt.Println(string(out))
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
