package worker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProviderResolutionOrder(t *testing.T) {
	resolverOpts := ProviderResolverOptions{
		ProviderTemplates: map[string][]string{
			"codex": {"codex-bin", "run", "--prompt", "{prompt}", "--model", "{model}", "--timeout", "{timeout}"},
		},
		ModelTemplates: map[string][]string{
			"custom-model": {"model-bin", "{prompt}", "{model}", "{timeout}"},
		},
		PlatformDispatchNames: []string{"codex", "claude"},
		APICallNames: map[string]bool{
			"api-llm": true,
		},
	}
	resolver := NewProviderCommandResolver(resolverOpts)

	tests := []struct {
		name        string
		prompt      string
		provider    string
		model       string
		timeout     string
		wantArgv    []string
		wantErrPart string
		wantMiss    bool
	}{
		{
			name:     "provider codex hits template with placeholders substituted",
			prompt:   "analyze code",
			provider: "codex",
			model:    "gpt-5",
			timeout:  "45s",
			wantArgv: []string{"codex-bin", "run", "--prompt", "analyze code", "--model", "gpt-5", "--timeout", "45s"},
		},
		{
			name:        "provider ghost fails listing available names",
			prompt:      "test",
			provider:    "ghost",
			model:       "m1",
			timeout:     "30s",
			wantErrPart: "provider \"ghost\" not found among platform_dispatch entries (available: claude, codex)",
		},
		{
			name:        "api_call provider fails with distinct error",
			prompt:      "test",
			provider:    "api-llm",
			model:       "m1",
			timeout:     "30s",
			wantErrPart: "provider \"api-llm\" is an api_call provider and is not executable on the queue path",
		},
		{
			name:     "legacy task with matching model hits model template",
			prompt:   "legacy prompt",
			provider: "",
			model:    "custom-model",
			timeout:  "20s",
			wantArgv: []string{"model-bin", "legacy prompt", "custom-model", "20s"},
		},
		{
			name:     "legacy task with unknown model returns miss (nil, nil)",
			prompt:   "legacy prompt",
			provider: "",
			model:    "unknown-model",
			timeout:  "20s",
			wantMiss: true,
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
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantMiss {
				if argv != nil {
					t.Fatalf("expected miss (nil argv), got %v", argv)
				}
				return
			}
			if len(argv) != len(tt.wantArgv) {
				t.Fatalf("got argv %v, want %v", argv, tt.wantArgv)
			}
			for i := range argv {
				if argv[i] != tt.wantArgv[i] {
					t.Errorf("argv[%d] = %q, want %q", i, argv[i], tt.wantArgv[i])
				}
			}
		})
	}
}

func TestSupervisorProviderExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("provider codex hits template in supervisor", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		resolverOpts := ProviderResolverOptions{
			ProviderTemplates: map[string][]string{
				"codex": {"codex-bin", "run", "--prompt", "{prompt}", "--model", "{model}"},
			},
			PlatformDispatchNames: []string{"codex"},
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

		submitTask(t, env, "task-codex-exec", 1, map[string]any{
			"provider": "codex",
			"model":    "codex-base",
			"prompt":   "refactor",
		})

		task, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-codex", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if task == nil {
			t.Fatal("expected task, got nil")
		}

		foundCodex := false
		for _, arg := range capturedArgv {
			if arg == "codex-bin" {
				foundCodex = true
			}
		}
		if !foundCodex {
			t.Fatalf("expected codex-bin in argv, got: %v", capturedArgv)
		}
	})

	t.Run("provider ghost fails attempt listing available names and never runs agy default", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		resolverOpts := ProviderResolverOptions{
			ProviderTemplates: map[string][]string{
				"codex": {"codex-bin", "{prompt}"},
			},
			PlatformDispatchNames: []string{"codex", "claude"},
		}
		sup := NewSupervisor(env.store, env.runDir,
			WithRunner(env.runner),
			WithPollInterval(2*time.Millisecond),
			WithProviderCommandResolver(NewProviderCommandResolver(resolverOpts)),
		)

		spawnCalled := false
		var capturedArgv []string
		env.runner.factory = func(opts SpawnOptions) Child {
			spawnCalled = true
			capturedArgv = opts.Argv
			done := make(chan struct{})
			close(done)
			return &instantChild{done: done}
		}

		submitTask(t, env, "task-ghost-exec", 1, map[string]any{
			"provider": "ghost",
			"model":    "ghost-m",
			"prompt":   "do something",
		})

		task, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-ghost", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
		if task == nil {
			t.Fatal("expected task, got nil")
		}

		// Attempt error must list available names
		if task.LastError == nil {
			t.Fatalf("expected LastError on task, got nil")
		}
		lastErr := *task.LastError
		if !strings.Contains(lastErr, "provider \"ghost\" not found among platform_dispatch entries") {
			t.Errorf("LastError %q does not name requested provider 'ghost'", lastErr)
		}
		if !strings.Contains(lastErr, "claude, codex") && !strings.Contains(lastErr, "codex, claude") {
			t.Errorf("LastError %q does not list available providers", lastErr)
		}

		// Crucially: runner was never spawned with agy default argv!
		if spawnCalled {
			t.Errorf("spawn should not be called when resolution fails, but was called with: %v", capturedArgv)
		}
		for _, arg := range capturedArgv {
			if arg == "agy" {
				t.Fatalf("capturedArgv contains agy default binary!")
			}
		}
	})

	t.Run("legacy task with matching model hits model template", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		resolverOpts := ProviderResolverOptions{
			ModelTemplates: map[string][]string{
				"custom-model": {"custom-cli", "--prompt", "{prompt}"},
			},
			PlatformDispatchNames: []string{"custom-model"},
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

		submitTask(t, env, "task-legacy-match", 1, map[string]any{
			"model":  "custom-model",
			"prompt": "legacy",
		})

		_, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-leg-match", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		foundCLI := false
		for _, arg := range capturedArgv {
			if arg == "custom-cli" {
				foundCLI = true
			}
		}
		if !foundCLI {
			t.Fatalf("expected custom-cli in argv, got: %v", capturedArgv)
		}
	})

	t.Run("legacy task with no match falls back to agy default (regression guard)", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		resolverOpts := ProviderResolverOptions{
			ModelTemplates: map[string][]string{
				"other-model": {"other-cli", "{prompt}"},
			},
			PlatformDispatchNames: []string{"other-model"},
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

		submitTask(t, env, "task-legacy-nomatch", 1, map[string]any{
			"model":  "gemini-3.8-flash-high",
			"prompt": "fallback to agy",
		})

		_, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-leg-nomatch", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
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
	})

	t.Run("RunOptions.Provider claim filtering in supervisor", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		sup := NewSupervisor(env.store, env.runDir,
			WithRunner(env.runner),
			WithPollInterval(2*time.Millisecond),
		)

		done := make(chan struct{})
		close(done)
		env.runner.factory = func(opts SpawnOptions) Child {
			return &instantChild{done: done}
		}

		// Submit codex task and legacy task
		submitTask(t, env, "t-codex", 1, map[string]any{
			"provider": "codex",
			"prompt":   "codex task",
		})
		submitTask(t, env, "t-other", 1, map[string]any{
			"provider": "other",
			"prompt":   "other task",
		})

		// Worker filtered on "codex" only claims t-codex
		claimed, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-filter", LeaseSeconds: 10, Provider: "codex"})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if claimed == nil {
			t.Fatal("expected to claim task, got nil")
		}
		if claimed.IdempotencyKey != "t-codex" {
			t.Fatalf("expected t-codex, claimed %s", claimed.IdempotencyKey)
		}
	})
}
