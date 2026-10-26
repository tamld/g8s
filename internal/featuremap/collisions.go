package featuremap

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Collision recommendations (DELTA-41), ordered by severity.
const (
	RecommendParallelSafe = "parallel-safe"
	RecommendSplit        = "split"
	RecommendSerialize    = "serialize"
)

var recommendationRank = map[string]int{
	RecommendParallelSafe: 0,
	RecommendSplit:        1,
	RecommendSerialize:    2,
}

// PathTouch records which features and hotspots one scope pattern reaches.
type PathTouch struct {
	Path     string   `json:"path"`
	Features []string `json:"features,omitempty"`
	Hotspots []string `json:"hotspots,omitempty"`
}

// HotspotTouch records a hotspot reached by a scope.
type HotspotTouch struct {
	ID     string   `json:"id"`
	Policy string   `json:"policy"`
	Via    []string `json:"via"`
}

// ScopeAnalysis maps one write scope (receipt globs) onto the feature map.
type ScopeAnalysis struct {
	Label            string         `json:"label,omitempty"`
	Paths            []string       `json:"paths"`
	Features         []string       `json:"features"`
	CollisionDomains []string       `json:"collision_domains"`
	Hotspots         []HotspotTouch `json:"hotspots"`
	UnmappedPaths    []string       `json:"unmapped_paths,omitempty"`
	Touches          []PathTouch    `json:"touches"`
}

// SharedHotspot is a hotspot reached by both scopes, with the policy-derived action.
type SharedHotspot struct {
	ID     string   `json:"id"`
	Policy string   `json:"policy"`
	Action string   `json:"action"`
	ViaA   []string `json:"via_a"`
	ViaB   []string `json:"via_b"`
}

// PathOverlap is a pair of scope patterns that can match the same file.
type PathOverlap struct {
	A      string `json:"a"`
	B      string `json:"b"`
	Region string `json:"region"` // owned | hotspot | unmapped
}

// CollisionReport is the pre-dispatch verdict for two write scopes.
type CollisionReport struct {
	Recommendation         string          `json:"recommendation"`
	ScopeA                 ScopeAnalysis   `json:"scope_a"`
	ScopeB                 ScopeAnalysis   `json:"scope_b"`
	SharedFeatures         []string        `json:"shared_features"`
	SharedCollisionDomains []string        `json:"shared_collision_domains"`
	SharedHotspots         []SharedHotspot `json:"shared_hotspots"`
	PathOverlaps           []PathOverlap   `json:"path_overlaps"`
	Reasons                []string        `json:"reasons"`
	Advice                 []string        `json:"advice"`
}

