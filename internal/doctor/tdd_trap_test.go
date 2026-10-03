package doctor

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTDDPitfallsDetectsFabricatedField(t *testing.T) {
	tempDir := t.TempDir()
	pkgDir := filepath.Join(tempDir, "internal", "user")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prodCode := `package user

type User struct {
	ID   string
	Name string
}

func GetUser(id string) *User {
	return &User{ID: id, Name: "Test"}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "user.go"), []byte(prodCode), 0o644); err != nil {
		t.Fatalf("write prod: %v", err)
	}

	testCode := `package user

import "testing"

func TestUserLoyaltyPoints(t *testing.T) {
	u := &User{
		ID:             "u1",
		loyalty_points: 100, // Fabricated field not in production User struct
	}
	if u.loyalty_points != 100 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "user_test.go"), []byte(testCode), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
	}

	pitfalls, err := CheckTDDPitfalls(tempDir)
	if err != nil {
		t.Fatalf("CheckTDDPitfalls failed: %v", err)
	}

	if len(pitfalls) == 0 {
		t.Fatalf("expected pitfalls to be detected, got 0")
	}

	var foundFabricated bool
	for _, p := range pitfalls {
		if p.Category == "fabricated" && strings.Contains(p.Symbol, "User.loyalty_points") {
			foundFabricated = true
			if !strings.Contains(p.Message, "TEST REFERENCES UNDEFINED") {
				t.Errorf("unexpected message: %s", p.Message)
			}
		}
	}

	if !foundFabricated {
		t.Errorf("expected fabricated field User.loyalty_points to be detected, got: %+v", pitfalls)
	}
}

func TestCheckTDDPitfallsDetectsImplDetailLock(t *testing.T) {
	tempDir := t.TempDir()
	pkgDir := filepath.Join(tempDir, "internal", "conn")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prodCode := `package conn

type Connection struct {
	Endpoint           string
	internalConnStatus int
}

func NewConnection(endpoint string) *Connection {
	return &Connection{Endpoint: endpoint, internalConnStatus: 1}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "conn.go"), []byte(prodCode), 0o644); err != nil {
		t.Fatalf("write prod: %v", err)
	}

	testCode := `package conn

import "testing"

func TestConnectionStatus(t *testing.T) {
	c := NewConnection("localhost:8080")
	if c.internalConnStatus != 1 { // Locks private implementation detail
		t.Errorf("expected status 1")
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "conn_test.go"), []byte(testCode), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
	}

	pitfalls, err := CheckTDDPitfalls(tempDir)
	if err != nil {
		t.Fatalf("CheckTDDPitfalls failed: %v", err)
	}

	if len(pitfalls) == 0 {
		t.Fatalf("expected pitfalls, got 0")
	}

	var foundImplLock bool
	for _, p := range pitfalls {
		if p.Category == "locks-impl-detail" && strings.Contains(p.Symbol, "internalConnStatus") {
			foundImplLock = true
			if !strings.Contains(p.Message, "TEST LOCKS IMPLEMENTATION DETAIL") {
				t.Errorf("unexpected message: %s", p.Message)
			}
		}
	}

	if !foundImplLock {
		t.Errorf("expected locks-impl-detail to be detected, got: %+v", pitfalls)
	}
}

func TestCheckTDDPitfallsCleanRepo(t *testing.T) {
	tempDir := t.TempDir()
	pkgDir := filepath.Join(tempDir, "internal", "math")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prodCode := `package math

type Calculator struct {
	Precision int
}

func (c *Calculator) Add(a, b int) int {
	return a + b
}
`
	testCode := `package math

import "testing"

func TestAdd(t *testing.T) {
	c := &Calculator{Precision: 2}
	if got := c.Add(1, 2); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}
`
	_ = os.WriteFile(filepath.Join(pkgDir, "math.go"), []byte(prodCode), 0o644)
	_ = os.WriteFile(filepath.Join(pkgDir, "math_test.go"), []byte(testCode), 0o644)

	pitfalls, err := CheckTDDPitfalls(tempDir)
	if err != nil {
		t.Fatalf("CheckTDDPitfalls failed: %v", err)
	}

	if len(pitfalls) != 0 {
		t.Errorf("expected 0 pitfalls on clean code, got %d: %+v", len(pitfalls), pitfalls)
	}
}

