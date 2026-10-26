package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/featuremap"
	"github.com/tamld/g8s/internal/pathutil"
)

// Exit codes for `g8s feature collisions`.
const (
	collisionsExitOK          = 0
	collisionsExitRuntime     = 1
	collisionsExitUsage       = 2
	collisionsExitNotParallel = 3
)

const collisionsUsage = "usage: g8s feature collisions --paths <glob,...> | --task-a <glob,...|task:<id>> --task-b <glob,...|task:<id>> [--file <path>] [--strict] [--json]"

// runFeatureCollisions implements `g8s feature collisions` (DELTA-41): it maps
// one or two write scopes onto owned_paths, collision domains, and hotspots and
// recommends parallel-safe | split | serialize before F0 dispatches workers.
func runFeatureCollisions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("feature-collisions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fileFlag := fs.String("file", featuremap.DefaultFeatureMapPath, "Path to feature map YAML")
	pathsFlag := fs.String("paths", "", "Single write scope to analyze (comma-separated globs)")
	taskAFlag := fs.String("task-a", "", "First write scope: comma-separated receipt globs or task:<id>")
	taskBFlag := fs.String("task-b", "", "Second write scope: comma-separated receipt globs or task:<id>")
	labelAFlag := fs.String("label-a", "", "Display label for scope A (default: A or the task id)")
	labelBFlag := fs.String("label-b", "", "Display label for scope B (default: B or the task id)")
	strictFlag := fs.Bool("strict", false, "Exit 3 unless the recommendation is parallel-safe")
	jsonFlag := fs.Bool("json", false, "Output machine-readable JSON")

	if err := fs.Parse(normalizeFlags(args)); err != nil {
		return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, err.Error(), collisionsExitUsage)
	}

	single := strings.TrimSpace(*pathsFlag) != ""
	pair := strings.TrimSpace(*taskAFlag) != "" || strings.TrimSpace(*taskBFlag) != ""
	switch {
	case single && pair:
		return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, "--paths cannot be combined with --task-a/--task-b", collisionsExitUsage)
	case !single && !pair:
		return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, "provide --paths or both --task-a and --task-b", collisionsExitUsage)
	case pair && (strings.TrimSpace(*taskAFlag) == "" || strings.TrimSpace(*taskBFlag) == ""):
		return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, "--task-a and --task-b must both be provided", collisionsExitUsage)
	}

	fm, err := featuremap.Load(*fileFlag)
	if err != nil {
		return collisionsFail(stderr, *jsonFlag, cli.CodeRuntime, err.Error(), collisionsExitRuntime)
	}

	if single {
		sa := fm.AnalyzeScope("", featuremap.ParseScope(*pathsFlag))
		if len(sa.Paths) == 0 {
			return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, featuremap.ErrEmptyScope.Error(), collisionsExitUsage)
		}
		if *jsonFlag {
			return writeCollisionsJSON(stdout, stderr, sa)
		}
		printScopeAnalysis(stdout, "Scope", sa)
		return collisionsExitOK
	}

	scopeA, labelA, err := resolveCollisionScope(*taskAFlag, *labelAFlag, "A")
	if err != nil {
		return collisionsFail(stderr, *jsonFlag, cli.CodeNotFound, err.Error(), collisionsExitRuntime)
	}
	scopeB, labelB, err := resolveCollisionScope(*taskBFlag, *labelBFlag, "B")
	if err != nil {
		return collisionsFail(stderr, *jsonFlag, cli.CodeNotFound, err.Error(), collisionsExitRuntime)
	}

	rep, err := fm.CompareScopes(labelA, scopeA, labelB, scopeB)
	if err != nil {
		code := collisionsExitRuntime
		if errors.Is(err, featuremap.ErrEmptyScope) {
			code = collisionsExitUsage
		}
		return collisionsFail(stderr, *jsonFlag, cli.CodeUsage, err.Error(), code)
	}

	exit := collisionsExitOK
	if *strictFlag && rep.Recommendation != featuremap.RecommendParallelSafe {
		exit = collisionsExitNotParallel
	}
	if *jsonFlag {
		if rc := writeCollisionsJSON(stdout, stderr, rep); rc != collisionsExitOK {
			return rc
		}
		return exit
	}
	printCollisionReport(stdout, rep)
	return exit
}

func collisionsFail(stderr io.Writer, jsonMode bool, code, msg string, exit int) int {
	if jsonMode {
		env := cli.NewErrorEnvelope("feature", "collisions", "", code, msg, collisionsUsage, "")
		_ = cli.WriteResponse(stderr, env, true)
		return exit
	}
	fmt.Fprintf(stderr, "Error: %s\n%s\n", msg, collisionsUsage)
	return exit
}

