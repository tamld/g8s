package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CurrentCatalogSchemaVersion is the required schema version for agent-models catalogs.
const CurrentCatalogSchemaVersion = "agent-models.v1"

// DefaultCatalogPath is the repository-relative path to the catalog.
const DefaultCatalogPath = ".g8s/agent-models.yml"

// SchemaVersionError reports an unsupported or missing schema version in the catalog.
type SchemaVersionError struct {
	Got  string
	Want string
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("unsupported schema version %q (want %q)", e.Got, e.Want)
}

// ParseError indicates a syntax or structure error while parsing catalog YAML.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("catalog parse error at line %d: %s", e.Line, e.Msg)
	}
	return fmt.Sprintf("catalog parse error: %s", e.Msg)
}

// ValidationError indicates a semantic validation failure in a catalog entry.
type ValidationError struct {
	Provider string
	Model    string
	Field    string
	Msg      string
}

func (e *ValidationError) Error() string {
	if e.Model != "" {
		return fmt.Sprintf("model %q: field %q: %s", e.Model, e.Field, e.Msg)
	}
	if e.Provider != "" {
		return fmt.Sprintf("provider %q: field %q: %s", e.Provider, e.Field, e.Msg)
	}
	return fmt.Sprintf("field %q: %s", e.Field, e.Msg)
}

// Catalog represents the agent-models catalog (agent-models.v1).
type Catalog struct {
	SchemaVersion  string                     `json:"schema_version"`
	CatalogVersion int                        `json:"catalog_version"`
	VerifiedAt     string                     `json:"verified_at"`
	Sources        []string                   `json:"sources"`
	Providers      map[string]ProviderCatalog `json:"providers"`
}

