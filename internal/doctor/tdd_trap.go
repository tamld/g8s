package doctor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// TDDPitfall represents a detected TDD anti-pattern in test files per DEBT-49.
type TDDPitfall struct {
	File     string `json:"file"`     // Path to test file
	Line     int    `json:"line"`     // 1-indexed line number
	Symbol   string `json:"symbol"`   // e.g. "User.loyalty_points"
	Category string `json:"category"` // "fabricated" | "locks-impl-detail"
	Message  string `json:"message"`  // Actionable diagnostic message
}

// typeDef stores production struct/interface definitions for comparison.
type typeDef struct {
	Name      string
	IsStruct  bool
	Fields    map[string]bool // field names
	Methods   map[string]bool // method names
	IsPrivate bool
}

// pkgDef stores all symbols defined in a production package.
type pkgDef struct {
	Name      string
	Types     map[string]*typeDef // TypeName -> typeDef
	Functions map[string]bool     // package level functions
	VarsConst map[string]bool     // package level vars/consts
}

// CheckTDDPitfalls walks repoPath, parses production and test code with go/parser,
// and detects test files that pin fabricated symbols or lock internal implementation details.
func CheckTDDPitfalls(repoPath string) ([]TDDPitfall, error) {
	if repoPath == "" {
		repoPath = "."
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("tdd_trap: abs repo path: %w", err)
	}

	packages := make(map[string]*pkgDef)
	testFilePaths := make(map[string][]string) // dir -> []testFiles

	fset := token.NewFileSet()

	// 1. Walk directory tree and collect all Go files
	err = filepath.Walk(absRepo, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "reference" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		dir := filepath.Dir(path)
		if strings.HasSuffix(info.Name(), "_test.go") {
			testFilePaths[dir] = append(testFilePaths[dir], path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("tdd_trap: walk repo: %w", err)
	}

	// 2. Parse all production files per package directory
	for dir := range testFilePaths {
		pkgs, parseErr := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ParseComments)
		if parseErr != nil {
			continue
		}

		for pkgName, pkgAst := range pkgs {
			pDef := &pkgDef{
				Name:      pkgName,
				Types:     make(map[string]*typeDef),
				Functions: make(map[string]bool),
				VarsConst: make(map[string]bool),
			}

			for _, file := range pkgAst.Files {
				collectProdSymbols(file, pDef)
			}
			packages[dir] = pDef
		}
	}

	// 3. Inspect test files for pitfalls
	var pitfalls []TDDPitfall

	for dir, tests := range testFilePaths {
		pDef := packages[dir]
		if pDef == nil {
			pDef = &pkgDef{
				Types:     make(map[string]*typeDef),
				Functions: make(map[string]bool),
				VarsConst: make(map[string]bool),
			}
		}

		for _, testPath := range tests {
			src, rErr := os.ReadFile(testPath)
			if rErr != nil {
				continue
			}

			testAst, parseErr := parser.ParseFile(fset, testPath, src, parser.ParseComments)
			if parseErr != nil {
				continue
			}

			filePitfalls := inspectTestFile(fset, testPath, testAst, pDef)
			pitfalls = append(pitfalls, filePitfalls...)
		}
	}

	return pitfalls, nil
}

func collectProdSymbols(file *ast.File, pDef *pkgDef) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					tName := s.Name.Name
					tDef := &typeDef{
						Name:      tName,
						Fields:    make(map[string]bool),
						Methods:   make(map[string]bool),
						IsPrivate: !unicode.IsUpper(rune(tName[0])),
					}

					switch t := s.Type.(type) {
					case *ast.StructType:
						tDef.IsStruct = true
						if t.Fields != nil {
							for _, f := range t.Fields.List {
								for _, name := range f.Names {
									tDef.Fields[name.Name] = true
								}
							}
						}
					case *ast.InterfaceType:
						if t.Methods != nil {
							for _, m := range t.Methods.List {
								for _, name := range m.Names {
									tDef.Methods[name.Name] = true
								}
							}
						}
					}
					pDef.Types[tName] = tDef

				case *ast.ValueSpec:
					for _, name := range s.Names {
						pDef.VarsConst[name.Name] = true
					}
				}
			}

		case *ast.FuncDecl:
			fnName := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				// Method declaration
				recvType := extractReceiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					if tDef, ok := pDef.Types[recvType]; ok {
						tDef.Methods[fnName] = true
					}
				}
			} else {
				pDef.Functions[fnName] = true
			}
		}
	}
}

func extractReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return extractReceiverTypeName(t.X)
	}
	return ""
}

