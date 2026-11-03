package reflex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// CodeReflectionUnverifiedVerdict is the error code for missing evidence refs.
	CodeReflectionUnverifiedVerdict = "E_REFLECTION_UNVERIFIED_VERDICT"
)

// ErrUnverifiedVerdict indicates a reflection verdict lacks verifiable evidence.
var ErrUnverifiedVerdict = errors.New("E_REFLECTION_UNVERIFIED_VERDICT: reflection verdict or action missing verifiable evidence_refs")

const processReflectionReceiptSchemaURI = "g8s://schemas/process-reflection-receipt-v1.schema.json"

// processReflectionReceiptSchemaJSON mirrors schemas/process-reflection-receipt-v1.schema.json byte-for-byte.
const processReflectionReceiptSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "g8s Process Reflection Receipt Schema v1",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "schema_version",
    "cycle_id",
    "evaluated_at",
    "jev_model",
    "jev_prompt_sha256",
    "consecutive_empty_prune_cycles",
    "prune_ratio",
    "verdicts",
    "prune_list",
    "add_list"
  ],
  "properties": {
    "schema_version": {
      "const": "1.0"
    },
    "cycle_id": {
      "type": "string",
      "minLength": 1
    },
    "evaluated_at": {
      "type": "string",
      "minLength": 1
    },
    "jev_model": {
      "type": "string",
      "minLength": 1
    },
    "jev_prompt_sha256": {
      "type": "string",
      "pattern": "^[a-f0-9]{64}$"
    },
    "consecutive_empty_prune_cycles": {
      "type": "integer",
      "minimum": 0
    },
    "prune_ratio": {
      "type": "number",
      "minimum": 0
    },
    "verdicts": {
      "type": "array",
      "items": {
        "$ref": "#/$defs/artifact_verdict"
      }
    },
    "prune_list": {
      "type": "array",
      "items": {
        "$ref": "#/$defs/prune_entry"
      }
    },
    "add_list": {
      "type": "array",
      "items": {
        "$ref": "#/$defs/add_proposal"
      }
    },
    "is_fallback": {
      "type": "boolean"
    },
    "batch_requests": {
      "type": "integer",
      "minimum": 0
    },
    "cache_hits": {
      "type": "integer",
      "minimum": 0
    }
  },
  "$defs": {
    "artifact_verdict": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "artifact_path",
        "artifact_sha256",
        "score",
        "theater_prob",
        "classification",
        "evidence_refs"
      ],
      "properties": {
        "artifact_path": {
          "type": "string",
          "minLength": 1
        },
        "artifact_sha256": {
          "type": "string",
          "pattern": "^[a-f0-9]{64}$"
        },
        "score": {
          "type": "number",
          "minimum": 0,
          "maximum": 5
        },
        "theater_prob": {
          "type": "number",
          "minimum": 0,
          "maximum": 1
        },
        "classification": {
          "type": "string",
          "enum": ["keep", "prune", "amend"]
        },
        "evidence_refs": {
          "type": "array",
          "items": {
            "type": "string",
            "minLength": 1
          },
          "minItems": 1
        },
        "source": {
          "type": "string",
          "enum": ["jev", "jev-cache", "deterministic"]
        },
        "rationale": {
          "type": "string"
        }
      }
    },
    "prune_entry": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "artifact_path",
        "artifact_sha256",
        "reason",
        "evidence_refs"
      ],
      "properties": {
        "artifact_path": {
          "type": "string",
          "minLength": 1
        },
        "artifact_sha256": {
          "type": "string",
          "pattern": "^[a-f0-9]{64}$"
        },
        "reason": {
          "type": "string",
          "minLength": 1
        },
        "evidence_refs": {
          "type": "array",
          "items": {
            "type": "string",
            "minLength": 1
          },
          "minItems": 1
        }
      }
    },
    "add_proposal": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "proposal_id",
        "target_path",
        "summary",
        "lane",
        "evidence_refs"
      ],
      "properties": {
        "proposal_id": {
          "type": "string",
          "minLength": 1
        },
        "target_path": {
          "type": "string",
          "minLength": 1
        },
        "summary": {
          "type": "string",
          "minLength": 1
        },
        "lane": {
          "type": "string",
          "enum": ["0", "D", "R", "F", "S"]
        },
        "evidence_refs": {
          "type": "array",
          "items": {
            "type": "string",
            "minLength": 1
          },
          "minItems": 1
        }
      }
    }
  }
}
`

// ProcessReflectionSchemaSHA256 returns the SHA-256 hex digest of the embedded receipt schema JSON.
func ProcessReflectionSchemaSHA256() string {
	return sha256Hex([]byte(processReflectionReceiptSchemaJSON))
}

// ArtifactVerdict is one D-09 artifact classification row.
type ArtifactVerdict struct {
	ArtifactPath   string   `json:"artifact_path"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Score          float64  `json:"score"`
	TheaterProb    float64  `json:"theater_prob"`
	Classification string   `json:"classification"`
	EvidenceRefs   []string `json:"evidence_refs"`
	Source         string   `json:"source,omitempty"`
	Rationale      string   `json:"rationale,omitempty"`
}

