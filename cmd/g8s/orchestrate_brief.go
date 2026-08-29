package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

// parseBriefContent parses markdown content to extract a title from the first
// heading, DoD items from a DoD section, and retains the full content as payload.
func parseBriefContent(content, fallbackTitle string) (title, payload, dod string) {
	payload = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			break
		}
	}
	if title == "" {
		title = fallbackTitle
	}
	if title == "" {
		title = "Structured Task Brief"
	}

	var inDoD bool
	var dodLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## dod") || strings.HasPrefix(lower, "### dod") ||
			strings.HasPrefix(lower, "## definition of done") || strings.HasPrefix(lower, "### definition of done") {
			inDoD = true
			continue
		}
		if inDoD {
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			if trimmed != "" {
				dodLines = append(dodLines, line)
			}
		}
	}

	if len(dodLines) > 0 {
		dod = strings.TrimSpace(strings.Join(dodLines, "\n"))
	}
	if dod == "" {
		dod = "- [ ] Brief execution completed"
	}

	return title, payload, dod
}

// executeOrchestrateBriefFile reads a markdown brief file, issues it as an
// active brief, and writes the brief ID (or formatted JSON) to w.
func executeOrchestrateBriefFile(
	w io.Writer,
	store *controlplane.Store,
	filePath, issuedBy, ttlStr, titleOverride, dodOverride string,
	emitJSON bool,
) (brief.Brief, error) {
	if strings.TrimSpace(filePath) == "" {
		return brief.Brief{}, errors.New("orchestrate: brief-file path is required")
	}

	ttl := 2 * time.Hour
	if ttlStr != "" {
		parsedTTL, err := time.ParseDuration(ttlStr)
		if err != nil {
			return brief.Brief{}, fmt.Errorf("orchestrate: parse ttl: %w", err)
		}
		ttl = parsedTTL
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return brief.Brief{}, fmt.Errorf("orchestrate: read brief file: %w", err)
	}

	rawContent := string(data)
	if strings.TrimSpace(rawContent) == "" {
		return brief.Brief{}, fmt.Errorf("orchestrate: brief file %q is empty", filePath)
	}

	title, payload, dod := parseBriefContent(rawContent, filepath.Base(filePath))
	if titleOverride != "" {
		title = titleOverride
	}
	if dodOverride != "" {
		dod = dodOverride
	}
	if issuedBy == "" {
		issuedBy = "sisyphus"
	}

	b, err := brief.Issue(store, title, payload, dod, issuedBy, ttl)
	if err != nil {
		return brief.Brief{}, fmt.Errorf("orchestrate: issue brief: %w", err)
	}

	if emitJSON {
		out, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			return brief.Brief{}, fmt.Errorf("orchestrate: marshal brief json: %w", err)
		}
		fmt.Fprintln(w, string(out))
	} else {
		fmt.Fprintln(w, b.ID)
	}

	return b, nil
}

// executeOrchestrateDispatch re-issues an existing stored brief for resume/retry
// workflows and writes the new brief ID (or formatted JSON) to w.
func executeOrchestrateDispatch(
	w io.Writer,
	store *controlplane.Store,
	dispatchID, issuedBy, ttlStr string,
	emitJSON bool,
) (brief.Brief, error) {
	if strings.TrimSpace(dispatchID) == "" {
		return brief.Brief{}, errors.New("orchestrate: dispatch brief id is required")
	}

	ctx := context.Background()
	orig, err := store.GetBrief(ctx, dispatchID)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnknownBrief) {
			return brief.Brief{}, fmt.Errorf("orchestrate: stored brief not found: %s", dispatchID)
		}
		return brief.Brief{}, fmt.Errorf("orchestrate: get stored brief: %w", err)
	}

	ttl := 2 * time.Hour
	if ttlStr != "" {
		parsedTTL, err := time.ParseDuration(ttlStr)
		if err != nil {
			return brief.Brief{}, fmt.Errorf("orchestrate: parse ttl: %w", err)
		}
		ttl = parsedTTL
	}

	issuer := issuedBy
	if issuer == "" {
		issuer = orig.IssuedBy
	}
	if issuer == "" {
		issuer = "sisyphus"
	}

	b, err := brief.Issue(store, orig.Title, orig.PayloadMD, orig.DodMD, issuer, ttl)
	if err != nil {
		return brief.Brief{}, fmt.Errorf("orchestrate: re-issue brief: %w", err)
	}

	if emitJSON {
		out, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			return brief.Brief{}, fmt.Errorf("orchestrate: marshal brief json: %w", err)
		}
		fmt.Fprintln(w, string(out))
	} else {
		fmt.Fprintln(w, b.ID)
	}

	return b, nil
}
