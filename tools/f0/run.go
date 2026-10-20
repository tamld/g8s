package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func runCommand(args []string) int {
	var cmdArgs []string
	if len(args) > 0 && args[0] == "--" {
		cmdArgs = args[1:]
	} else {
		dashDash := -1
		for i, a := range args {
			if a == "--" {
				dashDash = i
				break
			}
		}
		if dashDash >= 0 {
			cmdArgs = args[dashDash+1:]
		} else {
			cmdArgs = args
		}
	}

	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "usage: f0 run -- <cmd...>\n")
		return 1
	}

	kind := detectKind(cmdArgs)
	execArgs := rewriteArgv(kind, cmdArgs)

	repoRoot, branch, sha, dirty, dirtyHash := getGitState(".")

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	var combinedBuf bytes.Buffer
	var combinedMu sync.Mutex

	swOut := &syncWriter{direct: os.Stdout, buf: &stdoutBuf, combined: &combinedBuf, mu: &combinedMu}
	swErr := &syncWriter{direct: os.Stderr, buf: &stderrBuf, combined: &combinedBuf, mu: &combinedMu}

	cmd := exec.Command(execArgs[0], execArgs[1:]...)
	cmd.Stdout = swOut
	cmd.Stderr = swErr
	cmd.Stdin = os.Stdin

	start := time.Now()
	runErr := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	stdoutSHA := sha256Hex(stdoutBuf.Bytes())
	stderrSHA := sha256Hex(stderrBuf.Bytes())

	combinedBytes := combinedBuf.Bytes()
	var tailBytes []byte
	if len(combinedBytes) > 1024 {
		tailBytes = combinedBytes[len(combinedBytes)-1024:]
	} else {
		tailBytes = combinedBytes
	}
	tail := redactSecrets(string(tailBytes))

	extra := make(map[string]any)
	items := -1

	switch kind {
	case "go-test":
		items = parseGoTest(stdoutBuf.Bytes())
	case "node-test", "npm-test":
		items = parseNodeTest(combinedBuf.String())
	case "cargo-test":
		items = parseCargoTest(combinedBuf.String())
	case "did-verify":
		items = parseDidVerify(execArgs)
	case "g8s-offer-check":
		items = parseOfferCheck(combinedBuf.String())
	case "g8s-deliver":
		items = parseDeliver(execArgs, stdoutBuf.Bytes(), repoRoot, extra)
	case "g8s-verify":
		items = parseG8sVerify(stdoutBuf.Bytes())
	default:
		items = -1
	}

	if durationMS < 50 && items > 0 {
		extra["suspect_fast"] = true
	}

	var status string
	if exitCode != 0 {
		status = "FAIL"
	} else if items == 0 {
		status = "VACUOUS"
	} else if items > 0 {
		status = "PASS"
	} else {
		status = "UNKNOWN"
	}

	id := newEvidenceID()
	entry := &Entry{
		ID:           id,
		Ts:           time.Now().UTC().Format(time.RFC3339),
		Kind:         kind,
		Repo:         repoRoot,
		Branch:       branch,
		SHA:          sha,
		Dirty:        dirty,
		DirtyHash:    dirtyHash,
		Cmd:          execArgs,
		Exit:         exitCode,
		DurationMS:   durationMS,
		StdoutSHA256: stdoutSHA,
		StderrSHA256: stderrSHA,
		Tail:         tail,
		ItemsChecked: items,
		Status:       status,
		Extra:        extra,
	}
	if len(extra) == 0 {
		entry.Extra = nil
	}

	if ledger, err := OpenRepoLedger(repoRoot); err == nil {
		if appErr := ledger.Append(entry); appErr != nil {
			fmt.Fprintf(os.Stderr, "[f0] warning: failed to append evidence to ledger: %v\n", appErr)
		}
	}

	shortSHA := sha
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	if shortSHA == "" {
		shortSHA = "none"
	}
	dirtySuffix := ""
	if dirty {
		dirtySuffix = " DIRTY"
	}
	fmt.Fprintf(os.Stderr, "[f0] evidence %s %s items=%d sha=%s%s\n", entry.ID, entry.Status, entry.ItemsChecked, shortSHA, dirtySuffix)

	return exitCode
}

