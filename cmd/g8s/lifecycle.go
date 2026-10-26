package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/lifecycle"
	"github.com/tamld/g8s/internal/service"
)

// runLifecycle dispatches the DELTA-40 binary lifecycle commands so main.go
// carries a single registration line for both `update` and `uninstall`.
func runLifecycle(command string, args []string) {
	switch command {
	case "update":
		runUpdate(args)
	case "uninstall":
		runUninstall(args)
	default:
		exitUsage(command, "", "", fmt.Sprintf("unknown lifecycle command %q", command), "", false)
	}
}

// Test seams: tests point the updater at an httptest TLS server, trust its
// certificate, and redirect the replacement target away from the test binary.
var (
	lifecycleAPIBase    = lifecycle.DefaultAPIBase
	lifecycleHTTPClient = func(timeout time.Duration) lifecycle.HTTPDoer { return &http.Client{Timeout: timeout} }
	lifecycleTargetPath = ""
	lifecycleSmoke      = smokeBinary
)

// runUpdate implements `g8s update [--check] [--version vX.Y.Z] [--force] [--json]`.
func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	checkOnly := fs.Bool("check", false, "only report whether an update is available; make no changes")
	version := fs.String("version", "", "install this release tag (e.g. v0.21.0) instead of the latest")
	force := fs.Bool("force", false, "reinstall even when the target version is already running")
	timeout := fs.Duration("timeout", 120*time.Second, "overall network timeout for release resolution and download")
	if err := fs.Parse(args); err != nil {
		exitUsage("update", "", *traceID, err.Error(), "", *jsonl)
	}

	token := os.Getenv("G8S_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	u := &lifecycle.Updater{
		Client:         lifecycleHTTPClient(*timeout),
		APIBase:        lifecycleAPIBase,
		CurrentVersion: Version,
		TargetPath:     lifecycleTargetPath,
		Token:          token,
		Smoke:          lifecycleSmoke,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var (
		res *lifecycle.UpdateResult
		err error
	)
	if *checkOnly {
		res, _, err = u.Check(ctx, *version)
	} else {
		res, err = u.Apply(ctx, *version, *force)
	}
	if err != nil {
		exitRuntime("update", "", *traceID, cli.CodeRuntime, err, "verify network access to github.com, or pin a release with --version", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("update_result", "update", "", res)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	switch {
	case res.Applied:
		pterm.Success.Printf("%s\n", res.Message)
		fmt.Printf("  binary: %s\n  backup: %s\n  sha256: %s (verified)\n", res.TargetPath, res.BackupPath, res.ActualSHA256)
	case res.UpdateAvailable:
		pterm.Info.Printf("%s\n", res.Message)
		if res.Asset != "" {
			fmt.Printf("  asset: %s\n", res.Asset)
		}
		fmt.Println("  run `g8s update` to install it")
	default:
		pterm.Success.Printf("%s\n", res.Message)
	}
}

// smokeBinary runs `<bin> version --json` against a freshly installed binary.
func smokeBinary(bin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s version --json: %w (%s)", bin, err, string(out))
	}
	return nil
}

// runUninstall implements `g8s uninstall [--dry-run] [--purge-state] [--yes] [--json]`.
// Without --yes it only prints the plan.
func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	dryRun := fs.Bool("dry-run", false, "list what would be removed without removing anything (default unless --yes)")
	purge := fs.Bool("purge-state", false, "also remove g8s state, config, cache and log directories")
	yes := fs.Bool("yes", false, "apply the uninstall plan")
	if err := fs.Parse(args); err != nil {
		exitUsage("uninstall", "", *traceID, err.Error(), "", *jsonl)
	}

	home, _ := os.UserHomeDir()
	bin := ""
	if exe, err := os.Executable(); err == nil {
		if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
			exe = resolved
		}
		bin = exe
	}
	opts := lifecycle.UninstallOptions{
		BinaryPath: bin,
		GOOS:       runtime.GOOS,
		MCPConfigs: lifecycle.DefaultMCPConfigs(home),
		Service:    newServiceHook(home, bin),
		StateDirs:  lifecycle.DefaultStateDirs(home),
		PurgeState: *purge,
		Home:       home,
	}
	plan := lifecycle.PlanUninstall(opts)
	report := plan
	var applyErr error
	if *yes && !*dryRun {
		report, applyErr = lifecycle.ApplyUninstall(opts, plan)
	}

	if *jsonMode || *jsonl {
		kind := "uninstall_plan"
		if !report.DryRun {
			kind = "uninstall_result"
		}
		env := cli.NewEnvelope(kind, "uninstall", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		renderUninstall(report)
	}
	if applyErr != nil {
		exitRuntime("uninstall", "", *traceID, cli.CodeIO, applyErr, "re-run with --dry-run to inspect remaining items", *jsonl)
	}
}

func renderUninstall(r *lifecycle.UninstallReport) {
	if r.DryRun {
		pterm.Info.Println("g8s uninstall plan (DRY-RUN — nothing removed; re-run with --yes to apply)")
	} else {
		pterm.Info.Println("g8s uninstall result")
	}
	if len(r.Items) == 0 {
		fmt.Println("  nothing to remove")
	}
	for _, it := range r.Items {
		line := fmt.Sprintf("  [%s] %-13s %s", it.Action, it.Kind, it.Path)
		if it.Error != "" {
			line += "  error: " + it.Error
		}
		fmt.Println(line)
	}
	for _, it := range r.Kept {
		fmt.Printf("  [kept] %-13s %s  (%s)\n", it.Kind, it.Path, it.Detail)
	}
}

// serviceHook adapts the platform service manager to lifecycle.ServiceHook.
type serviceHook struct {
	home string
	bin  string
}

func newServiceHook(home, bin string) lifecycle.ServiceHook {
	return &serviceHook{home: home, bin: bin}
}

// Detect reports whether a g8s unit file written by `g8s service install` exists.
func (s *serviceHook) Detect() (bool, string) {
	var unit string
	switch runtime.GOOS {
	case "darwin":
		unit = filepath.Join(s.home, "Library", "LaunchAgents", "com.g8s.worker.plist")
	case "linux":
		unit = filepath.Join(s.home, ".config", "systemd", "user", "g8s.service")
	case "windows":
		if err := exec.Command("sc", "query", "g8s").Run(); err == nil {
			return true, "windows service g8s"
		}
		return false, ""
	default:
		return false, ""
	}
	if _, err := os.Stat(unit); err == nil {
		return true, unit
	}
	return false, ""
}

// Uninstall stops and removes the unit through the platform service manager.
func (s *serviceHook) Uninstall() error {
	dbPath, err := databasePath()
	if err != nil {
		return err
	}
	mgr, err := service.NewPlatformServiceManager(service.Config{
		BinaryPath:   s.bin,
		DatabasePath: dbPath,
		Home:         s.home,
	}, nil, nil)
	if err != nil {
		return err
	}
	return mgr.Uninstall()
}
