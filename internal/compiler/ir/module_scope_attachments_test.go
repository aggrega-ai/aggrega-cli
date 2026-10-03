// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs here cover which components a scope-wide attachment reaches when an
// Aggregate imports a module whose own subsystem carries the same name as one of
// the importer's.
//
// They are written against a composition a real flattening produced rather than
// a hand-built node set, because the coincidence they exercise is one flattening
// creates: a module's components carry a subsystem path relative to the module's
// own document, so a module component in the module's subsystem "tier" and an
// importer's component in the importer's subsystem "tier" carry the same path.
// A hand-built node could be given any path, and would prove the shape the test
// author assumed rather than the one the build makes.

const (
	scopeRootFile      = "aggregate.yaml"
	scopeModuleFile    = "catalog/aggregate/local/sessions/1.0.0/aggregate.yaml"
	scopeModuleType    = "local/sessions"
	scopeComponentType = "core/datastore/postgres"
	scopeFeatureType   = "core/logging"
)

// scopeFixture names one composition: the importer's subsystem "tier" holding
// "ledger", and the imported module "sessions" whose own top-level subsystem —
// named moduleScope — holds "store" and, nested inside it, "replica".
type scopeFixture struct {
	// moduleScope is the name of the module's own top-level subsystem.
	moduleScope string
	// rootAttaches is whether the importer's "tier" declares the attachment
	// "tiersecret".
	rootAttaches bool
	// moduleAttaches is whether the module's own subsystem declares the
	// attachment "modsecret".
	moduleAttaches bool
}

// scopeComponents is a set of components of one catalog type, in author order.
func scopeComponents(names ...string) types.OrderedMap[types.ComponentInstance] {
	out := types.OrderedMap[types.ComponentInstance]{}
	for _, name := range names {
		out.Set(name, types.ComponentInstance{Type: scopeComponentType})
	}
	return out
}

// scopeAttachmentFrom is one scope-wide attachment whose single value names the
// document it was written in, so an assertion can tell whose attachment a
// component received and not merely that it received one.
func scopeAttachmentFrom(document string) types.FeatureInstance {
	values := types.OrderedMap[any]{}
	values.Set("written-in", document)
	return types.FeatureInstance{Type: scopeFeatureType, Values: values}
}

// buildScopeComposition flattens the fixture through the topology build, with a
// loader handing back the module, and returns the composition together with the
// resolve result both stages read.
func buildScopeComposition(t *testing.T, f scopeFixture) (*topology.TopologyResult, *resolver.ResolveResult) {
	t.Helper()

	inner := types.Subsystem{Components: scopeComponents("replica")}
	moduleScope := types.Subsystem{Components: scopeComponents("store")}
	moduleScope.Subsystems.Set("inner", inner)
	if f.moduleAttaches {
		moduleScope.Features.Set("modsecret", scopeAttachmentFrom("module"))
	}
	moduleSubs := types.OrderedMap[types.Subsystem]{}
	moduleSubs.Set(f.moduleScope, moduleScope)
	module := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: scopeModuleType},
		Spec:     types.AggregateSpec{Subsystems: moduleSubs},
	}

	tier := types.Subsystem{Components: scopeComponents("ledger")}
	if f.rootAttaches {
		tier.Features.Set("tiersecret", scopeAttachmentFrom("root"))
	}
	rootSubs := types.OrderedMap[types.Subsystem]{}
	rootSubs.Set("tier", tier)
	imports := types.OrderedMap[types.SubAggregate]{}
	imports.Set("sessions", types.SubAggregate{Type: scopeModuleType})
	root := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "storefront"},
		Spec:     types.AggregateSpec{Subsystems: rootSubs, Aggregates: imports},
	}

	placed := map[string]*resolver.BoundaryInfra{}
	for _, id := range []string{"ledger", "sessions.store", "sessions.replica"} {
		placed[id] = &resolver.BoundaryInfra{BoundaryPath: "data"}
	}
	res := &resolver.ResolveResult{
		Aggregate:     root,
		AggregatePath: scopeRootFile,
		BoundaryInfra: placed,
	}
	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == scopeModuleType {
			return module, scopeModuleFile, nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown module type %q", typeName)
	}
	topo, err := topology.Build(topology.BuildConfig{
		ResolveResult:     res,
		LoadSubAggregate:  loader,
		RootAggregateFile: scopeRootFile,
	})
	if err != nil {
		t.Fatalf("topology build: %v", err)
	}
	return topo, res
}

// attachmentNames is the attachments the graph holds for one node, in the
// graph's order, and nil for a node holding none.
func attachmentNames(graph *types.IRGraph, nodeID string) []string {
	held, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		return nil
	}
	return held.Keys()
}

