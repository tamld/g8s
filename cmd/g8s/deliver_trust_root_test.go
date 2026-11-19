package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/signing"
	_ "modernc.org/sqlite"
)

type deliverTrustRootEnv struct {
	binPath  string
	repoDir  string
	stateDir string
	dbPath   string
	rcDBPath string
	s0Pub    ed25519.PublicKey
	s0Priv   ed25519.PrivateKey
	s0PubHex string
}

func newDeliverTrustRootEnv(t *testing.T) deliverTrustRootEnv {
	t.Helper()
	binPath := buildG8sBinary(t)
	repoDir := initGitRepo(t)
	stateDir := t.TempDir()
	dbPath := filepath.Join(stateDir, "cp.db")
	rcDBPath := filepath.Join(stateDir, "receipts.db")

	s0Pub, s0Priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey s0: %v", err)
	}
	s0PubHex := hex.EncodeToString(s0Pub)
	if err := os.WriteFile(filepath.Join(stateDir, signing.S0PublicKeyFile), []byte(s0PubHex+"\n"), 0o600); err != nil {
		t.Fatalf("write s0-human.pub: %v", err)
	}

	return deliverTrustRootEnv{
		binPath:  binPath,
		repoDir:  repoDir,
		stateDir: stateDir,
		dbPath:   dbPath,
		rcDBPath: rcDBPath,
		s0Pub:    s0Pub,
		s0Priv:   s0Priv,
		s0PubHex: s0PubHex,
	}
}

func runDeliverHermetic(t *testing.T, env deliverTrustRootEnv, extraEnv []string, args ...string) deliverRunResult {
	t.Helper()
	cmdArgs := append([]string{"deliver"}, args...)
	cmd := exec.Command(env.binPath, cmdArgs...)
	cmd.Dir = env.repoDir

	scrub := map[string]bool{
		"G8S_DB":             true,
		"G8S_STATE_DIR":      true,
		"G8S_TIER":           true,
		"G8S_STRATUM":        true,
		"G8S_S0_PUBKEY_PATH": true,
	}
	var procEnv []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if scrub[strings.ToUpper(k)] {
			continue
		}
		procEnv = append(procEnv, kv)
	}
	procEnv = append(procEnv, "G8S_STATE_DIR="+env.stateDir, "G8S_DB="+env.dbPath)
	procEnv = append(procEnv, extraEnv...)
	cmd.Env = procEnv

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("exec deliver failed: %v", err)
		}
	}
	stdoutStr := strings.TrimSpace(outBuf.String())
	stderrStr := strings.TrimSpace(errBuf.String())
	var parsed testEnvelope
	if stdoutStr != "" && strings.HasPrefix(stdoutStr, "{") {
		_ = json.Unmarshal([]byte(stdoutStr), &parsed)
	}
	return deliverRunResult{
		exitCode: exitCode,
		stdout:   stdoutStr,
		stderr:   stderrStr,
		envelope: parsed,
	}
}

func (e deliverTrustRootEnv) writeWorktreeTCBFile(t *testing.T, wtDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(wtDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write worktree TCB file %s: %v", relPath, err)
	}
}

func (e deliverTrustRootEnv) readTaskResultBytes(t *testing.T, taskID string) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatalf("open cp db: %v", err)
	}
	defer db.Close()
	var res string
	if err := db.QueryRow("SELECT result_json FROM tasks WHERE task_id = ?", taskID).Scan(&res); err != nil {
		t.Fatalf("query task result_json: %v", err)
	}
	return []byte(res)
}

func (e deliverTrustRootEnv) setTaskStateAndResult(t *testing.T, taskID, state, resultJSON string) {
	t.Helper()
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatalf("open cp db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE tasks SET state = ?, result_json = ? WHERE task_id = ?", state, resultJSON, taskID); err != nil {
		t.Fatalf("update task state/result: %v", err)
	}
}

func (e deliverTrustRootEnv) writeApprovalFile(t *testing.T, att *harness.S0ApprovalAttestation) string {
	t.Helper()
	raw, err := json.MarshalIndent(att, "", "  ")
	if err != nil {
		t.Fatalf("marshal approval: %v", err)
	}
	p := filepath.Join(e.stateDir, "approval-"+att.Nonce+".json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("write approval file: %v", err)
	}
	return p
}

