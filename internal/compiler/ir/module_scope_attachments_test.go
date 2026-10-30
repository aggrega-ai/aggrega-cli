// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
	// attachment "modsecret". Its values name the module as the document it was
	// written in, and carry a second leaf, "scope-only", no other declaration
	// writes.
	moduleAttaches bool
	// innerAttaches is whether the module's nested subsystem "inner" declares the
	// attachment "innersecret".
	innerAttaches bool
	// componentAttaches is whether the module's component "store" declares an
	// attachment of its own, "storeown".
	componentAttaches bool
	// componentOverrides is whether "store" declares "modsecret" itself, naming
	// the component as the place it was written.
	componentOverrides bool
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
	if f.innerAttaches {
		inner.Features.Set("innersecret", scopeAttachmentFrom("module-inner"))
	}
	moduleScope := types.Subsystem{Components: scopeComponents("store")}
	if f.componentAttaches || f.componentOverrides {
		store, _ := moduleScope.Components.Get("store")
		if f.componentAttaches {
			store.Features.Set("storeown", scopeAttachmentFrom("module-component"))
		}
		if f.componentOverrides {
			store.Features.Set("modsecret", scopeAttachmentFrom("component"))
		}
		moduleScope.Components.Set("store", store)
	}
	moduleScope.Subsystems.Set("inner", inner)
	if f.moduleAttaches {
		modsecret := scopeAttachmentFrom("module")
		modsecret.Values.Set("scope-only", "kept")
		moduleScope.Features.Set("modsecret", modsecret)
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
// the module's components beneath that scope, nested ones included, and never
// the importer's component sharing the scope's name.
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
			name:     "a module's own scope attachment reaches the module's components beneath it",
			fixture:  scopeFixture{moduleScope: "tier", moduleAttaches: true},
			samePath: true,
			want:     map[string][]string{"sessions.store": {"modsecret"}, "sessions.replica": {"modsecret"}},
		},
		{
			name:     "both declared under one scope name, each stays with its own document's components",
			fixture:  scopeFixture{moduleScope: "tier", rootAttaches: true, moduleAttaches: true},
			samePath: true,
			want: map[string][]string{
				"ledger": {"tiersecret"}, "sessions.store": {"modsecret"}, "sessions.replica": {"modsecret"},
			},
		},
		{
			name:     "the control: a module scope under another name, each attachment reaches its own document's components",
			fixture:  scopeFixture{moduleScope: "zone", rootAttaches: true, moduleAttaches: true},
			samePath: false,
			want: map[string][]string{
				"ledger": {"tiersecret"}, "sessions.store": {"modsecret"}, "sessions.replica": {"modsecret"},
			},
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
			for _, node := range []string{"sessions.store", "sessions.replica"} {
				if entry, ok := graph.Spec.FeatureValuesMapping.Get(node); ok {
					if held, ok := entry.Get("modsecret"); ok {
						if writtenIn, _ := held.Values.Get("written-in"); writtenIn != "module" {
							t.Errorf("%s's modsecret was written in %v, want the module's", node, writtenIn)
						}
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

// buildScopeGraph builds the graph of one fixture composition, failing the test
// on a build error and on a panic.
func buildScopeGraph(t *testing.T, f scopeFixture) *types.IRGraph {
	t.Helper()
	topo, res := buildScopeComposition(t, f)
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
	return graph
}

// leafOf is one value leaf of one attachment a node holds, and whether it is
// there.
func leafOf(graph *types.IRGraph, nodeID, attachment, leaf string) (any, bool) {
	held, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		return nil, false
	}
	entry, ok := held.Get(attachment)
	if !ok {
		return nil, false
	}
	return entry.Values.Get(leaf)
}

// TestBuildIR_AModuleScopeAttachmentExecutesWithinTheModulesTier proves where a
// module's own subsystem attachments execute: directly after the module
// component's own attachments, and outer subsystem before inner — the order the
// graph records a node's attachments in, which is the order their mappings run.
func TestBuildIR_AModuleScopeAttachmentExecutesWithinTheModulesTier(t *testing.T) {
	t.Parallel()
	graph := buildScopeGraph(t, scopeFixture{
		moduleScope: "tier", moduleAttaches: true, innerAttaches: true, componentAttaches: true,
	})
	for node, want := range map[string][]string{
		"sessions.store":   {"storeown", "modsecret"},
		"sessions.replica": {"modsecret", "innersecret"},
	} {
		if got := attachmentNames(graph, node); !slices.Equal(got, want) {
			t.Errorf("attachments on %q = %v, want %v", node, got, want)
		}
	}
}

// TestBuildIR_AModuleComponentRefinesItsSubsystemsAttachmentLeafByLeaf proves the
// precedence on a contested leaf: an attachment the module's component declares
// under the same name as one its own subsystem declares wins the leaf both write,
// and the leaf only the subsystem writes survives — the narrower scope refines
// the broader one inside the module exactly as it does in the Aggregate.
func TestBuildIR_AModuleComponentRefinesItsSubsystemsAttachmentLeafByLeaf(t *testing.T) {
	t.Parallel()
	graph := buildScopeGraph(t, scopeFixture{moduleScope: "tier", moduleAttaches: true, componentOverrides: true})
	if got, _ := leafOf(graph, "sessions.store", "modsecret", "written-in"); got != "component" {
		t.Errorf("store's modsecret written-in = %v, want the component's (component)", got)
	}
	if got, _ := leafOf(graph, "sessions.store", "modsecret", "scope-only"); got != "kept" {
		t.Errorf("store's modsecret scope-only = %v, want the subsystem's leaf kept", got)
	}
	// The nested component declares nothing of its own, so the subsystem's value
	// stands there.
	if got, _ := leafOf(graph, "sessions.replica", "modsecret", "written-in"); got != "module" {
		t.Errorf("replica's modsecret written-in = %v, want the subsystem's (module)", got)
	}
}

// TestAttachmentScope_AModulesSubsystemRanksBelowItsComponentsAndItsComposer pins
// the rank a module's own subsystem attachment takes on a contested leaf: below
// the module's own component attachments, and below every scope of the composing
// document — the component scope and the shared scope alike — so a module cannot
// defeat the Aggregate importing it by declaring at a scope of its own. Within the
// module it outranks nothing it is compared with here.
//
// Only the comparison is asserted, because no composing-document contribution
// reaches a module component's values in a build: the composing Aggregate's
// scopes do not reach a module's components and the Release writes the other
// pillar. The rank is what decides such a contest wherever one arises.
func TestAttachmentScope_AModulesSubsystemRanksBelowItsComponentsAndItsComposer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		higher attachmentScope
		lower  attachmentScope
	}{
		{"the module's component over its subsystem", scopeComposedComponent, scopeComposedShared},
		{"the composing document's shared scope over a module's subsystem", scopeShared, scopeComposedShared},
		{"the composing document's component over a module's subsystem", scopeComponent, scopeComposedShared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.higher.outranks(tc.lower) {
				t.Errorf("%+v does not outrank %+v", tc.higher, tc.lower)
			}
			if tc.lower.outranks(tc.higher) {
				t.Errorf("%+v outranks %+v", tc.lower, tc.higher)
			}
		})
	}
}

