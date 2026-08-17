// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// determinismFixtureInput names the determinism fixture for the build-faithful
// harness: the full 5-step pipeline (resolve → topology → IR → analyze →
// render) with the recipe-resolution-completeness check on, exactly as the CLI
// build runs it. No shell-out to `aggrega build` — that introduces environment
// dependencies and makes flake-debugging harder. The Aggregate and Manifest
// are derived from the Release's spec pointers.
func determinismFixtureInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/determinism/fullcompile/release.yaml",
		CatalogPaths: []string{
			"testdata/determinism/fullcompile/catalog/base",
			"testdata/determinism/fullcompile/catalog/recipes/helm",
		},
	}
}

// TestDeterministicCompilation_FullPipeline compiles the determinism fixture
// repeatedly and asserts the resulting artifact maps are byte-identical across
// all corresponding files. Repeats 10 times to guard against any lingering
// map[string]X iteration that produces stable output most runs but diverges
// occasionally due to Go's map-iteration randomization. This is the helm-flat
// provider's always-on double-run guard.
func TestDeterministicCompilation_FullPipeline(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministicN(t, determinismFixtureInput(), 10)
}

// TestDeterministicCompilation_IRAuthorOrder asserts that the IRGraph's
// JSON-emitted `nodes` object preserves the Aggregate's author-declared
// component order (e.g., zulu, alpha, middle) rather than alphabetical.
//
// Activated 2026-04-25 after topology.Build was refactored to preserve
// author order through subsystem flattening (TopologyResult.Nodes became
// types.OrderedMap[*TopologyNode]; the IR builder now iterates via .All()
// rather than the old sortedStringKeys path). The contract is end-to-end:
// Aggregate author order → topology flattening → IR construction → IR
// JSON emission via OrderedMap.MarshalJSON.
//
// Author order applies to author-declared collections (Aggregate
// components, Workload containers, env vars, edges via the compositional
// cross-product). Synthesized derived collections that lack a single
// authoring surface sort alphabetically; see the rationale comment at
// internal/compiler/topology/topology.go in the Build orchestrator.
func TestDeterministicCompilation_IRAuthorOrder(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, determinismFixtureInput())

	jsonBytes, err := json.Marshal(result.IRGraph)
	if err != nil {
		t.Fatalf("Marshal IRGraph: %v", err)
	}

	gotOrder, err := extractNodeOrderFromJSON(jsonBytes)
	if err != nil {
		t.Fatalf("extract node order: %v", err)
	}

	wantOrder := []string{"zulu", "alpha", "middle"}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Errorf("IR JSON nodes key order = %v, want %v (Aggregate author order lost)\n\nfull JSON:\n%s",
			gotOrder, wantOrder, jsonBytes)
	}
}

// extractNodeOrderFromJSON reads the IRGraph's spec.nodes object key order
// from the emitted JSON bytes via json.Decoder.Token(). Cannot use
// json.Unmarshal into a map — that decode collapses key order. Walks the
// token stream until it finds the spec.nodes object, then collects keys
// in source order.
func extractNodeOrderFromJSON(jsonBytes []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))

	// Outer object opening brace.
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, asTokenErr("expected outer key string", keyTok)
		}
		if key != "spec" {
			if err := skipValue(dec); err != nil {
				return nil, err
			}
			continue
		}
		// We're at the `spec` value — should be an object.
		if err := expectDelim(dec, '{'); err != nil {
			return nil, err
		}
		for dec.More() {
			specKeyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			specKey, ok := specKeyTok.(string)
			if !ok {
				return nil, asTokenErr("expected spec key string", specKeyTok)
			}
			if specKey != "nodes" {
				if err := skipValue(dec); err != nil {
					return nil, err
				}
				continue
			}
			// We're at spec.nodes — collect its keys in order.
			if err := expectDelim(dec, '{'); err != nil {
				return nil, err
			}
			var keys []string
			for dec.More() {
				nodeKeyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				nodeKey, ok := nodeKeyTok.(string)
				if !ok {
					return nil, asTokenErr("expected nodes key string", nodeKeyTok)
				}
				keys = append(keys, nodeKey)
				if err := skipValue(dec); err != nil {
					return nil, err
				}
			}
			return keys, nil
		}
	}
	return nil, asTokenErr("spec.nodes not found in IR JSON", nil)
}

// expectDelim reads the next token and verifies it equals the named delim.
func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	got, ok := tok.(json.Delim)
	if !ok || got != want {
		return asTokenErr(fmt.Sprintf("expected delim %v", want), tok)
	}
	return nil
}

// skipValue consumes one full JSON value (scalar / object / array) from
// the decoder without retaining its content. Used to skip over fields the
// caller does not care about while preserving stream position.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else if d == '}' || d == ']' {
				depth--
			}
		}
	}
	_ = delim
	return nil
}

func asTokenErr(msg string, tok any) error {
	return &tokenErr{msg: msg, tok: tok}
}

type tokenErr struct {
	msg string
	tok any
}

func (e *tokenErr) Error() string {
	if e.tok == nil {
		return e.msg
	}
	return e.msg + ": " + tokenAsString(e.tok)
}

func tokenAsString(tok any) string {
	switch t := tok.(type) {
	case json.Delim:
		return string(t)
	case string:
		return strings.ReplaceAll(t, "\n", "\\n")
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "<unknown>"
		}
		return string(b)
	}
}

