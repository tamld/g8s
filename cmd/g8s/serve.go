// Package main — serve.go implements the g8s serve command for running
// the daemon mode with HTTP API server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/server"
)

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	_, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	configFile := fs.String("config", "", "path to server config YAML file")
	address := fs.String("address", "", "address to listen on (overrides config)")
	daemon := fs.Bool("daemon", false, "run as daemon (block until stopped)")
	if err := fs.Parse(args); err != nil {
		exitUsage("serve", "", *traceID, err.Error(), "", *jsonl)
	}

	cfg := server.DefaultConfig()
	if *configFile != "" {
		// For now, just use defaults - YAML config loading can be added later
		_ = configFile
	}
	if *address != "" {
		cfg.Address = *address
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("serve", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("serve", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	srv := server.NewServer(cfg, store)

	if *daemon {
		// Run in background, wait for signal
		if err := srv.Start(); err != nil {
			exitRuntime("serve", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping server: %v\n", err)
		}
	} else {
		// Run and block
		if err := srv.Run(); err != nil {
			exitRuntime("serve", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("serve", "serve", "", map[string]any{
			"status":  "stopped",
			"address": cfg.Address,
		})
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	}
}