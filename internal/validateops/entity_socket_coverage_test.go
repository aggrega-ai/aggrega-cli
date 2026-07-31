// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	socketedType = "example/compute/socketed"
	otherType    = "example/compute/other"
)

// socketedComponent declares, in author order, one required socket, one
// optional socket, and a second required socket — so a test can tell author
// order from sorted order and required from optional in one fixture.
func socketedComponent(name string) types.Component {
	us := types.OrderedMap[types.UsesSchemaEntry]{}
	us.Set("zebra_backend", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=datastore", Required: true})
	us.Set("optional_cache", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=datastore"})
	us.Set("alpha_registry", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=devtool", Required: true})
	return types.Component{
		Metadata: types.ComponentMetadata{Name: name},
		Spec:     types.ComponentSpec{UsesSchema: us},
	}
}

func socketedLookup() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case socketedType, otherType:
				return socketedComponent(typeURI), true
			}
			return types.Component{}, false
		},
	}
}

func TestDeclaredRequiredSockets_AuthorOrderAndRequiredOnly(t *testing.T) {
	t.Parallel()
	comp := socketedComponent(socketedType)
	got := declaredRequiredSockets(&comp)
	// Author order, not sorted: zebra_backend precedes alpha_registry because
	// that is how the entity declares them, and the report has to be
	// byte-identical across runs of the same input.
	want := []string{"zebra_backend", "alpha_registry"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("declaredRequiredSockets = %v, want %v", got, want)
	}
}

func TestDeclaredRequiredSockets_NoUsesSchemaIsEmpty(t *testing.T) {
	t.Parallel()
	var comp types.Component
	if got := declaredRequiredSockets(&comp); len(got) != 0 {
		t.Fatalf("declaredRequiredSockets on a socketless Component = %v, want empty", got)
	}
}

func TestDeclaredRequiredSockets_AllOptionalIsEmpty(t *testing.T) {
	t.Parallel()
	us := types.OrderedMap[types.UsesSchemaEntry]{}
	us.Set("cache", types.UsesSchemaEntry{Selector: "x=y"})
	comp := types.Component{Spec: types.ComponentSpec{UsesSchema: us}}
	if got := declaredRequiredSockets(&comp); len(got) != 0 {
		t.Fatalf("declaredRequiredSockets with no required socket = %v, want empty", got)
	}
}

func TestLookupWithSocketsOptional_ClearsOnlyNamedSocketsOfNamedType(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithSocketsOptional(socketedLookup(), socketedType, []string{"zebra_backend", "alpha_registry"})

	comp, found := narrowed.Component(socketedType)
	if !found {
		t.Fatalf("narrowed lookup lost type %q", socketedType)
	}
	for _, socket := range []string{"zebra_backend", "alpha_registry"} {
		entry, ok := comp.Spec.UsesSchema.Get(socket)
		if !ok {
			t.Fatalf("socket %q disappeared from the narrowed view", socket)
		}
		if entry.Required {
			t.Errorf("socket %q still required in the narrowed view", socket)
		}
		if entry.Selector == "" {
			t.Errorf("socket %q lost its selector; only the required flag may change", socket)
		}
	}

	// A different type resolved through the same narrowed lookup is untouched.
	other, found := narrowed.Component(otherType)
	if !found {
		t.Fatalf("narrowed lookup lost type %q", otherType)
	}
	entry, _ := other.Spec.UsesSchema.Get("zebra_backend")
	if !entry.Required {
		t.Error("narrowing reached a type it was not scoped to")
	}
}

func TestLookupWithSocketsOptional_PreservesAuthorOrder(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithSocketsOptional(socketedLookup(), socketedType, []string{"zebra_backend"})
	comp, _ := narrowed.Component(socketedType)
	want := "zebra_backend,optional_cache,alpha_registry"
	if got := strings.Join(comp.Spec.UsesSchema.Keys(), ","); got != want {
		t.Fatalf("socket order = %q, want %q", got, want)
	}
}

func TestLookupWithSocketsOptional_DoesNotMutateTheSourceView(t *testing.T) {
	t.Parallel()
	base := socketedLookup()
	narrowed := lookupWithSocketsOptional(base, socketedType, []string{"zebra_backend"})
	if _, found := narrowed.Component(socketedType); !found {
		t.Fatal("narrowed lookup lost the type")
	}
	// Reading through the narrowed view must not have written back into the
	// component the underlying accessor serves.
	comp, _ := base.Component(socketedType)
	entry, _ := comp.Spec.UsesSchema.Get("zebra_backend")
	if !entry.Required {
		t.Error("the narrowed view wrote through to the source component")
	}
}

func TestLookupWithSocketsOptional_NoSocketsIsIdentity(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithSocketsOptional(socketedLookup(), socketedType, nil)
	comp, _ := narrowed.Component(socketedType)
	entry, _ := comp.Spec.UsesSchema.Get("zebra_backend")
	if !entry.Required {
		t.Error("an empty socket list must leave the view untouched")
	}
}

// TestLookupWithSocketsOptional_RuleStaysInOnePlace asks the resolver's own
// exported required-socket rule about both views. It is the guard against this
// harness growing a second, local notion of what a required socket means: the
// narrowing has to change the ANSWER the shared rule gives, while the rule
// itself stays the only thing deciding. If someone later reimplements the rule
// inside this package, the un-narrowed half of this test keeps reporting the
// error and the reimplementation gains no cover.
func TestLookupWithSocketsOptional_RuleStaysInOnePlace(t *testing.T) {
	t.Parallel()
	base := socketedLookup()
	comp, _ := base.Component(socketedType)

	// Nothing populated, view as authored: the shared rule reports both
	// required sockets. This is what a build sees.
	asAuthored := resolver.ValidateRequiredSockets(comp, syntheticInstanceName, ".spec.components[\"instance\"]", nil)
	if len(asAuthored) != 2 {
		t.Fatalf("as authored: expected the shared rule to report 2 unbound required sockets, got %d: %+v", len(asAuthored), asAuthored)
	}

	// Same rule, same empty population, narrowed view: nothing to report.
	narrowed := lookupWithSocketsOptional(base, socketedType, declaredRequiredSockets(&comp))
	narrowedComp, _ := narrowed.Component(socketedType)
	declined := resolver.ValidateRequiredSockets(narrowedComp, syntheticInstanceName, ".spec.components[\"instance\"]", nil)
	if len(declined) != 0 {
		t.Fatalf("narrowed: expected the shared rule to report nothing, got %d: %+v", len(declined), declined)
	}
}
