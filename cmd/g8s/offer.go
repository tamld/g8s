package main

// #420 distribution model: `g8s offer` — the pull-bundle made
// deterministic. RED-first: offer_test.go pins scaffold content, exit
// codes, idempotency, and the deny-all seed before implementation.

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s"
	"github.com/tamld/g8s/internal/cli"
)

const offerBundleVersion = "0.12.0"

const knowledgeEffortClassesSeed = `# Effort Classes Configuration — Knowledge Profile (issue #550 / C2)
# Maps task write-scope paths to reasoning effort defaults.
# Schema: schema_version "effort-classes.v1", first match by priority wins.
# Unregistered tasks implicitly default to medium (fail-open floor).

schema_version: "effort-classes.v1"

classes:
  # pilot-proven: doc-freshness audits and markdown updates run reliably at low effort (SCORECARD Gate 3.4)
  - name: docs
    default_effort: low
    priority: 10
    paths:
      - "*.md"
      - "docs/**"

  - name: test
    default_effort: medium
    priority: 20
    paths:
      - "*_test.*"
      - "*_test.go"
      - "**/*_test.go"
`

func runOffer(args []string) {
	if len(args) == 0 {
		exitUsage("offer", "", "", "unknown offer subcommand", "g8s offer init --profile <name> | check | version", false)
		return
	}
	sub := args[0]
	switch sub {
	case "init":
		runOfferInit(args[1:])
	case "check":
		runOfferCheck(args[1:])
	case "version":
		runOfferVersion()
	case "--help", "-h", "help":
		printOfferUsage()
	default:
		exitUsage("offer", sub, "", fmt.Sprintf("unknown offer subcommand %q (valid: init, check, version)", sub), "g8s offer init --profile <name>", false)
	}
}

func printOfferUsage() {
	fmt.Println("Usage: g8s offer <init|check|version> [flags]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  init --profile <knowledge|security|infra|utility> [--dir <target>] [--force]")
	fmt.Println("                                Scaffold .g8s/ + gate scripts from the embedded profile")
	fmt.Println("  check [--dir <target>] [--gates <list>]   Run the copied gates (first audit)")
	fmt.Println("  version                                   Print the offer bundle version")
}

