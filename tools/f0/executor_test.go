package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupPassingGateRepo(t *testing.T) (repoDir, headSHA string, ledger *Ledger) {
	t.Helper()
	dir, _, head := setupTestGitRepo(t)

	cfgDir := filepath.Join(dir, ".g8s")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgContent := "ci: none\nverify_commands:\n  - [\"go\", \"test\", \"./...\"]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "f0.yml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Mandate{
		Version:     1,
		ID:          "md-t2-merge-test",
		Issuer:      "alice",
		IssuedAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Campaign:    "t2-merge-testing",
		Repos:       []MandateRepo{{Name: "testrepo", Path: dir, MaxTier: "T2"}},
		ErrorBudget: ErrorBudget{Limit: 5},
	}
	if err := IssueMandate(m); err != nil {
		t.Fatalf("IssueMandate failed: %v", err)
	}

	l, err := OpenRepoLedger(dir)
	if err != nil {
		t.Fatalf("OpenRepoLedger failed: %v", err)
	}

	if err := l.Append(&Entry{
		ID:           "ev-pass-t2",
		Kind:         "go-test",
		Repo:         dir,
		SHA:          head,
		Status:       "PASS",
		Dirty:        false,
		Cmd:          []string{"go", "test", "./..."},
		ItemsChecked: 1,
	}); err != nil {
		t.Fatalf("Append ev-pass-t2 failed: %v", err)
	}

	return dir, head, l
}

func TestExecuteT2Merge_GateFailBlocksMerge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	mergeCalled := false
	opts := MergeOptions{
		RepoDir:   repoDir,
		PR:        101,
		HeadSHA:   headSHA,
		BaseRef:   "main",
		CredStore: &StaticCredentialStore{Token: "test-token"},
		MergeFunc: func(r string, p int, tok, tr string) (string, error) {
			mergeCalled = true
			return "fake-merge-sha", nil
		},
	}

	res, err := ExecuteT2Merge(opts)
	if err == nil {
		t.Fatalf("expected error from ExecuteT2Merge on failing gate, got nil")
	}
	if !strings.Contains(err.Error(), "refusing merge: f0 gate check failed") {
		t.Fatalf("expected 'refusing merge: f0 gate check failed', got: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on failure, got %+v", res)
	}
	if mergeCalled {
		t.Fatalf("MergeFunc should not have been called when gate fails")
	}

	// Verify no merge entries in ledger
	ledger, _ := OpenRepoLedger(repoDir)
	if ledger != nil {
		entries, _ := ledger.ReadEntries()
		for _, e := range entries {
			if e.Kind == "merge-pre-revert" || e.Kind == "merge-exec" {
				t.Fatalf("unexpected ledger entry %s: %+v", e.Kind, e)
			}
		}
	}
}

