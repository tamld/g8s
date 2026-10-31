package cli

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

const envelopePropMaxCases = 200

func envelopePropRand() *rand.Rand {
	return rand.New(rand.NewSource(44))
}

func decodeEnvelopeSafe(raw []byte) (env Envelope, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during decode: %v", r)
		}
	}()
	err = json.Unmarshal(raw, &env)
	return env, err
}

func selectPathSafe(raw []byte, expr string) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during select: %v", r)
		}
	}()
	return SelectPath(raw, expr)
}

func TestEnvelopeDecodePropertyFuzzNeverPanics(t *testing.T) {
	r := envelopePropRand()
	cfg := &quick.Config{MaxCount: envelopePropMaxCases, Rand: r}
	err := quick.Check(func(n uint16) bool {
		buf := make([]byte, n%512)
		for i := range buf {
			buf[i] = byte(r.Intn(256))
		}
		_, _ = decodeEnvelopeSafe(buf)
		_, _ = selectPathSafe(buf, ".v")
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnvelopePropertyMarshalRoundtripIdentity(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	orig := NewEnvelope("task", "submit", "run", map[string]any{
		"task_id": "t-prop-1",
		"n":       42,
	})
	orig.At = at
	orig.TraceID = "trace-prop-1"

	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dec, err := decodeEnvelopeSafe(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if dec.V != orig.V || dec.Kind != orig.Kind || dec.Command != orig.Command || dec.Subcommand != orig.Subcommand {
		t.Fatalf("field mismatch: orig=%+v dec=%+v", orig, dec)
	}
	if dec.TraceID != orig.TraceID {
		t.Fatalf("trace_id: got %q want %q", dec.TraceID, orig.TraceID)
	}
	data, ok := dec.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", dec.Data)
	}
	if data["task_id"] != "t-prop-1" {
		t.Fatalf("task_id corrupted: %v", data["task_id"])
	}
}

func TestEnvelopePropertyUnknownFieldsDoNotCorruptKnown(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	base := NewEnvelope("read", "read", "", map[string]string{"path": "README.md"})
	base.At = at
	base.V = CurrentEnvelopeVersion

	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Inject unknown top-level fields; encoding/json ignores them on decode but must not break known fields.
	withExtra := strings.TrimSpace(string(raw))
	withExtra = strings.TrimSuffix(withExtra, "}") + `,"unknown_field":"noise","extra":123}`
	dec, err := decodeEnvelopeSafe([]byte(withExtra))
	if err != nil {
		t.Fatalf("decode with unknown fields: %v", err)
	}
	if dec.V != CurrentEnvelopeVersion || dec.Kind != "read" || dec.Command != "read" {
		t.Fatalf("known fields corrupted: %+v", dec)
	}
	payload, ok := dec.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", dec.Data)
	}
	if payload["path"] != "README.md" {
		t.Fatalf("data.path corrupted: %v", payload["path"])
	}
}

func TestEnvelopePropertyQuickGeneratedRoundtrip(t *testing.T) {
	r := envelopePropRand()
	cfg := &quick.Config{MaxCount: 50, Rand: r}
	err := quick.Check(func(kindIdx, cmdIdx uint8) bool {
		kind := fmt.Sprintf("kind-%d", kindIdx%10)
		cmd := fmt.Sprintf("cmd-%d", cmdIdx%10)
		env := NewEnvelope(kind, cmd, "sub", map[string]any{"x": int(kindIdx)})
		raw, mErr := json.Marshal(env)
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		dec, dErr := decodeEnvelopeSafe(raw)
		if dErr != nil {
			t.Fatalf("unmarshal: %v", dErr)
		}
		if dec.Kind != kind || dec.Command != cmd {
			t.Fatalf("roundtrip mismatch kind=%q cmd=%q got kind=%q cmd=%q", kind, cmd, dec.Kind, dec.Command)
		}
		return true
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
}
