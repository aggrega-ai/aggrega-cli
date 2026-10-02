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
// optional socket, and a second required socket — and the same shape again for
// attachment slots — so a test can tell author order from sorted order and
// required from optional in one fixture, on both surfaces.
func socketedComponent(name string) types.Component {
	us := types.OrderedMap[types.UsesSchemaEntry]{}
	us.Set("zebra_backend", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=datastore", Required: true})
	us.Set("optional_cache", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=datastore"})
	us.Set("alpha_registry", types.UsesSchemaEntry{Selector: "component.aggrega.ai/category=devtool", Required: true})
	fs := types.OrderedMap[types.AttachmentSlot]{}
	fs.Set("zeta_password", types.AttachmentSlot{Selector: "feature.aggrega.ai/kind=secret", Inject: []string{"env"}, Required: true})
	fs.Set("optional_token", types.AttachmentSlot{Selector: "feature.aggrega.ai/kind=secret"})
	fs.Set("alpha_key", types.AttachmentSlot{Selector: "feature.aggrega.ai/kind=secret", Required: true})
	return types.Component{
		Metadata: types.ComponentMetadata{Name: name},
		Spec:     types.ComponentSpec{UsesSchema: us, FeaturesSchema: fs},
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

func TestDeclaredRequiredSlots_AuthorOrderAndRequiredOnly(t *testing.T) {
	t.Parallel()
	comp := socketedComponent(socketedType)
	got := declaredRequiredSlots(&comp)
	// Author order, as for sockets: zeta_password precedes alpha_key.
	want := []string{"zeta_password", "alpha_key"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("declaredRequiredSlots = %v, want %v", got, want)
	}
}

func TestDeclaredRequiredSlots_NoFeaturesSchemaOrAllOptionalIsEmpty(t *testing.T) {
	t.Parallel()
	var bare types.Component
	if got := declaredRequiredSlots(&bare); len(got) != 0 {
		t.Fatalf("declaredRequiredSlots on a slotless Component = %v, want empty", got)
	}
	fs := types.OrderedMap[types.AttachmentSlot]{}
	fs.Set("token", types.AttachmentSlot{Selector: "x=y"})
	optional := types.Component{Spec: types.ComponentSpec{FeaturesSchema: fs}}
	if got := declaredRequiredSlots(&optional); len(got) != 0 {
		t.Fatalf("declaredRequiredSlots with no required slot = %v, want empty", got)
	}
}

func TestLookupWithRequirementsDeclined_ClearsOnlyNamedSocketsAndSlotsOfNamedType(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithRequirementsDeclined(socketedLookup(), socketedType, declinedRequirements{
		sockets: []string{"zebra_backend", "alpha_registry"},
		slots:   []string{"zeta_password", "alpha_key"},
	})

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
	for _, name := range []string{"zeta_password", "alpha_key"} {
		slot, ok := comp.Spec.FeaturesSchema.Get(name)
		if !ok {
			t.Fatalf("slot %q disappeared from the narrowed view", name)
		}
		if slot.Required {
			t.Errorf("slot %q still required in the narrowed view", name)
		}
		if slot.Selector == "" {
			t.Errorf("slot %q lost its label constraint; only the required flag may change", name)
		}
	}
	if slot, _ := comp.Spec.FeaturesSchema.Get("zeta_password"); strings.Join(slot.Inject, ",") != "env" {
		t.Errorf("slot zeta_password delivery mechanisms = %v, want [env]; only the required flag may change", slot.Inject)
	}

	// A different type resolved through the same narrowed lookup is untouched.
	other, found := narrowed.Component(otherType)
	if !found {
		t.Fatalf("narrowed lookup lost type %q", otherType)
	}
	if entry, _ := other.Spec.UsesSchema.Get("zebra_backend"); !entry.Required {
		t.Error("the socket narrowing reached a type it was not scoped to")
	}
	if slot, _ := other.Spec.FeaturesSchema.Get("zeta_password"); !slot.Required {
		t.Error("the slot narrowing reached a type it was not scoped to")
	}
}

// TestLookupWithRequirementsDeclined_EachSurfaceOnlyWhatItNames declines on one
// surface at a time, so a narrowing that cleared every requirement whenever it
// was asked to clear any would fail here.
func TestLookupWithRequirementsDeclined_EachSurfaceOnlyWhatItNames(t *testing.T) {
	t.Parallel()
	socketsOnly := lookupWithRequirementsDeclined(socketedLookup(), socketedType, declinedRequirements{sockets: []string{"zebra_backend"}})
	comp, _ := socketsOnly.Component(socketedType)
	if entry, _ := comp.Spec.UsesSchema.Get("alpha_registry"); !entry.Required {
		t.Error("declining zebra_backend also cleared alpha_registry")
	}
	if slot, _ := comp.Spec.FeaturesSchema.Get("zeta_password"); !slot.Required {
		t.Error("declining a socket cleared a slot")
	}

	slotsOnly := lookupWithRequirementsDeclined(socketedLookup(), socketedType, declinedRequirements{slots: []string{"alpha_key"}})
	comp, _ = slotsOnly.Component(socketedType)
	if slot, _ := comp.Spec.FeaturesSchema.Get("alpha_key"); slot.Required {
		t.Error("slot alpha_key still required after being declined")
	}
	if slot, _ := comp.Spec.FeaturesSchema.Get("zeta_password"); !slot.Required {
		t.Error("declining alpha_key also cleared zeta_password")
	}
	if entry, _ := comp.Spec.UsesSchema.Get("zebra_backend"); !entry.Required {
		t.Error("declining a slot cleared a socket")
	}
}

func TestLookupWithRequirementsDeclined_PreservesAuthorOrder(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithRequirementsDeclined(socketedLookup(), socketedType, declinedRequirements{
		sockets: []string{"zebra_backend"},
		slots:   []string{"zeta_password"},
	})
	comp, _ := narrowed.Component(socketedType)
	if got, want := strings.Join(comp.Spec.UsesSchema.Keys(), ","), "zebra_backend,optional_cache,alpha_registry"; got != want {
		t.Fatalf("socket order = %q, want %q", got, want)
	}
	if got, want := strings.Join(comp.Spec.FeaturesSchema.Keys(), ","), "zeta_password,optional_token,alpha_key"; got != want {
		t.Fatalf("slot order = %q, want %q", got, want)
	}
}

func TestLookupWithRequirementsDeclined_DoesNotMutateTheSourceView(t *testing.T) {
	t.Parallel()
	base := socketedLookup()
	narrowed := lookupWithRequirementsDeclined(base, socketedType, declinedRequirements{
		sockets: []string{"zebra_backend"},
		slots:   []string{"zeta_password"},
	})
	if _, found := narrowed.Component(socketedType); !found {
		t.Fatal("narrowed lookup lost the type")
	}
	// Reading through the narrowed view must not have written back into the
	// component the underlying accessor serves.
	comp, _ := base.Component(socketedType)
	if entry, _ := comp.Spec.UsesSchema.Get("zebra_backend"); !entry.Required {
		t.Error("the narrowed view wrote a socket through to the source component")
	}
	if slot, _ := comp.Spec.FeaturesSchema.Get("zeta_password"); !slot.Required {
		t.Error("the narrowed view wrote a slot through to the source component")
	}
}

func TestLookupWithRequirementsDeclined_NothingDeclinedIsIdentity(t *testing.T) {
	t.Parallel()
	narrowed := lookupWithRequirementsDeclined(socketedLookup(), socketedType, declinedRequirements{})
	comp, _ := narrowed.Component(socketedType)
	if entry, _ := comp.Spec.UsesSchema.Get("zebra_backend"); !entry.Required {
		t.Error("an empty decline must leave the sockets untouched")
	}
	if slot, _ := comp.Spec.FeaturesSchema.Get("zeta_password"); !slot.Required {
		t.Error("an empty decline must leave the slots untouched")
	}
}

// requiredThroughTheCompilersOwnReading reports which of a type's declared
// sockets and declared slots carry a requirement, read through the compiler's
// own presentation rather than off the entity.
//
// Both each set and each requirement come from that presentation, which is what
// the required-socket and required-slot rules themselves read, so this helper
// cannot answer differently from a build. Reading the entity's own maps here
// would be the second notion of a requirement the test below exists to refuse.
//
// The presentation is built over an empty Aggregate because none of these facts
// is keyed by a component: they answer about what a type declares, which is the
// whole of what the narrowing changes.
func requiredThroughTheCompilersOwnReading(lookup resolver.CatalogLookup, typeURI string) (sockets, slots []string) {
	view := resolver.NewNormalizedView(types.Normalize(&types.Aggregate{}), nil, lookup, nil)
	for _, socket := range view.DeclaredSocketNames(typeURI) {
		if view.SocketRequired(typeURI, socket) {
			sockets = append(sockets, socket)
		}
	}
	for _, slot := range view.DeclaredSlotNames(typeURI) {
		if view.SlotRequired(typeURI, slot) {
			slots = append(slots, slot)
		}
	}
	return sockets, slots
}

// TestLookupWithRequirementsDeclined_RuleStaysInOnePlace asks the compiler's own
// reading of "is this socket required" and "is this slot required" about both
// views. It is the guard against this harness growing a second, local notion of
// what either requirement means: the narrowing has to change the ANSWER that
// reading gives, while the reading stays the only thing deciding. If someone
// later reimplements a requirement inside this package, the un-narrowed half of
// this test keeps reporting it and the reimplementation gains no cover.
func TestLookupWithRequirementsDeclined_RuleStaysInOnePlace(t *testing.T) {
	t.Parallel()
	base := socketedLookup()
	comp, _ := base.Component(socketedType)

	// The view as authored: the compiler's reading finds every requirement.
	// This is what a build sees.
	sockets, slots := requiredThroughTheCompilersOwnReading(base, socketedType)
	if len(sockets) != 2 || len(slots) != 2 {
		t.Fatalf("as authored: expected the compiler's reading to find 2 required sockets and 2 required slots, got %v and %v", sockets, slots)
	}

	// Same reading, narrowed view: no requirement left to report.
	narrowed := lookupWithRequirementsDeclined(base, socketedType, declinedRequirementsOf(&comp))
	sockets, slots = requiredThroughTheCompilersOwnReading(narrowed, socketedType)
	if len(sockets) != 0 || len(slots) != 0 {
		t.Fatalf("narrowed: expected the compiler's reading to find nothing required, got sockets %v and slots %v", sockets, slots)
	}
}
