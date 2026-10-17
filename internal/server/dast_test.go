package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

func TestHTTPDaemon_DAST_FSAdversarialSuite(t *testing.T) {
	// Isolate from ambient worker environment variables
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_SCOPE_ROOT", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_PERMISSION", "")

	tmpDir := t.TempDir()
	scopeDir := filepath.Join(tmpDir, "scope")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("mkdir scope: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content sensitive"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	symlinkFile := filepath.Join(scopeDir, "symlink.txt")
	if err := os.Symlink(outsideFile, symlinkFile); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	rcDbPath := filepath.Join(tmpDir, "receipts.db")
	mgr, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		t.Fatalf("create receipt manager: %v", err)
	}
	defer mgr.Close()

	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)
	srv.SetReceiptVerifier(mgr)

	ts := httptest.NewServer(srv.httpSrv.Handler)
	defer ts.Close()

	issueTestReceipt := func(paths []string) *receipt.WriteReceipt {
		t.Helper()
		rc, err := mgr.IssueReceipt("brain", paths, 10*time.Minute,
			receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
				SchemaURI:      "g8s://envelope/write-receipt/v1",
				FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
				RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
			}),
			receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
				RulesetVersion: "v1",
				PipelineDigest: "dast-test",
			}),
			receipt.WithProvenanceContext("g8s/v0.9.0", "HEAD", "trace-dast"),
		)
		if err != nil {
			t.Fatalf("issue receipt: %v", err)
		}
		return rc
	}

	t.Run("single_use_receipt_replay_blocked", func(t *testing.T) {
		allowedFile := filepath.Join(scopeDir, "allowed.txt")
		if err := os.WriteFile(allowedFile, []byte("initial version"), 0o600); err != nil {
			t.Fatalf("write allowed.txt: %v", err)
		}

		rc := issueTestReceipt([]string{"allowed.txt"})

		// First edit with receipt -> 200 OK
		editReq1, _ := json.Marshal(map[string]any{
			"path":       allowedFile,
			"old_string": "initial version",
			"new_string": "updated version",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
			"dry_run":    false,
		})
		resp1, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq1))
		if err != nil {
			t.Fatalf("first edit post failed: %v", err)
		}
		defer resp1.Body.Close()
		if resp1.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK on first edit, got %d", resp1.StatusCode)
		}

		// Second edit with same receipt -> 403 Forbidden (already consumed)
		editReq2, _ := json.Marshal(map[string]any{
			"path":       allowedFile,
			"old_string": "updated version",
			"new_string": "hacked version",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
			"dry_run":    false,
		})
		resp2, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq2))
		if err != nil {
			t.Fatalf("second edit post failed: %v", err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden on replayed receipt, got %d", resp2.StatusCode)
		}
	})

	t.Run("concurrent_10_way_receipt_race", func(t *testing.T) {
		raceFile := filepath.Join(scopeDir, "race.txt")
		if err := os.WriteFile(raceFile, []byte("initial race text"), 0o600); err != nil {
			t.Fatalf("write race.txt: %v", err)
		}

		raceRc := issueTestReceipt([]string{"race.txt"})

		concurrency := 10
		statusCodes := make([]int, concurrency)
		var wg sync.WaitGroup
		startBarrier := make(chan struct{})

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-startBarrier

				reqBody, _ := json.Marshal(map[string]any{
					"path":       raceFile,
					"old_string": "initial race text",
					"new_string": fmt.Sprintf("race win by goroutine %d", idx),
					"scope_root": scopeDir,
					"receipt_id": raceRc.ReceiptID,
					"dry_run":    false,
				})
				resp, postErr := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(reqBody))
				if postErr != nil {
					t.Errorf("race request %d failed: %v", idx, postErr)
					return
				}
				defer resp.Body.Close()
				statusCodes[idx] = resp.StatusCode
			}(i)
		}

		close(startBarrier)
		wg.Wait()

		var count200, count403 int
		for _, code := range statusCodes {
			switch code {
			case http.StatusOK:
				count200++
			case http.StatusForbidden:
				count403++
			}
		}

		if count200 != 1 || count403 != 9 {
			t.Fatalf("expected exactly 1 200 OK and 9 403 Forbidden, got %d 200s and %d 403s (status codes: %v)",
				count200, count403, statusCodes)
		}
	})

	t.Run("symlink_escape_blocked", func(t *testing.T) {
		// 1. Direct symlink file access via /api/v1/fs/read
		readReq, _ := json.Marshal(map[string]any{
			"path":       symlinkFile,
			"scope_root": scopeDir,
		})
		respRead, err := http.Post(ts.URL+"/api/v1/fs/read", "application/json", bytes.NewReader(readReq))
		if err != nil {
			t.Fatalf("read symlink post failed: %v", err)
		}
		defer respRead.Body.Close()
		if respRead.StatusCode == http.StatusOK {
			t.Fatalf("expected read on symlink to be rejected, got 200 OK")
		}
		if respRead.StatusCode != http.StatusForbidden && respRead.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request on symlink read, got %d", respRead.StatusCode)
		}

		// 2. Direct symlink file mutation via /api/v1/fs/edit
		symRc := issueTestReceipt([]string{"symlink.txt"})
		editReq, _ := json.Marshal(map[string]any{
			"path":       symlinkFile,
			"old_string": "outside content sensitive",
			"new_string": "malicious overwrite",
			"scope_root": scopeDir,
			"receipt_id": symRc.ReceiptID,
		})
		respEdit, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq))
		if err != nil {
			t.Fatalf("edit symlink post failed: %v", err)
		}
		defer respEdit.Body.Close()
		if respEdit.StatusCode == http.StatusOK {
			t.Fatalf("expected edit on symlink to be rejected, got 200 OK")
		}
		if respEdit.StatusCode != http.StatusForbidden && respEdit.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request on symlink edit, got %d", respEdit.StatusCode)
		}

		// 3. Symlink root access via /api/v1/fs/search
		searchReq, _ := json.Marshal(map[string]any{
			"pattern":    "sensitive",
			"roots":      []string{symlinkFile},
			"scope_root": scopeDir,
		})
		respSearch, err := http.Post(ts.URL+"/api/v1/fs/search", "application/json", bytes.NewReader(searchReq))
		if err != nil {
			t.Fatalf("search symlink post failed: %v", err)
		}
		defer respSearch.Body.Close()
		if respSearch.StatusCode == http.StatusOK {
			t.Fatalf("expected search on symlink to be rejected, got 200 OK")
		}
		if respSearch.StatusCode != http.StatusForbidden && respSearch.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request on symlink search, got %d", respSearch.StatusCode)
		}

		// 4. Delegated scope root symlink escape under G8S_TIER=F1 (strictly asserts 403 Forbidden)
		symlinkDir := filepath.Join(scopeDir, "symlink_dir")
		if err := os.Symlink(outsideDir, symlinkDir); err == nil {
			t.Setenv("G8S_TIER", "F1")
			t.Setenv("G8S_SCOPE_ROOT", scopeDir)

			for _, endpoint := range []string{"/api/v1/fs/read", "/api/v1/fs/edit", "/api/v1/fs/search"} {
				body, _ := json.Marshal(map[string]any{
					"path":       filepath.Join(symlinkDir, "outside.txt"),
					"pattern":    "sensitive",
					"roots":      []string{symlinkDir},
					"old_string": "foo",
					"new_string": "bar",
					"scope_root": symlinkDir,
					"receipt_id": "any-rc",
				})
				resp, postErr := http.Post(ts.URL+endpoint, "application/json", bytes.NewReader(body))
				if postErr != nil {
					t.Fatalf("post to %s failed: %v", endpoint, postErr)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("expected 403 Forbidden on %s with escaping scope_root, got %d", endpoint, resp.StatusCode)
				}
			}
			t.Setenv("G8S_TIER", "")
			t.Setenv("G8S_SCOPE_ROOT", "")
		}
	})

	t.Run("oversized_body_and_malformed_json_rejected", func(t *testing.T) {
		endpoints := []string{"/api/v1/fs/search", "/api/v1/fs/read", "/api/v1/fs/edit"}

		// Oversized payload (> 1.1 MiB)
		oversizedLen := 1150 * 1024 // 1.15 MiB > 1 MiB limit
		oversized := bytes.Repeat([]byte("X"), oversizedLen)

		for _, endpoint := range endpoints {
			resp, err := http.Post(ts.URL+endpoint, "application/json", bytes.NewReader(oversized))
			if err != nil {
				t.Fatalf("post oversized payload to %s failed: %v", endpoint, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request on oversized payload to %s, got %d", endpoint, resp.StatusCode)
			}
		}

		// Malformed JSON payloads
		malformedPayloads := [][]byte{
			[]byte("{invalid-json"),
			[]byte("not a json at all"),
			[]byte(`{"pattern": 12345, missing_quotes}`),
			[]byte(""),
		}

		for _, endpoint := range endpoints {
			for idx, malformed := range malformedPayloads {
				resp, err := http.Post(ts.URL+endpoint, "application/json", bytes.NewReader(malformed))
				if err != nil {
					t.Fatalf("post malformed payload %d to %s failed: %v", idx, endpoint, err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("expected 400 Bad Request on malformed JSON (case %d) to %s, got %d", idx, endpoint, resp.StatusCode)
				}
			}
		}
	})

	t.Run("precheck_failure_preserves_unconsumed_receipt", func(t *testing.T) {
		precheckFile := filepath.Join(scopeDir, "allowed_precheck.txt")
		if err := os.WriteFile(precheckFile, []byte("precheck version 1"), 0o600); err != nil {
			t.Fatalf("write allowed_precheck.txt: %v", err)
		}

		rc := issueTestReceipt([]string{"allowed_precheck.txt"})

		// Attempt 1: /api/v1/fs/edit targeting outside.txt with receipt -> fails VerifyScope (403 Forbidden)
		editReq1, _ := json.Marshal(map[string]any{
			"path":       outsideFile,
			"old_string": "outside content sensitive",
			"new_string": "tampered",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		resp1, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq1))
		if err != nil {
			t.Fatalf("attempt 1 post failed: %v", err)
		}
		resp1.Body.Close()
		if resp1.StatusCode != http.StatusForbidden {
			t.Fatalf("attempt 1: expected 403 Forbidden on out-of-scope edit, got %d", resp1.StatusCode)
		}
		rcCheck1, err := mgr.VerifyReceipt(rc.ReceiptID)
		if err != nil {
			t.Fatalf("verify receipt after attempt 1: %v", err)
		}
		if rcCheck1.Consumed {
			t.Fatalf("attempt 1: receipt should NOT be consumed on scope precheck failure")
		}

		// Attempt 2: /api/v1/fs/edit targeting allowed_precheck.txt with stale if_sha256 -> fails IfSHA256 (400 Bad Request)
		editReq2, _ := json.Marshal(map[string]any{
			"path":       precheckFile,
			"old_string": "precheck version 1",
			"new_string": "precheck version 2",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
			"if_sha256":  "0000000000000000000000000000000000000000000000000000000000000000",
		})
		resp2, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq2))
		if err != nil {
			t.Fatalf("attempt 2 post failed: %v", err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusBadRequest {
			t.Fatalf("attempt 2: expected 400 Bad Request on sha256 mismatch, got %d", resp2.StatusCode)
		}
		rcCheck2, err := mgr.VerifyReceipt(rc.ReceiptID)
		if err != nil {
			t.Fatalf("verify receipt after attempt 2: %v", err)
		}
		if rcCheck2.Consumed {
			t.Fatalf("attempt 2: receipt should NOT be consumed on sha256 precheck failure")
		}

		// Attempt 3: /api/v1/fs/edit targeting allowed_precheck.txt with valid parameters -> 200 OK
		editReq3, _ := json.Marshal(map[string]any{
			"path":       precheckFile,
			"old_string": "precheck version 1",
			"new_string": "precheck version 2",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		resp3, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq3))
		if err != nil {
			t.Fatalf("attempt 3 post failed: %v", err)
		}
		resp3.Body.Close()
		if resp3.StatusCode != http.StatusOK {
			t.Fatalf("attempt 3: expected 200 OK on valid edit, got %d", resp3.StatusCode)
		}
		rcCheck3, err := mgr.VerifyReceipt(rc.ReceiptID)
		if err != nil {
			t.Fatalf("verify receipt after attempt 3: %v", err)
		}
		if !rcCheck3.Consumed {
			t.Fatalf("attempt 3: receipt MUST be consumed after successful edit")
		}

		// Attempt 4: Replay with same receipt -> 403 Forbidden
		editReq4, _ := json.Marshal(map[string]any{
			"path":       precheckFile,
			"old_string": "precheck version 2",
			"new_string": "precheck version 3",
			"scope_root": scopeDir,
			"receipt_id": rc.ReceiptID,
		})
		resp4, err := http.Post(ts.URL+"/api/v1/fs/edit", "application/json", bytes.NewReader(editReq4))
		if err != nil {
			t.Fatalf("attempt 4 post failed: %v", err)
		}
		resp4.Body.Close()
		if resp4.StatusCode != http.StatusForbidden {
			t.Fatalf("attempt 4: expected 403 Forbidden on replay, got %d", resp4.StatusCode)
		}
	})
}
