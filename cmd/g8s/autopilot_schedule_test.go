package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAutopilotSchedule_CrontabGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("crontab generation applies to posix platforms")
	}

	bin := "/usr/local/bin/g8s"
	line := generateCrontabLine(bin, 10*time.Minute)

	if !strings.Contains(line, "# g8s-autopilot") {
		t.Errorf("expected crontab line to contain marker # g8s-autopilot, got:\n%s", line)
	}
	if !strings.Contains(line, "*/10 * * * *") {
		t.Errorf("expected crontab line to contain interval */10 * * * *, got:\n%s", line)
	}
	if !strings.Contains(line, bin+" autopilot tick") {
		t.Errorf("expected crontab line to invoke %s autopilot tick, got:\n%s", bin, line)
	}

	// Hourly line
	hourly := generateCrontabLine(bin, 60*time.Minute)
	if !strings.Contains(hourly, "0 * * * *") {
		t.Errorf("expected hourly crontab line to contain 0 * * * *, got:\n%s", hourly)
	}
	if !strings.Contains(hourly, "# g8s-autopilot") {
		t.Errorf("expected hourly crontab line to contain marker, got:\n%s", hourly)
	}

	// Update existing crontab: idempotent replacement
	existing := "0 2 * * * /scripts/backup.sh\n*/5 * * * * /old/path/g8s autopilot tick # g8s-autopilot\n"
	updated := updateCrontabContent(existing, line)
	if strings.Count(updated, "# g8s-autopilot") != 1 {
		t.Errorf("expected exactly 1 g8s-autopilot marker in updated crontab, got %d:\n%s", strings.Count(updated, "# g8s-autopilot"), updated)
	}
	if !strings.Contains(updated, "/scripts/backup.sh") {
		t.Errorf("expected non-g8s crontab lines to be preserved:\n%s", updated)
	}
	if strings.Contains(updated, "/old/path/g8s") {
		t.Errorf("expected old g8s line to be replaced:\n%s", updated)
	}

	// Uninstall removes the artifact line
	uninstalled := updateCrontabContent(updated, "")
	if strings.Contains(uninstalled, "# g8s-autopilot") {
		t.Errorf("expected # g8s-autopilot to be completely removed upon uninstall:\n%s", uninstalled)
	}
	if !strings.Contains(uninstalled, "/scripts/backup.sh") {
		t.Errorf("expected /scripts/backup.sh to remain after g8s uninstall:\n%s", uninstalled)
	}
}

func TestAutopilotSchedule_LaunchAgentPlistGeneration(t *testing.T) {
	bin := "/opt/homebrew/bin/g8s"
	dur := 15 * time.Minute
	plist := generateLaunchAgentPlist(bin, dur)

	if !strings.Contains(plist, "<key>StartInterval</key>") {
		t.Errorf("expected plist to define StartInterval key")
	}
	if !strings.Contains(plist, "<integer>900</integer>") {
		t.Errorf("expected StartInterval to be 900 seconds (15m)")
	}
	if !strings.Contains(plist, "<string>"+bin+"</string>") {
		t.Errorf("expected plist to contain binary path %s", bin)
	}
	if !strings.Contains(plist, "<string>autopilot</string>") || !strings.Contains(plist, "<string>tick</string>") {
		t.Errorf("expected plist arguments to include autopilot tick")
	}
	if !strings.Contains(plist, "<string>g8s.autopilot</string>") {
		t.Errorf("expected plist label to be g8s.autopilot")
	}
}

func TestAutopilotSchedule_WindowsSchtasksGeneration(t *testing.T) {
	bin := `C:\Program Files\g8s\g8s.exe`
	dur := 20 * time.Minute

	createArgs := windowsSchtasksCreateArgs(bin, dur)
	joined := strings.Join(createArgs, " ")

	if !strings.Contains(joined, "/Create") || !strings.Contains(joined, "/F") {
		t.Errorf("expected schtasks /Create /F, got: %s", joined)
	}
	if !strings.Contains(joined, "/SC MINUTE") {
		t.Errorf("expected /SC MINUTE, got: %s", joined)
	}
	if !strings.Contains(joined, "/MO 20") {
		t.Errorf("expected /MO 20, got: %s", joined)
	}
	if !strings.Contains(joined, "/TN g8s-autopilot") {
		t.Errorf("expected /TN g8s-autopilot, got: %s", joined)
	}
	expectedTR := `"` + bin + `" autopilot tick`
	if !strings.Contains(joined, expectedTR) {
		t.Errorf("expected task run command %q in %s", expectedTR, joined)
	}

	deleteArgs := windowsSchtasksDeleteArgs()
	joinedDel := strings.Join(deleteArgs, " ")
	if !strings.Contains(joinedDel, "/Delete") || !strings.Contains(joinedDel, "/TN g8s-autopilot") {
		t.Errorf("expected schtasks /Delete /TN g8s-autopilot, got: %s", joinedDel)
	}

	if runtime.GOOS != "windows" {
		t.Skip("skipping live Windows schtasks execution on non-Windows host")
	}
}

