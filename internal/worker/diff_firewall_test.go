package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/receipt"
)

// Seed the same ledger used by the control plane's receipt consumer.
func issueDiffFirewallTestReceipt(t *testing.T, env *workerEnv) string {
	t.Helper()
	dir := filepath.Dir(env.runDir)
	t.Setenv("G8S_STATE_DIR", dir)
	mgr, err := receipt.NewReceiptManager(filepath.Join(dir, "receipts.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	rc, err := mgr.IssueReceipt("brain", []string{"src/**"}, time.Minute,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI: "g8s://envelope/write-receipt/v1", FieldOrder: []string{"receipt_id", "issuer", "allowed_paths"}, RequiredFields: []string{"receipt_id"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{RulesetVersion: "v1", PipelineDigest: "deadbeef", ADRRef: "docs/adr/0001.md"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return rc.ReceiptID
}

func TestDiffFirewallCollectReceiptFailure(t *testing.T) {
	for _, state := range []string{"empty ID", "unknown ID", "missing git", "valid"} {
		t.Run(state, func(t *testing.T) {
			dir := initGitRepoForDiffFirewall(t)
			if state == "missing git" {
				dir = t.TempDir()
			}
			iso := &fakeWorktreeIsolator{dir: dir}
			env := newIsolationEnv(t, iso)
			id := issueDiffFirewallTestReceipt(t, env)
			wantCode := "E_DIFF_FIREWALL_RECEIPT_LOOKUP"
			switch state {
			case "empty ID":
				id = ""
			case "unknown ID":
				id = "unknown-id"
			case "missing git":
				wantCode = "E_DIFF_FIREWALL_UNVERIFIABLE"
			}
			task := submitTask(t, env, "firewall-collect", 1, map[string]any{"permission": "workspace_write", "receipt_id": id})
			if _, err := env.sup.RunOnce(context.Background(), RunOptions{WorkerID: "firewall", LeaseSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			got, err := env.store.GetTask(context.Background(), task.TaskID)
			if err != nil || got == nil {
				t.Fatalf("get task: %v", err)
			}
			var result workerResult
			if err := json.Unmarshal(got.Result, &result); err != nil {
				t.Fatal(err)
			}
			if state == "valid" {
				if !result.OK || !iso.keepFlag {
					t.Fatalf("valid scope rejected: %+v", result)
				}
			} else if result.OK || result.Status != "contract_violation" || !strings.HasPrefix(result.Reason, wantCode) || iso.keepFlag {
				t.Fatalf("expected %s and discarded worktree, got %+v keep=%v", wantCode, result, iso.keepFlag)
			}
		})
	}
}

func initGitRepoForDiffFirewall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	runGit("init")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	return dir
}

func TestDiffFirewallFailLoud(t *testing.T) {
	for _, permission := range []string{"workspace_write", "read_only", "automation_read", "supplied_text"} {
		for _, state := range []string{"empty dir", "missing git", "broken git", "clean", "unscoped mutation"} {
			t.Run(permission+"/"+state, func(t *testing.T) {
				var dir string
				var wantCode string
				var wantTouched []string
				switch state {
				case "empty dir":
					if permission == "workspace_write" {
						wantCode = "E_DIFF_FIREWALL_UNVERIFIABLE"
					}
				case "missing git":
					dir = t.TempDir()
					wantCode = "E_DIFF_FIREWALL_UNVERIFIABLE"
				default:
					dir = initGitRepoForDiffFirewall(t)
					switch state {
					case "broken git":
						if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("invalid HEAD\n"), 0o644); err != nil {
							t.Fatal(err)
						}
						wantCode = "E_DIFF_FIREWALL_UNVERIFIABLE: git status failed"
					case "unscoped mutation":
						if err := os.WriteFile(filepath.Join(dir, "changed.txt"), []byte("change\n"), 0o644); err != nil {
							t.Fatal(err)
						}
						wantTouched = []string{"changed.txt"}
						wantCode = "E_DIFF_FIREWALL_READ_ONLY_MUTATION"
						if permission == "workspace_write" {
							wantCode = "E_DIFF_FIREWALL_UNSCOPED: 1 files modified but no allowed-paths scope available"
						}
					}
				}
				touched, err := VerifyWorktreeDiffScope(dir, permission, nil)
				if wantCode == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
				} else if err == nil || !strings.HasPrefix(err.Error(), wantCode) {
					t.Fatalf("want %s, got %v", wantCode, err)
				}
				if !reflect.DeepEqual(touched, wantTouched) {
					t.Fatalf("touched = %v, want %v", touched, wantTouched)
				}
			})
		}
	}
}

func TestResolveReceiptAllowedPaths(t *testing.T) {
	for _, state := range []string{"empty ID", "missing database", "invalid database", "unknown ID", "valid", "consumed"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("G8S_STATE_DIR", dir)
			dbPath := filepath.Join(dir, "receipts.db")
			id := "unknown-id"
			wantPaths := []string{"src/**", "tests/**"}
			switch state {
			case "empty ID":
				id = " "
			case "missing database":
			case "invalid database":
				if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644); err != nil {
					t.Fatal(err)
				}
			default:
				mgr, err := receipt.NewReceiptManager(dbPath, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = mgr.Close() })
				if state != "unknown ID" {
					rc, err := mgr.IssueReceipt("brain", wantPaths, time.Minute,
						receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
							SchemaURI: "g8s://envelope/write-receipt/v1", FieldOrder: []string{"receipt_id", "issuer", "allowed_paths"}, RequiredFields: []string{"receipt_id"},
						}),
						receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{RulesetVersion: "v1", PipelineDigest: "deadbeef", ADRRef: "docs/adr/0001.md"}),
					)
					if err != nil {
						t.Fatal(err)
					}
					id = rc.ReceiptID
					if state == "consumed" {
						if _, err := mgr.ValidateAndConsume(id, "test-task"); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			paths, err := resolveReceiptAllowedPaths(id)
			if state == "valid" || state == "consumed" {
				if err != nil || !reflect.DeepEqual(paths, wantPaths) {
					t.Fatalf("paths = %v, err = %v; want %v", paths, err, wantPaths)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), "E_DIFF_FIREWALL_RECEIPT_LOOKUP:") || paths != nil {
				t.Fatalf("expected lookup error and nil paths, got paths=%v err=%v", paths, err)
			}
		})
	}
}

func TestWorktreeDiffFirewallScopeEnforcement(t *testing.T) {
	t.Run("default CLI templates resolve for opencode, commandcode, qwen, codex, cursor", func(t *testing.T) {
		resolver := NewProviderCommandResolver(ProviderResolverOptions{
			PlatformDispatchNames: []string{"opencode", "commandcode", "qwen", "codex", "cursor"},
		})

		argv, err := resolver("scan repo", "opencode", "opencode/free", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"opencode", "run", "--model", "opencode/free", "scan repo"}
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("got %v, want %v", argv, want)
		}

		argvNoModel, err := resolver("scan repo", "commandcode", "", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantNoModel := []string{"commandcode", "--no-session", "--skip-onboarding", "--no-auto-update", "--yolo", "-p", "scan repo"}
		if !reflect.DeepEqual(argvNoModel, wantNoModel) {
			t.Fatalf("got %v, want %v", argvNoModel, wantNoModel)
		}

		argvQwen, err := resolver("scan repo", "qwen", "docs-retriever", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantQwen := []string{"qwen", "--yolo", "--model", "docs-retriever", "-p", "scan repo"}
		if !reflect.DeepEqual(argvQwen, wantQwen) {
			t.Fatalf("got %v, want %v", argvQwen, wantQwen)
		}

		argvCursor, err := resolver("scan repo", "cursor", "composer-2.5", "30s")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantCursor := []string{"cursor-agent", "--print", "--trust", "--yolo", "--model", "composer-2.5", "scan repo"}
		if !reflect.DeepEqual(argvCursor, wantCursor) {
			t.Fatalf("got %v, want %v", argvCursor, wantCursor)
		}
	})

	t.Run("in-scope mutation under workspace_write passes and ignores CLI runtime dirs", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n// updated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, metaDir := range []string{".omo", ".opencode", ".commandcode", ".qwen", ".cursor"} {
			if err := os.MkdirAll(filepath.Join(dir, metaDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, metaDir, "session.json"), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"})
		if err != nil || len(violations) != 0 {
			t.Fatalf("expected nil error and 0 violations, got violations=%v err=%v", violations, err)
		}
	})

	t.Run("mutation under read_only fails closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "src", "allowed.go"), []byte("package src\n// sneaky edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "read_only", nil)
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_READ_ONLY_MUTATION") {
			t.Fatalf("expected E_DIFF_FIREWALL_READ_ONLY_MUTATION, got violations=%v err=%v", violations, err)
		}
	})

	t.Run("out-of-scope mutation under workspace_write fails closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# unauthorized\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"})
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected E_DIFF_FIREWALL_OUT_OF_SCOPE, got violations=%v err=%v", violations, err)
		}
		if len(violations) != 1 || violations[0] != "README.md" {
			t.Fatalf("unexpected violations: %v", violations)
		}
	})
}

func runDiffFirewallGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, string(out))
	}
}

func TestDiffFirewall_Issue935_Hardening(t *testing.T) {
	t.Run("VectorA_non_claim_git_mv_out_of_scope_fails_closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "allowed"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "secret", "key.txt"), []byte("topsecret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "allowed", "ok.txt"), []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runDiffFirewallGit(t, dir, "add", ".")
		runDiffFirewallGit(t, dir, "commit", "-m", "seed secret and allowed")

		runDiffFirewallGit(t, dir, "mv", "secret/key.txt", "allowed/moved.txt")

		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"allowed/**"})
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected E_DIFF_FIREWALL_OUT_OF_SCOPE on non-claim rename from secret/key.txt, got violations=%v err=%v", violations, err)
		}
		foundSource := false
		for _, v := range violations {
			if v == "secret/key.txt" {
				foundSource = true
			}
		}
		if !foundSource {
			t.Fatalf("expected violations %v to include rename source secret/key.txt", violations)
		}
	})

	t.Run("VectorB_g8s_and_prefix_overmatch_never_exempted", func(t *testing.T) {
		for _, target := range []string{
			".g8s/trust-boundaries.yml",
			".g8s/policy.yml",
			".g8s-extra/evil.txt",
			".cursorrules",
		} {
			t.Run(target, func(t *testing.T) {
				dir := initGitRepoForDiffFirewall(t)
				full := filepath.Join(dir, filepath.FromSlash(target))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("tampered\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"allowed/**"})
				if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
					t.Fatalf("expected E_DIFF_FIREWALL_OUT_OF_SCOPE for %s, got violations=%v err=%v", target, violations, err)
				}
			})
		}
	})

	t.Run("VectorC_tracked_CLI_runtime_dir_fails_untracked_passes_only_without_claim", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".cursor", "config.json"), []byte(`{"v":1}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runDiffFirewallGit(t, dir, "add", ".cursor/config.json")
		runDiffFirewallGit(t, dir, "commit", "-m", "track .cursor/config.json")

		// Untracked session file in .cursor/ passes when claim == nil...
		if err := os.WriteFile(filepath.Join(dir, ".cursor", "session.json"), []byte(`{}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}); err != nil || len(violations) != 0 {
			t.Fatalf("expected untracked .cursor/session.json allowed without claim, got violations=%v err=%v", violations, err)
		}
		// ...but fails closed when claim != nil.
		claim := &receipt.ClaimBinding{
			TaskID:          "task-c1",
			NodeID:          "node-1",
			Incarnation:     "inc-1",
			Resource:        "repo",
			ContextRevision: 1,
			Paths:           []string{"src/allowed.go"},
			ExpiresAt:       time.Now().Add(time.Hour),
		}
		if _, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}, claim); err == nil {
			t.Fatalf("expected untracked .cursor/session.json rejected under ClaimBinding")
		}
		// Remove untracked session file, modify tracked .cursor/config.json -> MUST fail even when claim == nil!
		if err := os.Remove(filepath.Join(dir, ".cursor", "session.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".cursor", "config.json"), []byte(`{"v":2}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}); err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected tracked .cursor/config.json modification to fail closed, got violations=%v err=%v", violations, err)
		}
	})

	t.Run("VectorD_special_filenames_with_spaces_and_utf8_under_z_parser", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.MkdirAll(filepath.Join(dir, "allowed"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
			t.Fatal(err)
		}
		allowedSpace := filepath.Join(dir, "allowed", "hello world.txt")
		if err := os.WriteFile(allowedSpace, []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"allowed/**"}); err != nil || len(violations) != 0 {
			t.Fatalf("expected spaced in-scope path to pass, got violations=%v err=%v", violations, err)
		}
		secretSpace := filepath.Join(dir, "secret", "my secret.txt")
		if err := os.WriteFile(secretSpace, []byte("leak\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		secretUTF8 := filepath.Join(dir, "secret", "bí mật.txt")
		if err := os.WriteFile(secretUTF8, []byte("leak\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"allowed/**"})
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected E_DIFF_FIREWALL_OUT_OF_SCOPE for spaced/UTF-8 paths, got violations=%v err=%v", violations, err)
		}
		if !reflect.DeepEqual(violations, []string{"secret/bí mật.txt", "secret/my secret.txt"}) {
			t.Fatalf("expected unescaped UTF-8 and spaced paths, got %v", violations)
		}
	})

	t.Run("VectorE_read_only_missing_git_fails_closed", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyWorktreeDiffScope(dir, "read_only", nil); err == nil || !strings.HasPrefix(err.Error(), "E_DIFF_FIREWALL_UNVERIFIABLE") {
			t.Fatalf("expected E_DIFF_FIREWALL_UNVERIFIABLE when read_only worktree loses .git, got %v", err)
		}
	})

	t.Run("VectorF_gitignore_delta_detects_new_and_modified_ignored_files", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\nbuild/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runDiffFirewallGit(t, dir, "add", ".gitignore")
		runDiffFirewallGit(t, dir, "commit", "-m", "add gitignore")

		if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
			t.Fatal(err)
		}
		prePath := filepath.Join(dir, "build", "preexisting.bin")
		if err := os.WriteFile(prePath, []byte("seed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		baseline, err := SnapshotIgnoredFiles(dir)
		if err != nil {
			t.Fatalf("SnapshotIgnoredFiles: %v", err)
		}

		// Untouched pre-existing ignored artifact passes under baseline.
		if violations, err := VerifyWorktreeDiffScopeWithOptions(dir, "read_only", nil, DiffFirewallOptions{
			BaselineIgnored: baseline,
			CheckIgnored:    true,
		}); err != nil || len(violations) != 0 {
			t.Fatalf("expected untouched baseline ignored file to pass, got violations=%v err=%v", violations, err)
		}

		// Modifying pre-existing ignored file with same byte length must STILL fail closed!
		if err := os.WriteFile(prePath, []byte("pwn!\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if violations, err := VerifyWorktreeDiffScopeWithOptions(dir, "read_only", nil, DiffFirewallOptions{
			BaselineIgnored: baseline,
			CheckIgnored:    true,
		}); err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_READ_ONLY_MUTATION") {
			t.Fatalf("expected modified pre-existing ignored file to fail read_only, got violations=%v err=%v", violations, err)
		}

		// Restore untouched baseline and test newly created .env + build/exfil.txt under workspace_write.
		if err := os.WriteFile(prePath, []byte("seed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		baseline2, err := SnapshotIgnoredFiles(dir)
		if err != nil {
			t.Fatalf("SnapshotIgnoredFiles 2: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "build", "exfil.txt"), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		violations, err := VerifyWorktreeDiffScopeWithOptions(dir, "workspace_write", []string{"src/**"}, DiffFirewallOptions{
			BaselineIgnored: baseline2,
			CheckIgnored:    true,
		})
		if err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_OUT_OF_SCOPE") {
			t.Fatalf("expected new ignored files to fail workspace_write, got violations=%v err=%v", violations, err)
		}
		if !reflect.DeepEqual(violations, []string{".env", "build/exfil.txt"}) {
			t.Fatalf("expected [.env build/exfil.txt], got %v", violations)
		}
	})

	t.Run("VectorG_adr0041_git_requests_marker_scoping", func(t *testing.T) {
		dir := initGitRepoForDiffFirewall(t)
		reqFile := filepath.Join(dir, gitCommitRequestsDir, "req-1.json")
		if err := os.MkdirAll(filepath.Dir(reqFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reqFile, []byte(`{"v":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// Allowed untracked during non-claim workspace_write attempt.
		if violations, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}); err != nil || len(violations) != 0 {
			t.Fatalf("expected .g8s-git-requests marker allowed under workspace_write without claim, got violations=%v err=%v", violations, err)
		}
		// Disallowed under read_only.
		if _, err := VerifyWorktreeDiffScope(dir, "read_only", nil); err == nil || !strings.Contains(err.Error(), "E_DIFF_FIREWALL_READ_ONLY_MUTATION") {
			t.Fatalf("expected .g8s-git-requests rejected under read_only, got %v", err)
		}
		// Disallowed when ClaimBinding is active.
		claim := &receipt.ClaimBinding{
			TaskID:          "task-g1",
			NodeID:          "node-1",
			Incarnation:     "inc-1",
			Resource:        "repo",
			ContextRevision: 1,
			Paths:           []string{"src/allowed.go"},
			ExpiresAt:       time.Now().Add(time.Hour),
		}
		if _, err := VerifyWorktreeDiffScope(dir, "workspace_write", []string{"src/**"}, claim); err == nil {
			t.Fatalf("expected .g8s-git-requests rejected when ClaimBinding is active")
		}
	})
}
