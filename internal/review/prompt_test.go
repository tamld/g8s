package review

import (
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/diffintel"
)

func TestBuildVerifierPrompt(t *testing.T) {
	tests := []struct {
		name       string
		chunk      diffintel.ReviewChunk
		intent     string
		wantIntent bool
	}{
		{
			name: "with intent",
			chunk: diffintel.ReviewChunk{
				FormattedBody: "L0001: +func main() {}",
			},
			intent:     "Review for security",
			wantIntent: true,
		},
		{
			name: "empty intent",
			chunk: diffintel.ReviewChunk{
				FormattedBody: "L0001: +func helper() {}",
			},
			intent:     "   ",
			wantIntent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := BuildVerifierPrompt(tt.chunk, tt.intent)
			if !strings.Contains(prompt, "You are a specialized Verifier Subagent") {
				t.Errorf("expected prompt to contain subagent header")
			}
			if !strings.Contains(prompt, tt.chunk.FormattedBody) {
				t.Errorf("expected prompt to contain chunk formatted body")
			}
			if tt.wantIntent && !strings.Contains(prompt, "Review Intent: Review for security") {
				t.Errorf("expected prompt to contain Review Intent")
			}
			if !tt.wantIntent && strings.Contains(prompt, "Review Intent:") {
				t.Errorf("expected prompt not to contain Review Intent")
			}
		})
	}
}