// TestBuildIR_AnImportersScopeAttachmentReachesOnlyTheComponentsItDeclares proves
// a scope-wide attachment the importing Aggregate declares reaches the components
// that Aggregate declares in that scope, and never a module component that only
// shares the scope's name; and that a module's own scope-wide attachment reaches
// none of the module's components.
//
// Every row first checks the coincidence is really there — the importer's
// component and the module's component carry the same subsystem path — so a row
// cannot pass because the composition stopped producing the shape it is about.
func TestBuildIR_AnImportersScopeAttachmentReachesOnlyTheComponentsItDeclares(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fixture scopeFixture
		// samePath is whether "ledger" and "sessions.store" carry one path.
		samePath bool
		// want is each node's attachments, in order; a node absent here holds none.
		want map[string][]string
	}{
		{
			name:     "the importer's attachment reaches its own component and not the module's under the same scope name",
			fixture:  scopeFixture{moduleScope: "tier", rootAttaches: true},
			samePath: true,
			want:     map[string][]string{"ledger": {"tiersecret"}},
		},
		{
			name:     "a module's own scope attachment reaches none of its components",
			fixture:  scopeFixture{moduleScope: "tier", moduleAttaches: true},
			samePath: true,
			want:     map[string][]string{},
		},
		{
			name:     "both declared under one scope name, the importer's stays with its own component",
			fixture:  scopeFixture{moduleScope: "tier", rootAttaches: true, moduleAttaches: true},
			samePath: true,
			want:     map[string][]string{"ledger": {"tiersecret"}},
		},
		{
			name:     "the control: a module scope under another name receives nothing either",
			fixture:  scopeFixture{moduleScope: "zone", rootAttaches: true, moduleAttaches: true},
			samePath: false,
			want:     map[string][]string{"ledger": {"tiersecret"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			topo, res := buildScopeComposition(t, tc.fixture)

			var graph *types.IRGraph
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the graph build panicked: %v", r)
					}
				}()
				graph, _, _, err = buildIR(BuildIRConfig{
					Topology:      topo,
					ResolveResult: res,
					CatalogLookup: testCatalog(),
				})
			}()
			if err != nil {
				t.Fatalf("graph build: %v", err)
			}

			ledger, ok := graph.Spec.Nodes.Get("ledger")
			if !ok {
				t.Fatal("the graph holds no node \"ledger\"")
			}
			store, ok := graph.Spec.Nodes.Get("sessions.store")
			if !ok {
				t.Fatal("the graph holds no node \"sessions.store\"")
			}
			if got := ledger.SubsystemPath == store.SubsystemPath; got != tc.samePath {
				t.Fatalf("subsystem paths ledger=%q sessions.store=%q; same=%v, want %v",
					ledger.SubsystemPath, store.SubsystemPath, got, tc.samePath)
			}

			for _, node := range []string{"ledger", "sessions.store", "sessions.replica"} {
				if got := attachmentNames(graph, node); !slices.Equal(got, tc.want[node]) {
					t.Errorf("attachments on %q = %v, want %v", node, got, tc.want[node])
				}
			}
			if entry, ok := graph.Spec.FeatureValuesMapping.Get("ledger"); ok {
				if held, ok := entry.Get("tiersecret"); ok {
					if writtenIn, _ := held.Values.Get("written-in"); writtenIn != "root" {
						t.Errorf("ledger's tiersecret was written in %v, want the importer's (root)", writtenIn)
					}
				}
			}
		})
	}
}

// TestAttachmentOrigins_AModuleComponentIsNeverPositionedInTheImportersScope
// proves the record of where an attachment a connection came from was written
// reads the importer's scopes for the importer's own component and never for a
// module component sharing the scope's name — so a connection a module's
// component draws is never located in a document that did not declare it.
//
// The importer's own component is the positive control: its entry names the
// importer's file and scope, so the module's missing entry cannot be read as the
// record holding nothing for anyone.
func TestAttachmentOrigins_AModuleComponentIsNeverPositionedInTheImportersScope(t *testing.T) {
	t.Parallel()
	topo, res := buildScopeComposition(t, scopeFixture{moduleScope: "tier", rootAttaches: true})

	cases := []struct {
		name string
		node string
		// positioned is whether the node's record names the importer's scope for
		// "tiersecret".
		positioned bool
	}{
		{name: "the importer's own component", node: "ledger", positioned: true},
		{name: "a module component under the same scope name", node: "sessions.store", positioned: false},
		{name: "a module component nested beneath it", node: "sessions.replica", positioned: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			topoNode, ok := topo.Nodes.Get(tc.node)
			if !ok {
				t.Fatalf("the composition holds no node %q", tc.node)
			}
			origin, recorded := attachmentOrigins(tc.node, topoNode, topo, res)["tiersecret"]
			if !tc.positioned {
				if recorded {
					t.Errorf("%q has an origin for the importer's tiersecret, at %q in %q; want none",
						tc.node, origin.Site.ScopePath, origin.SourceFile)
				}
				return
			}
			if !recorded {
				t.Fatalf("%q has no origin for tiersecret; want the importer's scope", tc.node)
			}
			if origin.SourceFile != scopeRootFile || origin.Site.ScopePath != "tier" {
				t.Errorf("%q's tiersecret origin = %q in %q, want %q in %q",
					tc.node, origin.Site.ScopePath, origin.SourceFile, "tier", scopeRootFile)
			}
		})
	}
}
