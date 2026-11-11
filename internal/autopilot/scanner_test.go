package autopilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanStaticAnalysis_InjectedRunner(t *testing.T) {
	pq := NewPriorityQueue()
	cfg := DefaultConfig()
	cfg.CodebasePath = t.TempDir()
	cfg.MinScoreThreshold = 0.01
	cfg.ScanSources = ScanSources{StaticAnalysis: true}

	s, err := NewScanner(cfg, pq)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	s.staticAnalysisRunner = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("pkg/foo.go:12:3: undefined: bar\n"), nil
	}
	if err := s.scanStaticAnalysis(context.Background()); err != nil {
		t.Fatalf("scanStaticAnalysis: %v", err)
	}
	if pq.Len() != 1 {
		t.Fatalf("expected 1 queued item, got %d", pq.Len())
	}
	item := pq.Peek()
	if item == nil || item.Source != SourceStaticAnalysis {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.LineNumber != 12 || item.Description != "undefined: bar" {
		t.Fatalf("unexpected parsed item: %+v", item)
	}
}

func TestScanStaticAnalysis_SkipsWithoutGoMod(t *testing.T) {
	pq := NewPriorityQueue()
	cfg := DefaultConfig()
	cfg.CodebasePath = t.TempDir()
	cfg.ScanSources = ScanSources{StaticAnalysis: true}
	s, err := NewScanner(cfg, pq)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	if err := s.scanStaticAnalysis(context.Background()); err != nil {
		t.Fatalf("scanStaticAnalysis: %v", err)
	}
	if pq.Len() != 0 {
		t.Fatalf("expected empty queue, got %d", pq.Len())
	}
}

func TestScanStaticAnalysis_WithGoModDefaultRunner(t *testing.T) {
	tmp := t.TempDir()
	mod := filepath.Join(tmp, "go.mod")
	if err := os.WriteFile(mod, []byte("module example.com/autopilottest\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	pkgDir := filepath.Join(tmp, "pkg")
	if err := os.Mkdir(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badGo := filepath.Join(pkgDir, "bad.go")
	src := "package pkg\n\nvar _ = fmt.Println\n"
	if err := os.WriteFile(badGo, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	pq := NewPriorityQueue()
	cfg := DefaultConfig()
	cfg.CodebasePath = tmp
	cfg.MinScoreThreshold = 0.01
	cfg.ScanSources = ScanSources{StaticAnalysis: true}
	s, err := NewScanner(cfg, pq)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	if err := s.scanStaticAnalysis(context.Background()); err != nil {
		t.Fatalf("scanStaticAnalysis: %v", err)
	}
	if pq.Len() == 0 {
		t.Fatalf("expected vet findings enqueued, queue len %d", pq.Len())
	}
	top := pq.Peek()
	if top == nil {
		t.Fatal("nil peek")
	}
	if top.Source != SourceStaticAnalysis {
		t.Fatalf("source=%s", top.Source)
	}
	_ = fmt.Sprintf("%d", top.LineNumber)
}
