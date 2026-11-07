// Package reflection defines the evidence-bound D-09 JUDGE contract.
package reflection

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrUnevidencedVerdict is returned for absent, null, or empty evidence_refs.
var ErrUnevidencedVerdict = errors.New("E_REFLEX_VERDICT_UNEVIDENCED")

//go:embed process-reflection-receipt.schema.json
var receiptSchemaJSON []byte

type Cycle struct {
	Timestamp time.Time `json:"timestamp"`
	Trigger   string    `json:"trigger"`
}

type JudgeIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Verdict struct {
	ArtifactPath string   `json:"artifact_path"`
	Dimension    string   `json:"dimension"`
	Verdict      string   `json:"verdict"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type ProcessReflectionReceipt struct {
	ReceiptID          string        `json:"receipt_id"`
	Cycle              Cycle         `json:"cycle"`
	Judge              JudgeIdentity `json:"judge"`
	Verdicts           []Verdict     `json:"verdicts"`
	AddList            []string      `json:"add_list"`
	PruneList          []string      `json:"prune_list"`
	PruneRatio         float64       `json:"prune_ratio"`
	SelfTombstoneAfter int           `json:"self_tombstone_after"`
}

// ComputedPruneRatio follows the existing reflex receipt convention. With no
// additions, the ratio is the prune count (including zero for an empty cycle).
func (r ProcessReflectionReceipt) ComputedPruneRatio() float64 {
	if len(r.AddList) == 0 {
		return float64(len(r.PruneList))
	}
	return float64(len(r.PruneList)) / float64(len(r.AddList))
}

type blockedLoader struct{}

func (blockedLoader) Load(uri string) (any, error) {
	return nil, fmt.Errorf("external reflection schema loading disabled: %s", uri)
}

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(blockedLoader{})
	compiler.AssertFormat()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(receiptSchemaJSON))
	if err != nil {
		return nil, err
	}
	const uri = "g8s://reflection/process-reflection-receipt.schema.json"
	if err := compiler.AddResource(uri, doc); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
})

// ValidateProcessReflectionReceipt validates the wire document, including all
// required fields and unknown fields at every object level. Evidence validation
// checks shape only: it never resolves files, row IDs, or remote resources.
func ValidateProcessReflectionReceipt(raw []byte) (*ProcessReflectionReceipt, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode reflection receipt: %w", err)
	}
	// Give missing evidence its stable typed error before generic schema errors.
	if obj, ok := doc.(map[string]any); ok {
		if rows, ok := obj["verdicts"].([]any); ok {
			for i, row := range rows {
				if verdict, ok := row.(map[string]any); ok {
					refs, present := verdict["evidence_refs"]
					list, isList := refs.([]any)
					if !present || refs == nil || (isList && len(list) == 0) {
						return nil, fmt.Errorf("%w: verdicts[%d]", ErrUnevidencedVerdict, i)
					}
				}
			}
		}
	}
	schema, err := compiledSchema()
	if err != nil {
		return nil, fmt.Errorf("compile reflection schema: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return nil, fmt.Errorf("reflection receipt: %w", err)
	}
	var receipt ProcessReflectionReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, err
	}
	if receipt.PruneRatio != receipt.ComputedPruneRatio() {
		return nil, fmt.Errorf("prune_ratio must equal computed ratio %g", receipt.ComputedPruneRatio())
	}
	return &receipt, nil
}

// Validate checks a Go value through the same strict wire contract.
func (r ProcessReflectionReceipt) Validate() error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = ValidateProcessReflectionReceipt(raw)
	return err
}

var artifactPathPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)

// ValidArtifactPath accepts portable repo-relative paths without dot segments.
func ValidArtifactPath(p string) bool {
	if !artifactPathPattern.MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
