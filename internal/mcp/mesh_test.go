package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/mesh"
)

func TestCoordinatorMeshNativeRegistrationAndParity(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "")

	s := NewServer(strings.NewReader(""), &bytes.Buffer{}, nil, nil, nil)

	// Verify tool is registered in tools/list
	resp := s.handleLine(context.Background(), requestLine("tools/list", nil))
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var listResult struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	foundInList := false
	for _, td := range listResult.Tools {
		if td.Name == "g8s_coordinator_mesh" {
			foundInList = true
			break
		}
	}
	if !foundInList {
		t.Fatalf("g8s_coordinator_mesh not found in tools/list response")
	}

	dir := t.TempDir()

	// 1. call s.coordinatorMeshTool register
	regArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "register",
		"input": {
			"node_id": "test-coordinator",
			"incarnation": "inc-001",
			"capabilities": {"role": "coordinator"}
		}
	}`, dir))
	if _, rpcErr := s.coordinatorMeshTool(context.Background(), regArgs); rpcErr != nil {
		t.Fatalf("coordinatorMeshTool register failed: %+v", rpcErr)
	}

	// 2. call s.coordinatorMeshTool quota-publish
	obsTime := time.Now().UTC().Format(time.RFC3339)
	quotaArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "quota-publish",
		"input": {
			"node_id": "test-coordinator",
			"incarnation": "inc-001",
			"observation": {
				"source": "coordinator-telemetry",
				"observed_at": %q,
				"windows": [
					{
						"name": "rolling-5h",
						"window_minutes": 300,
						"used_percent": null,
						"remaining_percent": null,
						"resets_at": null,
						"status": "unknown"
					}
				]
			}
		}
	}`, dir, obsTime))
	if _, rpcErr := s.coordinatorMeshTool(context.Background(), quotaArgs); rpcErr != nil {
		t.Fatalf("coordinatorMeshTool quota-publish failed: %+v", rpcErr)
	}

	// 3. call s.coordinatorMeshTool snapshot
	snapArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "snapshot",
		"input": {}
	}`, dir))
	res, rpcErr := s.coordinatorMeshTool(context.Background(), snapArgs)
	if rpcErr != nil {
		t.Fatalf("coordinatorMeshTool snapshot failed: %+v", rpcErr)
	}

	raw, ok := res.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage result, got %T", res)
	}

	// Validate exact node, null unknown percentages, and same source
	var snap struct {
		Revision int64 `json:"revision"`
		Nodes    []struct {
			NodeID       string         `json:"node_id"`
			Incarnation  string         `json:"incarnation"`
			Capabilities map[string]any `json:"capabilities"`
			ObservedAt   string         `json:"observed_at"`
		} `json:"nodes"`
		Quotas []struct {
			NodeID      string `json:"node_id"`
			Incarnation string `json:"incarnation"`
			Observation struct {
				Source     string `json:"source"`
				ObservedAt string `json:"observed_at"`
				Windows    []struct {
					Name             string   `json:"name"`
					WindowMinutes    int      `json:"window_minutes"`
					UsedPercent      *float64 `json:"used_percent"`
					RemainingPercent *float64 `json:"remaining_percent"`
					ResetsAt         *string  `json:"resets_at"`
					Status           string   `json:"status"`
				} `json:"windows"`
			} `json:"observation"`
		} `json:"quotas"`
		Claims []any `json:"claims"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal snapshot JSON: %v", err)
	}

	if len(snap.Nodes) != 1 {
		t.Fatalf("expected exactly 1 node, got %d", len(snap.Nodes))
	}
	if snap.Nodes[0].NodeID != "test-coordinator" {
		t.Fatalf("expected node_id %q, got %q", "test-coordinator", snap.Nodes[0].NodeID)
	}
	if snap.Nodes[0].Incarnation != "inc-001" {
		t.Fatalf("expected incarnation %q, got %q", "inc-001", snap.Nodes[0].Incarnation)
	}

	if len(snap.Quotas) != 1 {
		t.Fatalf("expected exactly 1 quota entry, got %d", len(snap.Quotas))
	}
	if snap.Quotas[0].NodeID != "test-coordinator" || snap.Quotas[0].Incarnation != "inc-001" {
		t.Fatalf("unexpected quota node: %+v", snap.Quotas[0])
	}
	if snap.Quotas[0].Observation.Source != "coordinator-telemetry" {
		t.Fatalf("expected source %q, got %q", "coordinator-telemetry", snap.Quotas[0].Observation.Source)
	}
	if len(snap.Quotas[0].Observation.Windows) != 1 {
		t.Fatalf("expected 1 quota window, got %d", len(snap.Quotas[0].Observation.Windows))
	}
	win := snap.Quotas[0].Observation.Windows[0]
	if win.Status != "unknown" {
		t.Fatalf("expected window status 'unknown', got %q", win.Status)
	}
	if win.UsedPercent != nil {
		t.Fatalf("expected null UsedPercent for unknown window, got %v", *win.UsedPercent)
	}
	if win.RemainingPercent != nil {
		t.Fatalf("expected null RemainingPercent for unknown window, got %v", *win.RemainingPercent)
	}

	// Compare semantic output with core Open.Execute snapshot
	coreStore, err := mesh.Open(dir)
	if err != nil {
		t.Fatalf("mesh.Open failed: %v", err)
	}
	defer coreStore.Close()

	coreRaw, err := coreStore.Execute(context.Background(), "snapshot", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("coreStore.Execute snapshot failed: %v", err)
	}

	var mcpParsed, coreParsed map[string]any
	if err := json.Unmarshal(raw, &mcpParsed); err != nil {
		t.Fatalf("unmarshal MCP snapshot: %v", err)
	}
	if err := json.Unmarshal(coreRaw, &coreParsed); err != nil {
		t.Fatalf("unmarshal core snapshot: %v", err)
	}
	if !reflect.DeepEqual(mcpParsed, coreParsed) {
		t.Fatalf("semantic mismatch between MCP output and core output:\nMCP:  %s\nCore: %s", string(raw), string(coreRaw))
	}

	// MCP prune dry-run parity
	sendArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "send",
		"input": {
			"node_id": "test-coordinator",
			"incarnation": "inc-001",
			"message_id": "parity-msg",
			"to_node": "test-coordinator",
			"to_incarnation": "inc-001",
			"kind": "note",
			"payload": {"text": "prune parity"}
		}
	}`, dir))
	if _, rpcErr := s.coordinatorMeshTool(context.Background(), sendArgs); rpcErr != nil {
		t.Fatalf("coordinatorMeshTool send failed: %+v", rpcErr)
	}
	ackArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "ack",
		"input": {
			"node_id": "test-coordinator",
			"incarnation": "inc-001",
			"message_id": "parity-msg"
		}
	}`, dir))
	if _, rpcErr := s.coordinatorMeshTool(context.Background(), ackArgs); rpcErr != nil {
		t.Fatalf("coordinatorMeshTool ack failed: %+v", rpcErr)
	}

	pruneArgs := json.RawMessage(fmt.Sprintf(`{
		"mesh_dir": %q,
		"operation": "prune",
		"input": {"older_than": "168h", "dry_run": true}
	}`, dir))
	pruneRes, rpcErr := s.coordinatorMeshTool(context.Background(), pruneArgs)
	if rpcErr != nil {
		t.Fatalf("coordinatorMeshTool prune dry-run failed: %+v", rpcErr)
	}
	pruneRaw, ok := pruneRes.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage prune result, got %T", pruneRes)
	}
	corePruneRaw, err := coreStore.Execute(context.Background(), "prune", json.RawMessage(`{"older_than":"168h","dry_run":true}`))
	if err != nil {
		t.Fatalf("coreStore.Execute prune dry-run failed: %v", err)
	}
	var mcpPrune, corePrune map[string]any
	if err := json.Unmarshal(pruneRaw, &mcpPrune); err != nil {
		t.Fatalf("unmarshal MCP prune: %v", err)
	}
	if err := json.Unmarshal(corePruneRaw, &corePrune); err != nil {
		t.Fatalf("unmarshal core prune: %v", err)
	}
	if !reflect.DeepEqual(mcpPrune, corePrune) {
		t.Fatalf("prune dry-run mismatch MCP=%s core=%s", string(pruneRaw), string(corePruneRaw))
	}
}

