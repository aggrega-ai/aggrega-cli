// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

package embedded_test

import (
	"context"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	labelInjectorURI  = "governance/label-injector"
	selectorTaggerURI = "governance/selector-tagger"
)

// twoNodeTopo returns a minimal topology with a labelled frontend node and a
// labelled backend node, in author order, for the mutator guest tests.
func twoNodeTopo() *topology.TopologyResult {
	nodes := types.NewOrderedMap[*topology.TopologyNode]()

	web := types.NewOrderedMap[string]()
	web.Set("tier", "frontend")
	nodes.Set("web", &topology.TopologyNode{InstanceID: "web", ComponentType: "core/web", Labels: *web})

	db := types.NewOrderedMap[string]()
	db.Set("tier", "backend")
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/db", Labels: *db})

	return &topology.TopologyResult{Nodes: *nodes}
}

// runMutatorGuest loads the embedded guest at uri, dispatches its mutate export
// with payload, and returns the raw MutatorOutput bytes. It exercises the guest
// exactly as the host does: load embedded bytes, describe-gate, invoke.
func runMutatorGuest(t *testing.T, uri string, payload []byte) []byte {
	t.Helper()
	bytesGz, ok := embedded.Lookup(uri)
	if !ok {
		t.Fatalf("embedded guest %q not registered (rebuild embedded guests: go generate ./internal/wasm/embedded/...)", uri)
	}
	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name:       uri,
		Bytes:      bytesGz,
		PluginType: wasm.PluginTypeGovernance,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe(%q): %v", uri, err)
	}
	defer plugin.Close(ctx)
	if desc.PluginType != string(wasm.PluginTypeGovernance) {
		t.Fatalf("%q describe pluginType = %q, want %q", uri, desc.PluginType, wasm.PluginTypeGovernance)
	}
	out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.MutateExport, payload)
	if err != nil {
		t.Fatalf("InvokeExport(%q, mutate): %v", uri, err)
	}
	return out
}

func mustAssembleInput(t *testing.T, topo *topology.TopologyResult, sources []topology.MutatorSource, settings map[string]any) []byte {
	t.Helper()
	mi, err := topology.AssembleMutatorInput(topo, resolver.CatalogLookup{},
		topology.CompilationContext{ReleaseName: "demo", Environment: "dev"}, sources, settings)
	if err != nil {
		t.Fatalf("AssembleMutatorInput: %v", err)
	}
	payload, err := mi.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal MutatorInput: %v", err)
	}
	return payload
}

func labelOf(t *testing.T, topo *topology.TopologyResult, nodeID, key string) (string, bool) {
	t.Helper()
	node, ok := topo.Nodes.Get(nodeID)
	if !ok {
		t.Fatalf("node %q absent", nodeID)
	}
	return node.Labels.Get(key)
}

// TestEmbeddedMutatorsResolve proves both mutator guests register under their
// bare identities in the embedded set — the identities the plugin-resolution
// cascade's embed tier and every Plugin entity's metadata.name resolve against.
func TestEmbeddedMutatorsResolve(t *testing.T) {
	uris := embedded.URIs()
	for _, want := range []string{labelInjectorURI, selectorTaggerURI} {
		if !slices.Contains(uris, want) {
			t.Errorf("embedded URIs %v do not include %q", uris, want)
		}
		if b, ok := embedded.Lookup(want); !ok || len(b) == 0 {
			t.Errorf("embedded.Lookup(%q) = (%d bytes, %v), want non-empty hit", want, len(b), ok)
		}
	}
}

// TestLabelInjectorGuestAppliesWithinConfinement runs the label-injector guest
// end-to-end: a settings rule matching the frontend node injects a selector
// label, and the emitted patches apply through the host confinement check onto
// the topology. Applying via ApplyMutatorPatches is the confinement proof — a
// non-label patch would halt with ErrMutatorPatchRejected.
func TestLabelInjectorGuestAppliesWithinConfinement(t *testing.T) {
	topo := twoNodeTopo()
	settings := map[string]any{
		"rules": []any{
			map[string]any{
				"match": map[string]any{"tier": "frontend"},
				"set":   map[string]any{"feature.aggrega/selector": "shared-logging"},
			},
		},
	}
	payload := mustAssembleInput(t, topo, nil, settings)

	out := runMutatorGuest(t, labelInjectorURI, payload)

	var mo topology.MutatorOutput
	if err := mo.UnmarshalJSON(out); err != nil {
		t.Fatalf("decode MutatorOutput: %v", err)
	}
	if len(mo.Patches) != 1 {
		t.Fatalf("patch count = %d, want 1 (frontend node only)", len(mo.Patches))
	}
	if err := topology.ApplyMutatorPatches(topo, mo); err != nil {
		t.Fatalf("ApplyMutatorPatches (confinement): %v", err)
	}

	if v, ok := labelOf(t, topo, "web", "feature.aggrega/selector"); !ok || v != "shared-logging" {
		t.Errorf("web selector label = (%q,%v), want (\"shared-logging\",true)", v, ok)
	}
	if _, ok := labelOf(t, topo, "db", "feature.aggrega/selector"); ok {
		t.Errorf("db gained a selector label; the rule should match the frontend node only")
	}
}

// TestSelectorTaggerGuestAppliesWithinConfinement runs the selector-tagger guest
// end-to-end from a definition file: a component-to-tag map tags both nodes, and
// the emitted patches apply through the host confinement check.
func TestSelectorTaggerGuestAppliesWithinConfinement(t *testing.T) {
	topo := twoNodeTopo()
	sources := []topology.MutatorSource{{
		Path:    "mapping.yaml",
		Content: "core/web: frontend\ncore/db: backend\n",
	}}
	settings := map[string]any{"default": "untagged"}
	payload := mustAssembleInput(t, topo, sources, settings)

	out := runMutatorGuest(t, selectorTaggerURI, payload)

	var mo topology.MutatorOutput
	if err := mo.UnmarshalJSON(out); err != nil {
		t.Fatalf("decode MutatorOutput: %v", err)
	}
	if err := topology.ApplyMutatorPatches(topo, mo); err != nil {
		t.Fatalf("ApplyMutatorPatches (confinement): %v", err)
	}

	if v, ok := labelOf(t, topo, "web", "tag"); !ok || v != "frontend" {
		t.Errorf("web tag = (%q,%v), want (\"frontend\",true)", v, ok)
	}
	if v, ok := labelOf(t, topo, "db", "tag"); !ok || v != "backend" {
		t.Errorf("db tag = (%q,%v), want (\"backend\",true)", v, ok)
	}
}

// TestMutatorGuestDeterministicOutput proves the label-injector guest emits
// byte-identical MutatorOutput across repeated invocations of identical input.
func TestMutatorGuestDeterministicOutput(t *testing.T) {
	topo := twoNodeTopo()
	settings := map[string]any{
		"rules": []any{
			map[string]any{"match": map[string]any{}, "set": map[string]any{"b": "2", "a": "1"}},
		},
	}
	payload := mustAssembleInput(t, topo, nil, settings)

	first := runMutatorGuest(t, labelInjectorURI, payload)
	for i := 0; i < 4; i++ {
		again := runMutatorGuest(t, labelInjectorURI, payload)
		if string(again) != string(first) {
			t.Fatalf("guest output not deterministic:\n first: %s\nagain: %s", first, again)
		}
	}
}