func (e deliverTrustRootEnv) buildSignedApproval(t *testing.T, wtDir, relPath string, resultBytes []byte, nonce string, priv ed25519.PrivateKey, sign bool) *harness.S0ApprovalAttestation {
	t.Helper()
	artDigest, err := harness.ComputeDeliverableArtifactDigest(wtDir, []string{relPath}, nil)
	if err != nil {
		t.Fatalf("ComputeDeliverableArtifactDigest: %v", err)
	}
	now := time.Now().UTC()
	att := &harness.S0ApprovalAttestation{
		ApprovedBy:                "human:tamld",
		PrincipalStratum:          "S0",
		Nonce:                     nonce,
		IssuedAt:                  now.Add(-2 * time.Minute).Format(time.RFC3339),
		ExpiresAt:                 now.Add(30 * time.Minute).Format(time.RFC3339),
		TargetPaths:               []string{relPath},
		DeterministicChecksPassed: true,
		ChecksDigest:              harness.ComputeChecksDigest(resultBytes),
		ArtifactDigest:            artDigest,
	}
	if sign {
		if err := harness.SignS0ApprovalAttestation(att, priv); err != nil {
			t.Fatalf("SignS0ApprovalAttestation: %v", err)
		}
	}
	return att
}

func TestDeliverTrustRoot_BlocksTCBWithoutS0Approval(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-no-approval")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// unauthorized\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)

	res := runDeliverHermetic(t, env, nil, taskID)
	if res.exitCode == 0 {
		t.Fatalf("expected TCB delivery without S0 approval to fail")
	}
	if !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootS0ApprovalRequired) {
		t.Fatalf("expected %s, got stderr=%q stdout=%q", harness.CodeTrustRootS0ApprovalRequired, res.stderr, res.stdout)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, filepath.FromSlash(target))); !os.IsNotExist(err) {
		t.Fatalf("target repo TCB file must not exist after refusal, err=%v", err)
	}
}

func TestDeliverTrustRoot_BlocksForgedUnsignedJSON(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-unsigned")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// unsigned\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)
	att := env.buildSignedApproval(t, wtDir, target, env.readTaskResultBytes(t, taskID), "nonce-deliver-unsigned-01", env.s0Priv, false)
	att.ApprovedBy = "alice"
	att.Signature = "ed25519:v1:deadbeef"
	attFile := env.writeApprovalFile(t, att)

	res := runDeliverHermetic(t, env, nil, taskID, "--s0-approval", attFile)
	if res.exitCode == 0 {
		t.Fatalf("expected unsigned/forged S0 approval to fail")
	}
	if !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootS0SignatureInvalid) {
		t.Fatalf("expected %s, got stderr=%q stdout=%q", harness.CodeTrustRootS0SignatureInvalid, res.stderr, res.stdout)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, filepath.FromSlash(target))); !os.IsNotExist(err) {
		t.Fatalf("target repo TCB file must not exist after forged refusal, err=%v", err)
	}
}

