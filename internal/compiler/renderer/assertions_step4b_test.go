// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// helper — build an OrderedMap[any] from sorted key/value pairs
func omAny(t *testing.T, pairs ...any) types.OrderedMap[any] {
	t.Helper()
	m := types.OrderedMap[any]{}
	for i := 0; i < len(pairs); i += 2 {
		k, _ := pairs[i].(string)
		m.Set(k, pairs[i+1])
	}
	return m
}

// helper — build an OrderedMap[string] from sorted key/value pairs
func omString(t *testing.T, pairs ...string) types.OrderedMap[string] {
	t.Helper()
	m := types.OrderedMap[string]{}
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// helper — build a graph with one node, given values mapping
func graphWithNode(t *testing.T, nodeID, componentType string, values types.OrderedMap[any]) *types.IRGraph {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{Component: componentType})

	vm := types.OrderedMap[json.RawMessage]{}
	if values.Len() > 0 {
		bytes, err := json.Marshal(values.TemplateAlphabeticalMapDeep())
		if err != nil {
			t.Fatalf("marshal values: %v", err)
		}
		vm.Set(nodeID, bytes)
	}

	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			ValuesMapping:        vm,
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
			Secrets:              types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{},
		},
	}
}

// TestStep4b_ComponentAssertionPasses pins the happy path.
func TestStep4b_ComponentAssertionPasses(t *testing.T) {
	t.Parallel()
	graph := graphWithNode(t, "web", "core/server", omAny(t, "replicas", 3))
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"replicas-positive", `{{ if lt (.Values.replicas | int) 1 }}{{ fail "replicas must be positive" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return comp, true },
	}
	if err := EvaluateComponentAndFeatureAssertions(graph, cl); err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

// TestStep4b_ComponentAssertionFailsHaltsBuild pins the substantive fail path.
func TestStep4b_ComponentAssertionFailsHaltsBuild(t *testing.T) {
	t.Parallel()
	graph := graphWithNode(t, "web", "core/server", omAny(t, "replicas", 0))
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"replicas-positive", `{{ if lt (.Values.replicas | int) 1 }}{{ fail "replicas must be positive" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return comp, true },
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrAssertionFailed) {
		t.Errorf("expected ErrAssertionFailed; got %v", err)
	}
	for _, want := range []string{"web", "core/server", "replicas-positive", "replicas must be positive"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error; got %q", want, err.Error())
		}
	}
}

