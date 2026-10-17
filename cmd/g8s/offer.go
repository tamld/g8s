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

const securityEffortClassesSeed = `# Effort Classes Configuration — Security Profile (issue #550 / C2)
# Maps task write-scope paths to reasoning effort defaults.
# Schema: schema_version "effort-classes.v1", first match by priority wins.
# Unregistered tasks implicitly default to medium (fail-open floor).

schema_version: "effort-classes.v1"

classes:
  - name: security-review
    default_effort: high
    priority: 10
    paths:
      - "src/guards/**"
      - "src/core/**"
      - "SECURITY.md"

  - name: test
    default_effort: medium
    priority: 20
    paths:
      - "tests/**"
      - "*_test.*"
      - "**/*_test.*"

  - name: docs
    default_effort: low
    priority: 30
    paths:
      - "*.md"
      - "docs/**"
      - "wiki/**"
      - "templates/**"
      - "examples/**"
`

const infraEffortClassesSeed = `# Effort Classes Configuration — Infra Profile (issue #550 / C2)
# Maps task write-scope paths to reasoning effort defaults.
# Schema: schema_version "effort-classes.v1", first match by priority wins.
# Unregistered tasks implicitly default to medium (fail-open floor).

schema_version: "effort-classes.v1"

classes:
  - name: infra-review
    default_effort: high
    priority: 10
    paths:
      - "infrastructure/**"
      - "proxmox/**"
      - "traefik/**"
      - "teleport/**"
      - "vaultwarden/**"
      - "docker-compose*.yml"
      - "*.env*"

  - name: test
    default_effort: medium
    priority: 20
    paths:
      - "tests/**"
      - "scripts/**"
      - "bin/**"

  - name: docs
    default_effort: low
    priority: 30
    paths:
      - "*.md"
      - "docs/**"
      - "knowledge/**"
      - "templates/**"
`

const utilityEffortClassesSeed = `# Effort Classes Configuration — Utility Profile (issue #550 / C2)
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
      - "tests/**"
      - "*_test.*"
      - "**/*_test.*"
      - "**/*.test.*"
      - "**/*.spec.*"
`

func effortClassesSeedForProfile(profile string) string {
	switch profile {
	case "security":
		return securityEffortClassesSeed
	case "infra":
		return infraEffortClassesSeed
	case "utility":
		return utilityEffortClassesSeed
	default:
		return knowledgeEffortClassesSeed
	}
}

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
	fmt.Println("  init --profile <knowledge|security|infra|utility> [--dir <target>] [--force] [--gates-only] [--gates-dir <dir>]")
	fmt.Println("                                Scaffold .g8s/ + gate scripts from the embedded profile")
	fmt.Println("  check [--dir <target>] [--gates <list>] [--gates-dir <dir>]")
	fmt.Println("                                Run the copied gates (first audit)")
	fmt.Println("  version                       Print the offer bundle version")
}

