// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file hold that a FeatureRecipe rendering once for its boundary
// has its locals and provides evaluated once, at boundary scope, and that every
// consumer the boundary holds reads that one evaluation.
//
// The fixture is built so that no consumer could produce the values it reads by
// evaluating anything itself: the local and both provides read .boundary, which a
// consumer's context does not carry and which every per-consumer evaluator refuses.
// A consumer site that evaluated the FeatureRecipe again would halt rather than read
// a value, so the values reaching each consumer are themselves the evidence that
// nothing was evaluated there. The count on the state is the evidence for the
// boundary side: the pre-pass and the feature-rendering dispatch together make one
// evaluation.

const (
	onceFeatureType = "probe/seen:1.0.0"
	onceInstance    = "gateway"
	onceBoundary    = "bridge"
)

// onceFeatureRecipe is a boundary-scope FeatureRecipe whose local and provides read
// the boundary view. Its provides are declared in an order that is not sorted, so a
// reader that loses the declared order shows it.
func onceFeatureRecipe() *types.FeatureRecipe {
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "probe-seen-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     onceFeatureType,
			ExecutionScope: executionScopeBoundary,
			InjectMapping: `{"env": {"PROBE_EDGES": "{{ (index .features .attachment_name).provides.edges }}", ` +
				`"PROBE_WHERE": "{{ .locals.where }}"}}`,
		},
	}
	fr.Spec.Locals.Set("where", "{{ .boundary.path }}")
	fr.Spec.Provides.Set("where", "{{ .locals.where }}")
	fr.Spec.Provides.Set("edges", "{{ len .boundary.internal_uses_edges }}")
	return fr
}

// onceFixture is one boundary holding the attachment, and three consumers it holds:
// two in nested boundaries, so the read walks up to the declaring boundary, and one
// in the boundary itself. Two connections run inside the boundary, so the boundary's
// edge count is 2. Every consumer's attachment carries an authored inject block
// naming one of the provides.
type onceFixture struct {
	graph   *types.IRGraph
	release *types.Release
	lookup  RecipeLookup
	order   map[string][]string
	key     boundaryFRKey
}

func newOnceFixture(fr *types.FeatureRecipe) onceFixture {
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "app"}}
	graph.Spec.Nodes.Set("adminer", types.IRNode{Component: "core/adminer", BoundaryPath: "bridge/frontend"})
	graph.Spec.Nodes.Set("cache", types.IRNode{Component: "core/cache", BoundaryPath: "bridge"})
	graph.Spec.Nodes.Set("db", types.IRNode{Component: "core/db", BoundaryPath: "bridge/backend"})
	graph.Spec.Edges = []types.IREdge{
		{Source: "adminer", Target: "db", Socket: "database", Alias: "db"},
		{Source: "cache", Target: "db", Socket: "database", Alias: "db"},
	}

	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set(onceInstance, types.IRBoundaryFeature{Type: onceFeatureType})
	graph.Spec.BoundaryFeatures.Set(onceBoundary, inner)

	order := map[string][]string{}
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		entry := types.IRFeatureEntry{Type: onceFeatureType}
		env := types.OrderedMap[string]{}
		env.Set("GW_EDGES", "edges")
		entry.Inject.Set("env", env)
		attachments := types.OrderedMap[types.IRFeatureEntry]{}
		attachments.Set(onceInstance, entry)
		graph.Spec.FeatureValuesMapping.Set(nodeID, attachments)
		order[nodeID] = []string{onceInstance}
	}

	nested := types.OrderedMap[types.Boundary]{}
	nested.Set("frontend", types.Boundary{Description: "frontend"})
	nested.Set("backend", types.Boundary{Description: "backend"})
	release := &types.Release{}
	release.Spec.Boundaries.Set(onceBoundary, types.Boundary{Description: "bridge", Boundaries: nested})

	return onceFixture{
		graph:   graph,
		release: release,
		lookup: RecipeLookup{FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == onceFeatureType {
				return fr, true
			}
			return nil, false
		}},
		order: order,
		key:   boundaryFRKey{BoundaryPath: onceBoundary, FRInstance: onceInstance},
	}
}

// prepass runs the boundary-scope pre-pass over the fixture, as the render does
// before its node walk.
func (f onceFixture) prepass(t *testing.T, state *BoundaryFRState, catalog resolver.CatalogLookup) error {
	t.Helper()
	return PrepopulateBoundaryScopeProvides(f.graph, f.release, nil, f.lookup, catalog, nil, state, nil)
}

// consumerRead is what one consumer reads of the attachment, through the production
// sites that read it in the node walk, in the order the walk reaches them.
type consumerRead struct {
	providesKeys []string
	edges, where string
	injected     string
	mappedEdges  string
	mappedWhere  string
	edgeRoute    string
}

