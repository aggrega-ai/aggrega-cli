// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// slotFillCatalog returns a lookup whose one Component declares a required slot
// and an optional one, mirroring the shipped datastore shape.
func slotFillCatalog() resolver.CatalogLookup {
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("root_password", types.AttachmentSlot{Required: true})
	slots.Set("user_password", types.AttachmentSlot{})
	comp := types.Component{}
	comp.Metadata.Name = "core/datastore/db"
	comp.Spec.FeaturesSchema = slots
	return resolver.CatalogLookup{
		Component: func(uri string) (types.Component, bool) {
			if uri == "core/datastore/db" {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// slotFillGraph builds a graph of the named nodes, each of the slot-declaring
// component type, with the supplied attachment instance names bound on each.
// The attachment set is the node's FINAL set — the cascade has already folded —
// so a fixture cannot distinguish a component-scope fill from a cascaded one,
// which is the point: the rule is blind to the scope that supplied it.
func slotFillGraph(nodesWithAttachments map[string][]string, nodeOrder ...string) *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	per := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	for _, id := range nodeOrder {
		nodes.Set(id, types.IRNode{
			Component:  "core/datastore/db",
			SourceFile: "aggregate.yaml",
		})
		entries := types.OrderedMap[types.IRFeatureEntry]{}
		for _, name := range nodesWithAttachments[id] {
			entries.Set(name, types.IRFeatureEntry{Type: "core/secret-value"})
		}
		per.Set(id, entries)
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, FeatureValuesMapping: per}}
}

func TestRequiredAttachmentSlots_UnfilledHalts(t *testing.T) {
	t.Parallel()
	graph := slotFillGraph(map[string][]string{"db": {"user_password"}}, "db")

	errs, err := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("want exactly one violation for the unbound required slot; got %v", errs)
	}
	if !errors.Is(errs[0].Sentinel, ErrRequiredSlotUnfilled) {
		t.Errorf("sentinel = %v; want ErrRequiredSlotUnfilled", errs[0].Sentinel)
	}
	for _, want := range []string{"root_password", "core/datastore/db"} {
		if !strings.Contains(errs[0].Message, want) {
			t.Errorf("message must name %q; got %q", want, errs[0].Message)
		}
	}
	if errs[0].Field != ".spec.components.db" {
		t.Errorf("field = %q; want the component-instance locator so the editor can position the diagnostic", errs[0].Field)
	}
	if errs[0].OriginFile != "aggregate.yaml" {
		t.Errorf("origin = %q; want the node's source file", errs[0].OriginFile)
	}
}

func TestRequiredAttachmentSlots_BoundIsClean(t *testing.T) {
	t.Parallel()
	graph := slotFillGraph(map[string][]string{"db": {"root_password"}}, "db")

	errs, err := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("a bound required slot is satisfied; got %v", errs)
	}
}

func TestRequiredAttachmentSlots_SharedAttachmentSatisfiesEveryConsumer(t *testing.T) {
	t.Parallel()
	// The shared form, at N=2. One credential attached at a scope both
	// components inherit from reaches both nodes' final attachment sets under
	// the slot's name, so BOTH are satisfied with neither authoring a
	// component-scope fill. Asserted at two consumers rather than one because a
	// per-node check that happened to test only the first node would pass at
	// N=1 and fail here.
	graph := slotFillGraph(map[string][]string{
		"db1": {"root_password"},
		"db2": {"root_password"},
	}, "db1", "db2")

	errs, err := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("a cascaded attachment satisfies the slot for every consumer; got %v", errs)
	}
}

func TestRequiredAttachmentSlots_OneViolationPerUnboundConsumer(t *testing.T) {
	t.Parallel()
	// The mirror of the shared-form case: when nothing binds the slot, every
	// consumer is reported, so a partial fix cannot read as a complete one.
	graph := slotFillGraph(map[string][]string{
		"db1": {},
		"db2": {"root_password"},
		"db3": {},
	}, "db1", "db2", "db3")

	errs, err := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 2 {
		t.Fatalf("want one violation per unbound consumer (db1, db3); got %v", errs)
	}
	if errs[0].Source != "db1" || errs[1].Source != "db3" {
		t.Errorf("nodes must be reported in sorted node order; got %q then %q", errs[0].Source, errs[1].Source)
	}
}

func TestRequiredAttachmentSlots_FreeAttachmentDoesNotBindTheSlot(t *testing.T) {
	t.Parallel()
	// A slot is a NAMED binding point. An attachment of the very type a fill
	// would use, under a different name, is a free attachment and leaves the
	// slot unbound — which is exactly the difference between this rule and the
	// presence mandate, whose satisfying set is types at any name.
	graph := slotFillGraph(map[string][]string{"db": {"some_other_credential"}}, "db")

	errs, err := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("a free attachment does not bind a named slot; got %v", errs)
	}
}

func TestRequiredAttachmentSlots_OptionalSlotNeverReported(t *testing.T) {
	t.Parallel()
	graph := slotFillGraph(map[string][]string{"db": {"root_password"}}, "db")

	errs, _ := validateRequiredAttachmentSlots(context.Background(), graph, slotFillCatalog())
	for _, e := range errs {
		if strings.Contains(e.Message, "user_password") {
			t.Fatalf("an optional slot must never be reported: %v", e)
		}
	}
}

func TestRequiredAttachmentSlots_CatalogMissAndCatalogLessSkip(t *testing.T) {
	t.Parallel()
	graph := slotFillGraph(map[string][]string{"db": {}}, "db")

	if errs, _ := validateRequiredAttachmentSlots(context.Background(), graph, resolver.CatalogLookup{}); len(errs) != 0 {
		t.Errorf("a catalog-less path has no slot declarations to enforce; got %v", errs)
	}
	miss := resolver.CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, false
	}}
	if errs, _ := validateRequiredAttachmentSlots(context.Background(), graph, miss); len(errs) != 0 {
		t.Errorf("an unresolvable component is Step 1's report, not this pass's; got %v", errs)
	}
}

func TestRequiredAttachmentSlots_ContextCancellationPropagates(t *testing.T) {
	t.Parallel()
	graph := slotFillGraph(map[string][]string{"db": {}}, "db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := validateRequiredAttachmentSlots(ctx, graph, slotFillCatalog()); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}