func TestCoordinatorMeshMCPEnumMatchesMeshHandlers(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_STRATUM", "")

	s := NewServer(strings.NewReader(""), &bytes.Buffer{}, nil, nil, nil)
	resp := s.handleLine(context.Background(), requestLine("tools/list", nil))
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var listResult struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	var meshTool *toolDescriptor
	for i := range listResult.Tools {
		if listResult.Tools[i].Name == "g8s_coordinator_mesh" {
			meshTool = &listResult.Tools[i]
			break
		}
	}
	if meshTool == nil {
		t.Fatal("g8s_coordinator_mesh not found in tools/list")
	}
	var schema struct {
		Properties struct {
			Operation struct {
				Enum []string `json:"enum"`
			} `json:"operation"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(meshTool.InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal coordinator mesh input schema: %v", err)
	}
	want := mesh.Operations()
	got := append([]string(nil), schema.Properties.Operation.Enum...)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP operation enum mismatch: got %v want %v", got, want)
	}
}

func TestCoordinatorMeshRejectsWorkerAndUnknownArgs(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{}, nil, nil, nil)
	validArgs := func(dir string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"mesh_dir":%q,"operation":"snapshot","input":{}}`, dir))
	}

	t.Run("F1 denies", func(t *testing.T) {
		t.Setenv("G8S_TIER", "F1")
		_, rpcErr := s.coordinatorMeshTool(context.Background(), validArgs("/tmp/test"))
		if rpcErr == nil {
			t.Fatal("expected denial when G8S_TIER=F1, got nil error")
		}
	})

	t.Run("F2 denies", func(t *testing.T) {
		t.Setenv("G8S_TIER", "F2")
		_, rpcErr := s.coordinatorMeshTool(context.Background(), validArgs("/tmp/test"))
		if rpcErr == nil {
			t.Fatal("expected denial when G8S_TIER=F2, got nil error")
		}
	})

	t.Run("unknown JSON field denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"snapshot","input":{},"unknown_field":"rejected"}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for unknown JSON field, got nil error")
		}
	})

	t.Run("missing mesh_dir denies", func(t *testing.T) {
		args := json.RawMessage(`{"operation":"snapshot","input":{}}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for missing mesh_dir, got nil error")
		}
	})

	t.Run("empty mesh_dir denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"","operation":"snapshot","input":{}}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for empty mesh_dir, got nil error")
		}
	})

	t.Run("trailing bytes denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"snapshot","input":{}} trailing`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for trailing bytes, got nil error")
		}
	})

	t.Run("missing operation denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","input":{}}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for missing operation, got nil error")
		}
	})

	t.Run("invalid operation denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"invalid_op","input":{}}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for invalid operation, got nil error")
		}
	})

	t.Run("missing input denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"snapshot"}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for missing input, got nil error")
		}
	})

	t.Run("null input denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"snapshot","input":null}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for null input, got nil error")
		}
	})

	t.Run("non-object input denies", func(t *testing.T) {
		args := json.RawMessage(`{"mesh_dir":"/tmp/test","operation":"snapshot","input":"string"}`)
		_, rpcErr := s.coordinatorMeshTool(context.Background(), args)
		if rpcErr == nil {
			t.Fatal("expected denial for non-object input, got nil error")
		}
	})
}
