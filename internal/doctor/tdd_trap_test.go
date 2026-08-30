package doctor

import (
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