func inspectTestFile(fset *token.FileSet, testPath string, file *ast.File, pDef *pkgDef) []TDDPitfall {
	var pitfalls []TDDPitfall

	// Phase 1: Collect any structs/types declared within the test file itself (e.g. stubs/fakes/mocks)
	testLocalTypes := make(map[string]*typeDef)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					tName := ts.Name.Name
					tDef := &typeDef{
						Name:    tName,
						Fields:  make(map[string]bool),
						Methods: make(map[string]bool),
					}
					if st, ok := ts.Type.(*ast.StructType); ok {
						tDef.IsStruct = true
						if st.Fields != nil {
							for _, f := range st.Fields.List {
								for _, fn := range f.Names {
									tDef.Fields[fn.Name] = true
								}
							}
						}
					}
					testLocalTypes[tName] = tDef
				}
			}
		case *ast.FuncDecl:
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := extractReceiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					if tDef, ok := testLocalTypes[recvType]; ok {
						tDef.Methods[d.Name.Name] = true
					}
				}
			}
		}
	}

	// Phase 2: Inspect functions with scoped variable tracking
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		scopedTypes := make(map[string]string)

		// Register receiver
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			rName := ""
			if len(fn.Recv.List[0].Names) > 0 {
				rName = fn.Recv.List[0].Names[0].Name
			}
			rType := extractReceiverTypeName(fn.Recv.List[0].Type)
			if rName != "" && rType != "" {
				scopedTypes[rName] = rType
			}
		}

		// Register parameters
		if fn.Type != nil && fn.Type.Params != nil {
			for _, p := range fn.Type.Params.List {
				pType := extractTypeNameFromExpr(p.Type)
				for _, pName := range p.Names {
					if pType != "" {
						scopedTypes[pName.Name] = pType
					}
				}
			}
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			pos := fset.Position(n.Pos())

			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, lh := range node.Lhs {
					ident, isIdent := lh.(*ast.Ident)
					if !isIdent || i >= len(node.Rhs) {
						continue
					}
					rhs := node.Rhs[i]
					typeName := extractTypeNameFromExpr(rhs)
					if typeName != "" {
						scopedTypes[ident.Name] = typeName
					}
				}

			case *ast.CompositeLit:
				// Struct literal: User{loyalty_points: 10}
				typeName := extractTypeNameFromExpr(node.Type)
				if typeName != "" {
					if _, isTestLocal := testLocalTypes[typeName]; !isTestLocal {
						if tDef, exists := pDef.Types[typeName]; exists && tDef.IsStruct {
							for _, elt := range node.Elts {
								if kv, ok := elt.(*ast.KeyValueExpr); ok {
									if kIdent, ok := kv.Key.(*ast.Ident); ok {
										fieldName := kIdent.Name
										if !tDef.Fields[fieldName] {
											// Fabricated field!
											symbol := fmt.Sprintf("%s.%s", typeName, fieldName)
											pitfalls = append(pitfalls, TDDPitfall{
												File:     testPath,
												Line:     pos.Line,
												Symbol:   symbol,
												Category: "fabricated",
												Message:  fmt.Sprintf("TEST REFERENCES UNDEFINED: %s at %s:%d — this is the TDD trap", symbol, testPath, pos.Line),
											})
										}
									}
								}
							}
						}
					}
				}

			case *ast.SelectorExpr:
				fieldName := node.Sel.Name
				var targetType string

				if xIdent, ok := node.X.(*ast.Ident); ok {
					if tName, ok := scopedTypes[xIdent.Name]; ok {
						targetType = tName
					} else if tDef, ok := pDef.Types[xIdent.Name]; ok {
						targetType = tDef.Name
					}
				}

				if targetType != "" {
					if _, isTestLocal := testLocalTypes[targetType]; !isTestLocal {
						if tDef, exists := pDef.Types[targetType]; exists && tDef.IsStruct {
							// Check 1: Fabricated field or method
							if !tDef.Fields[fieldName] && !tDef.Methods[fieldName] {
								symbol := fmt.Sprintf("%s.%s", targetType, fieldName)
								pitfalls = append(pitfalls, TDDPitfall{
									File:     testPath,
									Line:     pos.Line,
									Symbol:   symbol,
									Category: "fabricated",
									Message:  fmt.Sprintf("TEST REFERENCES UNDEFINED: %s at %s:%d — this is the TDD trap", symbol, testPath, pos.Line),
								})
							}

							// Check 2: Lock implementation details
							if tDef.Fields[fieldName] && isImplementationDetailField(fieldName) {
								symbol := fmt.Sprintf("%s.%s", targetType, fieldName)
								pitfalls = append(pitfalls, TDDPitfall{
									File:     testPath,
									Line:     pos.Line,
									Symbol:   symbol,
									Category: "locks-impl-detail",
									Message:  fmt.Sprintf("TEST LOCKS IMPLEMENTATION DETAIL: %s at %s:%d — test asserts on private internal state instead of public behavior", symbol, testPath, pos.Line),
								})
							}
						}
					}
				} else {
					if isImplementationDetailField(fieldName) {
						symbol := fieldName
						pitfalls = append(pitfalls, TDDPitfall{
							File:     testPath,
							Line:     pos.Line,
							Symbol:   symbol,
							Category: "locks-impl-detail",
							Message:  fmt.Sprintf("TEST LOCKS IMPLEMENTATION DETAIL: %s at %s:%d — test asserts on private internal state", symbol, testPath, pos.Line),
						})
					}
				}
			}

			return true
		})
	}

	return pitfalls
}

func isImplementationDetailField(name string) bool {
	if len(name) == 0 {
		return false
	}
	// Only unexported identifiers can be private implementation details
	if unicode.IsUpper(rune(name[0])) {
		return false
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "internal") ||
		strings.HasPrefix(lower, "privatestate") ||
		strings.Contains(lower, "connstatus") ||
		strings.Contains(lower, "lockstatus")
}

func extractTypeNameFromExpr(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return extractTypeNameFromExpr(t.X)
	case *ast.UnaryExpr:
		return extractTypeNameFromExpr(t.X)
	case *ast.CompositeLit:
		return extractTypeNameFromExpr(t.Type)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}
