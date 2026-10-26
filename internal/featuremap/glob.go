package featuremap

import (
	"path"
	"path/filepath"
	"strings"
)

// Glob semantics (DELTA-41):
//
//   - Patterns are slash-separated, repo-relative paths.
//   - A standalone `**` segment matches zero or more path segments.
//   - Within a segment, `*`, `?`, `[class]`, and `\x` escapes follow path.Match.
//   - A trailing `/` is shorthand for `/**`.
//
// Overlap and containment are decided purely on patterns (no filesystem access)
// so that write scopes for files that do not exist yet (new commands, new
// packages) can still be reasoned about before dispatch. Both decisions are
// conservative in the safe direction: GlobsOverlap may report a false positive
// for exotic character classes, and GlobCovers may report a false negative.

// NormalizePattern canonicalises a repo-relative glob pattern.
func NormalizePattern(p string) string {
	p = strings.TrimSpace(p)
	// Preserve glob character escapes (\*, \?, \[, \]) across filepath.ToSlash on Windows.
	const (
		escStar   = "\x00ESC_STAR\x00"
		escQmark  = "\x00ESC_QMARK\x00"
		escLBrack = "\x00ESC_LBRACK\x00"
		escRBrack = "\x00ESC_RBRACK\x00"
	)
	p = strings.ReplaceAll(p, `\*`, escStar)
	p = strings.ReplaceAll(p, `\?`, escQmark)
	p = strings.ReplaceAll(p, `\[`, escLBrack)
	p = strings.ReplaceAll(p, `\]`, escRBrack)
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, escStar, `\*`)
	p = strings.ReplaceAll(p, escQmark, `\?`)
	p = strings.ReplaceAll(p, escLBrack, `\[`)
	p = strings.ReplaceAll(p, escRBrack, `\]`)
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if strings.HasSuffix(p, "/") && p != "/" {
		p += "**"
	}
	return p
}