func (f onceFixture) read(t *testing.T, state *BoundaryFRState, nodeID string) consumerRead {
	t.Helper()
	node, _ := f.graph.Spec.Nodes.Get(nodeID)
	attachments, _ := f.graph.Spec.FeatureValuesMapping.Get(nodeID)

	// An inject block authored on the attachment.
	inject, injectRaw := BuildInjectPipe(nil)
	if _, err := ResolveAttachmentInject(&inject, f.graph, nodeID, "", f.lookup, nil, nil, state); err != nil {
		t.Fatalf("%s: the attachment's inject block was not resolved: %v", nodeID, err)
	}
	injected, _ := inject.Env.Get("GW_EDGES")

	// .features.<attachment>.provides.
	bag, err := ResolveAttachmentProvides(attachments, nodeID, "", f.lookup, resolver.CatalogLookup{}, nil, state, node.BoundaryPath)
	if err != nil {
		t.Fatalf("%s: the attachment's provides were not resolved: %v", nodeID, err)
	}
	ctx := AssembleNodeContext(nodeID, f.graph, nil, nil, inject, injectRaw, nil, bag)
	attachment, ok := ctx.Features.Get(onceInstance)
	if !ok {
		t.Fatalf("%s: the attachment is not on .features", nodeID)
	}
	edges, _ := attachment.Provides.Get("edges")
	where, _ := attachment.Provides.Get("where")

	// The .locals the injectMapping reads, and the provides it reads through .features.
	refs := resolveFeatureRecipes(nodeID, f.order, f.graph, f.lookup, state)
	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("%s: the attachment's injectMapping did not render: %v", nodeID, err)
	}
	mappedEdges, _ := ctx.Context.Inject.Env.Get("PROBE_EDGES")
	mappedWhere, _ := ctx.Context.Inject.Env.Get("PROBE_WHERE")

	// A uses edge the attachment carries.
	route := &types.InjectPipe{}
	routeInject := types.OrderedMap[types.OrderedMap[string]]{}
	routeEnv := types.OrderedMap[string]{}
	routeEnv.Set("ROUTE_EDGES", "edges")
	routeInject.Set("env", routeEnv)
	RehydrateInjectFromProvides(route, []types.IREdge{{
		Source: nodeID, Target: onceInstance, OriginFeature: onceFeatureType, OriginFeatureInstance: onceInstance,
		Inject: routeInject,
	}}, nodeID, NodeProvides{}, f.graph, f.lookup, state)
	edgeRoute, _ := route.Env.Get("ROUTE_EDGES")

	return consumerRead{
		providesKeys: attachment.Provides.Keys(),
		edges:        edges, where: where,
		injected:    injected,
		mappedEdges: mappedEdges, mappedWhere: mappedWhere,
		edgeRoute: edgeRoute,
	}
}

// TestABoundaryScopeFeatureRecipeIsEvaluatedOnceForTheWholeBuild is the
// one-evaluation proof: for a boundary holding several consumers, the FeatureRecipe's
// locals and provides are evaluated once across the pre-pass, every consumer's reads
// and the feature-rendering dispatch.
//
// # What counts
//
// The state counts the evaluations it made rather than read back. The consumer sites
// are not counted there, and need not be: each would have to evaluate templates
// reading .boundary, which every per-consumer evaluator refuses, so a consumer site
// that evaluated would halt the read rather than return the boundary's values.
func TestABoundaryScopeFeatureRecipeIsEvaluatedOnceForTheWholeBuild(t *testing.T) {
	t.Parallel()
	fr := onceFeatureRecipe()
	f := newOnceFixture(fr)
	state := NewBoundaryFRState()

	if err := f.prepass(t, state, resolver.CatalogLookup{}); err != nil {
		t.Fatalf("pre-pass: %v", err)
	}
	if made := state.evaluationsMade[f.key]; made != 1 {
		t.Fatalf("the pre-pass made %d evaluations; want 1", made)
	}

	for _, nodeID := range f.graph.Spec.Nodes.Keys() {
		_ = f.read(t, state, nodeID)
	}
	if made := state.evaluationsMade[f.key]; made != 1 {
		t.Errorf("after every consumer's read, %d evaluations were made; want the pre-pass's one", made)
	}

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, onceInstance, f.key, &PhaseBState{}, state,
		NodeRenderContext{}, factory, RecipeLookup{}, f.graph, f.release, nil, nil, RendererInput{},
	); err != nil {
		t.Fatalf("the feature-rendering dispatch: %v", err)
	}
	if made := state.evaluationsMade[f.key]; made != 1 {
		t.Errorf("after the feature-rendering dispatch, %d evaluations were made; want the pre-pass's one", made)
	}
	t.Logf("evaluations made for %v across the build: %d", f.key, state.evaluationsMade[f.key])
}

