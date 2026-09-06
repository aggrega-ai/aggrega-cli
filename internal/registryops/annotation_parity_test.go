// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"reflect"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fullySearchableMetadata is a searchable-annotation value with EVERY facet
// populated, and a distinct value per facet so no assertion below can pass by
// reading the wrong one.
//
// The label map carries a value containing both a comma and an equals sign. That
// is deliberate: those two characters are exactly what a delimiter-joined
// encoding of the map could not round-trip, so a label whose value contains them
// is the input that separates a real round trip from one that only looks like it.
func fullySearchableMetadata() oci.SearchableMetadata {
	labels := types.NewOrderedMap[string]()
	labels.Set("component.aggrega.ai/category", "datastore")
	labels.Set("example.test/awkward", "a=1,b=2")
	labels.Set("example.test/plain", "plain-value")

	return oci.SearchableMetadata{
		Description:  "a fully populated catalog entity",
		Category:     "datastore",
		Kind:         "Component",
		PluginType:   "provider",
		Labels:       labels,
		Capabilities: []string{"render", "describe"},
	}
}

// TestSearchableAnnotationsRoundTripToAnIndexRecord is the parity assertion
// between the two halves of the searchable-annotation contract: the writer that
// renders the annotations onto a published manifest, and the index reader that
// assembles a record back off them.
//
// Nothing else holds those two together — they share key constants and no test.
// The failure this exists to catch is a facet that has a field on the record and
// no route onto it, which is silent at every point: the publish succeeds, the
// index builds, the record simply never carries the facet, and a search filtered
// on it matches nothing in a way indistinguishable from the entity not existing.
//
// Two properties of the assertion are load-bearing, and each rules out a weaker
// version that would have passed while the defect was live:
//
// It compares VALUES, never key lists. A key-list comparison is satisfied when
// both sides name a key that neither carries a value for, which is precisely the
// shape of the failure.
//
// It is tied structurally to the writer's own field count, so a facet added to
// the writer cannot be added without either extending this test or reddening it.
// A hand-written list of facets would drift from the writer exactly as quietly as
// the reader did.
func TestSearchableAnnotationsRoundTripToAnIndexRecord(t *testing.T) {
	t.Parallel()

	md := fullySearchableMetadata()

	// Calibration: an unpopulated facet would let the corresponding assertion
	// below compare one zero value against another and pass while asserting
	// nothing. A vacuous pass reads exactly like a real one.
	mv := reflect.ValueOf(md)
	for i := range mv.NumField() {
		if mv.Field(i).IsZero() {
			t.Fatalf("facet %q is not populated by the fixture, so the assertion for it below would compare two zero values and pass vacuously",
				mv.Type().Field(i).Name)
		}
	}

	ann, err := md.Annotations()
	if err != nil {
		t.Fatalf("rendering the searchable annotations: %v", err)
	}
	// Every populated facet must have produced exactly one annotation. A facet
	// the writer holds but never renders would show up here as a short map.
	if len(ann) != mv.NumField() {
		t.Fatalf("a fully populated value rendered %d annotation(s) for %d facet(s): %v", len(ann), mv.NumField(), ann)
	}

	rec, err := recordFromAnnotations("registry.invalid.example", "example/component/full", "1.2.3", ann)
	if err != nil {
		t.Fatalf("assembling the index record: %v", err)
	}

	// One check per facet, each asserting the facet's VALUE arrived. The count is
	// pinned to the writer's field count below, so a new facet cannot be added to
	// the writer without landing here.
	checks := []struct {
		facet string
		got   any
		want  any
	}{
		{"Description", rec.Description, md.Description},
		{"Category", rec.Category, md.Category},
		{"Kind", rec.Kind, md.Kind},
		{"PluginType", rec.PluginType, md.PluginType},
		{"Capabilities", rec.Capabilities, md.Capabilities},
		{"Labels", orderedPairs(rec.Labels), orderedPairs(md.Labels)},
	}
	if len(checks) != mv.NumField() {
		t.Fatalf("%d facet(s) are checked for %d facet(s) the writer can emit; a facet was added to the writer without a parity check here", len(checks), mv.NumField())
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("facet %s did not survive the round trip: record has %#v; the published value was %#v", c.facet, c.got, c.want)
		}
	}

	// The reader must consume every annotation the writer emitted. Checking only
	// that each facet arrived would pass an annotation the reader silently
	// ignores, so the emitted keys are accounted for in the other direction too.
	consumed := map[string]bool{
		ocispec.AnnotationDescription: rec.Description != "",
		oci.AnnotationCategory:        rec.Category != "",
		oci.AnnotationKind:            rec.Kind != "",
		oci.AnnotationPluginType:      rec.PluginType != "",
		oci.AnnotationCapabilities:    len(rec.Capabilities) > 0,
		oci.AnnotationLabels:          rec.Labels != nil && rec.Labels.Len() > 0,
	}
	for key := range ann {
		landed, known := consumed[key]
		if !known {
			t.Errorf("the writer emitted annotation %q, which this test does not account for; the reader may be dropping it", key)
			continue
		}
		if !landed {
			t.Errorf("annotation %q was emitted but nothing carrying its value reached the record", key)
		}
	}
}

// TestSearchableAnnotationsRoundTripPreservesLabelOrder pins the author order of
// the label map across the round trip. Order is not decoration here: identical
// registry state must produce byte-identical index files, and a record whose
// labels came back in a different order rewrites those bytes on every refresh.
func TestSearchableAnnotationsRoundTripPreservesLabelOrder(t *testing.T) {
	t.Parallel()

	md := fullySearchableMetadata()
	ann, err := md.Annotations()
	if err != nil {
		t.Fatalf("rendering the searchable annotations: %v", err)
	}
	rec, err := recordFromAnnotations("registry.invalid.example", "example/component/full", "1.2.3", ann)
	if err != nil {
		t.Fatalf("assembling the index record: %v", err)
	}
	if rec.Labels == nil {
		t.Fatal("the record carries no labels, so the order assertion below asserts nothing")
	}

	want := md.Labels.Keys()
	got := rec.Labels.Keys()
	if len(want) < 2 {
		t.Fatalf("the fixture carries %d label(s); at least two are needed for an order assertion to mean anything", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("label author order did not survive the round trip: got %v; want %v", got, want)
	}
}

// orderedPairs projects an ordered map to a comparable slice of key/value pairs,
// so an equality check covers the keys, the values AND their order in one
// comparison — rather than the keys alone, which is the comparison that would
// pass while every value was empty.
func orderedPairs(m *types.OrderedMap[string]) [][2]string {
	if m == nil {
		return nil
	}
	pairs := make([][2]string, 0, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		pairs = append(pairs, [2]string{k, v})
	}
	return pairs
}