func runOfferInit(args []string) {
	fs := flag.NewFlagSet("offer init", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	profile := fs.String("profile", "", "profile: knowledge | security | infra | utility (required)")
	dir := fs.String("dir", ".", "target project directory")
	force := fs.Bool("force", false, "overwrite an existing scaffold")
	if err := fs.Parse(args); err != nil {
		exitUsage("offer", "init", *traceID, err.Error(), "", *jsonl)
	}
	known := false
	for name := range offer.Profiles() {
		if name == *profile {
			known = true
		}
	}
	if !known {
		exitUsage("offer", "init", *traceID, fmt.Sprintf("unknown profile %q (valid: knowledge, security, infra, utility)", *profile), "g8s offer init --profile security", *jsonl)
		return
	}

	g8sDir := filepath.Join(*dir, ".g8s")
	if !*force {
		if _, err := os.Stat(g8sDir); err == nil {
			exitUsage("offer", "init", *traceID, "target already scaffolded (.g8s/ exists) — use --force to overwrite", "g8s offer init --profile "+*profile+" --force", *jsonl)
			return
		}
	}

	seed, ok := offer.Seed(*profile, "trust")
	if !ok {
		exitRuntime("offer", "init", *traceID, cli.CodeRuntime, fmt.Errorf("no trust seed embedded for profile %q", *profile), "", *jsonl)
		return
	}
	lanes, ok := offer.Seed(*profile, "lanes")
	if !ok {
		exitRuntime("offer", "init", *traceID, cli.CodeRuntime, fmt.Errorf("no lanes seed embedded for profile %q", *profile), "", *jsonl)
		return
	}
	if mkErr := os.MkdirAll(g8sDir, 0o700); mkErr != nil {
		exitRuntime("offer", "init", *traceID, cli.CodeIO, mkErr, "", *jsonl)
		return
	}
	if werr := os.WriteFile(filepath.Join(g8sDir, "trust-boundaries.yml"), []byte(seed), 0o600); werr != nil {
		exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
		return
	}
	if werr := os.WriteFile(filepath.Join(g8sDir, "lane-bundles.yml"), []byte(lanes), 0o600); werr != nil {
		exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
		return
	}
	scaffold := []string{".g8s/trust-boundaries.yml", ".g8s/lane-bundles.yml"}
	if *profile == "knowledge" {
		if werr := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(knowledgeEffortClassesSeed), 0o600); werr != nil {
			exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
			return
		}
		scaffold = append(scaffold, ".g8s/effort-classes.yml")
	}

	toolsDir := filepath.Join(*dir, "tools")
	if mkErr := os.MkdirAll(toolsDir, 0o700); mkErr != nil {
		exitRuntime("offer", "init", *traceID, cli.CodeIO, mkErr, "", *jsonl)
		return
	}
	for _, name := range offer.GateScripts() {
		raw, ok := offer.GateScript(name)
		if !ok {
			continue
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if werr := os.WriteFile(filepath.Join(toolsDir, name), []byte(raw), mode); werr != nil {
			exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
			return
		}
	}

	out := map[string]any{
		"profile":  *profile,
		"target":   *dir,
		"scaffold": scaffold,
		"gates":    offer.GateScripts(),
		"next":     []string{"wire the gates into CI", "run 'g8s offer check' for the first audit", "report findings per Mode 3"},
	}
	env := cli.NewEnvelope("offer_init", "offer", "init", out)
	env.TraceID = *traceID
	if *jsonMode || *jsonl {
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("offer: scaffolded %s profile into %s\nnext: wire the gates into CI, then run 'g8s offer check'\n", *profile, *dir)
}

func runOfferCheck(args []string) {
	fs := flag.NewFlagSet("offer check", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	dir := fs.String("dir", ".", "target project directory")
	gates := fs.String("gates", "spec,structure,links", "comma list: spec,structure,links")
	if err := fs.Parse(args); err != nil {
		exitUsage("offer", "check", *traceID, err.Error(), "", *jsonl)
	}
	var results []map[string]any
	overall := 0
	run := func(name, script string) {
		out, err := runCapture(script, *dir)
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		results = append(results, map[string]any{"gate": name, "script": script, "exit": code, "output": out})
		if code != 0 {
			overall = 1
		}
	}
	for _, g := range strings.Split(*gates, ",") {
		switch strings.TrimSpace(g) {
		case "spec":
			run("spec", filepath.Join(*dir, "tools", "ci_spec_code_sync.sh"))
		case "structure":
			run("structure", filepath.Join(*dir, "tools", "ci_structure_sync.sh"))
		case "links":
			run("links", filepath.Join(*dir, "tools", "ci_link_integrity.sh"))
		default:
			exitUsage("offer", "check", *traceID, fmt.Sprintf("unknown gate %q (valid: spec, structure, links)", g), "", *jsonl)
			return
		}
	}
	env := cli.NewEnvelope("offer_check", "offer", "check", map[string]any{"gates": results, "overall": overallStatus(overall)})
	env.TraceID = *traceID
	if *jsonMode || *jsonl {
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	for _, r := range results {
		fmt.Printf("%-10s exit=%v (%s)\n", r["gate"], r["exit"], r["script"])
	}
	fmt.Printf("overall: %s\n", overallStatus(overall))
}

func overallStatus(fail int) string {
	if fail != 0 {
		return "FAIL"
	}
	return "PASS"
}

// runCapture executes a gate script with the target as repo root and
// returns combined output. Failures surface as non-zero exit codes (the
// caller records them — check never panics).
func runCapture(script, dir string) (string, error) {
	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runOfferVersion() {
	fmt.Printf("g8s offer bundle %s\n", offerBundleVersion)
}
