package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	defaultSelectorMu sync.RWMutex
	defaultSelector   string
)

// SetDefaultSelector sets the global default selector expression.
func SetDefaultSelector(expr string) {
	defaultSelectorMu.Lock()
	defaultSelector = expr
	defaultSelectorMu.Unlock()
}

// DefaultSelector returns the global default selector expression.
func DefaultSelector() string {
	defaultSelectorMu.RLock()
	defer defaultSelectorMu.RUnlock()
	return defaultSelector
}

// SelectPath evaluates a JSON path selector against raw JSON bytes.
// Supports dot notation (.data.receipt_id), array indexing (.matches[0]),
// and array projection (.matches[].file).
func SelectPath(rawJSON []byte, expr string) (string, error) {
	trimmed := strings.TrimSpace(expr)
	var root any
	dec := json.NewDecoder(bytes.NewReader(rawJSON))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return "", fmt.Errorf("invalid json: %w", err)
	}
	return evalPath(root, trimmed)
}

func evalPath(curr any, expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, ".") {
		expr = strings.TrimSpace(strings.TrimPrefix(expr, "."))
	}
	if expr == "" {
		return formatValue(curr)
	}

	for expr != "" {
		if strings.HasPrefix(expr, ".") {
			expr = strings.TrimSpace(strings.TrimPrefix(expr, "."))
			if expr == "" {
				return formatValue(curr)
			}
		}

		if strings.HasPrefix(expr, "[") {
			closeIdx := strings.Index(expr, "]")
			if closeIdx == -1 {
				return "", fmt.Errorf("unmatched '[' in selector expression %q", expr)
			}
			inside := strings.TrimSpace(expr[1:closeIdx])
			remaining := strings.TrimSpace(expr[closeIdx+1:])

			if inside == "" {
				// Array projection: []
				slice, ok := curr.([]any)
				if !ok {
					return "", fmt.Errorf("cannot project across non-array (%T)", curr)
				}
				results := make([]string, 0, len(slice))
				for _, item := range slice {
					res, err := evalPath(item, remaining)
					if err != nil {
						return "", err
					}
					results = append(results, res)
				}
				return strings.Join(results, "\n"), nil
			}

			idx, err := strconv.Atoi(inside)
			if err != nil {
				return "", fmt.Errorf("invalid array index %q", inside)
			}
			slice, ok := curr.([]any)
			if !ok {
				return "", fmt.Errorf("cannot index non-array (%T)", curr)
			}
			if idx < 0 || idx >= len(slice) {
				return "", fmt.Errorf("index %d out of bounds (len %d)", idx, len(slice))
			}
			curr = slice[idx]
			expr = remaining
			continue
		}

		// Field extraction
		end := len(expr)
		dotIdx := strings.Index(expr, ".")
		bracketIdx := strings.Index(expr, "[")

		if dotIdx != -1 && bracketIdx != -1 {
			if dotIdx < bracketIdx {
				end = dotIdx
			} else {
				end = bracketIdx
			}
		} else if dotIdx != -1 {
			end = dotIdx
		} else if bracketIdx != -1 {
			end = bracketIdx
		}

		fieldName := strings.TrimSpace(expr[:end])
		if fieldName == "" {
			return "", fmt.Errorf("empty field name in selector expression")
		}
		expr = strings.TrimSpace(expr[end:])

		obj, ok := curr.(map[string]any)
		if !ok {
			return "", fmt.Errorf("cannot access field %q on non-object (%T)", fieldName, curr)
		}
		val, exists := obj[fieldName]
		if !exists {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return "", fmt.Errorf("field %q not found (available keys: [%s])", fieldName, strings.Join(keys, ", "))
		}
		curr = val
	}

	return formatValue(curr)
}

func formatValue(val any) (string, error) {
	if val == nil {
		return "", nil
	}
	switch v := val.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return fmt.Sprint(v), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(v), nil
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}
