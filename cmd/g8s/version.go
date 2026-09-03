package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/tamld/g8s/internal/cli"
)

var (
	Version   = "0.6.1"
	Commit    = "unknown" // -ldflags "-X main.Commit=$(git rev-parse HEAD)"
	BuildTime = "unknown" // -ldflags "-X main.BuildTime=$(date -u)"
)

func init() {
	if Commit == "unknown" || BuildTime == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && Commit == "unknown" {
					Commit = setting.Value
				}
				if setting.Key == "vcs.time" && BuildTime == "unknown" {
					BuildTime = setting.Value
				}
			}
		}
	}
}

// runVersion emits application build and version metadata.
func runVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	if err := fs.Parse(args); err != nil {
		exitUsage("version", "", *traceID, err.Error(), "", *jsonl)
	}
	if *jsonMode || *jsonl {
		data := map[string]any{
			"app":        AppName,
			"version":    Version,
			"commit":     Commit,
			"build_time": BuildTime,
			"zero_cgo":   true,
			"runtime":    "pure-go",
			"actor":      *actor,
		}
		env := cli.NewEnvelope("version", "version", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}
	fmt.Printf("g8s version %s\n", Version)
	fmt.Printf("  commit: %s\n", Commit)
	fmt.Printf("  built:  %s\n", BuildTime)
}
