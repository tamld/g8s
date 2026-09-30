package probe

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression for the v0.13.0 release-gate catch: Probe.Runner is a func
// field; an inline-assigned runner made json.Marshal(DefaultSuite) fail
// with "unsupported type: func(...)" and `g8s eval list` printed NOTHING
// (the envelope writer swallowed the marshal error). The suite must stay
// JSON-marshalable — the eval CLI surface depends on it.
func TestDefaultSuiteJSONMarshalable(t *testing.T) {
	raw, err := json.Marshal(DefaultSuite())
	if err != nil {
		t.Fatalf("json.Marshal(DefaultSuite): %v", err)
	}
	if !strings.Contains(string(raw), "Default Adversarial Probe Suite") {
		t.Errorf("marshaled suite missing suite name")
	}
}
