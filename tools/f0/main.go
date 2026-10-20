package main

import (
	"fmt"
	"os"
)

type commandFunc func(args []string) int

var commands = map[string]commandFunc{
	"run":     runCommand,
	"ledger":  ledgerCommand,
	"mandate": mandateCommand,
	"gate":    gateCommand,
	"status":  statusCommand,
	"waive":   waiveCommand,
	"manual":  manualCommand,
	"anchor":  anchorCommand,
	"audit":   auditCommand,
	// Later phases will extend:
	// "hook":    hookCommand,
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "f0 - evidence-gated supervisor harness\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  f0 run -- <cmd...>\n")
	fmt.Fprintf(os.Stderr, "  f0 ledger verify [--repo P]\n")
	fmt.Fprintf(os.Stderr, "  f0 ledger show <id> [--repo P]\n")
	fmt.Fprintf(os.Stderr, "  f0 ledger list [--repo P] [--sha S] [--kind K] [--json]\n")
	fmt.Fprintf(os.Stderr, "  f0 mandate issue --issuer <name> --campaign <text> --repo <name>=<abs-path>:<tier> ... [--exclude <glob>] [--budget N] [--ttl 24h]\n")
	fmt.Fprintf(os.Stderr, "  f0 mandate show [--json]\n")
	fmt.Fprintf(os.Stderr, "  f0 mandate check --repo <path> --action <a> [--paths p1,p2] [--json]\n")
	fmt.Fprintf(os.Stderr, "  f0 gate check --repo <path> --action <a> [--pr <N> | --head-sha <S> --base <B>] [--report <file>] [--pr-body <text>] [--json]\n")
	fmt.Fprintf(os.Stderr, "  f0 status [--repo <path>] [--pr <N>] [--report-block] [--json]\n")
	fmt.Fprintf(os.Stderr, "  f0 waive --repo <path> --pr <N> --check <name> --issue <url> --reason <text> [--head-sha <sha>]\n")
	fmt.Fprintf(os.Stderr, "  f0 manual --repo <path> --file <rel> --reason <text>\n")
	fmt.Fprintf(os.Stderr, "  f0 anchor [--repo <path>]\n")
	fmt.Fprintf(os.Stderr, "  f0 audit [--repo <path>] [--since 30d] [--strict] [--json]\n")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmdName := os.Args[1]
	fn, ok := commands[cmdName]
	if !ok {
		fmt.Fprintf(os.Stderr, "f0: unknown command %q\n", cmdName)
		printUsage()
		os.Exit(1)
	}

	code := fn(os.Args[2:])
	os.Exit(code)
}
