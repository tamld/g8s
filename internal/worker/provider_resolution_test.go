package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProviderResolution(t *testing.T) {
	opts := ProviderResolverOptions{
		ProviderTemplates: map[string][]string{
			"codex": {"codex-bin", "run", "--prompt", "{prompt}", "--model", "{model}", "--timeout", "{timeout}"},
		},
		ModelTemplates: map[string][]string{
			"custom-model": {"model-bin", "{prompt}", "{model}", "{timeout}"},
		},
		PlatformDispatchNames: []string{"agy", "claude", "codex"},
		APICallNames: map[string]bool{
			"api-llm": true,
		},
	}

	resolver := NewProviderCommandResolver(opts)

	tests := []struct {
		name        string
		prompt      string
		provider    string
		model       string
		timeout     string
		wantArgv    []string
		wantDefault bool
		wantErrPart string
	}{
		{
			name:        "registered-without-template returns default argv and no error",
			prompt:      "test prompt",
			provider:    "agy",
			model:       "gemini-flash",
			timeout:     "30s",
			wantDefault: true,
		},
		{
			name:     "registered-WITH-template returns template argv",
			prompt:   "analyze code",
			provider: "codex",
			model:    "gpt-5",
			timeout:  "45s",
			wantArgv: []string{"codex-bin", "run", "--prompt", "analyze code", "--model", "gpt-5", "--timeout", "45s"},
		},
		{
			name:        "unknown provider returns error listing available names",
			prompt:      "test prompt",
			provider:    "ghost",
			model:       "m1",
			timeout:     "30s",
			wantErrPart: "provider \"ghost\" not found among platform_dispatch entries (available: agy, claude, codex)",
		},
		{
			name:        "api_call provider returns api_call rejection error",
			prompt:      "test prompt",
			provider:    "api-llm",
			model:       "m1",
			timeout:     "30s",
			wantErrPart: "provider \"api-llm\" is an api_call provider and is not executable on the queue path",
		},
		{
			name:     "empty provider + known model returns model template",
			prompt:   "legacy prompt",
			provider: "",
			model:    "custom-model",
			timeout:  "20s",
			wantArgv: []string{"model-bin", "legacy prompt", "custom-model", "20s"},
		},
		{
			name:        "empty provider + unknown model returns default agy argv",
			prompt:      "legacy prompt",
			provider:    "",
			model:       "unknown-model",
			timeout:     "20s",
			wantDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argv, err := resolver(tt.prompt, tt.provider, tt.model, tt.timeout)
			if tt.wantErrPart != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrPart)
				}
				if !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErrPart, err.Error())
				}
				if argv != nil {
					t.Fatalf("expected nil argv on error, got %v", argv)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantDefault {
				if argv != nil {
					t.Fatalf("expected default agy argv (nil), got %v", argv)
				}
				return
			}
			if !reflect.DeepEqual(argv, tt.wantArgv) {
				t.Fatalf("got argv %v, want %v", argv, tt.wantArgv)
			}
		})
	}
}

func TestSupervisorRegisteredWithoutTemplateFallback(t *testing.T) {
	ctx := context.Background()
	env := newWorkerEnv(t, nil)
	resolverOpts := ProviderResolverOptions{
		PlatformDispatchNames: []string{"agy", "claude"},
	}
	sup := NewSupervisor(env.store, env.runDir,
		WithRunner(env.runner),
		WithPollInterval(2*time.Millisecond),
		WithProviderCommandResolver(NewProviderCommandResolver(resolverOpts)),
	)

	var capturedArgv []string
	done := make(chan struct{})
	close(done)
	env.runner.factory = func(opts SpawnOptions) Child {
		capturedArgv = opts.Argv
		return &instantChild{done: done}
	}

	submitTask(t, env, "task-agy-fallback", 1, map[string]any{
		"provider": "agy",
		"model":    "gemini-3.8-flash-high",
		"prompt":   "run default agy",
	})

	task, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-agy", LeaseSeconds: 10})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if task == nil {
		t.Fatal("expected task, got nil")
	}

	foundAGY := false
	for _, arg := range capturedArgv {
		if arg == "agy" {
			foundAGY = true
		}
	}
	if !foundAGY {
		t.Fatalf("expected default agy in argv, got: %v", capturedArgv)
	}
}