// ParseScope splits a comma/whitespace separated list of globs into normalized patterns.
func ParseScope(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' ' || r == '\t'
	})
	var out []string
	seen := make(map[string]bool)
	for _, f := range fields {
		p := NormalizePattern(f)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AnalyzeScope maps a write scope onto owning features, collision domains, and hotspots.
func (fm *FeatureMap) AnalyzeScope(label string, paths []string) ScopeAnalysis {
	sa := ScopeAnalysis{
		Label:            label,
		Features:         []string{},
		CollisionDomains: []string{},
		Hotspots:         []HotspotTouch{},
		Touches:          []PathTouch{},
	}
	featureSet := make(map[string]bool)
	domainSet := make(map[string]bool)
	hotspotVia := make(map[string][]string)
	hotspotPolicy := make(map[string]string)

	for _, raw := range paths {
		p := NormalizePattern(raw)
		if p == "" {
			continue
		}
		sa.Paths = append(sa.Paths, p)
		touch := PathTouch{Path: p}
		if fm != nil {
			for i := range fm.Features {
				f := &fm.Features[i]
				if _, ok := f.OwnedSet().OverlapsPattern(p); ok {
					touch.Features = append(touch.Features, f.ID)
					featureSet[f.ID] = true
					domainSet[f.Domain()] = true
				}
			}
			for i := range fm.Hotspots {
				h := &fm.Hotspots[i]
				if _, ok := h.Set().OverlapsPattern(p); ok {
					touch.Hotspots = append(touch.Hotspots, h.ID)
					hotspotVia[h.ID] = append(hotspotVia[h.ID], p)
					hotspotPolicy[h.ID] = h.Policy
				}
			}
		}
		if len(touch.Features) == 0 && len(touch.Hotspots) == 0 {
			sa.UnmappedPaths = append(sa.UnmappedPaths, p)
		}
		sa.Touches = append(sa.Touches, touch)
	}
	sa.Features = sortedKeys(featureSet)
	sa.CollisionDomains = sortedKeys(domainSet)
	ids := make([]string, 0, len(hotspotVia))
	for id := range hotspotVia {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sa.Hotspots = append(sa.Hotspots, HotspotTouch{ID: id, Policy: hotspotPolicy[id], Via: hotspotVia[id]})
	}
	return sa
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	out := []string{}
	for _, y := range b {
		if set[y] {
			out = append(out, y)
			delete(set, y)
		}
	}
	sort.Strings(out)
	return out
}

func (fm *FeatureMap) coveredByHotspot(p string) bool {
	if fm == nil {
		return false
	}
	for i := range fm.Hotspots {
		if fm.Hotspots[i].Set().CoversPattern(p) {
			return true
		}
	}
	return false
}

// ErrEmptyScope is returned when a scope has no usable patterns.
var ErrEmptyScope = errors.New("write scope is empty")

// CompareScopes decides whether two write scopes may be dispatched to parallel
// workers. Severity is monotonic: serialize > split > parallel-safe.
//
//   - serialize: the scopes share a collision domain, or can write the same
//     non-hotspot file (owned or unmapped region).
//   - split: the only contact is a `serialize` hotspot (or an `owner:` hotspot
//     the scope does not own); carve the hotspot edit out of one scope and apply
//     it serially.
//   - parallel-safe: disjoint, or only `append-only` hotspots are shared.
//
// CompareScopes is a package-level wrapper around (*FeatureMap).CompareScopes.
func CompareScopes(fm *FeatureMap, labelA string, a []string, labelB string, b []string) (CollisionReport, error) {
	return fm.CompareScopes(labelA, a, labelB, b)
}

func (fm *FeatureMap) CompareScopes(labelA string, a []string, labelB string, b []string) (CollisionReport, error) {
	rep := CollisionReport{
		Recommendation:         RecommendParallelSafe,
		SharedFeatures:         []string{},
		SharedCollisionDomains: []string{},
		SharedHotspots:         []SharedHotspot{},
		PathOverlaps:           []PathOverlap{},
		Reasons:                []string{},
		Advice:                 []string{},
	}
	rep.ScopeA = fm.AnalyzeScope(labelA, a)
	rep.ScopeB = fm.AnalyzeScope(labelB, b)
	if len(rep.ScopeA.Paths) == 0 {
		return rep, fmt.Errorf("scope %s: %w", labelOr(labelA, "A"), ErrEmptyScope)
	}
	if len(rep.ScopeB.Paths) == 0 {
		return rep, fmt.Errorf("scope %s: %w", labelOr(labelB, "B"), ErrEmptyScope)
	}
	nameA := labelOr(labelA, "A")
	nameB := labelOr(labelB, "B")

	escalate := func(level, reason string) {
		if recommendationRank[level] > recommendationRank[rep.Recommendation] {
			rep.Recommendation = level
		}
		rep.Reasons = append(rep.Reasons, reason)
	}

	rep.SharedFeatures = intersect(rep.ScopeA.Features, rep.ScopeB.Features)
	rep.SharedCollisionDomains = intersect(rep.ScopeA.CollisionDomains, rep.ScopeB.CollisionDomains)
	for _, d := range rep.SharedCollisionDomains {
		escalate(RecommendSerialize, fmt.Sprintf("both scopes write collision domain %q", d))
	}

	for _, pa := range rep.ScopeA.Paths {
		for _, pb := range rep.ScopeB.Paths {
			if !GlobsOverlap(pa, pb) {
				continue
			}
			region := "owned"
			switch {
			case fm.coveredByHotspot(pa) || fm.coveredByHotspot(pb):
				region = "hotspot"
			case containsString(rep.ScopeA.UnmappedPaths, pa) && containsString(rep.ScopeB.UnmappedPaths, pb):
				region = "unmapped"
			}
			rep.PathOverlaps = append(rep.PathOverlaps, PathOverlap{A: pa, B: pb, Region: region})
			if region != "hotspot" {
				escalate(RecommendSerialize, fmt.Sprintf("scopes can write the same %s file(s): %q ∩ %q", region, pa, pb))
			}
		}
	}

	hotB := make(map[string]HotspotTouch, len(rep.ScopeB.Hotspots))
	for _, h := range rep.ScopeB.Hotspots {
		hotB[h.ID] = h
	}
	for _, ha := range rep.ScopeA.Hotspots {
		hb, ok := hotB[ha.ID]
		if !ok {
			continue
		}
		sh := SharedHotspot{ID: ha.ID, Policy: ha.Policy, ViaA: ha.Via, ViaB: hb.Via}
		switch {
		case ha.Policy == PolicyAppendOnly:
			sh.Action = "merge-append"
			rep.Advice = append(rep.Advice, fmt.Sprintf("hotspot %s is append-only: both scopes may append; merge the second PR after rebasing and resolve by union", ha.ID))
		case strings.HasPrefix(ha.Policy, PolicyOwnerPrefix):
			owner := strings.TrimSpace(strings.TrimPrefix(ha.Policy, PolicyOwnerPrefix))
			aOwns := containsString(rep.ScopeA.Features, owner)
			bOwns := containsString(rep.ScopeB.Features, owner)
			switch {
			case aOwns && !bOwns:
				sh.Action = fmt.Sprintf("split: %s keeps %s, %s routes its edit through owner %s", nameA, ha.ID, nameB, owner)
			case bOwns && !aOwns:
				sh.Action = fmt.Sprintf("split: %s keeps %s, %s routes its edit through owner %s", nameB, ha.ID, nameA, owner)
			default:
				sh.Action = fmt.Sprintf("split: neither scope uniquely owns %s; route both edits through owner %s serially", ha.ID, owner)
			}
			escalate(RecommendSplit, fmt.Sprintf("both scopes touch owner-governed hotspot %s (%s)", ha.ID, ha.Policy))
			rep.Advice = append(rep.Advice, sh.Action)
		default:
			sh.Action = "split"
			escalate(RecommendSplit, fmt.Sprintf("both scopes touch serialize hotspot %s (%s)", ha.ID, strings.Join(fm.hotspotPaths(ha.ID), ", ")))
			rep.Advice = append(rep.Advice, fmt.Sprintf("remove %s from %s's receipt and apply that edit serially after %s merges (or let F0 apply the single registration line); the remaining scopes are disjoint", strings.Join(hb.Via, ", "), nameB, nameA))
		}
		rep.SharedHotspots = append(rep.SharedHotspots, sh)
	}

	for _, sa := range []ScopeAnalysis{rep.ScopeA, rep.ScopeB} {
		if len(sa.UnmappedPaths) > 0 {
			rep.Advice = append(rep.Advice, fmt.Sprintf("scope %s has paths outside every owned_paths/hotspot (%s): declare ownership in .g8s/feature-map.yml before dispatch", labelOr(sa.Label, "?"), strings.Join(sa.UnmappedPaths, ", ")))
		}
	}

	switch rep.Recommendation {
	case RecommendParallelSafe:
		rep.Reasons = append(rep.Reasons, "no shared collision domain, no shared non-hotspot path, and no blocking hotspot")
	case RecommendSerialize:
		rep.Advice = append(rep.Advice, fmt.Sprintf("dispatch %s and %s sequentially (same write territory); re-run collisions after narrowing receipts", nameA, nameB))
	}
	return rep, nil
}

func (fm *FeatureMap) hotspotPaths(id string) []string {
	if fm == nil {
		return nil
	}
	for i := range fm.Hotspots {
		if fm.Hotspots[i].ID == id {
			return fm.Hotspots[i].Paths
		}
	}
	return nil
}

func labelOr(label, def string) string {
	if strings.TrimSpace(label) == "" {
		return def
	}
	return label
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
