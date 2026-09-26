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
	taskID := flag.String("task", "m", "")
	files := flag.String("files", "", "")
	summary := flag.String("summary", "", "")
	allowed := flag.String("allowed", "", "")
	flag.Parse()
	gate := reflex.NewReflexGate()
	req := reflex.TriageRequest{TaskID: *taskID, FilesModified: split(*files), DiffSummary: *summary, AllowedPaths: split(*allowed)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, err := gate.TriageMutation(ctx, req)
	if err != nil {
		os.Exit(1)
	}
	out, _ := json.Marshal(map[string]any{"action": v.Action, "risk": v.RiskScore, "breach": v.BreachProb, "confidence": v.Confidence})
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
