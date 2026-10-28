package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/featuremap"
	"github.com/tamld/g8s/internal/lane"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/vault"
)

// MasteryTier quantifies the Coordinator's experiential maturity in a feature domain (#690).
type MasteryTier string

const (
	MasteryTierNovice       MasteryTier = "NOVICE"
	MasteryTierPractitioner MasteryTier = "PRACTITIONER"
	MasteryTierExpert       MasteryTier = "EXPERT"
	MasteryTierMaster       MasteryTier = "MASTER"
)

// DomainMasteryStatus captures per-feature experiential metrics across Vault & Memory (#690).
type DomainMasteryStatus struct {
	FeatureID               string      `json:"feature_id"`
	Title                   string      `json:"title"`
	CollisionDomain         string      `json:"collision_domain"`
	MasteryTier             MasteryTier `json:"mastery_tier"`
	ActiveDoctrineCount     int         `json:"active_doctrine_count"`
	StaleAnchorCount        int         `json:"stale_anchor_count"`
	ContextualMemoryCount   int         `json:"contextual_memory_count"`
	AppliedCount            int         `json:"applied_count"`
	FirstPassSuccessCount   int         `json:"first_pass_success_count"`
	FailureAfterRecallCount int         `json:"failure_after_recall_count"`
	NeedsReviewCount        int         `json:"needs_review_count"`
	FirstPassVerifyRate     float64     `json:"first_pass_verify_rate"`
	RecommendedRigor        string      `json:"recommended_rigor"`
}

// MasteryScorecard aggregates experiential mastery across all repository feature nodes (#690).
type MasteryScorecard struct {
	TotalDomains     int                   `json:"total_domains"`
	MasterCount      int                   `json:"master_count"`
	ExpertCount      int                   `json:"expert_count"`
	PractitionerCnt  int                   `json:"practitioner_count"`
	NoviceCount      int                   `json:"novice_count"`
	OverallFirstPass float64               `json:"overall_first_pass_rate"`
	Domains          []DomainMasteryStatus `json:"domains"`
	GeneratedAt      string                `json:"generated_at"`
}

// CoordinatorIntakeRequest is the input to RunCoordinatorIntake (#688).
type CoordinatorIntakeRequest struct {
	Goal          string
	TargetFiles   []string
	ActiveContext map[string]string
	RepoRoot      string
	FeatureMap    *featuremap.FeatureMap
	Memory        *memory.LocalSQLiteMemoryAdapter
	Vault         *vault.Vault
}

// CoordinatorOrientation is Step 1 of the Coordinator Cognitive Intake (#688).
type CoordinatorOrientation struct {
	MatchedFeatures       []string                   `json:"matched_features"`
	CollisionDomains      []string                   `json:"collision_domains"`
	TransitiveBlastRadius []string                   `json:"transitive_blast_radius"`
	DerivedAllowedPaths   []string                   `json:"derived_allowed_paths"`
	GateLane              string                     `json:"gate_lane"`
	DomainMasteryTier     MasteryTier                `json:"domain_mastery_tier"`
	RecommendedRigor      string                     `json:"recommended_rigor"`
	VaultWhispers         []vault.DistillationRecord `json:"vault_whispers,omitempty"`
	RecalledMemories      []memory.RRFScoredEntry    `json:"recalled_memories,omitempty"`
}

// CoordinatorResearchGate is Step 2 (Pillar 1: Upstream/GitHub Research Gate) (#688).
type CoordinatorResearchGate struct {
	Required      bool     `json:"required"`
	Reason        string   `json:"reason"`
	ScoutCommands []string `json:"scout_commands"`
	FocusAreas    []string `json:"focus_areas"`
}

// CoordinatorDialecticalDeconstruction is Step 3 (Pillar 2: 3-Round Root-Core Loop) (#688).
type CoordinatorDialecticalDeconstruction struct {
	Level1SurfaceWhat  string   `json:"level_1_surface_what"`
	Level2MechanismHow string   `json:"level_2_mechanism_how"`
	Level3RootCoreWhy  string   `json:"level_3_root_core_why"`
	RootCoreProbes     []string `json:"root_core_probes"`
	SocraticAntithesis string   `json:"socratic_antithesis"`
	PersonaChecks      []string `json:"persona_checks"`
}

