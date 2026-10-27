package provider

// CatalogEntry describes one provider documented for `g8s providers recommend`.
// Compiled into the binary so users can discover what works without a network
// call. The catalog is read-only and intentionally minimal — install hints
// point operators at the source.
type CatalogEntry struct {
	Name        string   // registry key
	Class       string   // "api_call" or "platform_dispatch"
	Description string   // one-line description
	Binary      string   // resolved via exec.LookPath (platform_dispatch)
	BaseURL     string   // default HTTP root (api_call)
	AuthEnv     string   // env var carrying bearer token (api_call)
	InstallHint string   // where to get it
	Models      []string // example model IDs; empty = provider-specific
}

// Catalog is the read-only built-in provider directory. Adding entries here
// is additive and never modifies runtime behavior — the registry remains
// the source of truth for actually-configured providers.
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{
			Name:        "agy",
			Class:       "platform_dispatch",
			Description: "Antigravity Gemini CLI (recommended default worker)",
			Binary:      "agy",
			InstallHint: "Install from https://github.com/tamld/agy",
			Models:      []string{"Gemini 3.8 Flash (High)"},
		},
		{
			Name:        "claude",
			Class:       "platform_dispatch",
			Description: "Anthropic Claude Code CLI",
			Binary:      "claude",
			InstallHint: "Install from https://docs.anthropic.com/en/docs/claude-code",
			Models:      []string{"Claude Haiku 4.5"},
		},
		{
			Name:        "codex",
			Class:       "platform_dispatch",
			Description: "OpenAI Codex CLI",
			Binary:      "codex",
			InstallHint: "Install from https://github.com/openai/codex",
		},
		{
			Name:        "opencode",
			Class:       "platform_dispatch",
			Description: "OpenCode CLI Agent (built-in ready-to-use free tier & multi-provider)",
			Binary:      "opencode",
			InstallHint: "brew install anomalyco/tap/opencode (https://opencode.ai)",
			Models: []string{
				"opencode/beam-free",
				"opencode/deepseek-v4.1-flash-free",
				"opencode/flint-40b-free",
				"opencode/glm-5.3-flash-free",
				"opencode/mimo-v2.6-flash-free",
			},
		},
		{
			Name:        "commandcode",
			Class:       "platform_dispatch",
			Description: "Command Code CLI Agent ($0 free models on Go+ plan & zero-cost BYOK via 9router)",
			Binary:      "commandcode",
			InstallHint: "npm install -g command-code (https://commandcode.ai)",
			Models: []string{
				"reflection/beam:free",
				"poolside/laguna-s-2.1-free",
				"stealth/glyph-cluster:free",
				"9router/docs-retriever",
				"9router/reasoning-free",
			},
		},
		{
			Name:        "qwen",
			Class:       "platform_dispatch",
			Description: "Qwen Code CLI Agent (ready-to-use via 9router OpenAI-compatible gateway or Qwen OAuth)",
			Binary:      "qwen",
			InstallHint: "npm install -g @qwen-code/qwen-code",
			Models: []string{
				"docs-retriever",
				"reasoning-free",
			},
		},
		{
			Name:        "cursor",
			Class:       "platform_dispatch",
			Description: "Cursor Agent CLI & Cloud Agent API (standalone CURSOR_API_KEY endpoint)",
			Binary:      "cursor-agent",
			BaseURL:     "https://api.cursor.com",
			AuthEnv:     "CURSOR_API_KEY",
			InstallHint: "Install Cursor CLI (cursor-agent) and set CURSOR_API_KEY (https://cursor.com/docs/cloud-agent/api/endpoints)",
			Models: []string{
				"composer-2.5",
				"composer-2.5-fast",
				"claude-opus-5-thinking-high",
				"claude-sonnet-5-thinking-high",
				"claude-fable-5-thinking-high",
				"gpt-5.6-sol-high",
				"gpt-5.6-luna-high",
				"gemini-4-argon-high",
				"gemini-3.7-flash-high",
				"cursor-grok-4.6-high",
			},
		},
		{
			Name:        "ollama",
			Class:       "platform_dispatch",
			Description: "Local Ollama daemon (HTTP, no binary required)",
			Binary:      "ollama",
			BaseURL:     "http://127.0.0.1:11434",
			AuthEnv:     "OLLAMA_HOST",
			InstallHint: "Install from https://ollama.com/download",
			Models:      []string{"llama3.1"},
		},
		{
			Name:        "9router",
			Class:       "api_call",
			Description: "9Router OpenAI-compatible gateway (local 9router instance)",
			BaseURL:     "http://localhost:20128/v1",
			AuthEnv:     "OPENAI_API_KEY",
			InstallHint: "Install from https://github.com/decolua/9router (runs at localhost:20128)",
			Models: []string{
				"reasoning-free",
				"reasoning-high",
				"deep-coding",
				"fast-agent",
				"docs-retriever",
				"gpt-4o-mini",
				"claude-3.5-sonnet",
				"gemini-2.0-flash",
			},
		},
	}
}
