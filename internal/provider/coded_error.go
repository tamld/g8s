package provider

import "fmt"

const (
	codeBinaryNotFound = "E_PROVIDER_BINARY_NOT_FOUND"
	codeProbeFailed    = "E_PROVIDER_PROBE_FAILED"
	codeSpawnFailed    = "E_PROVIDER_SPAWN_FAILED"
	codeCLIAPIError    = "E_PROVIDER_CLI_API_ERROR"
)

func codedError(code, format string, args ...any) error {
	all := append([]any{code}, args...)
	return fmt.Errorf("%s: "+format, all...)
}
