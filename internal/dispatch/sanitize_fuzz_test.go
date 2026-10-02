package dispatch

import (
	"fmt"
	"strings"
	"testing"
)

func FuzzSanitizeOutput(f *testing.F) {
	// Seed 1: Escaped newline/quote pairs (#434 case 1 & 2)
	f.Add([]byte(`{"type":"item.completed","text":"set password: abc\"def\" here"}`))
	f.Add([]byte(`{"code":"def public_fixture(u):\n    token = False\n    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`))
	f.Add([]byte(`password:\"example-value-77\"`))

	// Seed 2: Nested JSONL
	f.Add([]byte("{\"event\":\"step\",\"data\":\"{\\\"key\\\":\\\"val\\\"}\"}\n{\"event\":\"result\",\"data\":\"{\\\"status\\\":\\\"OK\\\"}\"}"))

	// Seed 3: Empty input
	f.Add([]byte(""))

	// Seed 4: Very long input with mixed public literals and credentials
	f.Add([]byte(strings.Repeat("token = False and password = supersecret12345! ", 200)))

	// Seed 5: Additional #434 corpus cases
	f.Add([]byte(`{"code":"token = False\ngood = True"}`))
	f.Add([]byte(`{"code":"x = f(token=False, retries=0, timeout=None)"}`))
	f.Add([]byte(`{"cred":"credential=example-value-77\nnext chunk"}`))
	f.Add([]byte("{\"dsn\":\"postgresql://user:pass@db.example.com/sales\\n\"}"))

	// Incident #434 corpus cases that contain poison secrets to be redacted
	corpus434PoisonCases := []string{
		`{"type":"item.completed","text":"set password: abc\"def\" here"}`,
		`{"cred":"credential=example-value-77\nnext chunk"}`,
		"{\"dsn\":\"postgresql://user:pass@db.example.com/sales\\n\"}",
		`password:\"example-value-77\"`,
		`password: abc\"def\"`,
	}

	rawPoisonLiterals := []string{
		"example-value-77",
		`abc"def"`,
		`abc\"def\"`,
		"user:pass",
	}

	publicLiterals := []string{
		"token = False",
		"token=False",
		"retries=0",
		"retries = 0",
		"timeout=None",
		"timeout = None",
		"good = True",
		"token=nil",
		"token=null",
		"password=None",
		"secret = False",
		"token = False\n",
		`{"code":"def public_fixture(u):\n    token = False\n    return u.password is None and token is False\n","quoted_data":"fixture-payload-0042"}`,
		`{"source_lines":["def public_fixture(u):","    token = False","    return u.password is None and token is False"]}`,
		`{"code":"token = False\ngood = True"}`,
		`{"code":"x = f(token=False, retries=0, timeout=None)"}`,
		"loader = pipeline(model, token=False, trust_remote_code=None, retries=0)",
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		text := string(data)

		// 1. Never panic on arbitrary input
		out := SanitizeOutput(text)
		_ = out

		// 2. Idempotence: sanitize(sanitize(x)) == sanitize(x) for public-literal cases
		for _, pub := range publicLiterals {
			s1 := SanitizeOutput(pub)
			s2 := SanitizeOutput(s1)
			if s1 != s2 {
				t.Fatalf("idempotence violated for public literal %q: sanitize(x)=%q, sanitize(sanitize(x))=%q", pub, s1, s2)
			}
			if s1 != pub {
				t.Fatalf("public literal altered: want %q, got %q", pub, s1)
			}
		}

		// Also check idempotence with fuzz variations if text is a recognized public literal
		core := strings.TrimSpace(text)
		if i := strings.IndexByte(core, '\\'); i >= 0 {
			core = core[:i]
		}
		if publicLiteralPattern.MatchString(core) {
			pubExpr := fmt.Sprintf("token = %s", text)
			p1 := SanitizeOutput(pubExpr)
			p2 := SanitizeOutput(p1)
			if p1 != p2 {
				t.Fatalf("idempotence violated for generated public literal %q: %q != %q", pubExpr, p1, p2)
			}
		}

		// 3. Output never contains raw poison literals from the #434 corpus after sanitization
		for _, testCase := range corpus434PoisonCases {
			sanitizedCase := SanitizeOutput(testCase)
			for _, poison := range rawPoisonLiterals {
				if strings.Contains(sanitizedCase, poison) {
					t.Fatalf("raw poison literal %q leaked after sanitizing #434 corpus case %q: got %q", poison, testCase, sanitizedCase)
				}
			}
		}

		// Also verify with credential assignment wrappers
		for _, poison := range rawPoisonLiterals {
			poisonAssign := fmt.Sprintf("password: %s", poison)
			sanitized := SanitizeOutput(poisonAssign)
			if strings.Contains(sanitized, poison) {
				t.Fatalf("raw poison literal %q leaked after sanitizing %q: got %q", poison, poisonAssign, sanitized)
			}

			urlPoison := fmt.Sprintf("postgresql://%s@db.example.com/db", poison)
			sanitizedURL := SanitizeOutput(urlPoison)
			if strings.Contains(sanitizedURL, poison) {
				t.Fatalf("raw poison literal %q leaked after sanitizing URL %q: got %q", poison, urlPoison, sanitizedURL)
			}
		}
	})
}