// ProviderCatalog represents a provider's entry in the catalog.
type ProviderCatalog struct {
	Name          string         `json:"name,omitempty"`
	EffortStyle   string         `json:"effort_style,omitempty"`
	Field         string         `json:"field,omitempty"`
	DefaultEffort string         `json:"default_effort,omitempty"`
	Mandatory     bool           `json:"mandatory,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	Models        []ModelCatalog `json:"models"`
}

// FindModel searches for a model by ID within the provider catalog (case-insensitive).
func (p ProviderCatalog) FindModel(id string) (ModelCatalog, bool) {
	for _, m := range p.Models {
		if strings.EqualFold(m.ID, id) {
			return m, true
		}
	}
	return ModelCatalog{}, false
}

// ModelCatalog represents a model's entry in the catalog.
type ModelCatalog struct {
	ID               string         `json:"id"`
	EffortStyle      string         `json:"effort_style,omitempty"`
	SupportedEfforts []string       `json:"supported_efforts"`
	DefaultEffort    string         `json:"default_effort,omitempty"`
	Mandatory        bool           `json:"mandatory,omitempty"`
	EffortBudgetMap  map[string]int `json:"effort_budget_map,omitempty"`
	Notes            string         `json:"notes,omitempty"`
}

// MergeReport records the outcome of merging a Catalog into a File manifest.
type MergeReport struct {
	FilledModels         int      `json:"filled_models"`
	SkippedProviders     int      `json:"skipped_providers"`
	UnknownProviders     int      `json:"unknown_providers"`
	SkippedProviderNames []string `json:"skipped_provider_names,omitempty"`
}

// FindProvider searches for a provider by name within the catalog (case-insensitive).
func (c *Catalog) FindProvider(name string) (ProviderCatalog, bool) {
	if c == nil || c.Providers == nil {
		return ProviderCatalog{}, false
	}
	if p, ok := c.Providers[name]; ok {
		return p, true
	}
	for k, v := range c.Providers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return ProviderCatalog{}, false
}

// MergeInto fills manifest models' empty effort fields from catalog entries.
// It never overwrites user-set values in manifest (user manifest wins).
// Unknown-provider catalog entries are skipped and counted in the returned report.
func (c *Catalog) MergeInto(manifest *File) MergeReport {
	var report MergeReport
	if c == nil || manifest == nil {
		return report
	}

	manifestProvNames := make(map[string]bool)
	for _, p := range manifest.Providers {
		manifestProvNames[strings.ToLower(p.Name)] = true
	}

	for catProvName := range c.Providers {
		if !manifestProvNames[strings.ToLower(catProvName)] {
			report.SkippedProviders++
			report.UnknownProviders++
			report.SkippedProviderNames = append(report.SkippedProviderNames, catProvName)
		}
	}
	sort.Strings(report.SkippedProviderNames)

	for i := range manifest.Providers {
		p := &manifest.Providers[i]
		catProv, ok := c.FindProvider(p.Name)
		if !ok {
			continue
		}

		for j := range p.Models {
			m := &p.Models[j]
			catModel, ok := catProv.FindModel(m.ID)
			if !ok {
				trimmedID := strings.TrimSuffix(m.ID, "-{effort}")
				catModel, ok = catProv.FindModel(trimmedID)
				if !ok {
					continue
				}
			}

			filled := false

			// EffortStyle: empty = fill with catalog model's (or provider's) style
			if m.EffortStyle == "" {
				style := catModel.EffortStyle
				if style == "" {
					style = catProv.EffortStyle
				}
				if style != "" {
					m.EffortStyle = style
					filled = true
				}
			}

			// SupportedEfforts: empty = fill from catalog
			if len(m.SupportedEfforts) == 0 && len(catModel.SupportedEfforts) > 0 {
				m.SupportedEfforts = make([]string, len(catModel.SupportedEfforts))
				copy(m.SupportedEfforts, catModel.SupportedEfforts)
				filled = true
			}

			// DefaultEffort: empty = fill from catalog
			if m.DefaultEffort == "" {
				def := catModel.DefaultEffort
				if def == "" && len(m.SupportedEfforts) > 0 {
					if catProv.DefaultEffort == EffortAdaptive || catProv.DefaultEffort == EffortDynamic {
						def = catProv.DefaultEffort
					} else {
						for _, s := range m.SupportedEfforts {
							if s == catProv.DefaultEffort {
								def = catProv.DefaultEffort
								break
							}
						}
					}
				}
				if def != "" {
					m.DefaultEffort = def
					filled = true
				}
			}

			// Mandatory: if false on manifest, and true on catalog, fill
			if !m.Mandatory {
				effMandatory := catModel.Mandatory || catProv.Mandatory
				if effMandatory {
					m.Mandatory = true
					filled = true
				}
			}

			// EffortBudgetMap: empty = fill from catalog
			if len(m.EffortBudgetMap) == 0 && len(catModel.EffortBudgetMap) > 0 {
				m.EffortBudgetMap = make(map[string]int, len(catModel.EffortBudgetMap))
				for k, v := range catModel.EffortBudgetMap {
					m.EffortBudgetMap[k] = v
				}
				filled = true
			}

			if filled {
				report.FilledModels++
			}
		}
	}

	return report
}

func cleanYAMLValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}

func stripYAMLComment(line string) string {
	inDouble := false
	inSingle := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '#' && !inDouble && !inSingle {
			return line[:i]
		}
	}
	return line
}

func parseFlowArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("expected flow array enclosed in [], got %q", s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []string{}, nil
	}
	parts := strings.Split(inner, ",")
	res := make([]string, 0, len(parts))
	for _, part := range parts {
		item := cleanYAMLValue(part)
		if item != "" {
			res = append(res, item)
		}
	}
	return res, nil
}

func parseFlowMap(s string) (map[string]int, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, fmt.Errorf("expected flow map enclosed in {}, got %q", s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return make(map[string]int), nil
	}
	parts := strings.Split(inner, ",")
	res := make(map[string]int, len(parts))
	for _, part := range parts {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid map entry %q", part)
		}
		k := cleanYAMLValue(kv[0])
		vStr := cleanYAMLValue(kv[1])
		v, err := strconv.Atoi(vStr)
		if err != nil {
			return nil, fmt.Errorf("invalid integer for key %q: %q", k, vStr)
		}
		res[k] = v
	}
	return res, nil
}

func parseBool(s string) (bool, error) {
	val := strings.ToLower(cleanYAMLValue(s))
	switch val {
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", s)
	}
}

// ParseCatalog parses YAML data for an agent-models catalog using pure Go stdlib.
func ParseCatalog(data []byte) (*Catalog, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	cat := &Catalog{
		Sources:   []string{},
		Providers: make(map[string]ProviderCatalog),
	}

	var (
		currentSection         string // "sources" or "providers"
		currentProviderName    string
		currentProvider        *ProviderCatalog
		currentModel           *ModelCatalog
		inModelsList           bool
		inSupportedEffortsList bool
		inBudgetMapBlock       bool
	)

	commitCurrentModel := func() {
		if currentModel != nil && currentProvider != nil {
			currentProvider.Models = append(currentProvider.Models, *currentModel)
			currentModel = nil
		}
		inSupportedEffortsList = false
		inBudgetMapBlock = false
	}

	commitCurrentProvider := func() {
		commitCurrentModel()
		if currentProvider != nil && currentProviderName != "" {
			cat.Providers[currentProviderName] = *currentProvider
			currentProvider = nil
			currentProviderName = ""
		}
		inModelsList = false
	}

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		rawLine := scanner.Text()
		line := stripYAMLComment(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))

		// Top-level sections (indent 0)
		if leadingSpaces == 0 {
			commitCurrentProvider()
			currentSection = ""

			idx := strings.IndexByte(trimmed, ':')
			if idx == -1 {
				return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid top-level line %q", trimmed)}
			}
			key := cleanYAMLValue(trimmed[:idx])
			val := cleanYAMLValue(trimmed[idx+1:])

			switch key {
			case "schema_version":
				cat.SchemaVersion = val
			case "catalog_version":
				ver, err := strconv.Atoi(val)
				if err != nil {
					return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid catalog_version %q", val)}
				}
				cat.CatalogVersion = ver
			case "verified_at":
				cat.VerifiedAt = val
			case "sources":
				currentSection = "sources"
			case "providers":
				currentSection = "providers"
			default:
				// ignore unrecognized top-level keys
			}
			continue
		}

		// Indented lines
		switch currentSection {
		case "sources":
			if strings.HasPrefix(trimmed, "- ") {
				val := cleanYAMLValue(strings.TrimPrefix(trimmed, "- "))
				cat.Sources = append(cat.Sources, val)
				continue
			}

		case "providers":
			// Provider header: indent 1..3 spaces and ends with ":"
			if leadingSpaces >= 1 && leadingSpaces <= 3 && strings.HasSuffix(trimmed, ":") {
				commitCurrentProvider()
				provName := cleanYAMLValue(strings.TrimSuffix(trimmed, ":"))
				currentProviderName = provName
				currentProvider = &ProviderCatalog{
					Name:   provName,
					Models: []ModelCatalog{},
				}
				continue
			}

			if currentProvider == nil {
				return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("content outside provider definition: %q", trimmed)}
			}

			// If indent is at provider level (<= 5 spaces) and doesn't start with "- ",
			// we are at provider properties (commit any current model)
			if leadingSpaces <= 5 && !strings.HasPrefix(trimmed, "- ") {
				commitCurrentModel()
				inModelsList = false
			}

			// Inside models list:
			if inModelsList {
				if strings.HasPrefix(trimmed, "- ") {
					commitCurrentModel()
					currentModel = &ModelCatalog{}
					itemRest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
					if strings.HasPrefix(itemRest, "id:") {
						currentModel.ID = cleanYAMLValue(strings.TrimPrefix(itemRest, "id:"))
					}
					continue
				}

				if currentModel != nil {
					if inSupportedEffortsList {
						if strings.HasPrefix(trimmed, "- ") {
							currentModel.SupportedEfforts = append(currentModel.SupportedEfforts, cleanYAMLValue(strings.TrimPrefix(trimmed, "- ")))
							continue
						}
						inSupportedEffortsList = false
					}

					if inBudgetMapBlock {
						if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
							parts := strings.SplitN(trimmed, ":", 2)
							k := cleanYAMLValue(parts[0])
							vStr := cleanYAMLValue(parts[1])
							v, err := strconv.Atoi(vStr)
							if err != nil {
								return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid budget integer %q: %v", vStr, err)}
							}
							if currentModel.EffortBudgetMap == nil {
								currentModel.EffortBudgetMap = make(map[string]int)
							}
							currentModel.EffortBudgetMap[k] = v
							continue
						}
						inBudgetMapBlock = false
					}

					idx := strings.IndexByte(trimmed, ':')
					if idx != -1 {
						propKey := cleanYAMLValue(trimmed[:idx])
						propVal := strings.TrimSpace(trimmed[idx+1:])

						switch propKey {
						case "id":
							currentModel.ID = cleanYAMLValue(propVal)
							continue
						case "supported_efforts":
							if propVal == "" {
								inSupportedEffortsList = true
								currentModel.SupportedEfforts = []string{}
							} else {
								arr, err := parseFlowArray(propVal)
								if err != nil {
									return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid supported_efforts: %v", err)}
								}
								currentModel.SupportedEfforts = arr
							}
							continue
						case "default_effort":
							currentModel.DefaultEffort = cleanYAMLValue(propVal)
							continue
						case "effort_style":
							currentModel.EffortStyle = cleanYAMLValue(propVal)
							continue
						case "mandatory":
							b, err := parseBool(propVal)
							if err != nil {
								return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid mandatory value %q", propVal)}
							}
							currentModel.Mandatory = b
							continue
						case "effort_budget_map":
							if propVal == "" {
								inBudgetMapBlock = true
								currentModel.EffortBudgetMap = make(map[string]int)
							} else {
								bm, err := parseFlowMap(propVal)
								if err != nil {
									return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid effort_budget_map: %v", err)}
								}
								currentModel.EffortBudgetMap = bm
							}
							continue
						case "notes":
							currentModel.Notes = cleanYAMLValue(propVal)
							continue
						}
					}
				}
			}

			// Provider-level properties:
			if strings.HasPrefix(trimmed, "models:") {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "models:"))
				if rest == "[]" {
					commitCurrentModel()
					currentProvider.Models = []ModelCatalog{}
					inModelsList = false
				} else {
					commitCurrentModel()
					inModelsList = true
				}
				continue
			}

			idx := strings.IndexByte(trimmed, ':')
			if idx != -1 {
				pKey := cleanYAMLValue(trimmed[:idx])
				pVal := cleanYAMLValue(trimmed[idx+1:])
				switch pKey {
				case "effort_style":
					currentProvider.EffortStyle = pVal
				case "field":
					currentProvider.Field = pVal
				case "default_effort":
					currentProvider.DefaultEffort = pVal
				case "mandatory":
					b, err := parseBool(pVal)
					if err != nil {
						return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid mandatory value %q", pVal)}
					}
					currentProvider.Mandatory = b
				case "notes":
					currentProvider.Notes = pVal
				}
				continue
			}

			return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("unexpected line %q", trimmed)}

		default:
			return nil, &ParseError{Line: lineNum, Msg: fmt.Sprintf("unexpected line outside section %q", trimmed)}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, &ParseError{Line: lineNum, Msg: err.Error()}
	}

	commitCurrentProvider()

	if cat.SchemaVersion != CurrentCatalogSchemaVersion {
		return nil, &SchemaVersionError{Got: cat.SchemaVersion, Want: CurrentCatalogSchemaVersion}
	}

	if cat.VerifiedAt == "" {
		return nil, &ValidationError{Field: "verified_at", Msg: "verified_at is required"}
	}

	if err := cat.validate(); err != nil {
		return nil, err
	}

	return cat, nil
}

func (c *Catalog) validate() error {
	for provName, prov := range c.Providers {
		if prov.EffortStyle != "" && !IsValidEffortStyle(prov.EffortStyle) {
			return &ValidationError{Provider: provName, Field: "effort_style", Msg: fmt.Sprintf("unknown style %q", prov.EffortStyle)}
		}

		for _, m := range prov.Models {
			name := m.ID
			if name == "" {
				return &ValidationError{Provider: provName, Field: "id", Msg: "model id is required"}
			}

			if m.EffortStyle != "" && !IsValidEffortStyle(m.EffortStyle) {
				return &ValidationError{Provider: provName, Model: name, Field: "effort_style", Msg: fmt.Sprintf("unknown style %q", m.EffortStyle)}
			}

			effectiveStyle := m.EffortStyle
			if effectiveStyle == "" {
				effectiveStyle = prov.EffortStyle
			}

			for _, lvl := range m.SupportedEfforts {
				if !IsValidEffortLevel(lvl) {
					return &ValidationError{Provider: provName, Model: name, Field: "supported_efforts", Msg: fmt.Sprintf("level %q outside ladder", lvl)}
				}
			}

			if m.DefaultEffort != "" {
				if m.DefaultEffort != EffortAdaptive && m.DefaultEffort != EffortDynamic {
					found := false
					for _, lvl := range m.SupportedEfforts {
						if lvl == m.DefaultEffort {
							found = true
							break
						}
					}
					if !found {
						return &ValidationError{Provider: provName, Model: name, Field: "default_effort", Msg: fmt.Sprintf("default %q not in supported set", m.DefaultEffort)}
					}
				}
			}

			if len(m.EffortBudgetMap) > 0 {
				if effectiveStyle != EffortStyleBudget {
					return &ValidationError{Provider: provName, Model: name, Field: "effort_budget_map", Msg: fmt.Sprintf("budget map not allowed for non-budget style %q", effectiveStyle)}
				}
				for lvl, tokens := range m.EffortBudgetMap {
					if !IsValidEffortLevel(lvl) {
						return &ValidationError{Provider: provName, Model: name, Field: "effort_budget_map", Msg: fmt.Sprintf("level %q outside ladder", lvl)}
					}
					if tokens <= 0 {
						return &ValidationError{Provider: provName, Model: name, Field: "effort_budget_map", Msg: fmt.Sprintf("token budget for level %q must be > 0", lvl)}
					}
				}
			}
		}
	}
	return nil
}

// LoadCatalog reads and parses an agent-models catalog file.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog file: %w", err)
	}
	return ParseCatalog(data)
}

// LoadDefaultCatalog attempts to locate and load the default catalog file (.g8s/agent-models.yml)
// searching upward from the working directory.
func LoadDefaultCatalog() (*Catalog, error) {
	path, err := findFile(DefaultCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("find default catalog: %w", err)
	}
	return LoadCatalog(path)
}

func findFile(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		if _, err := os.Stat(relPath); err == nil {
			return relPath, nil
		}
		return "", os.ErrNotExist
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
