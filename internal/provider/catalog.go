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
			Models:      []string{"gpt-4o-mini", "claude-3.5-sonnet", "gemini-2.0-flash"},
		},
	}
}