func TestDeliverTrustRoot_BlocksF0KeyForgingAndS0F0KeyReuse(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-f0-forge")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// f0 forged\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)

	// Load F0's private signing key created by receipt issuance.
	f0KeyBytes, err := os.ReadFile(filepath.Join(env.stateDir, signing.F0PrivateKeyFile))
	if err != nil {
		t.Fatalf("read f0-signing.key: %v", err)
	}
	rawPriv, err := hex.DecodeString(strings.TrimSpace(string(f0KeyBytes)))
	if err != nil || len(rawPriv) != ed25519.PrivateKeySize {
		t.Fatalf("decode f0-signing.key: %v (len=%d)", err, len(rawPriv))
	}
	f0Priv := ed25519.PrivateKey(rawPriv)

	att := env.buildSignedApproval(t, wtDir, target, env.readTaskResultBytes(t, taskID), "nonce-deliver-f0-forge-01", f0Priv, true)
	attFile := env.writeApprovalFile(t, att)

	// Subcase A: pinned s0-human.pub differs from F0 key -> signature invalid.
	res := runDeliverHermetic(t, env, nil, taskID, "--s0-approval", attFile)
	if res.exitCode == 0 || !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootS0SignatureInvalid) {
		t.Fatalf("expected %s when F0 key signs against S0 pubkey, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootS0SignatureInvalid, res.exitCode, res.stderr, res.stdout)
	}

	// Subcase B: attacker swaps s0-human.pub to equal f0-signing.pub -> key reuse forbidden.
	f0PubBytes, err := os.ReadFile(filepath.Join(env.stateDir, signing.F0PublicKeyFile))
	if err != nil {
		t.Fatalf("read f0-signing.pub: %v", err)
	}
	swappedPub := filepath.Join(env.stateDir, "swapped-f0.pub")
	if err := os.WriteFile(swappedPub, f0PubBytes, 0o600); err != nil {
		t.Fatalf("write swapped pub: %v", err)
	}
	resReuse := runDeliverHermetic(t, env, nil, taskID, "--s0-approval", attFile, "--s0-pubkey", swappedPub)
	if resReuse.exitCode == 0 || !strings.Contains(resReuse.stderr+resReuse.stdout, harness.CodeTrustRootS0KeyReuseForbidden) {
		t.Fatalf("expected %s when S0 key collides with F0 key, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootS0KeyReuseForbidden, resReuse.exitCode, resReuse.stderr, resReuse.stdout)
	}
}

func TestDeliverTrustRoot_BlocksPostApprovalOneByteTamper(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-tamper")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// approved v1\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)
	att := env.buildSignedApproval(t, wtDir, target, env.readTaskResultBytes(t, taskID), "nonce-deliver-tamper-001", env.s0Priv, true)
	attFile := env.writeApprovalFile(t, att)

	// Mutate 1 byte AFTER S0 signed.
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// approved v2\n")

	res := runDeliverHermetic(t, env, nil, taskID, "--s0-approval", attFile)
	if res.exitCode == 0 || !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootDigestMismatch) {
		t.Fatalf("expected %s on post-approval tamper, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootDigestMismatch, res.exitCode, res.stderr, res.stdout)
	}
}

func TestDeliverTrustRoot_BlocksExpiredAttestation(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-expired")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// expired\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)
	att := env.buildSignedApproval(t, wtDir, target, env.readTaskResultBytes(t, taskID), "nonce-deliver-expired-01", env.s0Priv, false)
	now := time.Now().UTC()
	att.IssuedAt = now.Add(-2 * time.Hour).Format(time.RFC3339)
	att.ExpiresAt = now.Add(-1 * time.Minute).Format(time.RFC3339)
	if err := harness.SignS0ApprovalAttestation(att, env.s0Priv); err != nil {
		t.Fatalf("sign expired att: %v", err)
	}
	attFile := env.writeApprovalFile(t, att)

	res := runDeliverHermetic(t, env, nil, taskID, "--s0-approval", attFile)
	if res.exitCode == 0 || !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootS0Expired) {
		t.Fatalf("expected %s on expired approval, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootS0Expired, res.exitCode, res.stderr, res.stdout)
	}
}

func TestDeliverTrustRoot_BlocksUnverifiedOverrideOnTCB(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-unverified")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// unverified\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)
	resultJSON := `{"status":"completed","deliverable":{"mode":"worktree","dir":"` + filepath.ToSlash(wtDir) + `"}}`
	env.setTaskStateAndResult(t, taskID, "WORKER_COMPLETED", resultJSON)

	att := env.buildSignedApproval(t, wtDir, target, []byte(resultJSON), "nonce-deliver-unverif-01", env.s0Priv, true)
	attFile := env.writeApprovalFile(t, att)

	res := runDeliverHermetic(t, env, nil, taskID, "--allow-unverified-override", "--override-reason", "urgent", "--s0-approval", attFile)
	if res.exitCode == 0 || !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootDeterministicChecksRequired) {
		t.Fatalf("expected %s blocking --allow-unverified-override on TCB, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootDeterministicChecksRequired, res.exitCode, res.stderr, res.stdout)
	}
}

func TestDeliverTrustRoot_BlocksF1WorkerEvenWithValidSignature(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wtDir := createWorktree(t, env.repoDir, "tcb-worker")
	const target = "internal/sandbox/policy_contract.go"
	env.writeWorktreeTCBFile(t, wtDir, target, "package sandbox\n// worker attempt\n")

	rcID := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	taskID := setupDeliverTask(t, env.dbPath, rcID, wtDir)
	att := env.buildSignedApproval(t, wtDir, target, env.readTaskResultBytes(t, taskID), "nonce-deliver-worker-001", env.s0Priv, true)
	attFile := env.writeApprovalFile(t, att)

	// G8S_TIER=F1 must override even a spoofed G8S_STRATUM=F0.
	res := runDeliverHermetic(t, env, []string{"G8S_TIER=F1", "G8S_STRATUM=F0"}, taskID, "--s0-approval", attFile)
	if res.exitCode == 0 || !strings.Contains(res.stderr+res.stdout, harness.CodeTrustRootWorkerTCBDenied) {
		t.Fatalf("expected %s blocking F1 worker on TCB, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootWorkerTCBDenied, res.exitCode, res.stderr, res.stdout)
	}
}

func TestDeliverTrustRoot_ValidOnceDryRunNonConsumingAndReplayBlocked(t *testing.T) {
	env := newDeliverTrustRootEnv(t)
	wt1 := createWorktree(t, env.repoDir, "tcb-valid-1")
	wt2 := createWorktree(t, env.repoDir, "tcb-valid-2")
	const target = "internal/sandbox/policy_contract.go"
	const payload = "package sandbox\n// signed valid S0 delivery\n"
	env.writeWorktreeTCBFile(t, wt1, target, payload)
	env.writeWorktreeTCBFile(t, wt2, target, payload)

	// Share identical deterministic result_json across both tasks and point explicit --worktree at wt1/wt2
	// so artifact + checks digests match cleanly and test genuine replay blocking on Task 2.
	const sharedChecksJSON = `{"status":"succeeded","checks_passed":true}`
	rc1 := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	task1 := setupDeliverTask(t, env.dbPath, rc1, wt1)
	env.setTaskStateAndResult(t, task1, "SUPERVISOR_ACCEPTED", sharedChecksJSON)

	rc2 := issueTestReceipt(t, env.rcDBPath, []string{"internal/sandbox/**"})
	task2 := setupDeliverTask(t, env.dbPath, rc2, wt2)
	env.setTaskStateAndResult(t, task2, "SUPERVISOR_ACCEPTED", sharedChecksJSON)

	att := env.buildSignedApproval(t, wt1, target, []byte(sharedChecksJSON), "nonce-deliver-valid-0001", env.s0Priv, true)
	attFile := env.writeApprovalFile(t, att)

	// 1. Dry-run succeeds and does not write target or burn nonce.
	dry := runDeliverHermetic(t, env, nil, task1, "--worktree", wt1, "--s0-approval", attFile, "--dry-run", "--json")
	if dry.exitCode != 0 {
		t.Fatalf("expected dry-run rc=0, got %d stderr=%q stdout=%q", dry.exitCode, dry.stderr, dry.stdout)
	}
	if _, err := os.Stat(filepath.Join(env.repoDir, filepath.FromSlash(target))); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write target file, err=%v", err)
	}

	// 2. Live deliver succeeds once and lands bytes on disk.
	live := runDeliverHermetic(t, env, nil, task1, "--worktree", wt1, "--s0-approval", attFile, "--json")
	if live.exitCode != 0 {
		t.Fatalf("expected live TCB deliver rc=0, got %d stderr=%q stdout=%q", live.exitCode, live.stderr, live.stdout)
	}
	gotBytes, err := os.ReadFile(filepath.Join(env.repoDir, filepath.FromSlash(target)))
	if err != nil || string(gotBytes) != payload {
		t.Fatalf("expected delivered payload on disk, err=%v got=%q", err, string(gotBytes))
	}

	// 3. Replaying exact same signed attestation against task2 fails with replay code.
	replay := runDeliverHermetic(t, env, nil, task2, "--worktree", wt2, "--s0-approval", attFile, "--json")
	if replay.exitCode == 0 || !strings.Contains(replay.stderr+replay.stdout, harness.CodeTrustRootS0ReplayDetected) {
		t.Fatalf("expected %s on replay of used S0 approval, got rc=%d stderr=%q stdout=%q",
			harness.CodeTrustRootS0ReplayDetected, replay.exitCode, replay.stderr, replay.stdout)
	}
}