// TestAttachmentOrigins_AModuleScopeAttachmentIsPositionedInTheModulesDocument
// proves where a connection an attachment of a module's own subsystem draws is
// recorded as written: the module's own file, at the subsystem that declares the
// attachment — never the importing document.
func TestAttachmentOrigins_AModuleScopeAttachmentIsPositionedInTheModulesDocument(t *testing.T) {
	t.Parallel()
	topo, res := buildScopeComposition(t, scopeFixture{moduleScope: "tier", moduleAttaches: true})
	for _, node := range []string{"sessions.store", "sessions.replica"} {
		topoNode, ok := topo.Nodes.Get(node)
		if !ok {
			t.Fatalf("the composition holds no node %q", node)
		}
		origin, recorded := attachmentOrigins(node, topoNode, topo, res)["modsecret"]
		if !recorded {
			t.Fatalf("%q has no origin for modsecret; want the module's scope", node)
		}
		if origin.SourceFile != scopeModuleFile || origin.Site.ScopePath != "tier" ||
			origin.Document != constraint.AuthoringDocumentAggregate {
			t.Errorf("%q's modsecret origin = %q in %q (%v), want %q in %q",
				node, origin.Site.ScopePath, origin.SourceFile, origin.Document, "tier", scopeModuleFile)
		}
	}
}