func splitSegments(p string) []string {
	p = NormalizePattern(p)
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func hasMeta(s string) bool {
	return strings.ContainsAny(s, "*?[\\")
}

// MatchPath reports whether the concrete repo-relative file path matches pattern.
func MatchPath(pattern, file string) bool {
	ps := splitSegments(pattern)
	fs := splitSegments(file)
	if len(ps) == 0 || len(fs) == 0 {
		return false
	}
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		key := [2]int{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var res bool
		switch {
		case i == len(ps):
			res = j == len(fs)
		case ps[i] == "**":
			res = rec(i+1, j) || (j < len(fs) && rec(i, j+1))
		case j == len(fs):
			res = false
		default:
			ok, err := path.Match(ps[i], fs[j])
			res = err == nil && ok && rec(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return rec(0, 0)
}

// GlobsOverlap reports whether there may exist a path matched by both patterns.
func GlobsOverlap(a, b string) bool {
	as := splitSegments(a)
	bs := splitSegments(b)
	if len(as) == 0 || len(bs) == 0 {
		return false
	}
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		key := [2]int{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var res bool
		switch {
		case i == len(as) && j == len(bs):
			res = true
		case i < len(as) && as[i] == "**":
			res = rec(i+1, j) || (j < len(bs) && rec(i, j+1))
		case j < len(bs) && bs[j] == "**":
			res = rec(i, j+1) || (i < len(as) && rec(i+1, j))
		case i == len(as) || j == len(bs):
			res = false
		default:
			res = segmentsOverlap(as[i], bs[j]) && rec(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return rec(0, 0)
}

// GlobCovers reports whether every path matched by inner is also matched by outer.
// The check is conservative: it may return false for exotic patterns that are in
// fact contained, but never returns true when containment does not hold for the
// supported syntax.
func GlobCovers(outer, inner string) bool {
	xs := splitSegments(outer)
	ys := splitSegments(inner)
	if len(xs) == 0 || len(ys) == 0 {
		return false
	}
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		key := [2]int{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var res bool
		switch {
		case i == len(xs):
			res = j == len(ys)
		case xs[i] == "**":
			res = rec(i+1, j) || (j < len(ys) && rec(i, j+1))
		case j == len(ys):
			res = false
		case ys[j] == "**":
			res = false
		default:
			res = segmentCovers(xs[i], ys[j]) && rec(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return rec(0, 0)
}

func segmentCovers(x, y string) bool {
	if x == y || x == "*" {
		return true
	}
	if !hasMeta(y) {
		ok, err := path.Match(x, y)
		return err == nil && ok
	}
	return false
}

type tokenKind int

const (
	tokLit tokenKind = iota
	tokAny
	tokClass
	tokStar
)

type globToken struct {
	kind  tokenKind
	ch    rune
	class string
}

func tokenizeSegment(s string) []globToken {
	rs := []rune(s)
	var toks []globToken
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '*':
			if len(toks) > 0 && toks[len(toks)-1].kind == tokStar {
				continue
			}
			toks = append(toks, globToken{kind: tokStar})
		case '?':
			toks = append(toks, globToken{kind: tokAny})
		case '\\':
			if i+1 < len(rs) {
				i++
			}
			toks = append(toks, globToken{kind: tokLit, ch: rs[i]})
		case '[':
			end := -1
			for k := i + 1; k < len(rs); k++ {
				if rs[k] == ']' && k > i+1 && (k != i+2 || rs[i+1] != '^') {
					end = k
					break
				}
			}
			if end < 0 {
				toks = append(toks, globToken{kind: tokLit, ch: rs[i]})
				continue
			}
			toks = append(toks, globToken{kind: tokClass, class: string(rs[i : end+1])})
			i = end
		default:
			toks = append(toks, globToken{kind: tokLit, ch: rs[i]})
		}
	}
	return toks
}

func tokensCompatible(a, b globToken) bool {
	switch {
	case a.kind == tokAny || b.kind == tokAny:
		return true
	case a.kind == tokLit && b.kind == tokLit:
		return a.ch == b.ch
	case a.kind == tokClass && b.kind == tokLit:
		ok, err := path.Match(a.class, string(b.ch))
		return err != nil || ok
	case a.kind == tokLit && b.kind == tokClass:
		ok, err := path.Match(b.class, string(a.ch))
		return err != nil || ok
	default:
		// class vs class: conservatively assume intersection.
		return true
	}
}

func segmentsOverlap(x, y string) bool {
	a := tokenizeSegment(x)
	b := tokenizeSegment(y)
	memo := make(map[[2]int]bool)
	seen := make(map[[2]int]bool)
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		key := [2]int{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		var res bool
		switch {
		case i == len(a) && j == len(b):
			res = true
		case i < len(a) && a[i].kind == tokStar:
			res = rec(i+1, j) || (j < len(b) && rec(i, j+1))
		case j < len(b) && b[j].kind == tokStar:
			res = rec(i, j+1) || (i < len(a) && rec(i+1, j))
		case i == len(a) || j == len(b):
			res = false
		default:
			res = tokensCompatible(a[i], b[j]) && rec(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return rec(0, 0)
}

// PathSet is an ordered set of include globs minus exclude globs. Entries in
// owned_paths / hotspot paths prefixed with `!` are exclusions.
type PathSet struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude,omitempty"`
}

// NewPathSet builds a PathSet from raw entries, treating `!`-prefixed entries as exclusions.
func NewPathSet(entries []string) PathSet {
	var s PathSet
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "!") {
			if p := NormalizePattern(strings.TrimPrefix(e, "!")); p != "" {
				s.Exclude = append(s.Exclude, p)
			}
			continue
		}
		if p := NormalizePattern(e); p != "" {
			s.Include = append(s.Include, p)
		}
	}
	return s
}

// Empty reports whether the set has no include patterns.
func (s PathSet) Empty() bool { return len(s.Include) == 0 }

// Match reports whether a concrete file is in the set.
func (s PathSet) Match(file string) bool {
	for _, ex := range s.Exclude {
		if MatchPath(ex, file) {
			return false
		}
	}
	for _, in := range s.Include {
		if MatchPath(in, file) {
			return true
		}
	}
	return false
}

func coveredByAny(patterns []string, p string) bool {
	for _, x := range patterns {
		if GlobCovers(x, p) {
			return true
		}
	}
	return false
}

func overlapsAny(patterns []string, p string) bool {
	for _, x := range patterns {
		if GlobsOverlap(x, p) {
			return true
		}
	}
	return false
}

// OverlapsPattern reports whether pattern p may match a path inside the set,
// returning the first include pattern responsible for the overlap.
func (s PathSet) OverlapsPattern(p string) (string, bool) {
	p = NormalizePattern(p)
	if coveredByAny(s.Exclude, p) {
		return "", false
	}
	for _, in := range s.Include {
		if GlobsOverlap(in, p) {
			return in, true
		}
	}
	return "", false
}

// CoversPattern reports whether every path matched by p is inside the set.
func (s PathSet) CoversPattern(p string) bool {
	p = NormalizePattern(p)
	if overlapsAny(s.Exclude, p) {
		return false
	}
	return coveredByAny(s.Include, p)
}

// OverlapsSet returns the first pair of include patterns (a from s, b from o)
// whose intersection is not removed by either set's exclusions.
func (s PathSet) OverlapsSet(o PathSet) (string, string, bool) {
	for _, a := range s.Include {
		if coveredByAny(o.Exclude, a) {
			continue
		}
		for _, b := range o.Include {
			if coveredByAny(s.Exclude, b) {
				continue
			}
			if GlobsOverlap(a, b) {
				return a, b, true
			}
		}
	}
	return "", "", false
}
