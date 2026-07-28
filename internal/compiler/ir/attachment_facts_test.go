// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the per-attachment facts a consumer needs in order to judge an
// attachment for itself: the attached Feature's verbatim catalog labels, its
// verbatim catalog annotations, and the declared slot the attachment fills. Each
// runs end-to-end through BuildIR, so the production wiring is what is under
// test.
//
// The cases the surrounding end-to-end suites cannot reach live here: an
// attachment contributed by a later cascade tier, and a node whose Component
// resolves to no catalog entity. A real build always resolves its Components,
// so the honest-absence branch is only observable at this level.

package ir

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestAttachmentBinding_NamesTheSlotOrFree pairs the two answers: an attachment
// under a declared slot's name reports that name, and one under any other name
// reports free. A derivation that returned one answer for everything would pass
// either assertion alone.
func TestAttachmentBinding_NamesTheSlotOrFree(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("vault", "core/secret-values", vmap("host", "db.internal")),
		feat("extra", "core/plain", vmap("level", "info")),
	})
	if got := entry(t, attachments, "vault").Binding; got != "vault" {
		t.Errorf("an attachment filling the declared slot %q must report it; got %q", "vault", got)
	}
	if got := entry(t, attachments, "extra").Binding; got != types.AttachmentBindingFree {
		t.Errorf("an attachment filling no declared slot must report %q; got %q", types.AttachmentBindingFree, got)
	}
}

// TestAttachmentBinding_HoldsForALaterCascadeTier proves the fact is available
// for an attachment contributed by the Release tier, not only by the component
// tier. The declared slot set belongs to the NODE's Component, so it is the same
// set whichever tier contributed the attachment — this is what makes the marker
// well-defined for every tier rather than only for the authoring surface the
// slot is declared beside.
func TestAttachmentBinding_HoldsForALaterCascadeTier(t *testing.T) {
	t.Parallel()
	releaseFeats := types.OrderedMap[types.BoundaryFeature]{}
	releaseFeats.Set("vault", types.BoundaryFeature{Type: "core/secret-values"})
	releaseFeats.Set("extra", types.BoundaryFeature{Type: "core/plain"})

	attachments := buildSensitivityWith(t, nil, types.OrderedMap[types.FeatureInstance]{}, releaseFeats)
	if got := entry(t, attachments, "vault").Binding; got != "vault" {
		t.Errorf("a Release-tier attachment filling the declared slot %q must report it; got %q", "vault", got)
	}
	if got := entry(t, attachments, "extra").Binding; got != types.AttachmentBindingFree {
		t.Errorf("a Release-tier attachment filling no declared slot must report %q; got %q", types.AttachmentBindingFree, got)
	}
}

// TestAttachmentBinding_AbsentWhenTheSlotSetIsUnknown proves the marker
// distinguishes "fills no declared slot" from "the declared slot set could not
// be resolved". Reporting free for an unresolved Component would state a finding
// the compiler does not hold.
func TestAttachmentBinding_AbsentWhenTheSlotSetIsUnknown(t *testing.T) {
	t.Parallel()
	attachments := buildAttachmentsForComponent(t, "core/unknown", []topology.FeatureAttachment{
		feat("vault", "core/secret-values", vmap("host", "db.internal")),
	})
	got := entry(t, attachments, "vault")
	if got.Binding != "" {
		t.Errorf("the host Component resolves to no entity, so no binding finding exists; got %q", got.Binding)
	}
	// The paired control: the sensitivity roll-up still fires, because the leaf
	// half reads the Feature's own declaration and does not depend on the slot
	// set. Only the binding finding is withheld.
	if !got.Sensitive {
		t.Errorf("the Feature's own sensitive leaf must still roll up when the host Component is unresolved; got %+v", got)
	}
}

// TestAttachmentLabels_AreTheFeaturesOwnAndAbsentWhenUnresolved pairs a Feature
// that declares labels with one whose type resolves to no entity. The second is
// what proves the carry reads the entity rather than synthesising a label set.
func TestAttachmentLabels_AreTheFeaturesOwnAndAbsentWhenUnresolved(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("vault", "core/labelled", vmap("host", "db.internal")),
		feat("extra", "core/absent-from-catalog", vmap("level", "info")),
	})

	labelled := entry(t, attachments, "vault")
	// Author order: the entity declares zeta first, so an alphabetising carry
	// would return alpha first.
	if keys := labelled.Labels.Keys(); len(keys) != 2 || keys[0] != "zeta.example/second" || keys[1] != "alpha.example/first" {
		t.Fatalf("labels must carry the entity's own declaration order; got %v", keys)
	}
	if v, _ := labelled.Labels.Get("zeta.example/second"); v != "2" {
		t.Errorf("label zeta.example/second = %q, want %q", v, "2")
	}

	unresolved := entry(t, attachments, "extra")
	if got := unresolved.Labels.Len(); got != 0 {
		t.Errorf("an attachment whose Feature type resolves to no entity has no labels to carry; got %d", got)
	}
}