// TestEveryConsumerReadsTheBoundaryEvaluation is the consumer-read proof: every
// consumer the boundary holds, nested or not, reads the boundary's one evaluation at
// every site that reads the attachment — .features.<attachment>.provides in the
// FeatureRecipe's declared order, an inject block authored on the attachment, the
// .locals and provides its injectMapping reads, and a uses edge the attachment
// carries — and all of them read the same value.
func TestEveryConsumerReadsTheBoundaryEvaluation(t *testing.T) {
	t.Parallel()
	f := newOnceFixture(onceFeatureRecipe())
	state := NewBoundaryFRState()
	if err := f.prepass(t, state, resolver.CatalogLookup{}); err != nil {
		t.Fatalf("pre-pass: %v", err)
	}

	for _, nodeID := range f.graph.Spec.Nodes.Keys() {
		t.Run(nodeID, func(t *testing.T) {
			t.Parallel()
			got := f.read(t, state, nodeID)
			t.Logf("%s reads %+v", nodeID, got)
			if strings.Join(got.providesKeys, ",") != "where,edges" {
				t.Errorf(".features.%s.provides keys = %v; want the declared order [where edges]", onceInstance, got.providesKeys)
			}
			for _, c := range []struct{ site, got, want string }{
				{".features provides.edges", got.edges, "2"},
				{".features provides.where", got.where, onceBoundary},
				{"the attachment's inject block", got.injected, "2"},
				{"the injectMapping's provides read", got.mappedEdges, "2"},
				{"the injectMapping's .locals read", got.mappedWhere, onceBoundary},
				{"a uses edge the attachment carries", got.edgeRoute, "2"},
			} {
				if c.got != c.want {
					t.Errorf("%s: %q; want the boundary's %q", c.site, c.got, c.want)
				}
			}
		})
	}
}

// TestTheBoundaryProvidesContractIsEnforcedOnceAtTheBoundaryAttachment holds where
// the Feature's provides contract is enforced for a FeatureRecipe rendering once for
// its boundary: on the one evaluation, positioned at the boundary attachment, and
// not again per consumer.
func TestTheBoundaryProvidesContractIsEnforcedOnceAtTheBoundaryAttachment(t *testing.T) {
	t.Parallel()

	var schema yaml.Node
	if err := yaml.Unmarshal([]byte("provides:\n  edges: {type: string}\n  missing: {type: string}\n"), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	catalog := resolver.CatalogLookup{Feature: func(featureType string) (types.Feature, bool) {
		if featureType == onceFeatureType {
			return types.Feature{Spec: types.FeatureSpec{Schema: schema}}, true
		}
		return types.Feature{}, false
	}}

	t.Run("the pre-pass halts once, at the boundary attachment", func(t *testing.T) {
		t.Parallel()
		f := newOnceFixture(onceFeatureRecipe())
		err := f.prepass(t, NewBoundaryFRState(), catalog)
		if err == nil {
			t.Fatal("a FeatureRecipe omitting a key its Feature's contract declares was admitted")
		}
		if !errors.Is(err, ErrProvidesContractUnfulfilled()) {
			t.Fatalf("the halt does not carry %v: %v", ErrProvidesContractUnfulfilled(), err)
		}
		text := haltChainText(err)
		for _, phrase := range []string{`boundary "bridge" attachment "gateway"`, `"missing"`, `FeatureRecipe "probe-seen-fr"`} {
			if !strings.Contains(text, phrase) {
				t.Errorf("the halt does not carry %q: %s", phrase, text)
			}
		}
		if strings.Contains(text, `node "`) {
			t.Errorf("the halt is positioned at a consumer node rather than the boundary attachment: %s", text)
		}
		t.Logf("HALT %s", text)
	})

	t.Run("a consumer's read does not enforce it again", func(t *testing.T) {
		t.Parallel()
		f := newOnceFixture(onceFeatureRecipe())
		state := NewBoundaryFRState()
		// The evaluation made without the contract, so the consumers' reads are the
		// only place it could be enforced.
		if err := f.prepass(t, state, resolver.CatalogLookup{}); err != nil {
			t.Fatalf("pre-pass: %v", err)
		}
		for _, nodeID := range f.graph.Spec.Nodes.Keys() {
			node, _ := f.graph.Spec.Nodes.Get(nodeID)
			attachments, _ := f.graph.Spec.FeatureValuesMapping.Get(nodeID)
			if _, err := ResolveAttachmentProvides(attachments, nodeID, "", f.lookup, catalog, nil, state, node.BoundaryPath); err != nil {
				t.Errorf("%s: the contract was enforced again on a consumer's read: %v", nodeID, err)
			}
		}
	})
}
