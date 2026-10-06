package lane

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tamld/g8s/internal/config"
)

// CurrentEffortClassSchemaVersion is the required schema version for effort-classes.yml.
const CurrentEffortClassSchemaVersion = "effort-classes.v1"

// DefaultEffortClassesPath is the repository-relative path to the effort classes configuration.
const DefaultEffortClassesPath = ".g8s/effort-classes.yml"

// UnregisteredClassName is the fallback class name when no class matches or config is missing.
const UnregisteredClassName = "unregistered"

// SchemaVersionError reports an unsupported or missing schema version in the effort classes file.
type SchemaVersionError struct {
	Got  string
	Want string
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("unsupported schema version %q (want %q)", e.Got, e.Want)
}

// EffortClassValidationError indicates a semantic validation failure in an effort class.
type EffortClassValidationError struct {
	Class string
	Field string
	Msg   string
}

func (e *EffortClassValidationError) Error() string {
	if e.Class != "" {
		return fmt.Sprintf("effort class %q: field %q: %s", e.Class, e.Field, e.Msg)
	}
	return fmt.Sprintf("field %q: %s", e.Field, e.Msg)
}

// EffortClassParseError indicates a syntax error while parsing effort classes YAML.
type EffortClassParseError struct {
	Line int
	Msg  string
}

func (e *EffortClassParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("effort classes parse error at line %d: %s", e.Line, e.Msg)
	}
	return fmt.Sprintf("effort classes parse error: %s", e.Msg)
}

// EffortClass represents a single class entry in effort-classes.yml.
type EffortClass struct {
	Name          string   `json:"name" yaml:"name"`
	DefaultEffort string   `json:"default_effort" yaml:"default_effort"`
	Priority      int      `json:"priority" yaml:"priority"`
	Paths         []string `json:"paths" yaml:"paths"`
}

// EffortClasses represents the parsed effort classes configuration.
type EffortClasses struct {
	SchemaVersion string        `json:"schema_version"`
	Classes       []EffortClass `json:"classes"`
}

func cleanYAMLVal(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			return val[1 : len(val)-1]
		}
	}
	return val
}

func stripYAMLComments(line string) string {
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
		item := cleanYAMLVal(part)
		if item != "" {
			res = append(res, item)
		}
	}
	return res, nil
}