// TestAttachmentCatalogAnnotations_AreTheFeaturesOwnInAuthorOrder pairs the
// annotations carry with the two controls a positive-only assertion would pass
// blind: a Feature declaring labels and NO annotations, and one whose type
// resolves to no entity.
//
// The first control is the load-bearing one. Labels and annotations are the same
// shape, so nothing in the type system distinguishes a copy of the annotations
// from a copy of the labels; only an entity that declares one and not the other
// can tell them apart. The keys and values are disjoint between the two maps for
// the same reason.
func TestAttachmentCatalogAnnotations_AreTheFeaturesOwnInAuthorOrder(t *testing.T) {
	t.Parallel()
	attachments := buildSensitivity(t, []topology.FeatureAttachment{
		feat("vault", "core/annotated", vmap("host", "db.internal")),
		feat("plain", "core/labelled", vmap("host", "db.internal")),
		feat("extra", "core/absent-from-catalog", vmap("level", "info")),
	})

	annotated := entry(t, attachments, "vault")
	// Author order: the entity declares omega first, so an alphabetising carry —
	// or one routed through a plain Go map — would return delta first.
	if keys := annotated.CatalogAnnotations.Keys(); len(keys) != 2 ||
		keys[0] != "omega.example/reviewed" || keys[1] != "delta.example/owner" {
		t.Fatalf("catalog annotations must carry the entity's own declaration order; got %v", keys)
	}
	if v, _ := annotated.CatalogAnnotations.Get("omega.example/reviewed"); v != "no" {
		t.Errorf("annotation omega.example/reviewed = %q, want %q", v, "no")
	}
	if v, _ := annotated.CatalogAnnotations.Get("delta.example/owner"); v != "platform-team" {
		t.Errorf("annotation delta.example/owner = %q, want %q", v, "platform-team")
	}
	// The same entity's labels are still its labels: one resolution fills two
	// maps, and neither is the other.
	if keys := annotated.Labels.Keys(); len(keys) != 2 ||
		keys[0] != "kappa.example/tier" || keys[1] != "beta.example/region" {
		t.Errorf("labels must still carry the entity's own labels in author order; got %v", keys)
	}

	// A Feature declaring labels and no annotations carries no annotations. A
	// copy sourced from the labels map would satisfy every assertion above and
	// fail here.
	labelledOnly := entry(t, attachments, "plain")
	if labelledOnly.Labels.Len() != 2 {
		t.Fatalf("fixture drifted: the control must declare labels; got %v", labelledOnly.Labels.Keys())
	}
	if got := labelledOnly.CatalogAnnotations.Len(); got != 0 {
		t.Errorf("an entity declaring no annotations has none to carry; got %v", labelledOnly.CatalogAnnotations.Keys())
	}
	// And the field leaves the wire entirely in that state, so an attachment
	// declaring no annotations carries the bytes it carried before the field
	// existed.
	raw, err := json.Marshal(labelledOnly)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"catalog_annotations"`) {
		t.Errorf("an entity declaring no annotations must emit no catalog_annotations key; got %s", raw)
	}

	unresolved := entry(t, attachments, "extra")
	if got := unresolved.CatalogAnnotations.Len(); got != 0 {
		t.Errorf("an attachment whose Feature type resolves to no entity has no annotations to carry; got %d", got)
	}
}

// TestAttachmentCatalogAnnotations_EmittedIRConformsToSchema proves the Go field
// and the schema property landed together. The attachment entry's schema
// definition is closed, so a Go field with no declared property makes every
// document carrying it non-conformant.
func TestAttachmentCatalogAnnotations_EmittedIRConformsToSchema(t *testing.T) {
	t.Parallel()
	graph := buildSensitivityGraph(t,
		[]topology.FeatureAttachment{feat("vault", "core/annotated", vmap("host", "db.internal"))},
		types.OrderedMap[types.FeatureInstance]{},
		types.OrderedMap[types.BoundaryFeature]{},
	)
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	// The check is vacuous unless the field actually reached the wire, in author
	// order — which is also the wire-level order assertion the emitted bytes are
	// the only place to make.
	const want = `"catalog_annotations":{"omega.example/reviewed":"no","delta.example/owner":"platform-team"}`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("emitted IR does not carry %s:\n%s", want, raw)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("re-decode IR: %v", err)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, instance)
	if err != nil {
		t.Fatalf("IR schema engine error: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("emitted IR carrying the attached Feature's catalog annotations violates the IR schema (%d finding(s)):\n%s\n%+v",
			len(findings), raw, findings)
	}
}

// labelMap builds an OrderedMap from alternating key/value arguments, keeping
// the argument order as the declaration order a catalog entity would have.
func labelMap(pairs ...string) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i], pairs[i+1])
	}
	return out
}

// buildAttachmentsForComponent builds one node's attachments against an
// arbitrary Component type, so a case can exercise a type the fixture catalog
// does not resolve. It is the sensitivity fixture's graph builder with the
// Component type lifted into a parameter; the catalog and the tiers are
// otherwise identical.
func buildAttachmentsForComponent(t *testing.T, componentType string, feats []topology.FeatureAttachment) types.OrderedMap[types.IRFeatureEntry] {
	t.Helper()
	node := &topology.TopologyNode{InstanceID: "api", ComponentType: componentType}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)
	graph, _, err := BuildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{
			Nodes:              nodes,
			FeatureAttachments: map[string][]topology.FeatureAttachment{"api": feats},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		CatalogLookup: sensitivityCatalog(t),
	})
	if err != nil {
		t.Fatalf("BuildIR: %v", err)
	}
	attachments, ok := graph.Spec.FeatureValuesMapping.Get("api")
	if !ok {
		t.Fatalf("no attachments emitted for node api")
	}
	return attachments
}