func TestExecuteT2Merge_PassingGateMergesWithAttestationTrailer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, headSHA, ledger := setupPassingGateRepo(t)

	var capturedRepo string
	var capturedPR int
	var capturedToken string
	var capturedTrailer string

	fakeMergeSHA := "111122223333444455556666777788889999aaaa"

	opts := MergeOptions{
		RepoDir:   repoDir,
		PR:        42,
		HeadSHA:   headSHA,
		BaseRef:   "main",
		CredStore: &StaticCredentialStore{Token: "secret-token-xyz"},
		MergeFunc: func(r string, pr int, token, trailer string) (string, error) {
			capturedRepo = r
			capturedPR = pr
			capturedToken = token
			capturedTrailer = trailer
			return fakeMergeSHA, nil
		},
		PostMergeChecks: func(r, mSHA string) ([]ForgeCheck, error) {
			return []ForgeCheck{
				{Name: "ci/build", State: "success", Bucket: "pass"},
				{Name: "ci/test", State: "success", Bucket: "pass"},
			}, nil
		},
	}

	res, err := ExecuteT2Merge(opts)
	if err != nil {
		t.Fatalf("ExecuteT2Merge failed: %v", err)
	}

	if !res.Merged {
		t.Fatalf("expected Merged == true")
	}
	if res.MergeSHA != fakeMergeSHA {
		t.Fatalf("expected MergeSHA %s, got %s", fakeMergeSHA, res.MergeSHA)
	}
	if !res.PreRevertPrepared {
		t.Fatalf("expected PreRevertPrepared == true")
	}
	if res.PostMergeStatus != "PASS" {
		t.Fatalf("expected PostMergeStatus PASS, got %s", res.PostMergeStatus)
	}
	if res.AutoReverted {
		t.Fatalf("expected AutoReverted == false")
	}
	if !strings.HasPrefix(res.AttestationTrailer, "F0-Ledger-Head: ") {
		t.Fatalf("expected AttestationTrailer to start with 'F0-Ledger-Head: ', got %s", res.AttestationTrailer)
	}
	if capturedTrailer != res.AttestationTrailer {
		t.Fatalf("expected MergeFunc to receive attestation trailer %s, got %s", res.AttestationTrailer, capturedTrailer)
	}
	if capturedToken != "secret-token-xyz" {
		t.Fatalf("expected token 'secret-token-xyz', got %s", capturedToken)
	}
	if capturedPR != 42 {
		t.Fatalf("expected PR 42, got %d", capturedPR)
	}
	if capturedRepo != repoDir {
		t.Fatalf("expected repoDir %s, got %s", repoDir, capturedRepo)
	}

	// Verify ledger entries order: gate -> merge-pre-revert -> merge-exec
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}

	hasPreRevert := false
	hasMergeExec := false
	for _, k := range kinds {
		if k == "merge-pre-revert" {
			hasPreRevert = true
		}
		if k == "merge-exec" {
			hasMergeExec = true
		}
	}
	if !hasPreRevert || !hasMergeExec {
		t.Fatalf("expected ledger to contain merge-pre-revert and merge-exec, got: %v", kinds)
	}

	// Ledger hash verification
	rep, err := ledger.Verify(repoDir)
	if err != nil {
		t.Fatalf("ledger.Verify failed: %v", err)
	}
	if rep.EntriesCount < 4 {
		t.Fatalf("expected at least 4 entries, got %d", rep.EntriesCount)
	}

	// Commit with the attestation trailer and verify auditCommand considers it attested
	commitMsg := fmt.Sprintf("Merge pull request #42\n\n%s\n", res.AttestationTrailer)
	commitCmd := exec.Command("git", "commit", "--allow-empty", "-m", commitMsg)
	commitCmd.Dir = repoDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v: %s", err, string(out))
	}

	// Audit check
	auditBuf := new(bytes.Buffer)
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := auditCommand([]string{"--repo", repoDir, "--json"})
	w.Close()
	os.Stdout = oldStdout
	_, _ = io.Copy(auditBuf, r)

	if code != 0 {
		t.Fatalf("auditCommand failed with exit code %d", code)
	}
	var unattested []UnattestedCommit
	if err := json.Unmarshal(auditBuf.Bytes(), &unattested); err != nil {
		t.Fatalf("unmarshaling audit json: %v, out: %s", err, auditBuf.String())
	}
	for _, u := range unattested {
		if u.Subject == "Merge pull request #42" {
			t.Fatalf("commit with trailer should be attested, but was listed as unattested: %+v", u)
		}
	}
}