func TestIsImplementationDetailField_Matrix(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", false},
		{"ExportedField", false},
		{"InternalState", false}, // uppercase I -> exported
		{"internalState", true},
		{"privateStateMap", true},
		{"tcpConnStatus", true},
		{"agentLockStatus", true},
		{"regularField", false},
		{"status", false},
	}
	for _, tc := range cases {
		if got := isImplementationDetailField(tc.name); got != tc.want {
			t.Errorf("isImplementationDetailField(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExtractTypeNameFromExpr_Matrix(t *testing.T) {
	if got := extractTypeNameFromExpr(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.Ident{Name: "MyType"}); got != "MyType" {
		t.Errorf("expected MyType, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.StarExpr{X: &ast.Ident{Name: "StarType"}}); got != "StarType" {
		t.Errorf("expected StarType, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.UnaryExpr{X: &ast.Ident{Name: "UnaryType"}}); got != "UnaryType" {
		t.Errorf("expected UnaryType, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.CompositeLit{Type: &ast.Ident{Name: "CompType"}}); got != "CompType" {
		t.Errorf("expected CompType, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.SelectorExpr{Sel: &ast.Ident{Name: "SelType"}}); got != "SelType" {
		t.Errorf("expected SelType, got %q", got)
	}
	if got := extractTypeNameFromExpr(&ast.BasicLit{Value: "123"}); got != "" {
		t.Errorf("expected empty string for BasicLit, got %q", got)
	}
}

func TestExtractReceiverTypeName_Cases(t *testing.T) {
	if got := extractReceiverTypeName(nil); got != "" {
		t.Errorf("expected empty string for nil receiver, got %q", got)
	}
	if got := extractReceiverTypeName(&ast.Ident{Name: "Recv"}); got != "Recv" {
		t.Errorf("expected Recv, got %q", got)
	}
	if got := extractReceiverTypeName(&ast.StarExpr{X: &ast.Ident{Name: "StarRecv"}}); got != "StarRecv" {
		t.Errorf("expected StarRecv, got %q", got)
	}
	if got := extractReceiverTypeName(&ast.BasicLit{Value: "123"}); got != "" {
		t.Errorf("expected empty string for non-ident/non-star, got %q", got)
	}
}

func TestCheckTDDPitfalls_LocalTypesAndScopedParams(t *testing.T) {
	tempDir := t.TempDir()
	pkgDir := filepath.Join(tempDir, "internal", "localtest")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	prodCode := `package localtest

type Config struct {
	Timeout int
}

type Service interface {
	Run()
}

var DefaultPort = 8080
`
	if err := os.WriteFile(filepath.Join(pkgDir, "prod.go"), []byte(prodCode), 0o644); err != nil {
		t.Fatal(err)
	}

	testCode := `package localtest

import "testing"

type mockService struct {
	ran bool
}

func (m *mockService) Run() {
	m.ran = true
}

func helper(c Config) {
	_ = c.Timeout
}

func TestWithLocalMock(t *testing.T) {
	m := &mockService{ran: false}
	m.Run()
	_ = m.ran

	var unknownObj any
	_ = unknownObj.internalConnStatus
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "local_test.go"), []byte(testCode), 0o644); err != nil {
		t.Fatal(err)
	}

	pitfalls, err := CheckTDDPitfalls(tempDir)
	if err != nil {
		t.Fatalf("CheckTDDPitfalls failed: %v", err)
	}
	var found bool
	for _, p := range pitfalls {
		if p.Category == "locks-impl-detail" && strings.Contains(p.Symbol, "internalConnStatus") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected locks-impl-detail for unknownObj.internalConnStatus, got %+v", pitfalls)
	}
}
