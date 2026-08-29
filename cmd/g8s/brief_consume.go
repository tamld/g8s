package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func runBriefConsume(args []string) {
	fs := flag.NewFlagSet("brief-consume", flag.ExitOnError)
	id := fs.String("id", "", "brief ID to consume (required)")
	failIf(fs.Parse(args))

	if *id == "" {
		if fs.NArg() > 0 {
			*id = fs.Arg(0)
		} else {
			fmt.Fprintln(os.Stderr, "brief-consume requires --id <id>")
			os.Exit(2)
		}
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	_, err = executeBriefConsume(os.Stdout, store, *id)
	failIf(err)
}

func executeBriefConsume(w io.Writer, store *controlplane.Store, id string) (brief.Brief, error) {
	b, err := brief.Consume(store, id)
	if err != nil {
		return brief.Brief{}, err
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return brief.Brief{}, fmt.Errorf("format brief json: %w", err)
	}
	fmt.Fprintln(w, string(out))
	return b, nil
}