// ParseEffortClasses parses YAML data for effort classes using pure Go stdlib.
func ParseEffortClasses(data []byte) (*EffortClasses, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	ec := &EffortClasses{
		Classes: []EffortClass{},
	}

	var (
		currentSection string // "classes"
		currentClass   *EffortClass
		inPathsList    bool
	)

	commitCurrentClass := func() {
		if currentClass != nil {
			ec.Classes = append(ec.Classes, *currentClass)
			currentClass = nil
		}
		inPathsList = false
	}

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		rawLine := scanner.Text()
		line := stripYAMLComments(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))

		// Top-level sections (indent 0)
		if leadingSpaces == 0 {
			commitCurrentClass()
			currentSection = ""

			idx := strings.IndexByte(trimmed, ':')
			if idx == -1 {
				return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("invalid top-level line %q", trimmed)}
			}
			key := cleanYAMLVal(trimmed[:idx])
			val := cleanYAMLVal(trimmed[idx+1:])

			switch key {
			case "schema_version":
				ec.SchemaVersion = val
			case "classes":
				currentSection = "classes"
			default:
				// ignore unrecognized top-level keys
			}
			continue
		}

		if currentSection == "classes" {
			// A line at class-level indent (leadingSpaces <= 3) starting with "- " is a new class item
			isNewClass := leadingSpaces <= 3 && strings.HasPrefix(trimmed, "- ")
			if isNewClass {
				inPathsList = false
			}

			// Inside paths list: items have indent >= 4 and start with "- "
			if inPathsList {
				if strings.HasPrefix(trimmed, "- ") {
					pathVal := cleanYAMLVal(strings.TrimPrefix(trimmed, "- "))
					if currentClass != nil {
						currentClass.Paths = append(currentClass.Paths, pathVal)
					}
					continue
				}
				// Any line not starting with "- " terminates the paths list
				inPathsList = false
			}

			// Check for new class item: line starts with "- "
			if isNewClass || (currentClass == nil && strings.HasPrefix(trimmed, "- ")) {
				commitCurrentClass()
				currentClass = &EffortClass{
					Paths: []string{},
				}
				itemRest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if itemRest != "" {
					idx := strings.IndexByte(itemRest, ':')
					if idx != -1 {
						k := cleanYAMLVal(itemRest[:idx])
						v := cleanYAMLVal(itemRest[idx+1:])
						switch k {
						case "name":
							currentClass.Name = v
						case "default_effort":
							currentClass.DefaultEffort = v
						case "priority":
							p, err := strconv.Atoi(v)
							if err != nil {
								return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("invalid priority integer %q", v)}
							}
							currentClass.Priority = p
						case "paths":
							if strings.HasPrefix(v, "[") {
								arr, err := parseFlowArray(v)
								if err != nil {
									return nil, &EffortClassParseError{Line: lineNum, Msg: err.Error()}
								}
								currentClass.Paths = arr
							} else if v == "" {
								inPathsList = true
							}
						}
					}
				}
				continue
			}

			if currentClass == nil {
				return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("line outside class item: %q", trimmed)}
			}

			// Class properties
			idx := strings.IndexByte(trimmed, ':')
			if idx == -1 {
				return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("invalid class property line %q", trimmed)}
			}
			propKey := cleanYAMLVal(trimmed[:idx])
			propVal := cleanYAMLVal(trimmed[idx+1:])

			switch propKey {
			case "name":
				currentClass.Name = propVal
			case "default_effort":
				currentClass.DefaultEffort = propVal
			case "priority":
				p, err := strconv.Atoi(propVal)
				if err != nil {
					return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("invalid priority integer %q", propVal)}
				}
				currentClass.Priority = p
			case "paths":
				if strings.HasPrefix(propVal, "[") {
					arr, err := parseFlowArray(propVal)
					if err != nil {
						return nil, &EffortClassParseError{Line: lineNum, Msg: err.Error()}
					}
					currentClass.Paths = arr
				} else if propVal == "" {
					inPathsList = true
				}
			}
			continue
		}

		return nil, &EffortClassParseError{Line: lineNum, Msg: fmt.Sprintf("unexpected line %q", trimmed)}
	}

	if err := scanner.Err(); err != nil {
		return nil, &EffortClassParseError{Line: lineNum, Msg: err.Error()}
	}

	commitCurrentClass()

	if ec.SchemaVersion != CurrentEffortClassSchemaVersion {
		return nil, &SchemaVersionError{Got: ec.SchemaVersion, Want: CurrentEffortClassSchemaVersion}
	}

	if err := ec.validate(); err != nil {
		return nil, err
	}

	return ec, nil
}

func (ec *EffortClasses) validate() error {
	for i, c := range ec.Classes {
		if strings.TrimSpace(c.Name) == "" {
			return &EffortClassValidationError{
				Class: "",
				Field: "name",
				Msg:   fmt.Sprintf("class at index %d has empty name", i),
			}
		}
		if !config.IsValidEffortLevel(c.DefaultEffort) {
			return &EffortClassValidationError{
				Class: c.Name,
				Field: "default_effort",
				Msg:   fmt.Sprintf("unknown effort level %q", c.DefaultEffort),
			}
		}
	}
	return nil
}

// LoadEffortClassesFile reads and parses an effort classes file.
// If the file does not exist, an empty EffortClasses struct is returned (fail-open).
func LoadEffortClassesFile(path string) (*EffortClasses, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &EffortClasses{}, nil
		}
		return nil, fmt.Errorf("read effort classes: %w", err)
	}
	return ParseEffortClasses(data)
}

// LoadEffortClasses attempts to locate and load .g8s/effort-classes.yml searching upward from cwd.
// Missing file returns an empty resolver without error (fail-open).
func LoadEffortClasses() (*EffortClasses, error) {
	path, err := findFile(DefaultEffortClassesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &EffortClasses{}, nil
		}
		return nil, fmt.Errorf("find effort classes: %w", err)
	}
	return LoadEffortClassesFile(path)
}

// ResolveClass matches write-scope paths against registered classes deterministically.
// Rules:
//   - An entry matches when ALL paths fall inside its globs.
//   - Lowest-priority-number matching entry wins (first match by priority).
//   - Ties broken deterministically by name alphabetically.
//   - Empty paths, missing config, or no matching classes -> ("unregistered", config.EffortMedium).
func (ec *EffortClasses) ResolveClass(paths []string) (string, string) {
	if ec == nil || len(ec.Classes) == 0 || len(paths) == 0 {
		return UnregisteredClassName, config.EffortMedium
	}

	var candidates []EffortClass
	for _, c := range ec.Classes {
		if len(c.Paths) == 0 {
			continue
		}
		allMatch := true
		for _, p := range paths {
			clean := filepath.ToSlash(filepath.Clean(p))
			matched := false
			for _, pattern := range c.Paths {
				if pathMatches(clean, pattern) {
					matched = true
					break
				}
			}
			if !matched {
				allMatch = false
				break
			}
		}
		if allMatch {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return UnregisteredClassName, config.EffortMedium
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].Name < candidates[j].Name
	})

	return candidates[0].Name, candidates[0].DefaultEffort
}