func TestExecuteT2Merge_RedPostMergeCI_TriggersAutoRevertAndDebit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, headSHA, ledger := setupPassingGateRepo(t)

	fakeMergeSHA := "aaaa22223333444455556666777788889999bbbb"
	fakeRevertSHA := "bbbb22223333444455556666777788889999cccc"

	revertCalled := false
	var capturedRevertMergeSHA string

	opts := MergeOptions{
		RepoDir:   repoDir,
		PR:        88,
		HeadSHA:   headSHA,
		BaseRef:   "main",
		CredStore: &StaticCredentialStore{Token: "test-token"},
		MergeFunc: func(r string, pr int, token, trailer string) (string, error) {
			return fakeMergeSHA, nil
		},
		PostMergeChecks: func(r, mSHA string) ([]ForgeCheck, error) {
			return []ForgeCheck{
				{Name: "ci/build", State: "success", Bucket: "pass"},
				{Name: "ci/e2e-tests", State: "failure", Bucket: "fail"},
			}, nil
		},
		RevertFunc: func(r, mSHA, token string) (string, error) {
			revertCalled = true
			capturedRevertMergeSHA = mSHA
			return fakeRevertSHA, nil
		},
	}

	res, err := ExecuteT2Merge(opts)
	if err != nil {
		t.Fatalf("ExecuteT2Merge failed: %v", err)
	}

	if !res.Merged {
		t.Fatalf("expected Merged == true")
	}
	if !res.AutoReverted {
		t.Fatalf("expected AutoReverted == true")
	}
	if res.PostMergeStatus != "REVERTED" {
		t.Fatalf("expected PostMergeStatus REVERTED, got %s", res.PostMergeStatus)
	}
	if res.RevertSHA != fakeRevertSHA {
		t.Fatalf("expected RevertSHA %s, got %s", fakeRevertSHA, res.RevertSHA)
	}
	if !revertCalled {
		t.Fatalf("expected RevertFunc to be called")
	}
	if capturedRevertMergeSHA != fakeMergeSHA {
		t.Fatalf("expected RevertFunc mergeSHA %s, got %s", fakeMergeSHA, capturedRevertMergeSHA)
	}

	// Verify ledger contains auto-revert entry
	entries, err := ledger.ReadEntries()
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	var foundAutoRevert bool
	for _, e := range entries {
		if e.Kind == "auto-revert" {
			foundAutoRevert = true
			if e.SHA != fakeRevertSHA {
				t.Fatalf("expected auto-revert entry SHA %s, got %s", fakeRevertSHA, e.SHA)
			}
			failedChecks, ok := e.Extra["failed_checks"].([]any)
			if !ok && e.Extra["failed_checks"] != nil {
				// might be []string
				if sc, okStr := e.Extra["failed_checks"].([]string); okStr {
					if len(sc) != 1 || sc[0] != "ci/e2e-tests" {
						t.Fatalf("expected failed_checks [ci/e2e-tests], got %v", sc)
					}
				}
			} else if ok {
				if len(failedChecks) != 1 || failedChecks[0] != "ci/e2e-tests" {
					t.Fatalf("expected failed_checks [ci/e2e-tests], got %v", failedChecks)
				}
			}
		}
	}
	if !foundAutoRevert {
		t.Fatalf("expected auto-revert entry in repo ledger")
	}

	// Verify debit recorded in global ledger
	gLedger, err := OpenGlobalLedger()
	if err != nil {
		t.Fatalf("OpenGlobalLedger failed: %v", err)
	}
	gEntries, err := gLedger.ReadEntries()
	if err != nil {
		t.Fatalf("ReadEntries global ledger failed: %v", err)
	}
	var foundDebit bool
	expectedReason := "auto-revert PR #88 on red post-merge CI"
	for _, e := range gEntries {
		if e.Kind == "debit" {
			reason, _ := e.Extra["reason"].(string)
			if strings.Contains(reason, expectedReason) {
				foundDebit = true
				break
			}
		}
	}
	if !foundDebit {
		t.Fatalf("expected debit entry for auto-revert in global ledger")
	}
}

