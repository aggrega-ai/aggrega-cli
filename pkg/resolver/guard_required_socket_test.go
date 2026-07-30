// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func requiredSocketCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/compute/api" {
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server", Required: true})
				usesSchema.Set("caches", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=resource"})
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/compute/api"},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardRequiredUsesSockets_MissingRequiredSocket(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api"})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket, got %v", errs[0].Err)
	}
	// Top-level component folds into the synthesized default → the locator is
	// the root shape the author typed (with `.spec` and bracket-quoted), never
	// the bare `.components.api` the site emitted before joining the shared
	// flag-aware path helper.
	if errs[0].Field != `.spec.components["api"].uses["databases"]` {
		t.Errorf("field: %q", errs[0].Field)
	}
	if !strings.Contains(errs[0].Message, "databases") {
		t.Errorf("message: %q", errs[0].Message)
	}
}

// TestGuardRequiredUsesSockets_RealSubsystemRendersQualified is the accuracy
// proof: a missing-required-socket error on a component in a DECLARED subsystem
// renders the qualified locator, not the flattened `.components.<name>` the site
// emitted before routing through the flag-aware helper. RED before the fix
// (dropped both `.spec` and the subsystem path), GREEN after.
func TestGuardRequiredUsesSockets_RealSubsystemRendersQualified(t *testing.T) {
	t.Parallel()
	subComps := types.OrderedMap[types.ComponentInstance]{}
	subComps.Set("worker", types.ComponentInstance{Type: "core/compute/api"})
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("data", types.Subsystem{Components: subComps})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Subsystems: subs}}

	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Field != `.spec.subsystems.data.components["worker"].uses["databases"]` {
		t.Errorf("real subsystem component must render qualified; got %q", errs[0].Field)
	}
}

func TestGuardRequiredUsesSockets_OptionalMissingOK(t *testing.T) {
	t.Parallel()
	// "caches" is optional — its absence is fine, only "databases" (required) triggers.
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("main", types.VerboseUsesEntry{Target: "db"})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_CompactListFormPopulates(t *testing.T) {
	t.Parallel()
	// Compact list form: `databases: [db]` should count as populated.
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("compact list form should satisfy required socket, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_EmptyMapIsNotPopulated(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: types.OrderedMap[types.VerboseUsesEntry]{}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("empty verbose map should count as not-populated, got %+v", errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket")
	}
}

func TestGuardRequiredUsesSockets_SubsystemRecursion(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("nested-api", types.ComponentInstance{Type: "core/compute/api"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("frontend", types.Subsystem{Components: components})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}
	errs := guardRequiredUsesSockets(normalized(t, agg), nil, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from subsystem-nested component, got %+v", errs)
	}
	if !strings.Contains(errs[0].Field, "nested-api") {
		t.Errorf("expected nested-api in field path, got %q", errs[0].Field)
	}
}

// releaseBindingSocket builds a one-boundary Release whose boundary component
// "api" carries a per-edge `uses` entry under the given socket and alias. A
// non-empty target makes the entry an edge-creating binding; an empty target
// makes it an infra-only configuration of an edge that must already exist.
func releaseBindingSocket(socket, alias, target string) *types.Release {
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set(alias, types.ReleaseUsesEntry{Target: target})
	ruses := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	ruses.Set(socket, aliases)
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set("api", types.BoundaryComponent{Uses: ruses})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: comps})
	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
	rel.Spec.Boundaries = boundaries
	return rel
}

// aggWithUnboundRequiredSocket builds a one-component Aggregate ("api") that
// declares no `uses` at all, leaving the required "databases" socket its
// component type declares entirely unbound.
func aggWithUnboundRequiredSocket() *types.Aggregate {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api"})
	return &types.Aggregate{Spec: types.AggregateSpec{Components: components}}
}

// TestGuardRequiredUsesSockets_ReleaseBoundSocketSatisfies is the correctness
// proof for the operator edge surface: a required socket the Aggregate leaves
// unbound and the Release wires to an existing component is satisfied. The
// Release may create an edge into a declared-but-unbound socket, so the socket
// IS populated in the graph that gets rendered, and the population must be read
// from both authoring documents rather than from the Aggregate alone.
func TestGuardRequiredUsesSockets_ReleaseBoundSocketSatisfies(t *testing.T) {
	t.Parallel()
	rel := releaseBindingSocket("databases", "main", "db")
	errs := guardRequiredUsesSockets(normalized(t, aggWithUnboundRequiredSocket()), rel, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Fatalf("a Release-wired required socket must satisfy the rule; got %+v", errs)
	}
}