// ResolveClass loads the default effort classes configuration and resolves paths.
// Fails open to ("unregistered", config.EffortMedium).
func ResolveClass(paths []string) (string, string) {
	ec, err := LoadEffortClasses()
	if err != nil || ec == nil {
		return UnregisteredClassName, config.EffortMedium
	}
	return ec.ResolveClass(paths)
}

// isPathUnderRoot checks if a path resolves inside the given root.
// Returns the root-relative path and true if inside, or ("", false) otherwise.
// Pure function: operates lexically; reconciles relative/absolute paths without modifying process cwd.
func isPathUnderRoot(path, root string) (string, bool) {
	cleanP := filepath.Clean(path)
	cleanR := filepath.Clean(root)

	rel, err := filepath.Rel(cleanR, cleanP)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel, true
	}

	if filepath.IsAbs(cleanR) != filepath.IsAbs(cleanP) {
		absP, errP := filepath.Abs(cleanP)
		absR, errR := filepath.Abs(cleanR)
		if errP == nil && errR == nil {
			relAbs, errAbs := filepath.Rel(absR, absP)
			if errAbs == nil && relAbs != ".." && !strings.HasPrefix(relAbs, ".."+string(filepath.Separator)) {
				return relAbs, true
			}
		}
	}

	return "", false
}

// ResolveClassForRoots resolves write-scope paths against effort class registries across multiple roots.
// Sibling precedence rules (in order):
//  1. If ALL classPaths resolve under ONE root (filepath base check per path against each root)
//     and <root>/.g8s/effort-classes.yml EXISTS:
//     Load that file (LoadEffortClassesFile) and resolve the paths (relative to that root) against it.
//     Sibling config wins for sibling work.
//     If the sibling registry exists but is malformed, a typed error is returned (fails loudly).
//  2. Otherwise -> repo registry (upward findFile from CWD) with fail-open unregistered floor.
func ResolveClassForRoots(classPaths []string, roots []string) (className, defaultEffort string, err error) {
	if len(classPaths) > 0 && len(roots) > 0 {
		type candidateRoot struct {
			root     string
			relPaths []string
		}
		var candidates []candidateRoot

		for _, root := range roots {
			cleanRoot := filepath.Clean(root)
			allUnder := true
			relPaths := make([]string, 0, len(classPaths))
			for _, p := range classPaths {
				rel, ok := isPathUnderRoot(p, cleanRoot)
				if !ok {
					allUnder = false
					break
				}
				relPaths = append(relPaths, filepath.ToSlash(rel))
			}
			if allUnder {
				candidates = append(candidates, candidateRoot{
					root:     cleanRoot,
					relPaths: relPaths,
				})
			}
		}

		if len(candidates) > 0 {
			// Sort candidates by root path length descending (most specific / deepest root wins)
			sort.SliceStable(candidates, func(i, j int) bool {
				return len(candidates[i].root) > len(candidates[j].root)
			})
			chosen := candidates[0]
			siblingConfig := filepath.Join(chosen.root, DefaultEffortClassesPath)
			if fi, statErr := os.Stat(siblingConfig); statErr == nil && !fi.IsDir() {
				ec, loadErr := LoadEffortClassesFile(siblingConfig)
				if loadErr != nil {
					return "", "", loadErr
				}
				cls, eff := ec.ResolveClass(chosen.relPaths)
				return cls, eff, nil
			}
		}
	}

	// Fallback: upward findFile from CWD (repo registry)
	ec, loadErr := LoadEffortClasses()
	if loadErr != nil {
		return "", "", loadErr
	}
	cls, eff := ec.ResolveClass(classPaths)
	return cls, eff, nil
}

// EffortLadderIndex returns the index of level on config.EffortLadder, or -1 if outside ladder.
func EffortLadderIndex(level string) int {
	for i, l := range config.EffortLadder {
		if l == level {
			return i
		}
	}
	return -1
}

// IsOverrideDown reports whether flagEffort represents a lower effort level than classDefault on config.EffortLadder.
func IsOverrideDown(flagEffort, classDefault string) bool {
	fIdx := EffortLadderIndex(flagEffort)
	cIdx := EffortLadderIndex(classDefault)
	if fIdx >= 0 && cIdx >= 0 {
		return fIdx < cIdx
	}
	return false
}