type syncWriter struct {
	direct   io.Writer
	buf      *bytes.Buffer
	combined *bytes.Buffer
	mu       *sync.Mutex
}

func (sw *syncWriter) Write(p []byte) (int, error) {
	if sw.mu != nil && sw.combined != nil {
		sw.mu.Lock()
		sw.combined.Write(p)
		sw.mu.Unlock()
	}
	if sw.buf != nil {
		sw.buf.Write(p)
	}
	if sw.direct != nil {
		return sw.direct.Write(p)
	}
	return len(p), nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func newEvidenceID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("ev-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "ev-" + hex.EncodeToString(b)
}

func detectKind(argv []string) string {
	if len(argv) == 0 {
		return "generic"
	}
	base0 := filepath.Base(argv[0])
	hasArg := func(val string) bool {
		for _, a := range argv {
			if a == val {
				return true
			}
		}
		return false
	}

	if (base0 == "go" || strings.HasSuffix(argv[0], "/go") || strings.HasSuffix(argv[0], `\go`)) && len(argv) >= 2 && argv[1] == "test" {
		return "go-test"
	}
	if strings.Contains(base0, "node") && hasArg("--test") {
		return "node-test"
	}
	if (base0 == "npm" || base0 == "pnpm" || base0 == "yarn" || base0 == "bun") && hasArg("test") {
		return "npm-test"
	}
	if strings.Contains(base0, "cargo") && len(argv) >= 2 && argv[1] == "test" {
		return "cargo-test"
	}
	if strings.Contains(base0, "did-verify") || strings.Contains(base0, "did_verify") || hasArg("did-verify") {
		return "did-verify"
	}
	if strings.Contains(base0, "g8s") {
		if hasArg("offer") && hasArg("check") {
			return "g8s-offer-check"
		}
		if hasArg("deliver") {
			return "g8s-deliver"
		}
		if hasArg("verify") {
			return "g8s-verify"
		}
	}
	if strings.Contains(base0, "offer-check") {
		return "g8s-offer-check"
	}

	return "generic"
}

func rewriteArgv(kind string, argv []string) []string {
	if kind == "go-test" {
		hasJSON := false
		for _, a := range argv {
			if a == "-json" || a == "--json" {
				hasJSON = true
				break
			}
		}
		if !hasJSON {
			fmt.Fprintln(os.Stderr, "[f0] injecting -json flag into go test invocation")
			testIdx := 1
			for i, a := range argv {
				if a == "test" {
					testIdx = i
					break
				}
			}
			newArgv := make([]string, 0, len(argv)+1)
			newArgv = append(newArgv, argv[:testIdx+1]...)
			newArgv = append(newArgv, "-json")
			newArgv = append(newArgv, argv[testIdx+1:]...)
			return newArgv
		}
	} else if kind == "g8s-deliver" {
		hasJSON := false
		for _, a := range argv {
			if a == "--json" || a == "-json" {
				hasJSON = true
				break
			}
		}
		if !hasJSON {
			deliverIdx := 1
			for i, a := range argv {
				if a == "deliver" {
					deliverIdx = i
					break
				}
			}
			newArgv := make([]string, 0, len(argv)+1)
			newArgv = append(newArgv, argv[:deliverIdx+1]...)
			newArgv = append(newArgv, "--json")
			newArgv = append(newArgv, argv[deliverIdx+1:]...)
			return newArgv
		}
	}
	return argv
}

func getGitState(dir string) (repoRoot, branch, sha string, dirty bool, dirtyHash string) {
	if dir == "" {
		dir = "."
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		repoRoot = strings.TrimSpace(string(out))
	} else {
		repoRoot, _ = filepath.Abs(dir)
	}

	out, err = exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		branch = strings.TrimSpace(string(out))
	}

	out, err = exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err == nil {
		sha = strings.TrimSpace(string(out))
	}

	statusOut, err := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if err == nil && len(bytes.TrimSpace(statusOut)) > 0 {
		dirty = true
		diffOut, _ := exec.Command("git", "-C", repoRoot, "diff", "HEAD").CombinedOutput()
		h := sha256.New()
		h.Write(statusOut)
		h.Write(diffOut)
		dirtyHash = hex.EncodeToString(h.Sum(nil))
	}

	return repoRoot, branch, sha, dirty, dirtyHash
}