// TestGuardRequiredUsesSockets_ReleaseTargetlessEntryDoesNotSatisfy pins the
// boundary of the rule above. An entry carrying no target does not create an
// edge — it configures one that must already exist — so it cannot populate a
// socket nothing else bound, and the required-socket halt still fires.
func TestGuardRequiredUsesSockets_ReleaseTargetlessEntryDoesNotSatisfy(t *testing.T) {
	t.Parallel()
	rel := releaseBindingSocket("databases", "main", "")
	errs := guardRequiredUsesSockets(normalized(t, aggWithUnboundRequiredSocket()), rel, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("a targetless Release entry creates no edge and must not satisfy the socket; got %+v", errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket, got %v", errs[0].Err)
	}
}

// TestGuardRequiredUsesSockets_ReleaseBindsOtherSocketStillHalts proves the
// satisfaction is per-socket rather than per-component: a Release wiring some
// other socket leaves the required one unbound and the halt stands.
func TestGuardRequiredUsesSockets_ReleaseBindsOtherSocketStillHalts(t *testing.T) {
	t.Parallel()
	rel := releaseBindingSocket("caches", "hot", "redis")
	errs := guardRequiredUsesSockets(normalized(t, aggWithUnboundRequiredSocket()), rel, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("wiring an unrelated socket must not satisfy the required one; got %+v", errs)
	}
	if errs[0].Field != `.spec.components["api"].uses["databases"]` {
		t.Errorf("field: %q", errs[0].Field)
	}
}

// TestGuardRequiredUsesSockets_ReleaseBindsUnrelatedNodeStillHalts proves the
// satisfaction is per-node: a Release wiring the same socket name on a
// DIFFERENT component leaves this component's socket unbound.
func TestGuardRequiredUsesSockets_ReleaseBindsUnrelatedNodeStillHalts(t *testing.T) {
	t.Parallel()
	rel := releaseBindingSocket("databases", "main", "db")
	// Re-key the boundary component from "api" to a node that is not the one
	// carrying the required socket.
	comps := types.OrderedMap[types.BoundaryComponent]{}
	bc, _ := rel.Spec.Boundaries.Get("prod")
	inner, _ := bc.Components.Get("api")
	comps.Set("other", inner)
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: comps})
	rel.Spec.Boundaries = boundaries

	errs := guardRequiredUsesSockets(normalized(t, aggWithUnboundRequiredSocket()), rel, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("a binding on another node must not satisfy this node's socket; got %+v", errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket, got %v", errs[0].Err)
	}
}

// TestGuardRequiredUsesSockets_NilReleaseUnchanged proves the Aggregate-only
// path is untouched: with no Release at all the missing required socket halts
// exactly as before.
func TestGuardRequiredUsesSockets_NilReleaseUnchanged(t *testing.T) {
	t.Parallel()
	errs := guardRequiredUsesSockets(normalized(t, aggWithUnboundRequiredSocket()), nil, requiredSocketCatalog())
	if len(errs) != 1 || !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Fatalf("want exactly 1 ErrMissingRequiredSocket with no Release; got %+v", errs)
	}
}

// TestSocketIsPopulated_VerboseFormRecognizesPopulated pins the
// typed-wrapper Verbose-field arm of socketIsPopulated. The verbose
// form's emptiness is determined by the inner OrderedMap's length;
// empty Verbose with no Compact entries returns false; populated
// Verbose returns true.
func TestSocketIsPopulated_VerboseFormRecognizesPopulated(t *testing.T) {
	t.Parallel()

	t.Run("populated_verbose", func(t *testing.T) {
		t.Parallel()
		verbose := types.OrderedMap[types.VerboseUsesEntry]{}
		verbose.Set("alias", types.VerboseUsesEntry{Target: "main-db"})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
		if !socketIsPopulated(uses, "databases") {
			t.Error("populated Verbose should count as populated, got false")
		}
	})

	t.Run("empty_verbose_no_compact", func(t *testing.T) {
		t.Parallel()
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{})
		if socketIsPopulated(uses, "databases") {
			t.Error("empty UsesSocketValue should count as unpopulated, got true")
		}
	})

	t.Run("compact_with_empty_string_only_unpopulated", func(t *testing.T) {
		t.Parallel()
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Compact: []string{""}})
		if socketIsPopulated(uses, "databases") {
			t.Error("compact with only empty strings should count as unpopulated, got true")
		}
	})
}
