package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsers(t *testing.T) {
	testDir := filepath.Join("testdata", "parsers")

	t.Run("node_test", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testDir, "node_test.txt"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		items := parseNodeTest(string(data))
		if items != 1046 {
			t.Errorf("expected 1046 items, got %d", items)
		}

		// TAP variant
		tapText := "# tests 42\n# pass 40\n# fail 2"
		if got := parseNodeTest(tapText); got != 42 {
			t.Errorf("expected 42 items for tap, got %d", got)
		}

		// Empty/unmatched
		if got := parseNodeTest("random log without test counts"); got != 0 {
			t.Errorf("expected 0 items for unparseable node output, got %d", got)
		}
	})

	t.Run("cargo_test", func(t *testing.T) {
		dataOk, err := os.ReadFile(filepath.Join(testDir, "cargo_test.txt"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		itemsOk := parseCargoTest(string(dataOk))
		if itemsOk != 14 {
			t.Errorf("expected 14 items, got %d", itemsOk)
		}

		dataFail, err := os.ReadFile(filepath.Join(testDir, "cargo_test_fail.txt"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		itemsFail := parseCargoTest(string(dataFail))
		if itemsFail != 12 {
			t.Errorf("expected 12 items (10 passed + 2 failed), got %d", itemsFail)
		}

		// Multiple test results in workspace
		multi := "test result: ok. 5 passed; 0 failed; ...\ntest result: ok. 10 passed; 1 failed; ..."
		if got := parseCargoTest(multi); got != 16 {
			t.Errorf("expected 16 items for multi cargo, got %d", got)
		}
	})

	t.Run("go_test", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testDir, "go_test.json"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		items := parseGoTest(data)
		if items != 2 {
			t.Errorf("expected 2 items (TestParserAlpha + TestParserBeta), got %d", items)
		}

		emptyData, err := os.ReadFile(filepath.Join(testDir, "go_test_empty.json"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		emptyItems := parseGoTest(emptyData)
		if emptyItems != 0 {
			t.Errorf("expected 0 items for [no tests to run], got %d", emptyItems)
		}
	})

	t.Run("did_verify", func(t *testing.T) {
		// Empty --files
		if got := parseDidVerify([]string{"did-verify", "--files"}); got != 0 {
			t.Errorf("expected 0 for empty --files, got %d", got)
		}
		if got := parseDidVerify([]string{"did-verify"}); got != 0 {
			t.Errorf("expected 0 when --files is omitted, got %d", got)
		}

		// With files
		if got := parseDidVerify([]string{"did-verify", "--files", "a.go", "b.go", "c.go"}); got != 3 {
			t.Errorf("expected 3 files, got %d", got)
		}

		// Flag stops files list
		if got := parseDidVerify([]string{"did-verify", "--files", "a.go", "b.go", "--verbose"}); got != 2 {
			t.Errorf("expected 2 files before flag, got %d", got)
		}

		// Space-separated files in single arg
		if got := parseDidVerify([]string{"did-verify", "--files", "a.go b.go"}); got != 2 {
			t.Errorf("expected 2 files from space-separated arg, got %d", got)
		}
	})

	t.Run("offer_check", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testDir, "offer_check.txt"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		items := parseOfferCheck(string(data))
		if items != 4 {
			t.Errorf("expected 4 exit= gate lines, got %d", items)
		}
	})

	t.Run("deliver", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testDir, "deliver.json"))
		if err != nil {
			t.Fatalf("failed to read fixture: %v", err)
		}
		extra := make(map[string]any)
		items := parseDeliver([]string{"g8s", "deliver", "--dry-run"}, data, "", extra)
		if items != 3 {
			t.Errorf("expected 3 items (2 applied + 1 deleted), got %d", items)
		}
		if extra["dry_run"] != true {
			t.Errorf("expected extra.dry_run to be true")
		}
		if extra["task_id"] != "task-7b49a1d2" {
			t.Errorf("expected task_id task-7b49a1d2, got %v", extra["task_id"])
		}
		if extra["receipt_id"] != "rc-92a105c8" {
			t.Errorf("expected receipt_id rc-92a105c8, got %v", extra["receipt_id"])
		}
		applied, ok := extra["applied"].([]string)
		if !ok || len(applied) != 2 {
			t.Errorf("expected 2 applied files, got %v", extra["applied"])
		}
		deleted, ok := extra["deleted"].([]string)
		if !ok || len(deleted) != 1 {
			t.Errorf("expected 1 deleted file, got %v", extra["deleted"])
		}
	})

	t.Run("redact_secrets", func(t *testing.T) {
		tests := []struct {
			input    string
			contains string
			redacted string
		}{
			{
				input:    "Connecting to postgresql://admin:s3cr3t@db.corp.internal:5432/main_db...",
				redacted: "postgresql://<REDACTED>",
			},
			{
				input:    "Service URL: http://user:superpass@service.local/api",
				redacted: "://<REDACTED>:<REDACTED>@",
			},
			{
				input:    "Failed specifically `secret_command_with_tokens` in runner",
				redacted: "specifically `<REDACTED>`",
			},
			{
				input:    "Setting api_key: abc123def456ghi for access",
				redacted: "api_key: <REDACTED>",
			},
			{
				input:    "Exported token=xyz987654321000 in env",
				redacted: "token=<REDACTED>",
			},
			{
				input:    "Authorization: Bearer my-secret-jwt-token-value-1234567890",
				redacted: "Bearer <REDACTED>",
			},
			{
				input:    "Token is ghp_1234567890abcdef1234567890abcdef for github",
				redacted: "<REDACTED_GH_TOKEN>",
			},
		}

		for _, tc := range tests {
			out := redactSecrets(tc.input)
			if !strings.Contains(out, tc.redacted) {
				t.Errorf("for input %q: expected redacted substring %q, got %q", tc.input, tc.redacted, out)
			}
		}
	})
}
