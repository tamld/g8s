package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/featuremap"
)

// FeatureInspectOutput formats the output of g8s feature inspect.
type FeatureInspectOutput struct {
	Feature             *featuremap.FeatureNode `json:"feature"`
	DerivedAllowedPaths []string                `json:"derived_allowed_paths"`
	TransitiveConsumers []string                `json:"transitive_consumers"`
}

func runFeature(args []string) {
	if len(args) == 0 {
		exitUsage("feature", "", "", "usage: g8s feature <verify|inspect|list> [options]", "Supported subcommands: verify, inspect, list", false)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "verify":
		runFeatureVerify(subargs)
	case "inspect":
		runFeatureInspect(subargs)
	case "list":
		runFeatureList(subargs)
	case "help", "-h", "--help":
		printFeatureUsage()
	default:
		exitUsage("feature", subcmd, "", fmt.Sprintf("unknown feature subcommand %q", subcmd), "Supported subcommands: verify, inspect, list", false)
	}
}

func printFeatureUsage() {
	fmt.Println("Usage:")
	fmt.Println("  g8s feature verify [--file .g8s/feature-map.yml] [--repo .] [--json]")
	fmt.Println("  g8s feature inspect <feature-id> [--file .g8s/feature-map.yml] [--json]")
	fmt.Println("  g8s feature list [--file .g8s/feature-map.yml] [--json]")
}

func normalizeFlags(args []string) []string {
	var flags []string
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "--file" || arg == "-file" || arg == "--repo" || arg == "-repo" || arg == "--id" || arg == "-id") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, arg)
		}
	}
	return append(flags, pos...)
}

func runFeatureVerify(args []string) {
	fs := flag.NewFlagSet("feature-verify", flag.ContinueOnError)
	fileFlag := fs.String("file", featuremap.DefaultFeatureMapPath, "Path to feature map YAML")
	repoFlag := fs.String("repo", ".", "Path to repository root")
	jsonFlag := fs.Bool("json", false, "Output verification report as JSON")

	args = normalizeFlags(args)
	if err := fs.Parse(args); err != nil {
		exitUsage("feature", "verify", "", err.Error(), "usage: g8s feature verify [--file <path>] [--repo <dir>] [--json]", false)
	}

	fm, err := featuremap.Load(*fileFlag)
	if err != nil {
		if *jsonFlag {
			report := featuremap.ValidationReport{
				Valid:  false,
				Errors: []string{err.Error()},
			}
			out, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(out))
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error loading feature map %s: %v\n", *fileFlag, err)
		os.Exit(1)
	}

	report := featuremap.Validate(fm, *repoFlag)

	if *jsonFlag {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding report: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		if !report.Valid {
			os.Exit(1)
		}
		return
	}

	if report.Valid {
		fmt.Printf("Feature map %s is valid (%d features)\n", *fileFlag, report.FeatureCount)
		return
	}

	fmt.Fprintf(os.Stderr, "Feature map %s validation failed (%d features):\n", *fileFlag, report.FeatureCount)
	for _, errStr := range report.Errors {
		fmt.Fprintf(os.Stderr, "  - %s\n", errStr)
	}
	for _, o := range report.OrphanPaths {
		fmt.Fprintf(os.Stderr, "  - orphan: %s\n", o)
	}
	for _, u := range report.UnverifiedInvariants {
		fmt.Fprintf(os.Stderr, "  - unverified invariant: %s\n", u)
	}
	os.Exit(1)
}

