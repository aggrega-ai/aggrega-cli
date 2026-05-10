// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/analyzer"
	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	renderHelm "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// determinismCompileResult bundles the IR graph and rendered files from one
// in-process compilation pass. The IR graph is retained alongside the
// rendered files so the IR-author-order assertion can read it without a
// second compile.
type determinismCompileResult struct {
	Graph *types.IRGraph
	Files map[string][]byte
}

// compileInProcess drives the full 5-step pipeline (resolve → topology →
// IR → analyze → render) against the determinism fixture. No shell-out to
// `aggrega build` — shell-out introduces environment dependencies and
// makes flake-debugging harder. Mirrors the harness shape of runExample22
// in example22_test.go.
func compileInProcess(t *testing.T) *determinismCompileResult {
	t.Helper()

	base := "testdata/determinism/fullcompile/catalog/base"
	recipesDir := "testdata/determinism/fullcompile/catalog/recipes/helm"

	cat, err := catalog.LoadAll(base, recipesDir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/determinism/fullcompile/aggregate.yaml",
		ReleasePath:   "testdata/determinism/fullcompile/release.yaml",
		ManifestPath:  "testdata/determinism/fullcompile/manifest.yaml",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	catalogLookup := cat.CatalogLookup()

	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			t.Errorf("guard: %s", e.Error())
		}
		t.FailNow()
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		t.Fatalf("topology: %v", err)
	}

	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0-determinism",
	})
	if err != nil {
		t.Fatalf("IR: %v", err)
	}

	analysis := analyzer.Analyze(graph, catalogLookup)
	for _, e := range analysis.Errors {
		t.Errorf("selector: %s → %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	if t.Failed() {
		t.FailNow()
	}

	rendererInput := renderer.RendererInput{
		Graph:          graph,
		FeatureOrder:   featureOrder,
		ResolveResult:  resolveResult,
		CatalogLookup:  catalogLookup,
		ProviderLookup: cat.ProviderLookup,
	}
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		default:
			return nil, nil
		}
	}

	renderResult, err := renderer.RenderAll(
		context.Background(),
		rendererInput,
		cat.RecipeLookup(),
		engineFactory,
	)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return &determinismCompileResult{
		Graph: graph,
		Files: renderResult.Files,
	}
}

// TestDeterministicCompilation_FullPipeline compiles the determinism
// fixture twice and asserts the resulting artifact maps are byte-identical
// across all corresponding files. Repeats 10 times to guard against any
// lingering map[string]X iteration that produces stable output most runs
// but diverges occasionally due to Go's map-iteration randomization.
func TestDeterministicCompilation_FullPipeline(t *testing.T) {
	t.Parallel()

	first := compileInProcess(t)

	for i := 1; i < 10; i++ {
		this := compileInProcess(t)

		if len(first.Files) != len(this.Files) {
			t.Errorf("iteration %d: file-count mismatch: first %d, later %d", i, len(first.Files), len(this.Files))
			continue
		}

		for path, firstContent := range first.Files {
			thisContent, ok := this.Files[path]
			if !ok {
				t.Errorf("iteration %d: missing file %q (present in first run)", i, path)
				continue
			}
			if !bytes.Equal(firstContent, thisContent) {
				t.Errorf("iteration %d: byte-mismatch in %q\nfirst run:\n%s\nlater run:\n%s",
					i, path, firstContent, thisContent)
				return // First mismatch is sufficient — bail to avoid noise.
			}
		}
	}
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
// components, Workload containers, env vars, etc.). Synthesized derived
// collections (edges, sentinel lists) intentionally use alphabetical
// ordering; see the rationale comment at
// internal/compiler/topology/topology.go around the sortedNodeIDs call.
func TestDeterministicCompilation_IRAuthorOrder(t *testing.T) {
	t.Parallel()

	result := compileInProcess(t)

	jsonBytes, err := json.Marshal(result.Graph)
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