// PruneEntry records a prune recommendation with evidence.
type PruneEntry struct {
	ArtifactPath   string   `json:"artifact_path"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Reason         string   `json:"reason"`
	EvidenceRefs   []string `json:"evidence_refs"`
}

// AddProposal proposes a new artifact with lane routing.
type AddProposal struct {
	ProposalID   string   `json:"proposal_id"`
	TargetPath   string   `json:"target_path"`
	Summary      string   `json:"summary"`
	Lane         string   `json:"lane"`
	EvidenceRefs []string `json:"evidence_refs"`
}

// ProcessReflectionReceipt is the D-09 process reflection cycle output.
type ProcessReflectionReceipt struct {
	SchemaVersion               string            `json:"schema_version"`
	CycleID                     string            `json:"cycle_id"`
	EvaluatedAt                 string            `json:"evaluated_at"`
	JevModel                    string            `json:"jev_model"`
	JevPromptSHA256             string            `json:"jev_prompt_sha256"`
	ConsecutiveEmptyPruneCycles int               `json:"consecutive_empty_prune_cycles"`
	PruneRatio                  float64           `json:"prune_ratio"`
	Verdicts                    []ArtifactVerdict `json:"verdicts"`
	PruneList                   []PruneEntry      `json:"prune_list"`
	AddList                     []AddProposal     `json:"add_list"`
	IsFallback                  bool              `json:"is_fallback,omitempty"`
	BatchRequests               int               `json:"batch_requests,omitempty"`
	CacheHits                   int               `json:"cache_hits,omitempty"`
}

// SenseArtifact is input telemetry for one governance artifact.
type SenseArtifact struct {
	ArtifactPath   string
	ArtifactSHA256 string
	Category       string
	Excerpt        string
	UnfiredCycles  int
	EvidenceRefs   []string
}

// JudgeOptions configures JudgeProcessArtifacts.
type JudgeOptions struct {
	CycleID               string
	EvaluatedAt           time.Time
	PriorEmptyPruneCycles int
	AddProposals          []AddProposal
	KnownEvidenceRefs     map[string]bool
}

type reflectionBlockedURLLoader struct{}

func (reflectionBlockedURLLoader) Load(urlStr string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled: %s", urlStr)
}

var (
	processReflectionSchemaOnce sync.Once
	processReflectionSchema     *jsonschema.Schema
	processReflectionSchemaErr  error
)

func compiledProcessReflectionSchema() (*jsonschema.Schema, error) {
	processReflectionSchemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.DefaultDraft(jsonschema.Draft2020)
		c.UseLoader(reflectionBlockedURLLoader{})
		var doc any
		if err := json.Unmarshal([]byte(processReflectionReceiptSchemaJSON), &doc); err != nil {
			processReflectionSchemaErr = err
			return
		}
		if err := c.AddResource(processReflectionReceiptSchemaURI, doc); err != nil {
			processReflectionSchemaErr = err
			return
		}
		processReflectionSchema, processReflectionSchemaErr = c.Compile(processReflectionReceiptSchemaURI)
	})
	return processReflectionSchema, processReflectionSchemaErr
}

func evidenceRefsValid(refs []string, known map[string]bool) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return false
		}
		if known != nil && !known[ref] {
			return false
		}
	}
	return true
}

func unverifiedErr(detail string) error {
	return fmt.Errorf("%w: %s", ErrUnverifiedVerdict, detail)
}

func auditEvidenceOnReceipt(r *ProcessReflectionReceipt, known map[string]bool) error {
	for i, v := range r.Verdicts {
		if !evidenceRefsValid(v.EvidenceRefs, known) {
			return unverifiedErr(fmt.Sprintf("verdicts[%d] evidence_refs", i))
		}
	}
	for i, p := range r.PruneList {
		if !evidenceRefsValid(p.EvidenceRefs, known) {
			return unverifiedErr(fmt.Sprintf("prune_list[%d] evidence_refs", i))
		}
	}
	for i, a := range r.AddList {
		if !evidenceRefsValid(a.EvidenceRefs, known) {
			return unverifiedErr(fmt.Sprintf("add_list[%d] evidence_refs", i))
		}
	}
	return nil
}

// ValidateProcessReflectionReceipt validates evidence refs then Draft 2020-12 schema.
func ValidateProcessReflectionReceipt(rawJSON []byte, knownEvidenceRefs map[string]bool) (*ProcessReflectionReceipt, error) {
	var receipt ProcessReflectionReceipt
	if err := json.Unmarshal(rawJSON, &receipt); err != nil {
		return nil, fmt.Errorf("decode process reflection receipt: %w", err)
	}
	if err := auditEvidenceOnReceipt(&receipt, knownEvidenceRefs); err != nil {
		return nil, err
	}

	schema, err := compiledProcessReflectionSchema()
	if err != nil {
		return nil, fmt.Errorf("compile process reflection schema: %w", err)
	}
	var doc any
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		return nil, fmt.Errorf("decode receipt for schema validation: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}
	return &receipt, nil
}

// VerifyPromptAuditManifest ensures prompts file SHA is present and matches expected audit hash.
func VerifyPromptAuditManifest(promptsFilePath string, manifest map[string]string) error {
	norm := filepath.ToSlash(promptsFilePath)
	data, err := os.ReadFile(promptsFilePath)
	if err != nil {
		return fmt.Errorf("read prompts for audit: %w", err)
	}
	digest := sha256Hex(data)
	expected, ok := manifest[norm]
	if !ok {
		return fmt.Errorf("prompt audit manifest missing entry for %s", norm)
	}
	if expected != digest {
		return fmt.Errorf("prompt audit SHA drift for %s: manifest=%s disk=%s", norm, expected, digest)
	}
	if norm == ".g8s/jev-prompts.yml" && digest != DefaultPromptSHA256() {
		return fmt.Errorf("default prompts SHA drift: embedded=%s disk=%s", DefaultPromptSHA256(), digest)
	}
	return nil
}

func artifactPayload(a SenseArtifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "artifact_path: %s\n", a.ArtifactPath)
	fmt.Fprintf(&b, "artifact_sha256: %s\n", a.ArtifactSHA256)
	fmt.Fprintf(&b, "category: %s\n", a.Category)
	fmt.Fprintf(&b, "unfired_cycles: %d\n", a.UnfiredCycles)
	fmt.Fprintf(&b, "evidence_refs: %s\n", strings.Join(a.EvidenceRefs, ", "))
	fmt.Fprintf(&b, "excerpt:\n%s", a.Excerpt)
	return b.String()
}

func classifyReflection(score, theater float64, unfired int, th JevThresholds) string {
	if score >= th.ReflectionPruneMinScore || theater >= th.ReflectionPruneMinTheaterProb || unfired >= 3 {
		return "prune"
	}
	if score >= th.ReflectionAmendMinScore {
		return "amend"
	}
	return "keep"
}

func fallbackReflectionScores(unfired int) (score, theater float64, class string) {
	switch {
	case unfired >= 3:
		return 3.5, 0.70, "prune"
	case unfired == 2:
		return 2.2, 0.35, "amend"
	default:
		return 0.5, 0.05, "keep"
	}
}

// JudgeProcessArtifacts evaluates sense artifacts and returns a schema-valid receipt.
func (g *ReflexGate) JudgeProcessArtifacts(ctx context.Context, artifacts []SenseArtifact, opts JudgeOptions) (*ProcessReflectionReceipt, error) {
	known := opts.KnownEvidenceRefs
	for _, a := range artifacts {
		if !evidenceRefsValid(a.EvidenceRefs, known) {
			return nil, ErrUnverifiedVerdict
		}
	}
	for _, p := range opts.AddProposals {
		if !evidenceRefsValid(p.EvidenceRefs, known) {
			return nil, ErrUnverifiedVerdict
		}
	}

	cfg := g.PromptsConfig()
	d09 := cfg.Questions.D09ProcessReflection
	staleQ := d09["staleness_drift"]
	theaterQ := d09["theater_prob"]

	sharedState := map[string]any{
		"system_policy": cfg.Context.SystemPolicy,
		"must_reject":   cfg.Context.MustReject,
		"examples_good": cfg.Context.ExamplesGood,
		"examples_bad":  cfg.Context.ExamplesBad,
		"cycle_id":      opts.CycleID,
	}

	var batchQuestions []BatchQuestion
	for i, a := range artifacts {
		payload := artifactPayload(a)
		batchQuestions = append(batchQuestions,
			BatchQuestion{
				ID:               FormatBatchQuestionID(i, "staleness"),
				Type:             staleQ.Type,
				BaseInstructions: staleQ.Instructions,
				ItemPayload:      payload,
				Criteria:         staleQ.Criteria,
			},
			BatchQuestion{
				ID:               FormatBatchQuestionID(i, "theater"),
				Type:             theaterQ.Type,
				BaseInstructions: theaterQ.Instructions,
				ItemPayload:      payload,
				Criteria:         nil,
			},
		)
	}

	batchResult, _ := g.BatchEvaluate(ctx, sharedState, batchQuestions)

	evaluatedAt := opts.EvaluatedAt
	if evaluatedAt.IsZero() {
		evaluatedAt = time.Now().UTC()
	}

	receipt := ProcessReflectionReceipt{
		SchemaVersion:   "1.0",
		CycleID:         opts.CycleID,
		EvaluatedAt:     evaluatedAt.Format(time.RFC3339),
		JevModel:        g.Model(),
		JevPromptSHA256: g.PromptSHA256(),
		Verdicts:        make([]ArtifactVerdict, 0, len(artifacts)),
		PruneList:       []PruneEntry{},
		AddList:         opts.AddProposals,
		IsFallback:      batchResult.IsFallback,
		BatchRequests:   batchResult.RequestsSent,
		CacheHits:       batchResult.CacheHits,
	}
	if receipt.AddList == nil {
		receipt.AddList = []AddProposal{}
	}

	th := cfg.Thresholds
	source := "jev"
	if batchResult.IsFallback {
		source = "deterministic"
	} else if batchResult.RequestsSent == 0 && batchResult.CacheHits > 0 {
		source = "jev-cache"
	}

	for i, a := range artifacts {
		staleID := FormatBatchQuestionID(i, "staleness")
		theaterID := FormatBatchQuestionID(i, "theater")
		score, theater, class := fallbackReflectionScores(a.UnfiredCycles)
		if !batchResult.IsFallback {
			if ans, ok := batchResult.Answers[staleID]; ok {
				score = ans.Score
			}
			if ans, ok := batchResult.Answers[theaterID]; ok {
				theater = ans.Noul
			}
			class = classifyReflection(score, theater, a.UnfiredCycles, th)
		}

		verdict := ArtifactVerdict{
			ArtifactPath:   a.ArtifactPath,
			ArtifactSHA256: a.ArtifactSHA256,
			Score:          score,
			TheaterProb:    theater,
			Classification: class,
			EvidenceRefs:   append([]string(nil), a.EvidenceRefs...),
			Source:         source,
		}
		receipt.Verdicts = append(receipt.Verdicts, verdict)
		if class == "prune" {
			receipt.PruneList = append(receipt.PruneList, PruneEntry{
				ArtifactPath:   a.ArtifactPath,
				ArtifactSHA256: a.ArtifactSHA256,
				Reason:         fmt.Sprintf("staleness/theater classification=%s score=%.2f theater=%.2f", class, score, theater),
				EvidenceRefs:   append([]string(nil), a.EvidenceRefs...),
			})
		}
	}

	if len(receipt.PruneList) > 0 {
		receipt.ConsecutiveEmptyPruneCycles = 0
	} else {
		receipt.ConsecutiveEmptyPruneCycles = opts.PriorEmptyPruneCycles + 1
	}
	if len(receipt.AddList) == 0 {
		receipt.PruneRatio = float64(len(receipt.PruneList))
	} else {
		receipt.PruneRatio = float64(len(receipt.PruneList)) / float64(len(receipt.AddList))
	}

	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	validated, err := ValidateProcessReflectionReceipt(raw, known)
	if err != nil {
		return nil, err
	}
	return validated, nil
}