// TestDeterministicCompilation_IRSpecEdgesPreserveCompositionalAuthorOrder
// pins the IR JSONL `spec.edges` array's source-field ordering against
// the Aggregate's component declaration sequence. Aggregate authors
// declare components in a specific order; the edge slice flows through
// topology Step 4 in compositional author order (component sequence ×
// per-component `uses:` socket order × per-socket alias order); the IR
// builder reads the slice in order without re-sorting; the JSON marshal
// preserves slice order. End-to-end, the user-visible JSON edges array's
// source-field sequence matches the Aggregate's component declaration
// order rather than alphabetical-by-source.
//
// This test builds the IR directly (topology.Build -> ir.BuildIR -> marshal)
// against an in-memory Aggregate and a recipe-less stub catalog, with no
// render and no recipe-resolution-completeness check. It does NOT run through
// the build-faithful harness: the harness is file-based and gated, and the
// bespoke compositional uses-edge topology has no recipes, so it would halt at
// resolution before reaching the IR-marshal surface this test inspects. It
// isolates the IR-build stage's edge ordering — distinct from
// TestDeterministicCompilation_FullPipeline's rendered-output determinism, and
// the IR-author-order sibling that rides the build harness on a real fixture.
func TestDeterministicCompilation_IRSpecEdgesPreserveCompositionalAuthorOrder(t *testing.T) {
	t.Parallel()

	// Build a self-contained Aggregate in non-alphabetical declaration
	// order, each component declaring a uses entry pointing at "db".
	// Author order is z-svc, a-svc, m-svc, db; the edge slice should
	// emit z-svc → db, a-svc → db, m-svc → db in that order.
	usesZ := types.OrderedMap[types.UsesSocketValue]{}
	usesZ.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	usesA := types.OrderedMap[types.UsesSocketValue]{}
	usesA.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	usesM := types.OrderedMap[types.UsesSocketValue]{}
	usesM.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("z-svc", types.ComponentInstance{Type: "core/server", Uses: usesZ})
	components.Set("a-svc", types.ComponentInstance{Type: "core/server", Uses: usesA})
	components.Set("m-svc", types.ComponentInstance{Type: "core/server", Uses: usesM})
	components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "edge-author-order-test", Version: "1.0.0"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	cat := edgeAuthorOrderCatalog()
	res := &resolver.ResolveResult{
		Aggregate:     agg,
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult: res,
		CatalogLookup: cat,
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	graph, _, _, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   res,
		CatalogLookup:   cat,
		CompilerVersion: "test",
	})
	if err != nil {
		t.Fatalf("ir.BuildIR: %v", err)
	}

	jsonBytes, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("Marshal IRGraph: %v", err)
	}

	var partial struct {
		Spec struct {
			Edges []struct {
				Source string `json:"source"`
			} `json:"edges"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(jsonBytes, &partial); err != nil {
		t.Fatalf("partial unmarshal: %v", err)
	}

	wantSources := []string{"z-svc", "a-svc", "m-svc"}
	if len(partial.Spec.Edges) != len(wantSources) {
		t.Fatalf("edge count = %d, want %d\nfull JSON:\n%s",
			len(partial.Spec.Edges), len(wantSources), jsonBytes)
	}
	for i, want := range wantSources {
		if partial.Spec.Edges[i].Source != want {
			t.Errorf("edges[%d].source = %q, want %q (Aggregate author order, not alphabetical)\nfull JSON:\n%s",
				i, partial.Spec.Edges[i].Source, want, jsonBytes)
		}
	}
}

// edgeAuthorOrderCatalog stubs the minimum CatalogLookup needed by the
// edge-author-order test. core/server declares a usesSchema with a
// "databases" socket; core/datastore/postgres has the matching label.
func edgeAuthorOrderCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server":
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/server", Version: "1.0.0", Labels: labels},
					Spec:     types.ComponentSpec{UsesSchema: usesSchema},
				}, true
			case "core/datastore/postgres":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/postgres", Version: "1.0.0", Labels: labels},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

// TestDeterministicCompilation_OrderedMapInterfaceRoundTripIsByteIdentical
// pins the OrderedMap round-trip determinism contract at the OrderedMap[any]
// type level. The fixture exercises the recursive decode path with mixed
// shapes (nested objects, arrays of objects, high-precision integers, mixed
// scalars) and asserts byte-identical JSON marshal output across 10
// iterations. Guards against three potential non-determinism sources in the
// recursive-decode machinery:
//
//   - depth-counter race (ruled out by per-call stack-allocated parameter,
//     this iteration loop confirms in practice);
//   - reflection-cache race (sync.Map.LoadOrStore is documented atomic, this
//     loop confirms under concurrent test execution under -race);
//   - json.Number formatting drift (impossible because json.Number stores
//     the source string verbatim, this loop confirms).
//
// Companion to TestDeterministicCompilation_FullPipeline — that test pins
// determinism at the rendered-artifact level; this one pins it at the
// OrderedMap-decode level.
func TestDeterministicCompilation_OrderedMapInterfaceRoundTripIsByteIdentical(t *testing.T) {
	t.Parallel()

	fixture := []byte(`{"meta":{"version":"1.2.3","tags":["alpha","beta"]},"big":9007199254740993,"items":[{"z":1,"a":2}],"flag":true,"flat":"value"}`)

	var first []byte
	for i := 0; i < 10; i++ {
		var om types.OrderedMap[any]
		if err := json.Unmarshal(fixture, &om); err != nil {
			t.Fatalf("iteration %d unmarshal: %v", i, err)
		}
		out, err := json.Marshal(&om)
		if err != nil {
			t.Fatalf("iteration %d marshal: %v", i, err)
		}
		if i == 0 {
			first = out
			if !bytes.Equal(first, fixture) {
				t.Errorf("iteration 0 round-trip diverged from input\ninput:  %s\noutput: %s", fixture, first)
			}
			continue
		}
		if !bytes.Equal(out, first) {
			t.Fatalf("iteration %d byte-mismatch vs iteration 0\nfirst:  %s\nlater:  %s", i, first, out)
		}
	}
}
