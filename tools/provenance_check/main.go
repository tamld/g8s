// Command provenance_check validates a single commit or squash-merge message
// against ADR-0039 trailing provenance requirements.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tamld/g8s/internal/dogfood"
)

type checkResult struct {
	OK   bool   `json:"ok"`
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
	Hint string `json:"hint,omitempty"`
}

func validationHint(p dogfood.Provenance) string {
	if p.Kind == "classification_error" && p.Ref == dogfood.E_PROVENANCE_TAG_EMPTY_REASON {
		return "F0-direct trailer requires a non-empty reason after em-dash: Provenance: F0-direct — <reason>"
	}
	return "final blank-line-separated trailer paragraph must contain only valid trailers including either 'Receipt: <task-id>' (>=6 chars with a digit) or 'Provenance: F0-direct — <reason>' (keep 'Closes #...' above the blank line)"
}

func loadMessage(fileFlag, messageFlag string, stdin io.Reader) (string, error) {
	if messageFlag != "" {
		return messageFlag, nil
	}
	if fileFlag != "" && fileFlag != "-" {
		raw, err := os.ReadFile(fileFlag)
		if err != nil {
			return "", fmt.Errorf("read message file %q: %w", fileFlag, err)
		}
		return string(raw), nil
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read message stdin: %w", err)
	}
	return string(raw), nil
}

func writeLine(w io.Writer, msg string) int {
	if _, err := fmt.Fprintln(w, msg); err != nil {
		return 2
	}
	return 0
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("provenance_check", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fileFlag := fs.String("file", "", "path to file containing commit or squash message ('-' reads stdin)")
	messageFlag := fs.String("message", "", "inline commit or squash message text")
	jsonFlag := fs.Bool("json", false, "emit JSON result object to stdout")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *fileFlag != "" && *messageFlag != "" {
		writeLine(stderr, "error: -file and -message are mutually exclusive")
		return 2
	}
	if fs.NArg() > 0 {
		writeLine(stderr, fmt.Sprintf("error: unexpected positional arguments: %v", fs.Args()))
		return 2
	}

	msg, err := loadMessage(*fileFlag, *messageFlag, stdin)
	if err != nil {
		writeLine(stderr, fmt.Sprintf("error: %v", err))
		return 2
	}

	p := dogfood.ClassifyCommit(msg)
	ok := p.Kind == "receipt" || p.Kind == "f0_direct"
	var hint string
	if !ok {
		hint = validationHint(p)
	}

	if *jsonFlag {
		b, err := json.Marshal(checkResult{
			OK:   ok,
			Kind: p.Kind,
			Ref:  p.Ref,
			Hint: hint,
		})
		if err != nil {
			writeLine(stderr, fmt.Sprintf("error: marshal result: %v", err))
			return 2
		}
		if rc := writeLine(stdout, string(b)); rc != 0 {
			return rc
		}
	} else if ok {
		if rc := writeLine(stdout, fmt.Sprintf("OK: kind=%s ref=%s", p.Kind, p.Ref)); rc != 0 {
			return rc
		}
	} else {
		if rc := writeLine(stderr, fmt.Sprintf("FAIL: kind=%s ref=%s — %s", p.Kind, p.Ref, hint)); rc != 0 {
			return rc
		}
	}

	if !ok {
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
