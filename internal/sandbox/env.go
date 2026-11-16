package sandbox

import (
	"strings"
)

// ProviderAllowedEnvKeys returns the uppercased environment variable names
// that a given provider CLI is permitted to inherit from the host environment.
func ProviderAllowedEnvKeys(providerName string) map[string]bool {
	name := strings.ToLower(strings.TrimSpace(providerName))
	out := make(map[string]bool)
	allow := func(keys ...string) {
		for _, k := range keys {
			out[k] = true
		}
	}
	switch name {
	case "cursor":
		allow("CURSOR_API_KEY", "CURSOR_API_BASE_URL")
	case "openai", "codex":
		allow("OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY")
	case "anthropic", "claude":
		allow("ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN")
	case "google", "gemini", "agy":
		allow("GEMINI_API_KEY", "GOOGLE_API_KEY")
	case "qwen":
		allow("DASHSCOPE_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL")
	case "opencode", "commandcode", "9router":
		allow("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "NVIDIA_API_KEY")
	}
	return out
}

var exactSensitiveEnvKeys = map[string]bool{
	"SSH_AUTH_SOCK":                  true,
	"SSH_AGENT_PID":                  true,
	"GPG_AGENT_INFO":                 true,
	"GITHUB_TOKEN":                   true,
	"GH_TOKEN":                       true,
	"GH_CONFIG_DIR":                  true,
	"GITLAB_TOKEN":                   true,
	"NPM_TOKEN":                      true,
	"PYPI_API_TOKEN":                 true,
	"DOCKER_AUTH_CONFIG":             true,
	"DOCKER_CONFIG":                  true,
	"KUBECONFIG":                     true,
	"AZURE_CLIENT_SECRET":            true,
	"GOOGLE_APPLICATION_CREDENTIALS": true,
	"GCP_SERVICE_ACCOUNT_KEY":        true,
	"G8S_SERVE_TOKEN":                true,
	"G8S_API_TOKEN":                  true,
	"G8S_SIGNING_KEY":                true,
	"G8S_STRATUM_TOKEN":              true,
}

var sensitiveEnvSuffixes = []string{
	"_API_KEY",
	"_SECRET",
	"_SECRET_KEY",
	"_TOKEN",
	"_PASSWORD",
	"_PRIVATE_KEY",
	"_CREDENTIALS",
}

func isStrippedAmbientKey(upperKey string, allowed map[string]bool) bool {
	if allowed[upperKey] {
		return false
	}
	if strings.HasPrefix(upperKey, "AWS_") || strings.HasPrefix(upperKey, "G8S_F0_") {
		return true
	}
	if exactSensitiveEnvKeys[upperKey] {
		return true
	}
	for _, suffix := range sensitiveEnvSuffixes {
		if strings.HasSuffix(upperKey, suffix) {
			return true
		}
	}
	return false
}

// ScrubEnv filters ambient secrets out of baseEnv, applies explicit taskEnv
// overrides, and injects isolated TMPDIR/TMP/TEMP, GOCACHE, and G8S_SANDBOX_ID.
func ScrubEnv(baseEnv, taskEnv []string, providerName, tmpDir, gocacheDir, sandboxID string) []string {
	allowed := ProviderAllowedEnvKeys(providerName)
	var order []string
	values := make(map[string]string)
	upperToKey := make(map[string]string)

	setKV := func(k, v string) {
		if k == "" {
			return
		}
		upper := strings.ToUpper(k)
		if prevKey, ok := upperToKey[upper]; ok {
			if prevKey != k {
				delete(values, prevKey)
				for idx, existing := range order {
					if existing == prevKey {
						order[idx] = k
						break
					}
				}
			}
		} else {
			order = append(order, k)
		}
		upperToKey[upper] = k
		values[k] = v
	}

	for _, entry := range baseEnv {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		upper := strings.ToUpper(k)
		if isStrippedAmbientKey(upper, allowed) {
			continue
		}
		setKV(k, v)
	}

	taskHasGoCache := false
	for _, entry := range taskEnv {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		if strings.EqualFold(k, "GOCACHE") {
			taskHasGoCache = true
		}
		setKV(k, v)
	}

	if strings.TrimSpace(tmpDir) != "" {
		setKV("TMPDIR", tmpDir)
		setKV("TMP", tmpDir)
		setKV("TEMP", tmpDir)
	}
	if strings.TrimSpace(gocacheDir) != "" && !taskHasGoCache {
		setKV("GOCACHE", gocacheDir)
	}
	if strings.TrimSpace(sandboxID) != "" {
		setKV("G8S_SANDBOX_ID", sandboxID)
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		if v, exists := values[k]; exists {
			out = append(out, k+"="+v)
		}
	}
	return out
}