// TestStep4b_FeatureAssertionFailsHaltsBuild pins Feature-side evaluation.
func TestStep4b_FeatureAssertionFailsHaltsBuild(t *testing.T) {
	t.Parallel()
	graph := graphWithNode(t, "web", "core/server", omAny(t))
	// Attach a feature with values that fail its assertion
	fvm := types.OrderedMap[types.IRFeatureEntry]{}
	fvm.Set("audit-on-web", types.IRFeatureEntry{
		Type:   "ops/audit",
		Values: omAny(t, "level", "off"),
	})
	graph.Spec.FeatureValuesMapping.Set("web", fvm)

	feat := types.Feature{
		Spec: types.FeatureSpec{
			Assertions: omString(t,
				"audit-must-be-on", `{{ if eq .Values.level "off" }}{{ fail "audit cannot be off in this contract" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
		Feature:   func(string) (types.Feature, bool) { return feat, true },
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil || !errors.Is(err, ErrAssertionFailed) {
		t.Fatalf("expected ErrAssertionFailed; got %v", err)
	}
	if !strings.Contains(err.Error(), "audit cannot be off") {
		t.Errorf("expected fail message; got %q", err.Error())
	}
}

// TestStep4b_AuthorOrderRespected pins author-order evaluation: rule B
// fails; rule A passes and rule C is never reached. The error names B.
func TestStep4b_AuthorOrderRespected(t *testing.T) {
	t.Parallel()
	graph := graphWithNode(t, "web", "core/server", omAny(t, "replicas", 2, "name", "ok"))
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"rule-A", `{{ if lt (.Values.replicas | int) 1 }}{{ fail "A-fail" }}{{ end }}`,
				"rule-B", `{{ if eq .Values.name "ok" }}{{ fail "B-fail" }}{{ end }}`,
				"rule-C", `{{ fail "C-should-not-be-reached" }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return comp, true },
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "rule-B") || !strings.Contains(err.Error(), "B-fail") {
		t.Errorf("expected rule-B+B-fail in error; got %q", err.Error())
	}
	if strings.Contains(err.Error(), "C-should-not-be-reached") {
		t.Errorf("rule-C should not have been reached; got %q", err.Error())
	}
}

// TestStep4b_MultiInstanceEvaluatesEachInstance pins per-instance semantics.
func TestStep4b_MultiInstanceEvaluatesEachInstance(t *testing.T) {
	t.Parallel()
	// Three instances of the same Component type; instance #2 fails the assertion
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web-1", types.IRNode{Component: "core/server"})
	nodes.Set("web-2", types.IRNode{Component: "core/server"})
	nodes.Set("web-3", types.IRNode{Component: "core/server"})

	vm := types.OrderedMap[json.RawMessage]{}
	for id, replicas := range map[string]int{"web-1": 3, "web-2": 0, "web-3": 5} {
		b, _ := json.Marshal(map[string]any{"replicas": replicas})
		vm.Set(id, b)
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			ValuesMapping:        vm,
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
			Secrets:              types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{},
		},
	}
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"replicas-positive", `{{ if lt (.Values.replicas | int) 1 }}{{ fail "replicas must be positive" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return comp, true },
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil || !strings.Contains(err.Error(), "web-2") {
		t.Errorf("expected web-2 in error; got %v", err)
	}
}

// TestStep4b_ReferenceToStep5bFieldProducesMissingKeyError pins R-B:
// a Step 4b Component assertion referencing .recipe.metadata.name (a
// Step 5b-only field) halts with a missingkey=error diagnostic — the
// typo surface, not silent zero.
func TestStep4b_ReferenceToStep5bFieldProducesMissingKeyError(t *testing.T) {
	t.Parallel()
	graph := graphWithNode(t, "web", "core/server", omAny(t))
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"oops", `{{ if eq .Recipe.Metadata.name "" }}{{ fail "..." }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return comp, true },
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil {
		t.Fatal("expected missing-key error; got nil")
	}
	if errors.Is(err, ErrAssertionFailed) {
		t.Errorf("did not expect ErrAssertionFailed; this is a missing-key surface, got %v", err)
	}
}

// TestStep4b_UsesTargetReferenceWorks pins the uses-target boundary: a
// Step 4b assertion checking the resolved socket target works because
// the IR edge's Target is populated pre-render.
func TestStep4b_UsesTargetReferenceWorks(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web", types.IRNode{Component: "core/server"})
	nodes.Set("db", types.IRNode{Component: "core/database"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			Edges:                []types.IREdge{{Source: "web", Target: "db", Socket: "primary_db"}},
			ValuesMapping:        types.OrderedMap[json.RawMessage]{},
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
			Secrets:              types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{},
		},
	}
	// Assertion references .Uses.primary_db.target — populated by ExtractNodeBaseContext
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"primary-db-targets-prod",
				`{{ if ne (index .Uses.primary_db "target") "db" }}{{ fail "primary_db must target db" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server" {
				return comp, true
			}
			return types.Component{}, false
		},
	}
	if err := EvaluateComponentAndFeatureAssertions(graph, cl); err != nil {
		t.Errorf("expected nil (uses-target resolved); got %v", err)
	}
}

// TestStep4b_UsesProvidesReferenceFails pins the boundary: a Step 4b
// assertion referencing .Uses.<socket>.<provides_key> halts with
// missingkey=error because provides values aren't yet evaluated at Step 4b.
func TestStep4b_UsesProvidesReferenceFails(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web", types.IRNode{Component: "core/server"})
	nodes.Set("db", types.IRNode{Component: "core/database"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			Edges:                []types.IREdge{{Source: "web", Target: "db", Socket: "primary_db"}},
			ValuesMapping:        types.OrderedMap[json.RawMessage]{},
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
			Secrets:              types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{},
		},
	}
	comp := types.Component{
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"db-host",
				`{{ if eq .Uses.primary_db.host "" }}{{ fail "host required" }}{{ end }}`,
			),
		},
	}
	cl := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server" {
				return comp, true
			}
			return types.Component{}, false
		},
	}
	err := EvaluateComponentAndFeatureAssertions(graph, cl)
	if err == nil {
		t.Fatal("expected missing-key error on .Uses.<socket>.<provides_key>; got nil")
	}
	if errors.Is(err, ErrAssertionFailed) {
		t.Errorf("did not expect ErrAssertionFailed; expected missing-key, got %v", err)
	}
}
