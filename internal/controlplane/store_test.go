package controlplane

import "testing"

// #336: Windows file URIs must keep drive colons and separators literal
// while still escaping URI-significant characters (injection guarantee).
func TestSqlitePathEscapeFor(t *testing.T) {
	cases := []struct {
		goos, path, want string
	}{
		{"windows", `C:\Users\tamld\.local\state\g8s\g8s.db`, `C:\Users\tamld\.local\state\g8s\g8s.db`},
		{"windows", `C:\evil?x=1#g`, `C:\evil%3Fx=1%23g`},
		{"windows", `C:\pct%25path\db`, `C:\pct%2525path\db`},
		{"darwin", "/Users/tamld/state/g8s.db", "%2FUsers%2Ftamld%2Fstate%2Fg8s.db"},
		{"linux", "/opt/g8s/state.db", "%2Fopt%2Fg8s%2Fstate.db"},
	}
	for _, tc := range cases {
		if got := sqlitePathEscapeFor(tc.goos, tc.path); got != tc.want {
			t.Errorf("%s %q: got %q want %q", tc.goos, tc.path, got, tc.want)
		}
	}
}
