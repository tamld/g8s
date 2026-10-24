package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/charter"
	"github.com/tamld/g8s/internal/cli"
)

func runCharter(args []string) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "issue":
		runCharterIssue(args)
	case "verify":
		runCharterVerify(args)
	case "revoke":
		runCharterRevoke(args)
	case "complete":
		runCharterComplete(args)
	case "list":
		runCharterList(args)
	case "portfolio":
		runCharterPortfolio(args)
	case "help", "-h", "--help":
		fmt.Println("Usage: g8s charter <issue|verify|revoke|complete|list|portfolio> [flags]")
	default:
		exitUsage("charter", sub, "", fmt.Sprintf("unknown charter subcommand %q (expected issue|verify|revoke|complete|list|portfolio)", sub), "Run 'g8s charter --help'", false)
	}
}

func runCharterIssue(args []string) {
	fs := flag.NewFlagSet("charter issue", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	idFlag := fs.String("id", "", "optional explicit charter ID")
	issuerFlag := fs.String("issuer", "S1", "issuer stratum (S0 or S1)")
	repoFlag := fs.String("repo", "", "target repository identifier (e.g. tamld/g8s)")
	scopeRootFlag := fs.String("scope-root", ".", "target repository scope root")
	mandateFlag := fs.String("mandate", "", "immutable outcome mandate for the S2 DomainGovernor")
	budgetFlag := fs.Int("budget", 100000, "token/call budget ceiling")
	ttlFlag := fs.String("ttl", "2h", "charter time-to-live (max 24h)")
	var allowedPaths pathFlags
	fs.Var(&allowedPaths, "path", "allowed path glob (repeatable, defaults to '**')")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "issue", *traceID, err.Error(), "Run 'g8s charter issue --help'", *jsonl)
	}

	ttl, err := time.ParseDuration(strings.TrimSpace(*ttlFlag))
	if err != nil {
		exitUsage("charter", "issue", *traceID, fmt.Sprintf("invalid --ttl %q: %v", *ttlFlag, err), "Use duration like 1h or 30m", *jsonl)
	}
	issuer, err := charter.ParseStratum(*issuerFlag)
	if err != nil {
		exitUsage("charter", "issue", *traceID, err.Error(), "Specify --issuer=S0 or --issuer=S1", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "issue", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	c, err := store.Issue(charter.IssueRequest{
		CharterID:      *idFlag,
		IssuerStratum:  issuer,
		RepoID:         *repoFlag,
		ScopeRoot:      *scopeRootFlag,
		AllowedPaths:   allowedPaths,
		OutcomeMandate: *mandateFlag,
		BudgetTokens:   *budgetFlag,
		TTL:            ttl,
	})
	if err != nil {
		exitRuntime("charter", "issue", *traceID, cli.CodeDenied, err, "Ensure S0/S1 issuer, non-empty --repo and --mandate", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_issue", "charter", "issue", c)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("Issued Charter %s (repo=%s, epoch=%d, fence=%s, digest=%s)\n", c.CharterID, c.RepoID, c.Epoch, c.Fence, c.CharterDigest)
}

func runCharterVerify(args []string) {
	fs := flag.NewFlagSet("charter verify", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	idFlag := fs.String("id", "", "charter ID to verify")
	repoFlag := fs.String("repo", "", "optional repository identifier to match")
	epochFlag := fs.Int("epoch", 0, "expected epoch number")
	fenceFlag := fs.String("fence", "", "expected fence token")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "verify", *traceID, err.Error(), "Run 'g8s charter verify --help'", *jsonl)
	}
	if strings.TrimSpace(*idFlag) == "" {
		exitUsage("charter", "verify", *traceID, "--id is required", "Provide --id <charter-id>", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "verify", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	c, err := store.Verify(*idFlag, *repoFlag, *epochFlag, *fenceFlag)
	if err != nil {
		exitRuntime("charter", "verify", *traceID, cli.CodeDenied, err, "Charter is expired, revoked, or epoch/fence is stale", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_verify", "charter", "verify", map[string]any{
			"valid":   true,
			"charter": c,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("VERIFIED Charter %s (state=%s, epoch=%d, fence=%s)\n", c.CharterID, c.State, c.Epoch, c.Fence)
}

func runCharterRevoke(args []string) {
	fs := flag.NewFlagSet("charter revoke", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	idFlag := fs.String("id", "", "charter ID to revoke and reclaim")
	reasonFlag := fs.String("reason", "", "audit reason for revocation")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "revoke", *traceID, err.Error(), "Run 'g8s charter revoke --help'", *jsonl)
	}
	if strings.TrimSpace(*idFlag) == "" {
		exitUsage("charter", "revoke", *traceID, "--id is required", "Provide --id <charter-id>", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "revoke", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	c, err := store.RevokeAndReclaim(*idFlag, *reasonFlag)
	if err != nil {
		exitRuntime("charter", "revoke", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_revoke", "charter", "revoke", c)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("REVOKED Charter %s (new_epoch=%d, new_fence=%s)\n", c.CharterID, c.Epoch, c.Fence)
}

func runCharterComplete(args []string) {
	fs := flag.NewFlagSet("charter complete", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	idFlag := fs.String("id", "", "charter ID to complete")
	receiptHashFlag := fs.String("receipt-hash", "", "sealed RoundReceipt SHA-256 hash")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "complete", *traceID, err.Error(), "Run 'g8s charter complete --help'", *jsonl)
	}
	if strings.TrimSpace(*idFlag) == "" {
		exitUsage("charter", "complete", *traceID, "--id is required", "Provide --id <charter-id>", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "complete", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	c, err := store.Complete(*idFlag, *receiptHashFlag)
	if err != nil {
		exitRuntime("charter", "complete", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_complete", "charter", "complete", c)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("COMPLETED Charter %s (sealed_receipt_hash=%s)\n", c.CharterID, c.SealedReceiptHash)
}

func runCharterList(args []string) {
	fs := flag.NewFlagSet("charter list", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	repoFlag := fs.String("repo", "", "optional repository filter")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "list", *traceID, err.Error(), "Run 'g8s charter list --help'", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "list", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	list, err := store.List(*repoFlag)
	if err != nil {
		exitRuntime("charter", "list", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_list", "charter", "list", map[string]any{
			"count":    len(list),
			"charters": list,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	for _, c := range list {
		fmt.Printf("%s\t%s\t%s\tepoch=%d\tfence=%s\t%s\n", c.CharterID, c.RepoID, c.State, c.Epoch, c.Fence, c.OutcomeMandate)
	}
}

func runCharterPortfolio(args []string) {
	fs := flag.NewFlagSet("charter portfolio", flag.ContinueOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	dirFlag := fs.String("dir", "", "optional workspace directory for O-Plane portfolio projection")

	fs.SetOutput(os.Stdout)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("charter", "portfolio", *traceID, err.Error(), "Run 'g8s charter portfolio --help'", *jsonl)
	}

	store, err := charter.Open("")
	if err != nil {
		exitRuntime("charter", "portfolio", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	defer store.Close()

	snap, err := store.ProjectPortfolio(*dirFlag)
	if err != nil {
		exitRuntime("charter", "portfolio", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("charter_portfolio", "charter", "portfolio", snap)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("O-Plane Portfolio Projection (%s): total=%d active=%d completed=%d revoked=%d\n",
		snap.ProjectedAt, snap.TotalCount, snap.ActiveCount, snap.CompletedCount, snap.RevokedCount)
}
