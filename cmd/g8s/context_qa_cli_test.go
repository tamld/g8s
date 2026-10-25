package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextPackAndQAReportCLI(t *testing.T) {
	t.Run("ContextPackCLI", func(t *testing.T) {
		tmp := t.TempDir()
		sampleGo := "package demo\n\n// Add sums two integers.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
		if err := os.WriteFile(filepath.Join(tmp, "demo.go"), []byte(sampleGo), 0o600); err != nil {
			t.Fatal(err)
		}

		outMD := filepath.Join(tmp, "packed.md")
		runContextPack([]string{"--root", tmp, "--budget", "500", "--focus", "Add", "--out", outMD, "--json"})
		data, err := os.ReadFile(outMD)
		if err != nil || !strings.Contains(string(data), "demo.go") {
			t.Fatalf("expected packed.md to contain demo.go, err=%v", err)
		}
	})

	t.Run("QAReportCLI", func(t *testing.T) {
		tmp := t.TempDir()
		specPath := filepath.Join(tmp, "spec.md")
		specContent := strings.Join([]string{
			"# QA Spec",
			"- US-01 / REQ-FUNC-01",
			"- AC-01: Given boundary 0 and empty input, When concurrent workers retry on timeout, Then FSM transitions QUEUED -> RUNNING -> COMPLETED and secrets are redacted.",
		}, "\n")
		if err := os.WriteFile(specPath, []byte(specContent), 0o600); err != nil {
			t.Fatal(err)
		}

		runQAReport([]string{
			"--spec", specPath,
			"--holdout-paths", "holdout/**",
			"--allowed-paths", "internal/**",
			"--ac-bindings", "AC-01=TestAdd",
			"--json",
		})
	})

	t.Run("SanitizeCLIExportErrorRegexHoisted", func(t *testing.T) {
		t.Parallel()
		cat, msg := sanitizeCLIExportError("write", "receipt.json", errors.New("failed writing /Users/alice/secret/dir/file.json"))
		if cat != "io_error" || !strings.Contains(msg, "[path]") || strings.Contains(msg, "/Users/alice") {
			t.Fatalf("unexpected sanitized output: cat=%s msg=%s", cat, msg)
		}
	})
}

func TestIssue639SecurityStandardsAndPackagingVerification(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	// S2: Verify OWASP Agentic / SLSA / SBOM security standards mapping document
	secDocPath := filepath.Join(repoRoot, "docs", "security", "owasp-agentic-slsa-sbom.md")
	secDoc, err := os.ReadFile(secDocPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", secDocPath, err)
	}
	for _, token := range []string{"LLM01", "LLM02", "LLM06", "LLM09", "LLM10", "SLSA", "SPDX"} {
		if !strings.Contains(string(secDoc), token) {
			t.Fatalf("expected %s to document %s", secDocPath, token)
		}
	}

	// S2 & S3: Verify .goreleaser.yaml includes SPDX SBOM generation and Linux package formats (.deb, .rpm, .apk)
	goreleaserPath := filepath.Join(repoRoot, ".goreleaser.yaml")
	goreleaserBytes, err := os.ReadFile(goreleaserPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", goreleaserPath, err)
	}
	for _, token := range []string{"sboms:", "spdx", "- deb", "- rpm", "- apk"} {
		if !strings.Contains(string(goreleaserBytes), token) {
			t.Fatalf("expected .goreleaser.yaml to contain %q", token)
		}
	}

	// S3: Verify scripts/install.sh enforces fail-closed SHA-256 verification
	installPath := filepath.Join(repoRoot, "scripts", "install.sh")
	installBytes, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", installPath, err)
	}
	if !strings.Contains(string(installBytes), "SHA-256 checksum verification failed") {
		t.Fatalf("expected scripts/install.sh to fail closed on SHA-256 checksum verification failure")
	}

	// S3: Verify Windows Winget & Chocolatey manifests and SVG diagrams exist
	for _, rel := range []string{
		"packaging/winget/manifest.yaml",
		"packaging/chocolatey/g8s.nuspec",
		"assets/architecture.svg",
		"assets/workflow.svg",
	} {
		full := filepath.Join(repoRoot, rel)
		if info, err := os.Stat(full); err != nil || info.Size() == 0 {
			t.Fatalf("expected non-empty artifact %s: err=%v", rel, err)
		}
	}
}