func writeCollisionsJSON(stdout, stderr io.Writer, v any) int {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "Error encoding JSON: %v\n", err)
		return collisionsExitRuntime
	}
	fmt.Fprintln(stdout, string(out))
	return collisionsExitOK
}

// resolveCollisionScope turns a --task-a/--task-b value into receipt globs.
// `task:<id>` reads allowed_paths from the control-plane task request (or its
// receipt in receipts.db); anything else is parsed as comma-separated globs.
func resolveCollisionScope(raw, label, def string) ([]string, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "task:") {
		return featuremap.ParseScope(raw), labelOr(label, def), nil
	}
	taskID := strings.TrimSpace(strings.TrimPrefix(raw, "task:"))
	if taskID == "" {
		return nil, "", fmt.Errorf("empty task id in %q", raw)
	}
	dbPath, err := databasePath()
	if err != nil {
		return nil, "", err
	}
	paths, err := taskAllowedPathsFromDB(dbPath, taskID)
	if err != nil {
		return nil, "", err
	}
	return paths, labelOr(label, taskID), nil
}

func labelOr(label, def string) string {
	if strings.TrimSpace(label) == "" {
		return def
	}
	return strings.TrimSpace(label)
}

// taskAllowedPathsFromDB returns the write scope of a queued/claimed task.
func taskAllowedPathsFromDB(dbPath, taskID string) ([]string, error) {
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("open control plane: %w", err)
	}
	defer store.Close()

	ctx := context.Background()
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", taskID, err)
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", taskID)
	}
	var req struct {
		ReceiptID    string   `json:"receipt_id"`
		AllowedPaths []string `json:"allowed_paths"`
	}
	if len(task.Request) > 0 {
		_ = json.Unmarshal(task.Request, &req)
	}
	if len(req.AllowedPaths) == 0 && req.ReceiptID != "" {
		rcDBPath := filepath.Join(filepath.Dir(dbPath), "receipts.db")
		if db, err := sql.Open("sqlite", pathutil.SQLiteURI(rcDBPath, "mode=ro")); err == nil {
			defer db.Close()
			var pathsJSON string
			if err := db.QueryRowContext(ctx, "SELECT allowed_paths_json FROM write_receipts WHERE receipt_id = ?", req.ReceiptID).Scan(&pathsJSON); err == nil {
				_ = json.Unmarshal([]byte(pathsJSON), &req.AllowedPaths)
			}
		}
	}
	var scope []string
	for _, p := range req.AllowedPaths {
		scope = append(scope, featuremap.ParseScope(p)...)
	}
	if len(scope) == 0 {
		return nil, fmt.Errorf("task %q has no allowed_paths / receipt scope", taskID)
	}
	return scope, nil
}

func printScopeAnalysis(w io.Writer, title string, sa featuremap.ScopeAnalysis) {
	name := title
	if sa.Label != "" {
		name = fmt.Sprintf("%s %s", title, sa.Label)
	}
	fmt.Fprintf(w, "%s: %s\n", name, strings.Join(sa.Paths, ", "))
	fmt.Fprintf(w, "  features:          %s\n", orNone(sa.Features))
	fmt.Fprintf(w, "  collision domains: %s\n", orNone(sa.CollisionDomains))
	var hs []string
	for _, h := range sa.Hotspots {
		hs = append(hs, fmt.Sprintf("%s[%s]", h.ID, h.Policy))
	}
	fmt.Fprintf(w, "  hotspots:          %s\n", orNone(hs))
	if len(sa.UnmappedPaths) > 0 {
		fmt.Fprintf(w, "  unmapped paths:    %s\n", strings.Join(sa.UnmappedPaths, ", "))
	}
}

func printCollisionReport(w io.Writer, rep featuremap.CollisionReport) {
	fmt.Fprintf(w, "Recommendation: %s\n", rep.Recommendation)
	printScopeAnalysis(w, "Scope", rep.ScopeA)
	printScopeAnalysis(w, "Scope", rep.ScopeB)
	fmt.Fprintf(w, "Shared collision domains: %s\n", orNone(rep.SharedCollisionDomains))
	var hs []string
	for _, h := range rep.SharedHotspots {
		hs = append(hs, fmt.Sprintf("%s[%s] -> %s", h.ID, h.Policy, h.Action))
	}
	fmt.Fprintf(w, "Shared hotspots: %s\n", orNone(hs))
	for _, r := range rep.Reasons {
		fmt.Fprintf(w, "  reason: %s\n", r)
	}
	for _, a := range rep.Advice {
		fmt.Fprintf(w, "  advice: %s\n", a)
	}
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}