func TestAutopilotSchedule_RefuseUnresolvableBinary(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "autopilot", "install-schedule", "--every", "10m")
	cmd.Env = append(cmd.Environ(),
		"G8S_STATE_DIR="+tempDir,
		"G8S_BIN_PATH=/does/not/exist/binary/g8s_test_485",
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected install-schedule to fail on unresolvable binary path, but succeeded:\n%s", string(out))
	}

	outStr := string(out)
	if !strings.Contains(outStr, "cannot resolve") && !strings.Contains(outStr, "does not exist") {
		t.Errorf("expected failure message regarding unresolvable binary, got:\n%s", outStr)
	}
}

func TestAutopilotSchedule_GenerateOnly(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "autopilot", "install-schedule", "--every", "10m", "--generate-only")
	cmd.Env = append(cmd.Environ(),
		"G8S_STATE_DIR="+tempDir,
		"G8S_BIN_PATH="+binPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate-only failed: %v\nOutput: %s", err, string(out))
	}

	outStr := string(out)
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(outStr, "<plist") || !strings.Contains(outStr, "StartInterval") || !strings.Contains(outStr, "600") {
			t.Errorf("expected macOS plist in generate-only output, got:\n%s", outStr)
		}
	case "windows":
		if !strings.Contains(outStr, "schtasks") || !strings.Contains(outStr, "/MO 10") {
			t.Errorf("expected Windows schtasks command in generate-only output, got:\n%s", outStr)
		}
	default: // Linux
		if !strings.Contains(outStr, "*/10 * * * *") || !strings.Contains(outStr, "# g8s-autopilot") {
			t.Errorf("expected Linux crontab line in generate-only output, got:\n%s", outStr)
		}
	}
}

func TestAutopilotSchedule_InstallUninstall_Artifacts(t *testing.T) {
	binPath := buildG8sBinary(t)

	switch runtime.GOOS {
	case "darwin":
		tempAgentsDir := t.TempDir()

		// 1. Install schedule (without calling real launchctl)
		cmdInstall := exec.Command(binPath, "autopilot", "install-schedule", "--every", "10m")
		cmdInstall.Env = append(cmdInstall.Environ(),
			"G8S_LAUNCH_AGENTS_DIR="+tempAgentsDir,
			"G8S_LAUNCH_AGENTS_NO_LOAD=1",
			"G8S_BIN_PATH="+binPath,
		)
		outInstall, err := cmdInstall.CombinedOutput()
		if err != nil {
			t.Fatalf("install-schedule failed: %v\nOutput: %s", err, string(outInstall))
		}

		plistPath := filepath.Join(tempAgentsDir, "g8s.autopilot.plist")
		plistBytes, err := os.ReadFile(plistPath)
		if err != nil {
			t.Fatalf("expected plist artifact to exist at %s: %v", plistPath, err)
		}
		if !strings.Contains(string(plistBytes), "<integer>600</integer>") {
			t.Errorf("expected plist to contain StartInterval 600")
		}
		if !strings.Contains(string(plistBytes), binPath) {
			t.Errorf("expected plist to contain binPath %s", binPath)
		}

		// 2. Uninstall schedule: removes plist artifact
		cmdUninstall := exec.Command(binPath, "autopilot", "uninstall-schedule")
		cmdUninstall.Env = append(cmdUninstall.Environ(),
			"G8S_LAUNCH_AGENTS_DIR="+tempAgentsDir,
			"G8S_LAUNCH_AGENTS_NO_LOAD=1",
		)
		outUninstall, err := cmdUninstall.CombinedOutput()
		if err != nil {
			t.Fatalf("uninstall-schedule failed: %v\nOutput: %s", err, string(outUninstall))
		}

		if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
			t.Errorf("expected plist artifact %s to be removed after uninstall", plistPath)
		}
	case "windows":
		t.Skip("skipping live Windows task install/uninstall without elevation in test")
	default:
		t.Logf("Linux crontab: covered by TestAutopilotSchedule_CrontabGeneration")
	}
}
