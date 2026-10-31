package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/mesh"
)

func runCoordinatorMesh(args []string) {
	if tier := strings.ToUpper(strings.TrimSpace(os.Getenv("G8S_TIER"))); tier == "F1" || tier == "F2" {
		jsonl := false
		for _, a := range args {
			if a == "--jsonl" {
				jsonl = true
				break
			}
		}
		exitRuntime("coordinator", "mesh", "", cli.CodeDenied, fmt.Errorf("coordinator protocol is F0-only: worker tier %s is forbidden", tier), "Coordinator operations require F0 tier authority", jsonl)
	}

	fs := flag.NewFlagSet("coordinator mesh", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	_, traceID, jsonl, _ := cli.AddCommonFlagsWithDefaults(fs, true)
	meshDirFlag := fs.String("mesh-dir", os.Getenv("G8S_MESH_DIR"), "path to mesh state directory (defaults to G8S_MESH_DIR)")
	opFlag := fs.String("op", "", "mesh operation to execute (required)")
	inputFlag := fs.String("input", "", "optional path to JSON input file, '-' for stdin, or inline JSON (missing input uses {})")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		exitUsage("coordinator", "mesh", *traceID, err.Error(), "usage: g8s coordinator mesh --mesh-dir <dir> --op <operation> [--input <file|-|json>] [--json]", *jsonl)
	}

	meshDir := strings.TrimSpace(*meshDirFlag)
	if meshDir == "" {
		exitUsage("coordinator", "mesh", *traceID, "--mesh-dir is required (or set G8S_MESH_DIR)", "Provide --mesh-dir <dir> or export G8S_MESH_DIR=<dir>", *jsonl)
	}

	op := strings.TrimSpace(*opFlag)
	if op == "" {
		exitUsage("coordinator", "mesh", *traceID, "--op is required", "Valid operations: register, quota-publish, send, inbox, ack, claim, release, accept, snapshot, wait", *jsonl)
	}

	inputData := []byte("{}")
	inputVal := strings.TrimSpace(*inputFlag)
	if inputVal != "" {
		var r io.Reader
		var closer io.Closer

		if inputVal == "-" {
			r = os.Stdin
		} else if strings.HasPrefix(inputVal, "{") || strings.HasPrefix(inputVal, "[") {
			if info, err := os.Stat(inputVal); err != nil || info.IsDir() {
				r = strings.NewReader(inputVal)
			} else {
				f, err := os.Open(inputVal)
				if err != nil {
					exitRuntime("coordinator", "mesh", *traceID, cli.CodeIO, fmt.Errorf("open input file: %w", err), "Ensure input file is accessible", *jsonl)
				}
				closer = f
				r = f
			}
		} else {
			f, err := os.Open(inputVal)
			if err != nil {
				exitRuntime("coordinator", "mesh", *traceID, cli.CodeIO, fmt.Errorf("open input file: %w", err), "Provide a valid file path or JSON input", *jsonl)
			}
			closer = f
			r = f
		}

		if closer != nil {
			defer closer.Close()
		}

		data, err := io.ReadAll(io.LimitReader(r, 16385))
		if err != nil {
			exitRuntime("coordinator", "mesh", *traceID, cli.CodeIO, fmt.Errorf("read input: %w", err), "", *jsonl)
		}
		if len(data) > 16384 {
			exitRuntime("coordinator", "mesh", *traceID, cli.CodeInvalid, errors.New("input exceeds maximum size of 16384 bytes"), "Provide input <= 16384 bytes", *jsonl)
		}

		trimmed := strings.TrimSpace(string(data))
		if len(trimmed) == 0 {
			inputData = []byte("{}")
		} else {
			if !json.Valid(data) {
				exitRuntime("coordinator", "mesh", *traceID, cli.CodeInvalid, errors.New("input is not valid JSON"), "Provide valid JSON input", *jsonl)
			}
			inputData = data
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := mesh.Open(meshDir)
	if err != nil {
		exitRuntime("coordinator", "mesh", *traceID, cli.CodeRuntime, err, "Ensure mesh directory is accessible and valid", *jsonl)
	}
	defer store.Close()

	resRaw, err := store.Execute(ctx, op, json.RawMessage(inputData))
	if err != nil {
		exitRuntime("coordinator", "mesh", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if len(resRaw) == 0 {
		resRaw = json.RawMessage("{}")
	}

	env := cli.NewEnvelope("coordinator_mesh", "coordinator", "mesh", resRaw)
	env.TraceID = *traceID
	if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
		exitRuntime("coordinator", "mesh", *traceID, cli.CodeIO, err, "", *jsonl)
	}
}