func runOfferInit(args []string) {
	fs := flag.NewFlagSet("offer init", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	profile := fs.String("profile", "", "profile: knowledge | security | infra | utility (required unless --gates-only)")
	dir := fs.String("dir", ".", "target project directory")
	force := fs.Bool("force", false, "overwrite an existing scaffold")
	gatesOnly := fs.Bool("gates-only", false, "install/refresh tools/ci_*.sh gate scripts without modifying .g8s/ registries")
	gatesDir := fs.String("gates-dir", "tools", "relative directory to install gate scripts")
	if err := fs.Parse(args); err != nil {
		exitUsage("offer", "init", *traceID, err.Error(), "", *jsonl)
	}

	var scaffold []string
	if !*gatesOnly {
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
				exitUsage("offer", "init", *traceID, "target already scaffolded (.g8s/ exists) — use --force to overwrite or --gates-only to refresh gate scripts", "g8s offer init --profile "+*profile+" --force", *jsonl)
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
		effortSeed := effortClassesSeedForProfile(*profile)
		if werr := os.WriteFile(filepath.Join(g8sDir, "effort-classes.yml"), []byte(effortSeed), 0o600); werr != nil {
			exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
			return
		}
		scaffold = []string{".g8s/trust-boundaries.yml", ".g8s/lane-bundles.yml", ".g8s/effort-classes.yml"}
	}

	targetGatesDir := filepath.Join(*dir, *gatesDir)
	if mkErr := os.MkdirAll(targetGatesDir, 0o700); mkErr != nil {
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
		if werr := os.WriteFile(filepath.Join(targetGatesDir, name), []byte(raw), mode); werr != nil {
			exitRuntime("offer", "init", *traceID, cli.CodeIO, werr, "", *jsonl)
			return
		}
	}

	out := map[string]any{
		"profile":    *profile,
		"target":     *dir,
		"gates_only": *gatesOnly,
		"scaffold":   scaffold,
		"gates":      offer.GateScripts(),
		"next":       []string{"wire the gates into CI", "run 'g8s offer check' for the first audit", "report findings per Mode 3"},
	}
	env := cli.NewEnvelope("offer_init", "offer", "init", out)
	env.TraceID = *traceID
	if *jsonMode || *jsonl {
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	if *gatesOnly {
		fmt.Printf("offer: refreshed gate scripts into %s\n", targetGatesDir)
		return
	}
	fmt.Printf("offer: scaffolded %s profile into %s\nnext: wire the gates into CI, then run 'g8s offer check'\n", *profile, *dir)
}

func resolveGateScriptPath(dir, gatesDir, scriptName string) string {
	if gatesDir != "" {
		return filepath.Join(dir, gatesDir, scriptName)
	}
	toolsPath := filepath.Join(dir, "tools", scriptName)
	if _, err := os.Stat(toolsPath); err == nil {
		return toolsPath
	}
	opsPath := filepath.Join(dir, "ops", "gates", scriptName)
	if _, err := os.Stat(opsPath); err == nil {
		return opsPath
	}
	return toolsPath
}

func runOfferCheck(args []string) {
	fs := flag.NewFlagSet("offer check", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	dir := fs.String("dir", ".", "target project directory")
	gates := fs.String("gates", "spec,structure,links", "comma list: spec,structure,links")
	gatesDir := fs.String("gates-dir", "", "relative directory containing gate scripts")
	if err := fs.Parse(args); err != nil {
		exitUsage("offer", "check", *traceID, err.Error(), "", *jsonl)
	}
	var results []map[string]any
	overall := 0
	run := func(name, scriptName string) {
		script := resolveGateScriptPath(*dir, *gatesDir, scriptName)
		isStale := false
		if rawDisk, err := os.ReadFile(script); err == nil {
			if embedded, ok := offer.GateScript(scriptName); ok {
				if string(rawDisk) != embedded {
					isStale = true
				}
			}
		}
		out, err := runCapture(script, *dir)
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		r := map[string]any{"gate": name, "script": script, "exit": code, "output": out}
		if isStale {
			r["stale"] = true
		}
		results = append(results, r)
		if code != 0 {
			overall = 1
		}
	}
	for _, g := range strings.Split(*gates, ",") {
		switch strings.TrimSpace(g) {
		case "spec":
			run("spec", "ci_spec_code_sync.sh")
		case "structure":
			run("structure", "ci_structure_sync.sh")
		case "links":
			run("links", "ci_link_integrity.sh")
		default:
			exitUsage("offer", "check", *traceID, fmt.Sprintf("unknown gate %q (valid: spec, structure, links)", g), "", *jsonl)
			return
		}
	}
	env := cli.NewEnvelope("offer_check", "offer", "check", map[string]any{"gates": results, "overall": overallStatus(overall)})
	env.TraceID = *traceID
	if *jsonMode || *jsonl {
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		for _, r := range results {
			fmt.Printf("%-10s exit=%v (%s)\n", r["gate"], r["exit"], r["script"])
			if stale, _ := r["stale"].(bool); stale {
				fmt.Printf("  [warn] stale gate script %s differs from embedded bundle (run 'g8s offer init --gates-only --dir %s')\n", r["script"], *dir)
			}
			if code, _ := r["exit"].(int); code != 0 {
				if out, _ := r["output"].(string); strings.TrimSpace(out) != "" {
					trimmed := strings.ReplaceAll(strings.TrimSpace(out), "\r\n", "\n")
					for _, line := range strings.Split(trimmed, "\n") {
						fmt.Printf("    %s\n", line)
					}
				}
			}
		}
		fmt.Printf("overall: %s\n", overallStatus(overall))
	}
	if overall != 0 {
		os.Exit(1)
	}
}

func overallStatus(fail int) string {
	if fail != 0 {
		return "FAIL"
	}
	return "PASS"
}

// runCapture executes a gate script with the target as repo root and
// returns combined output. When the target repository does not vendor
// tools/ci_*.sh on disk (e.g. L1-domain-locked repos like aegis), it
// falls back to executing the embedded gate script from the binary.
func runCapture(script, dir string) (string, error) {
	if _, err := os.Stat(script); os.IsNotExist(err) {
		if raw, ok := offer.GateScript(filepath.Base(script)); ok {
			cmd := exec.Command("bash", "-s")
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader(raw)
			out, runErr := cmd.CombinedOutput()
			return string(out), runErr
		}
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runOfferVersion() {
	fmt.Printf("g8s offer bundle %s\n", offerBundleVersion)
}