// CoordinatorRoutingPlan is Step 4 (Pillar 3: Deterministic Skill/Tool/Worker Routing) (#688).
type CoordinatorRoutingPlan struct {
	SkillChain       []string `json:"skill_chain"`
	NativeTools      []string `json:"native_tools"`
	PrimaryWorker    string   `json:"primary_worker"`
	BlindAdjudicator string   `json:"blind_adjudicator,omitempty"`
	VerifierClasses  []string `json:"verifier_classes"`
	LayerBudgetNote  string   `json:"layer_budget_note"`
}

// CoordinatorBrief is the complete output envelope of `g8s coordinator intake` (#688).
type CoordinatorBrief struct {
	Goal                 string                               `json:"goal"`
	Orientation          CoordinatorOrientation               `json:"orientation"`
	ResearchGate         CoordinatorResearchGate              `json:"research_gate"`
	DialecticalQuestions CoordinatorDialecticalDeconstruction `json:"dialectical_questions"`
	RoutingPlan          CoordinatorRoutingPlan               `json:"routing_plan"`
	GeneratedAt          string                               `json:"generated_at"`
}

// ComputeDomainMastery evaluates per-domain mastery tiers across .g8s/feature-map.yml (#690).
func ComputeDomainMastery(ctx context.Context, fm *featuremap.FeatureMap, mem *memory.LocalSQLiteMemoryAdapter, v *vault.Vault, repoRoot string) (*MasteryScorecard, error) {
	if fm == nil {
		return nil, fmt.Errorf("supervisor: feature map is required for domain mastery computation")
	}

	var allEntries []*memory.MemoryEntry
	if mem != nil {
		if list, err := mem.ListEntries(ctx, memory.MemoryFilter{}); err == nil {
			allEntries = list
		}
	}

	var allRecords []vault.DistillationRecord
	if v != nil {
		if recs, err := v.List(ctx, vault.VaultFilter{Limit: 200}); err == nil {
			allRecords = recs
		}
	}

	scorecard := &MasteryScorecard{
		TotalDomains: len(fm.Features),
		Domains:      make([]DomainMasteryStatus, 0, len(fm.Features)),
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	var totalApplied, totalFirstPass int

	for _, feat := range fm.Features {
		st := DomainMasteryStatus{
			FeatureID:       feat.ID,
			Title:           feat.Title,
			CollisionDomain: feat.CollisionDomain,
		}

		// Match Vault records by feature code_paths / owned_paths / go_packages
		for _, rec := range allRecords {
			if matchesFeatureRecord(feat, rec) {
				switch strings.ToUpper(strings.TrimSpace(rec.Status)) {
				case "DOCTRINE", "APPLIED":
					st.ActiveDoctrineCount++
				case "STALE_ANCHOR":
					st.StaleAnchorCount++
				}
			}
		}

		// Match Memory entries by preconditions (feature/domain/file/package) or payload
		for _, e := range allEntries {
			if e.Lifecycle == memory.LifecycleRevoked {
				continue
			}
			if matchesFeatureMemory(feat, e) {
				st.ContextualMemoryCount++
				st.AppliedCount += e.AppliedCount
				st.FirstPassSuccessCount += e.FirstPassSuccessCount
				st.FailureAfterRecallCount += e.FailureAfterRecallCount
				if e.ReviewFlag == memory.ReviewFlagNeedsDialecticalReview {
					st.NeedsReviewCount++
				}
			}
		}

		totalApplied += st.AppliedCount
		totalFirstPass += st.FirstPassSuccessCount

		if st.AppliedCount > 0 {
			st.FirstPassVerifyRate = float64(st.FirstPassSuccessCount) / float64(st.AppliedCount)
		} else if st.ActiveDoctrineCount > 0 {
			st.FirstPassVerifyRate = 1.0
		}

		switch {
		case st.ActiveDoctrineCount >= 2 && st.StaleAnchorCount == 0 && st.FirstPassVerifyRate >= 0.90 && st.NeedsReviewCount == 0:
			st.MasteryTier = MasteryTierMaster
			st.RecommendedRigor = "FAST_TRACK_VERIFIED"
			scorecard.MasterCount++
		case st.ActiveDoctrineCount >= 1 && st.StaleAnchorCount == 0 && st.FirstPassVerifyRate >= 0.75 && st.NeedsReviewCount == 0:
			st.MasteryTier = MasteryTierExpert
			st.RecommendedRigor = "STANDARD_DIALECTICAL"
			scorecard.ExpertCount++
		case st.ActiveDoctrineCount >= 1 || st.ContextualMemoryCount >= 1 || st.AppliedCount >= 1:
			st.MasteryTier = MasteryTierPractitioner
			st.RecommendedRigor = "DEEP_DIALECTICAL_3_ROUND"
			scorecard.PractitionerCnt++
		default:
			st.MasteryTier = MasteryTierNovice
			st.RecommendedRigor = "DEEP_DIALECTICAL_3_ROUND"
			scorecard.NoviceCount++
		}

		scorecard.Domains = append(scorecard.Domains, st)
	}

	if totalApplied > 0 {
		scorecard.OverallFirstPass = float64(totalFirstPass) / float64(totalApplied)
	} else {
		scorecard.OverallFirstPass = 1.0
	}

	return scorecard, nil
}

// RunCoordinatorIntake executes the end-to-end Coordinator Cognitive Intake,
// Problem Deconstruction & Deterministic Tool/Skill/Worker Routing pipeline (#688).
func RunCoordinatorIntake(ctx context.Context, req CoordinatorIntakeRequest) (*CoordinatorBrief, error) {
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return nil, fmt.Errorf("supervisor: goal is required for coordinator intake")
	}
	repoRoot := strings.TrimSpace(req.RepoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}

	fm := req.FeatureMap
	if fm == nil {
		loaded, err := featuremap.Load(filepath.Join(repoRoot, featuremap.DefaultFeatureMapPath))
		if err == nil {
			fm = loaded
		}
	}

	var matchedIDs []string
	var collisionDomains []string
	var blastIDs []string
	var derivedAllowed []string
	var matchedInvariants []string
	var matchedPackages []string

	seenFeat := map[string]bool{}
	seenDom := map[string]bool{}
	seenBlast := map[string]bool{}
	seenAllow := map[string]bool{}
	seenInv := map[string]bool{}
	seenPkg := map[string]bool{}

	if fm != nil {
		for _, f := range fm.Features {
			if matchesFeatureIntake(f, goal, req.TargetFiles, req.ActiveContext) {
				if !seenFeat[f.ID] {
					seenFeat[f.ID] = true
					matchedIDs = append(matchedIDs, f.ID)
				}
				if f.CollisionDomain != "" && !seenDom[f.CollisionDomain] {
					seenDom[f.CollisionDomain] = true
					collisionDomains = append(collisionDomains, f.CollisionDomain)
				}
				for _, inv := range f.Invariants {
					if !seenInv[inv] {
						seenInv[inv] = true
						matchedInvariants = append(matchedInvariants, inv)
					}
				}
				if f.Interfaces != nil {
					for _, pkg := range f.Interfaces.GoPackages {
						if !seenPkg[pkg] {
							seenPkg[pkg] = true
							matchedPackages = append(matchedPackages, pkg)
						}
					}
				}
				if trans, err := fm.ResolveTransitiveFeatures(f.ID); err == nil {
					for _, tn := range trans {
						if !seenBlast[tn.ID] {
							seenBlast[tn.ID] = true
							blastIDs = append(blastIDs, tn.ID)
						}
					}
				}
				if allowed, err := fm.DeriveAllowedPaths(f.ID); err == nil {
					for _, p := range allowed {
						if !seenAllow[p] {
							seenAllow[p] = true
							derivedAllowed = append(derivedAllowed, p)
						}
					}
				}
			}
		}
	}

	pathsForLane := req.TargetFiles
	if len(pathsForLane) == 0 {
		pathsForLane = derivedAllowed
	}
	laneRouter := lane.NewRouter(
		lane.WithBundlesPath(filepath.Join(repoRoot, lane.DefaultLaneBundlesPath)),
		lane.WithTrustBoundariesPath(filepath.Join(repoRoot, lane.DefaultTrustBoundariesPath)),
	)
	routedLane := laneRouter.Route(lane.Input{
		Paths:   pathsForLane,
		Summary: goal,
	})
	if routedLane == "" {
		routedLane = lane.LaneFeature
	}

	// Mastery lookup
	domainTier := MasteryTierNovice
	recommendedRigor := "DEEP_DIALECTICAL_3_ROUND"
	if fm != nil {
		if sc, err := ComputeDomainMastery(ctx, fm, req.Memory, req.Vault, repoRoot); err == nil && len(matchedIDs) > 0 {
			for _, d := range sc.Domains {
				if d.FeatureID == matchedIDs[0] {
					domainTier = d.MasteryTier
					recommendedRigor = d.RecommendedRigor
					break
				}
			}
		}
	}

	// Vault spatial whispers + Memory RRF recall
	var whispers []vault.DistillationRecord
	if req.Vault != nil {
		qPaths := req.TargetFiles
		if len(qPaths) == 0 {
			qPaths = matchedPackages
		}
		seenWhisper := map[string]bool{}
		for _, qp := range qPaths {
			if w, err := req.Vault.MatchSpatial(ctx, qp, 5); err == nil {
				for _, rec := range w {
					if !seenWhisper[rec.ID] {
						seenWhisper[rec.ID] = true
						whispers = append(whispers, rec)
					}
				}
			}
		}
	}

	var recalled []memory.RRFScoredEntry
	if req.Memory != nil {
		nowUnix := float64(time.Now().UnixNano()) / 1e9
		if r, err := req.Memory.RecallHybridRRF(ctx, goal, nil, req.ActiveContext, 5, nowUnix); err == nil {
			recalled = r
		}
	}

	// Step 2: Research Gate
	researchReq := routedLane == lane.LaneFeature || routedLane == lane.LaneSecurity || domainTier == MasteryTierNovice
	researchReason := fmt.Sprintf("GateLane=%s, DomainMastery=%s", routedLane, domainTier)
	scoutCmd := fmt.Sprintf("./bin/g8s submit --idempotency-key \"scout-intake-01\" --role scout --permission read_only --provider 9router --model docs-retriever --prompt %q", "Scan existing patterns and upstream conventions for: "+goal)

	// Step 3: 3-Round Dialectical Deconstruction
	invSummary := "Preserve zero-orphan feature-map ownership and single-binary self-containment"
	if len(matchedInvariants) > 0 {
		invSummary = strings.Join(matchedInvariants, "; ")
	}
	pkgSummary := "target domain packages"
	if len(matchedPackages) > 0 {
		pkgSummary = strings.Join(matchedPackages, ", ")
	}

	deconstruction := CoordinatorDialecticalDeconstruction{
		Level1SurfaceWhat:  fmt.Sprintf("Deliver objective: %s", goal),
		Level2MechanismHow: fmt.Sprintf("Extend existing structures inside %s (collision domains: %s) without creating orphan packages", pkgSummary, strings.Join(collisionDomains, ", ")),
		Level3RootCoreWhy:  fmt.Sprintf("Enforce root invariants: %s", invSummary),
		RootCoreProbes: []string{
			fmt.Sprintf("Round 1 (wiki-deep-why): Which existing types/functions in %s already solve 80%% of %q so we avoid duplicate assets?", pkgSummary, goal),
			fmt.Sprintf("Round 2 (wiki-socratic): What counter-example or environmental failure would falsify a naive implementation of %q?", goal),
			fmt.Sprintf("Round 3 (ck:predict): Does this change preserve Layer-Ownership (S + W + C <= 1) and zero external runtime coupling across %s?", strings.Join(matchedIDs, ", ")),
		},
		SocraticAntithesis: "Reject ad-hoc surface patches or external tool dependencies; require self-contained SQLite/AST verification with mechanical Red-to-Green tests.",
		PersonaChecks: []string{
			"Architecture: Verify .g8s/feature-map.yml ownership and S + W + C <= 1 layer disjointness",
			"Security: Enforce 0600 DB permissions, DLP credential scrubbing, and WriteReceipt path bounds",
			"Reliability: Ensure idempotent SQLite migrations and deterministic fallback when Jev is offline",
			"Performance: Bound queries and avoid O(N^2) scans on hot paths",
			"Operator DX: Provide actionable ErrPayload hints and --json envelope output",
		},
	}

	// Step 4: Deterministic Routing Plan
	primaryWorker := "9router/cu/composer-2.5 (fallback: 9router/deep-coding)"
	blindAdj := ""
	verifiers := []string{"test", "diff-guard"}
	switch routedLane {
	case lane.LaneDocs:
		primaryWorker = "9router/docs-retriever"
	case lane.LaneRefactor:
		primaryWorker = "9router/fast-agent"
	case lane.LaneSecurity:
		primaryWorker = "9router/deep-coding"
		blindAdj = "9router/reasoning-high (S3-A disjoint blind verifier)"
		verifiers = []string{"test", "diff-guard", "blind-review"}
	}

	routingPlan := CoordinatorRoutingPlan{
		SkillChain: []string{
			"ck:xia",
			"wiki-deep-why",
			"wiki-socratic",
			"ck:predict",
			"ck:plan",
			"prove",
			"ck:cook",
			"code-simplification",
			"g8s feature audit --live",
		},
		NativeTools: []string{
			"g8s search",
			"g8s read --tier skel",
			"g8s edit --batch",
			"g8s feature collisions",
			"g8s feature audit --live",
		},
		PrimaryWorker:    primaryWorker,
		BlindAdjudicator: blindAdj,
		VerifierClasses:  verifiers,
		LayerBudgetNote:  "Enforce S + W + C <= 1 per push wave (separate internal/controlplane|internal/state, internal/worker, and cmd/g8s commits if >1 touched).",
	}

	return &CoordinatorBrief{
		Goal: goal,
		Orientation: CoordinatorOrientation{
			MatchedFeatures:       matchedIDs,
			CollisionDomains:      collisionDomains,
			TransitiveBlastRadius: blastIDs,
			DerivedAllowedPaths:   derivedAllowed,
			GateLane:              string(routedLane),
			DomainMasteryTier:     domainTier,
			RecommendedRigor:      recommendedRigor,
			VaultWhispers:         whispers,
			RecalledMemories:      recalled,
		},
		ResearchGate: CoordinatorResearchGate{
			Required:      researchReq,
			Reason:        researchReason,
			ScoutCommands: []string{scoutCmd},
			FocusAreas: []string{
				"Local repository reuse check (asset-resolver-jit) in " + pkgSummary,
				"Upstream GitHub battle-tested reference architectures (ck:xia)",
			},
		},
		DialecticalQuestions: deconstruction,
		RoutingPlan:          routingPlan,
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func matchesFeatureRecord(feat featuremap.FeatureNode, rec vault.DistillationRecord) bool {
	file := filepath.ToSlash(strings.TrimSpace(rec.SpatialCoordinates.File))
	pkg := filepath.ToSlash(strings.TrimSpace(rec.SpatialCoordinates.Package))
	if feat.Interfaces != nil {
		for _, gp := range feat.Interfaces.GoPackages {
			if pkg == gp || strings.HasPrefix(file, gp+"/") {
				return true
			}
		}
	}
	for _, cp := range feat.CodePaths {
		prefix := strings.TrimSuffix(strings.TrimSuffix(cp, "/**"), "/*")
		if prefix != "" && (file == prefix || strings.HasPrefix(file, prefix+"/")) {
			return true
		}
	}
	return false
}

func matchesFeatureMemory(feat featuremap.FeatureNode, e *memory.MemoryEntry) bool {
	if e == nil {
		return false
	}
	if strings.EqualFold(e.Preconditions["feature"], feat.ID) ||
		strings.EqualFold(e.Preconditions["domain"], feat.CollisionDomain) {
		return true
	}
	file := filepath.ToSlash(strings.TrimSpace(e.Preconditions["file"]))
	pkg := filepath.ToSlash(strings.TrimSpace(e.Preconditions["package"]))
	if feat.Interfaces != nil {
		for _, gp := range feat.Interfaces.GoPackages {
			if pkg == gp || (file != "" && strings.HasPrefix(file, gp+"/")) || strings.Contains(e.Payload, gp) {
				return true
			}
		}
	}
	return false
}

func matchesFeatureIntake(feat featuremap.FeatureNode, goal string, targetFiles []string, activeCtx map[string]string) bool {
	if activeCtx != nil {
		if strings.EqualFold(activeCtx["feature"], feat.ID) || strings.EqualFold(activeCtx["domain"], feat.CollisionDomain) {
			return true
		}
	}
	for _, tf := range targetFiles {
		clean := filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(tf), "./"))
		if feat.Interfaces != nil {
			for _, gp := range feat.Interfaces.GoPackages {
				if clean == gp || strings.HasPrefix(clean, gp+"/") {
					return true
				}
			}
		}
		for _, cp := range feat.CodePaths {
			prefix := strings.TrimSuffix(strings.TrimSuffix(cp, "/**"), "/*")
			if prefix != "" && (clean == prefix || strings.HasPrefix(clean, prefix+"/")) {
				return true
			}
		}
	}
	lowerGoal := strings.ToLower(goal)
	if strings.Contains(lowerGoal, strings.ToLower(feat.ID)) ||
		(feat.CollisionDomain != "" && strings.Contains(lowerGoal, strings.ToLower(feat.CollisionDomain))) {
		return true
	}
	if feat.Interfaces != nil {
		for _, gp := range feat.Interfaces.GoPackages {
			if strings.Contains(lowerGoal, strings.ToLower(gp)) {
				return true
			}
		}
	}
	sort.Strings(targetFiles)
	return false
}