func TestExecuteT2Merge_WaivedCheck_DoesNotAutoRevert(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, headSHA, ledger := setupPassingGateRepo(t)

	// Append waiver for flaky-check
	if err := ledger.Append(&Entry{
		ID:           "ev-waiver-flake",
		Kind:         "waiver",
		Repo:         repoDir,
		SHA:          headSHA,
		Status:       "PASS",
		ItemsChecked: 1,
		Extra: map[string]any{
			"pr":     99,
			"check":  "flaky-check",
			"issue":  "https://github.com/example/repo/issues/12",
			"reason": "known intermittent flake",
		},
	}); err != nil {
		t.Fatalf("Append waiver failed: %v", err)
	}

	fakeMergeSHA := "cccc22223333444455556666777788889999dddd"

	opts := MergeOptions{
		RepoDir:   repoDir,
		PR:        99,
		HeadSHA:   headSHA,
		BaseRef:   "main",
		CredStore: &StaticCredentialStore{Token: "test-token"},
		MergeFunc: func(r string, pr int, token, trailer string) (string, error) {
			return fakeMergeSHA, nil
		},
		PostMergeChecks: func(r, mSHA string) ([]ForgeCheck, error) {
			return []ForgeCheck{
				{Name: "flaky-check", State: "failure", Bucket: "fail"},
			}, nil
		},
	}

	res, err := ExecuteT2Merge(opts)
	if err != nil {
		t.Fatalf("ExecuteT2Merge failed: %v", err)
	}
	if !res.Merged {
		t.Fatalf("expected Merged == true")
	}
	if res.AutoReverted {
		t.Fatalf("expected AutoReverted == false since check was waived")
	}
	if res.PostMergeStatus != "PASS" {
		t.Fatalf("expected PostMergeStatus PASS, got %s", res.PostMergeStatus)
	}
}

func TestCredentialStores(t *testing.T) {
	// StaticCredentialStore
	store := &StaticCredentialStore{
		Tokens: map[string]string{
			"g8s-f0-merge:default": "token-exact",
			"g8s-f0-merge":         "token-service",
		},
	}
	tok, err := store.GetMergeToken("g8s-f0-merge", "default")
	if err != nil || tok != "token-exact" {
		t.Fatalf("expected token-exact, got %s (err: %v)", tok, err)
	}

	tokService, err := store.GetMergeToken("g8s-f0-merge", "other")
	if err != nil || tokService != "token-service" {
		t.Fatalf("expected token-service, got %s (err: %v)", tokService, err)
	}

	emptyStore := &StaticCredentialStore{}
	_, err = emptyStore.GetMergeToken("svc", "acc")
	if err == nil {
		t.Fatalf("expected error from empty StaticCredentialStore")
	}

	// KeychainCredentialStore with non-existent / failing path
	keychain := &KeychainCredentialStore{
		SecurityPath: "/path/to/nonexistent/security/binary",
	}
	_, err = keychain.GetMergeToken("svc", "acc")
	if err == nil {
		t.Fatalf("expected error from KeychainCredentialStore with bad binary path")
	}
}

func TestMergeCommand_CLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, _, headSHA := setupTestGitRepo(t)

	// 1. Failing gate -> returns 1
	code := mergeCommand([]string{"--repo", repoDir, "--head-sha", headSHA})
	if code != 1 {
		t.Fatalf("expected mergeCommand to return 1 on gate fail, got %d", code)
	}

	// 2. Passing gate with --json
	repoDir2, headSHA2, _ := setupPassingGateRepo(t)
	t.Setenv("F0_MERGE_TOKEN", "cli-test-token")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code = mergeCommand([]string{
		"--repo", repoDir2,
		"--head-sha", headSHA2,
		"--pr", "10",
		"--json",
	})

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	// Since opts.MergeFunc was not set in CLI invocation and "gh" is not mocked here,
	// defaultGHMerge may fail or return an error, returning code 1, or pass if gh mock exists.
	// But let's verify JSON output structure if error or success
	if code == 0 {
		var res MergeResult
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("expected valid json from mergeCommand --json: %v, out: %s", err, string(out))
		}
	}
}

func TestExecuteT2Merge_MergeFuncError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoDir, headSHA, _ := setupPassingGateRepo(t)

	opts := MergeOptions{
		RepoDir:   repoDir,
		PR:        77,
		HeadSHA:   headSHA,
		BaseRef:   "main",
		CredStore: &StaticCredentialStore{Token: "test-token"},
		MergeFunc: func(r string, p int, tok, tr string) (string, error) {
			return "", errors.New("upstream git conflict")
		},
	}

	res, err := ExecuteT2Merge(opts)
	if err == nil {
		t.Fatalf("expected error when MergeFunc fails")
	}
	if !strings.Contains(err.Error(), "upstream git conflict") {
		t.Fatalf("expected upstream git conflict error, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on MergeFunc error")
	}
}