func runFeatureInspect(args []string) {
	fs := flag.NewFlagSet("feature-inspect", flag.ContinueOnError)
	fileFlag := fs.String("file", featuremap.DefaultFeatureMapPath, "Path to feature map YAML")
	idFlag := fs.String("id", "", "Feature ID to inspect")
	jsonFlag := fs.Bool("json", false, "Output inspection result as JSON")

	args = normalizeFlags(args)
	if err := fs.Parse(args); err != nil {
		exitUsage("feature", "inspect", "", err.Error(), "usage: g8s feature inspect <feature-id> [--file <path>] [--json]", false)
	}

	featureID := *idFlag
	if featureID == "" && fs.NArg() > 0 {
		featureID = fs.Arg(0)
	}

	if strings.TrimSpace(featureID) == "" {
		if *jsonFlag {
			env := cli.NewErrorEnvelope("feature", "inspect", "", cli.CodeUsage, "feature ID is required", "Provide <feature-id>", "")
			_ = cli.WriteResponse(os.Stderr, env, true)
			os.Exit(2)
		}
		exitUsage("feature", "inspect", "", "feature ID is required", "usage: g8s feature inspect <feature-id> [--file <path>] [--json]", false)
	}

	fm, err := featuremap.Load(*fileFlag)
	if err != nil {
		if *jsonFlag {
			env := cli.NewErrorEnvelope("feature", "inspect", "", cli.CodeRuntime, err.Error(), "", "")
			_ = cli.WriteResponse(os.Stderr, env, true)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error loading feature map: %v\n", err)
		os.Exit(1)
	}

	node, ok := fm.GetFeature(featureID)
	if !ok {
		if *jsonFlag {
			env := cli.NewErrorEnvelope("feature", "inspect", "", cli.CodeNotFound, fmt.Sprintf("feature %q not found", featureID), "", "")
			_ = cli.WriteResponse(os.Stderr, env, true)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Feature %q not found in %s\n", featureID, *fileFlag)
		os.Exit(1)
	}

	allowedPaths, err := fm.DeriveAllowedPaths(featureID)
	if err != nil {
		allowedPaths = []string{}
	}

	transitive, err := fm.ResolveTransitiveFeatures(featureID)
	var consumers []string
	if err == nil {
		for _, tn := range transitive {
			if tn.ID != featureID {
				consumers = append(consumers, tn.ID)
			}
		}
	}

	if *jsonFlag {
		output := FeatureInspectOutput{
			Feature:             node,
			DerivedAllowedPaths: allowedPaths,
			TransitiveConsumers: consumers,
		}
		out, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(out))
		return
	}

	fmt.Printf("Feature: %s\n", node.ID)
	fmt.Printf("Title:   %s\n", node.Title)
	fmt.Printf("Story:   %s\n", node.PersonaStory)
	fmt.Printf("SRS:     %s\n", strings.Join(node.SRSReqs, ", "))
	if node.OpenSpecDelta != "" {
		fmt.Printf("Delta:   %s\n", node.OpenSpecDelta)
	}
	fmt.Printf("Class:   %s\n", node.VerifierClass)
	fmt.Println("Code Paths:")
	for _, cp := range node.CodePaths {
		fmt.Printf("  - %s\n", cp)
	}
	fmt.Println("Derived Allowed Paths:")
	for _, ap := range allowedPaths {
		fmt.Printf("  - %s\n", ap)
	}
	fmt.Println("Invariants:")
	for _, inv := range node.Invariants {
		fmt.Printf("  - %s\n", inv)
	}
	fmt.Println("Visible Tests:")
	for _, vt := range node.VisibleTests {
		fmt.Printf("  - %s\n", vt)
	}
	if len(consumers) > 0 {
		fmt.Printf("Transitive Direct Consumers: %s\n", strings.Join(consumers, ", "))
	} else {
		fmt.Println("Transitive Direct Consumers: (none)")
	}
}

func runFeatureList(args []string) {
	fs := flag.NewFlagSet("feature-list", flag.ContinueOnError)
	fileFlag := fs.String("file", featuremap.DefaultFeatureMapPath, "Path to feature map YAML")
	jsonFlag := fs.Bool("json", false, "Output feature list as JSON")

	args = normalizeFlags(args)
	if err := fs.Parse(args); err != nil {
		exitUsage("feature", "list", "", err.Error(), "usage: g8s feature list [--file <path>] [--json]", false)
	}

	fm, err := featuremap.Load(*fileFlag)
	if err != nil {
		if *jsonFlag {
			env := cli.NewErrorEnvelope("feature", "list", "", cli.CodeRuntime, err.Error(), "", "")
			_ = cli.WriteResponse(os.Stderr, env, true)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error loading feature map: %v\n", err)
		os.Exit(1)
	}

	if *jsonFlag {
		out, err := json.MarshalIndent(fm.Features, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding features: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("%-24s %-12s %s\n", "FEATURE ID", "CLASS", "TITLE")
	fmt.Println(strings.Repeat("-", 70))
	for _, f := range fm.Features {
		fmt.Printf("%-24s %-12s %s\n", f.ID, f.VerifierClass, f.Title)
	}
}
