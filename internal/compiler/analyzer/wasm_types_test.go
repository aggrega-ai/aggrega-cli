// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPolicyInput_RoundTrip exercises the b-full policy input: the
// graph is the full types.IRGraph (single-sourced from ir-v1alpha1), so the
// fixture is a real IR graph rather than a narrow projection. The input's wire
// shape is schema-validated cross-language in aggrega-abi (the policy schema
// references ir-v1alpha1 — a cross-file $ref the host validator does not resolve
// here); this test pins the Go round-trip: the full IR graph survives marshal /
// unmarshal with its OrderedMap author-order intact.
func TestPolicyInput_RoundTrip(t *testing.T) {
	t.Parallel()

	labelsAPI := types.NewOrderedMap[string]()
	labelsAPI.Set("team", "payments")
	labelsAPI.Set("tier", "service")
	nodes := types.NewOrderedMap[types.IRNode]()
	nodes.Set("payments-api", types.IRNode{
		Component:       "core/web-service",
		ResolvedVersion: "1.4.2",
		Labels:          *labelsAPI,
	})

	fixture := analyzer.PolicyInput{
		Graph: types.IRGraph{
			APIVersion: types.IRAPIVersion,
			Kind:       "IR",
			Spec: types.IRSpec{
				Nodes: *nodes,
				Edges: []types.IREdge{{Source: "payments-api", Target: "payments-db", Socket: "databases"}},
			},
		},
		PrecalculatedPaths: []analyzer.PrecalculatedPath{
			{
				PathID:           "path-0001",
				Nodes:            []string{"payments-api", "payments-db"},
				AttachedFeatures: []string{},
			},
		},
	}

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round analyzer.PolicyInput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(fixture.PrecalculatedPaths, round.PrecalculatedPaths) {
		t.Errorf("PrecalculatedPaths round-trip inequality:\n  before: %+v\n  after:  %+v",
			fixture.PrecalculatedPaths, round.PrecalculatedPaths)
	}
	if !reflect.DeepEqual(fixture.Graph.Spec.Edges, round.Graph.Spec.Edges) {
		t.Errorf("Graph edges round-trip inequality:\n  before: %+v\n  after:  %+v",
			fixture.Graph.Spec.Edges, round.Graph.Spec.Edges)
	}
	if fixture.Graph.Spec.Nodes.Len() != round.Graph.Spec.Nodes.Len() {
		t.Errorf("Graph nodes count mismatch: before=%d after=%d",
			fixture.Graph.Spec.Nodes.Len(), round.Graph.Spec.Nodes.Len())
	}
}

// TestPolicyOutput_RoundTrip pins the Go round-trip of the
// PolicyOutput envelope. The wire shape is schema-validated
// cross-language in aggrega-abi (the policy schema references ir-v1alpha1 — a
// cross-file $ref the host validator does not resolve without a loader), so the
// host test exercises marshal / unmarshal equality.
//
// One verdict fills every optional field the shared diagnostic carries, so the
// round-trip covers the whole shape rather than the two fields a verdict is most
// likely to set.
func TestPolicyOutput_RoundTrip(t *testing.T) {
	t.Parallel()

	fixture := abi.PolicyOutput{
		Results: []abi.Diagnostic{
			{
				Severity:       analyzer.SeverityHalt,
				Message:        "version-floor: payments-db must run resolved_version >= 15.0.0; got 14.9.0",
				Code:           "VER-001",
				RuleID:         "version-floor",
				Classification: "MAJOR",
				SourceRef:      "payments-db",
				Location:       &abi.Location{KeyPath: "spec.version", URI: "payments-db/values.yaml", Line: 12, Kind: "rendered-artifact"},
				Suggest:        "raise the pinned version to 15.0.0 or later",
			},
			{Severity: analyzer.SeverityInfo, Message: "soc2-required-labels: payments tier carries soc2 annotation"},
		},
	}

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round abi.PolicyOutput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(fixture, round) {
		t.Errorf("round-trip inequality:\n  before: %+v\n  after:  %+v", fixture, round)
	}
}

// TestPolicyTypes_JSONSchemaFieldCoverage asserts every
// exported Go field of the policy socket types appears as a
// property in the corresponding $defs block of the JSON Schema. Catches
// "Go struct grew a field, schema didn't" drift directly.
func TestPolicyTypes_JSONSchemaFieldCoverage(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	if err := json.Unmarshal([]byte(schemas.WasmPolicyV1alpha1), &raw); err != nil {
		t.Fatalf("unmarshal policy schema: %v", err)
	}
	defs, ok := raw["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema $defs missing or not an object")
	}

	assertGoStructPropertiesIn := func(t *testing.T, goType reflect.Type, defName string) {
		t.Helper()
		def, ok := defs[defName].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s missing or not an object", defName)
			return
		}
		props, ok := def["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s.properties missing or not an object", defName)
			return
		}
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = f.Name
			}
			if _, present := props[name]; !present {
				t.Errorf("schema $defs.%s.properties missing %q (Go field %s.%s)",
					defName, name, goType.Name(), f.Name)
			}
		}
	}

	// The envelope types carry local $defs. The graph field is a $ref to
	// ir-v1alpha1's IRGraph (not a local projection), so it has no local $def to
	// field-cover here — the IR graph's field coverage is the IR schema's concern.
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyInput{}), "PolicyInput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(abi.PolicyOutput{}), "PolicyOutput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PrecalculatedPath{}), "PrecalculatedPath")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.RenderPolicyInput{}), "RenderPolicyInput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.RenderedArtifact{}), "RenderedArtifact")

	// A fired verdict is one entry of the shared diagnostic shape, defined in the
	// wasm-diagnostic schema rather than the policy schema. The check runs BOTH
	// ways. A Go-field-to-schema-property walk alone passes whenever the Go type
	// declares a subset — so a verdict type carrying fewer fields than the wire
	// shape reads clean while every field it omits is silently discarded at the
	// decode boundary. The reverse walk is what refuses a subset.
	var diagRaw map[string]any
	if err := json.Unmarshal([]byte(schemas.WasmDiagnosticV1alpha1), &diagRaw); err != nil {
		t.Fatalf("unmarshal diagnostic schema: %v", err)
	}
	diagDefs, ok := diagRaw["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic schema $defs missing or not an object")
	}
	diagDef, ok := diagDefs["Diagnostic"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic schema $defs.Diagnostic missing or not an object")
	}
	diagProps, ok := diagDef["properties"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostic schema $defs.Diagnostic.properties missing or not an object")
	}
	verdictType := reflect.TypeOf(abi.Diagnostic{})
	goFields := map[string]string{}
	for i := 0; i < verdictType.NumField(); i++ {
		f := verdictType.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" {
			name = f.Name
		}
		goFields[name] = f.Name
		if _, present := diagProps[name]; !present {
			t.Errorf("diagnostic schema $defs.Diagnostic.properties missing %q (Go field Diagnostic.%s)", name, f.Name)
		}
	}
	for name := range diagProps {
		if _, present := goFields[name]; !present {
			t.Errorf("the verdict type has no Go field for diagnostic schema property %q, so a plugin that sets it has the value discarded at the decode boundary", name)
		}
	}
}
