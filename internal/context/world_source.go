package context

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

// WorldSnippetsFromFile loads world snippets from the path in G8S_WORLD_SNIPPETS_PATH.
// Missing env, missing file, or unreadable path yields healthy-empty ([]string{}, nil).
// File content must be a JSON array of strings; any other shape returns an error for
// the broker to fail-open on. At most maxPerSource entries are returned.
func WorldSnippetsFromFile(ctx context.Context) ([]string, error) {
	_ = ctx
	path := strings.TrimSpace(os.Getenv("G8S_WORLD_SNIPPETS_PATH"))
	if path == "" {
		return []string{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var snippets []string
	if err := json.Unmarshal(raw, &snippets); err != nil {
		return nil, err
	}
	if len(snippets) > maxPerSource {
		snippets = snippets[:maxPerSource]
	}
	return snippets, nil
}
