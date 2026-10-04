// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The connection-workload fixture's catalog: a router whose socket declares a
// sensitive credential slot and whose own block declares a sensitive token slot,
// the model it reaches, the credential Feature and its locator-publishing
// realization, and the Recipe whose Workload names each connection's slot through
// the connection.
const (
	connectionWorkloadCatalog = "../testdata/connection-workload/catalog/base"
	connectionRouterType      = "cw/router"
	connectionModelType       = "cw/model"
)

// dispatchRecorder is a provider engine that renders nothing of its own and
// records, per node, the two values the host hands a workload dispatch, so the
// projection is built from exactly what crossed rather than beside it.
type dispatchRecorder struct {
	nodeCtx  map[string]renderer.NodeRenderContext
	workload map[string]*types.Workload
}

func (r *dispatchRecorder) Render(context.Context, map[string]any, renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

func (r *dispatchRecorder) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (r *dispatchRecorder) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, w *types.Workload, nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	r.nodeCtx[nodeCtx.NodeID] = nodeCtx
	r.workload[nodeCtx.NodeID] = w
	return []renderer.EmittedFile{{Name: nodeCtx.NodeID + "-workload.yaml", Body: []byte("workload: " + nodeCtx.NodeID + "\n")}}, nil
}

// renderConnectionWorkload renders the fixture's router, which draws two
// connections carrying a credential each and holds one of its own, and returns the
// marshalled projection the host built for the router's workload dispatch.
// socketSensitive sets whether the socket's credential slot declares sensitive
// material, which is the one change between a run and its control.
func renderConnectionWorkload(t *testing.T, socketSensitive bool) map[string]json.RawMessage {
	t.Helper()
	cat, err := catalog.LoadAll("", connectionWorkloadCatalog)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if errs := cat.ResolveAllExtends(); len(errs) > 0 {
		t.Fatalf("catalog extends: %v", errs[0])
	}
	lookup := cat.CatalogLookup()
	router, found := lookup.Component(connectionRouterType)
	if !found {
		t.Fatalf("%s did not resolve from the fixture catalog", connectionRouterType)
	}
	socket, _ := router.Spec.UsesSchema.Get("models")
	slots := types.OrderedMap[types.AttachmentSlot]{}
	for name, slot := range socket.FeaturesSchema.All() {
		slot.Sensitive = socketSensitive
		slots.Set(name, slot)
	}
	socket.FeaturesSchema = slots
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("models", socket)
	router.Spec.UsesSchema = sockets
	inner := lookup.Component
	lookup.Component = func(ref string) (types.Component, bool) {
		if ref == connectionRouterType {
			return router, true
		}
		return inner(ref)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("router", types.IRNode{Component: connectionRouterType, BoundaryPath: "main"})
	nodes.Set("gpt-model", types.IRNode{Component: connectionModelType, BoundaryPath: "main"})
	nodes.Set("nim-model", types.IRNode{Component: connectionModelType, BoundaryPath: "main"})

	entries := types.OrderedMap[types.IRFeatureEntry]{}
	for _, alias := range []string{"gpt", "nim"} {
		values := types.OrderedMap[any]{}
		values.Set("origin", alias+"-team-secret")
		values.Set("key", "api-key")
		entries.Set(types.JoinConnectionAttachmentName(alias, "credential"), types.IRFeatureEntry{
			Type:       "feature.cw/secret-ref",
			Values:     values,
			Connection: &types.AttachmentConnection{Socket: "models", Alias: alias, Attachment: "credential"},
		})
	}
	own := types.OrderedMap[any]{}
	own.Set("origin", "router-token-secret")
	own.Set("key", "token")
	entries.Set("token", types.IRFeatureEntry{Type: "feature.cw/secret-ref", Values: own})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("router", entries)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "connection-workload-app"},
			Spec: types.IRSpec{
				Nodes: nodes,
				Edges: []types.IREdge{
					{Source: "router", Target: "gpt-model", Socket: "models", Alias: "gpt"},
					{Source: "router", Target: "nim-model", Socket: "models", Alias: "nim"},
				},
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{"router": {"gpt.credential", "nim.credential", "token"}},
		CatalogLookup: lookup,
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "connection-workload-app"}},
			Release:   &types.Release{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"router":    {BoundaryPath: "main"},
				"gpt-model": {BoundaryPath: "main"},
				"nim-model": {BoundaryPath: "main"},
			},
		},
	}
	componentTier := &types.MappingTier{}
	componentTier.Type.Set(connectionRouterType, "cw-router-recipe:1.0.0")
	componentTier.Type.Set(connectionModelType, "cw-model-recipe:1.0.0")
	featureTier := &types.MappingTier{}
	featureTier.Type.Set("feature.cw/secret-ref", "cw-secret-ref-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
		Components: componentTier,
		Features:   featureTier,
	}}}

	rec := &dispatchRecorder{nodeCtx: map[string]renderer.NodeRenderContext{}, workload: map[string]*types.Workload{}}
	factory := func(string) (renderer.Provider, error) { return rec, nil }
	if _, err := renderer.RenderAll(context.Background(), input, cat.RecipeLookupWithManifest(manifest), factory); err != nil {
		t.Fatalf("RenderAll over the fixture: %v", err)
	}
	nodeCtx, dispatched := rec.nodeCtx["router"]
	if !dispatched {
		t.Fatal("the router's workload was never dispatched, so nothing was observed at the boundary")
	}
	wire, err := json.Marshal(renderer.NodeProjection(nodeCtx, types.OrderedMap[any]{}, rec.workload["router"]))
	if err != nil {
		t.Fatalf("marshal the projection: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode the marshalled projection: %v", err)
	}
	return decoded
}

// TestSensitiveRenderedKeys_ReachesAConnectionSlotOnTheWire proves the marking on
// the projection a guest receives, marshalled as it crosses: each variable a
// Workload entry delivers from a connection's socket slot is named, with the
// socket slot's coordinate, beside the variable delivered from the Component's own
// slot. The control moves exactly one declaration — the socket slot's — and the
// connection variables leave the marking while the own variable stays, so the
// marking is attributable to the socket slot's declaration and not to an entry
// naming a connection at all.
func TestSensitiveRenderedKeys_ReachesAConnectionSlotOnTheWire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		socketSensitive bool
		want            map[string]string
	}{
		{
			name:            "the socket slot declares sensitive material",
			socketSensitive: true,
			want: map[string]string{
				"ROUTER_TOKEN":  "featuresSchema.token",
				"MODEL_KEY_GPT": "usesSchema.models.featuresSchema.credential",
				"MODEL_KEY_NIM": "usesSchema.models.featuresSchema.credential",
			},
		},
		{
			name: "control: the socket slot declares nothing",
			want: map[string]string{"ROUTER_TOKEN": "featuresSchema.token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			projection := renderConnectionWorkload(t, tc.socketSensitive)
			raw, present := projection["sensitive_rendered_keys"]
			if !present {
				t.Fatalf("the marshalled projection carries no sensitive_rendered_keys; want %v", tc.want)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode sensitive_rendered_keys: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sensitive_rendered_keys = %v; want %v", got, tc.want)
			}
		})
	}
}
