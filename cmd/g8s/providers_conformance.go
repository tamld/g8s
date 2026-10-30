package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/provider/conformance"
)

const conformanceUsage = "usage: g8s providers conformance [--provider <name[,name]>] [--live] [--allow-paid <name[,name]>] [--model <m>] [--json] [--write]"

// runProvidersConformance implements `g8s providers conformance` (DELTA-43).
func runProvidersConformance(args []string) {
	fs := flag.NewFlagSet("providers conformance", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	providerFlag := fs.String("provider", "", "comma-separated provider names to probe (default: all)")
	live := fs.Bool("live", false, "run quota-consuming live probes (free providers only unless --allow-paid)")
	allowPaid := fs.String("allow-paid", "", "comma-separated paid providers allowed for live probes (e.g. claude,codex); agy is never live-probed")
	model := fs.String("model", "", "model override for live probes (9router default: fast-agent; opencode default: a *free* model)")
	write := fs.Bool("write", false, "persist ground truth to .g8s/provider-conformance.yml and render docs/user-guide/provider-matrix.md")
	repo := fs.String("repo", "", "repository root for --write (default: git toplevel of cwd)")
	timeout := fs.Duration("timeout", 180*time.Second, "per-run deadline for live CLI probes")
	killAfter := fs.Duration("kill-after", 3*time.Second, "P4 deadline before the process group is killed")
	ctxTokens := fs.Int("context-probe-tokens", 0, "H3: send an N-token prompt to observe the context window (0 = skip)")
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("providers", "conformance", *traceID, err.Error(), conformanceUsage, *jsonl)
	}

	cfgFile, cfgErr := config.LoadProvidersConfig()
	if cfgErr != nil {
		cfgFile = nil
	}
	opts := conformance.Options{
		Live:               *live,
		AllowPaid:          conformance.ParseAllowPaid(*allowPaid),
		Model:              strings.TrimSpace(*model),
		Timeout:            *timeout,
		KillAfter:          *killAfter,
		ContextProbeTokens: *ctxTokens,
		ProvidersConfig:    cfgFile,
	}
	for _, p := range strings.Split(*providerFlag, ",") {
		if p = strings.TrimSpace(p); p != "" {
			if !conformance.KnownProvider(opts, p) {
				exitUsage("providers", "conformance", *traceID, fmt.Sprintf("unknown provider %q", p), conformanceUsage, *jsonl)
			}
			opts.Providers = append(opts.Providers, p)
		}
	}

	m := conformance.Run(context.Background(), opts)

	written := map[string]string{}
	if *write {
		root := strings.TrimSpace(*repo)
		if root == "" {
			root = gitToplevel()
		}
		merged, err := conformance.WriteMatrix(filepath.Join(root, conformance.DefaultMatrixPath), m)
		if err != nil {
			exitRuntime("providers", "conformance", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		if err := conformance.WriteDoc(filepath.Join(root, conformance.DefaultDocPath), merged); err != nil {
			exitRuntime("providers", "conformance", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		written["matrix"] = conformance.DefaultMatrixPath
		written["doc"] = conformance.DefaultDocPath
	}

	if *jsonMode || *jsonl {
		data := map[string]any{"matrix": m}
		if len(written) > 0 {
			data["written"] = written
		}
		env := cli.NewEnvelope("provider_conformance", "providers", "conformance", data)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("providers", "conformance", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	td := pterm.TableData{{"Provider", "Class", "Mode", "Version", "Probes", "Failed required"}}
	for _, p := range m.Providers {
		var cells []string
		for _, r := range p.Probes {
			cells = append(cells, fmt.Sprintf("%s=%s", r.ID, r.Status))
		}
		failed := "-"
		if f := p.FailedRequired(); len(f) > 0 {
			ids := make([]string, len(f))
			for i, id := range f {
				ids[i] = string(id)
			}
			failed = strings.Join(ids, ",")
		}
		ver := p.Version
		if ver == "" {
			ver = "-"
		}
		td = append(td, []string{p.Name, string(p.Class), p.Mode, ver, strings.Join(cells, " "), failed})
	}
	if err := pterm.DefaultTable.WithHasHeader().WithData(td).Render(); err != nil {
		exitRuntime("providers", "conformance", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	for k, v := range written {
		fmt.Printf("wrote %s: %s\n", k, v)
	}
}

func gitToplevel() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		return "."
	}
	return wd
}

// conformanceGate consults .g8s/provider-conformance.yml before dispatch.
// It returns a warning message when the provider FAILed required probes and
// refuse=true only when enforcement is enabled (--enforce-conformance or
// G8S_ENFORCE_CONFORMANCE=1). A missing or unreadable matrix never refuses.
func conformanceGate(providerName string, enforce bool) (warning string, refuse bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	v, err := conformance.CheckDispatchFromDir(wd, providerName)
	if err != nil {
		return "conformance matrix unreadable (gate skipped): " + err.Error(), false
	}
	if !v.Refuse {
		return "", false
	}
	if !enforce && os.Getenv("G8S_ENFORCE_CONFORMANCE") == "1" {
		enforce = true
	}
	return v.Message, enforce
}
