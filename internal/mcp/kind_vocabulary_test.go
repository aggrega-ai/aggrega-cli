// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// TestCatalogURISegmentsResolveEveryEntityKind holds the catalog:// address
// surface to the declared entity-kind vocabulary.
//
// A segment the resolver does not recognise is reported as not-found before the
// workspace is even consulted, so a kind missing from the address surface is
// unaddressable no matter what the workspace holds — and the reader is told the
// resource does not exist rather than that the address is unknown. Asserting
// against the declared set rather than a hand-written list is what keeps a newly
// declared kind from being unaddressable on the day it ships.
func TestCatalogURISegmentsResolveEveryEntityKind(t *testing.T) {
	for _, e := range entitykind.Entries() {
		segment := entitykind.Segment(e.Kind)
		got, ok := catalogKindForSegment(segment)
		if !ok {
			t.Errorf("catalog:// address surface does not resolve segment %q, so a %s is unaddressable through it", segment, e.Kind)
			continue
		}
		if got != string(e.Kind) {
			t.Errorf("segment %q resolved to kind %q, want %q", segment, got, e.Kind)
		}
	}
	if _, ok := catalogKindForSegment("notakind"); ok {
		t.Error("the catalog:// address surface resolved a segment that names no entity kind")
	}
}

// TestCatalogResourceDescriptionListsEverySegment holds the catalog-entity
// resource template's own prose to the segments it actually resolves.
//
// The description is what a reader consults to learn which addresses exist. When
// it is written by hand beside the resolution it describes, the two drift and the
// reader is told a wrong set.
func TestCatalogResourceDescriptionListsEverySegment(t *testing.T) {
	desc := catalogResourceTemplate().Description
	for _, segment := range entitykind.Segments() {
		if !strings.Contains(desc, segment) {
			t.Errorf("the catalog-entity resource description omits the %q segment, so a reader is told a kind set narrower than the one that resolves", segment)
		}
	}
}

// TestKnownEntityKindsCoversTheDeclaredVocabulary holds the capability tool's
// kind filter to the declared entity-kind vocabulary.
//
// The filter rejects an unrecognised kind outright, so a kind missing from it
// cannot be asked about at all — an author working on that kind is told their
// kind does not exist. The filter previously read the keys of the guidance
// pointer map, which conflated two questions: which kinds exist, and which of
// them have a reference page written. Those answers legitimately differ, and
// deriving one from the other made a kind without a page indistinguishable from a
// kind that does not exist.
func TestKnownEntityKindsCoversTheDeclaredVocabulary(t *testing.T) {
	known := map[string]bool{}
	for _, k := range knownEntityKinds() {
		known[k] = true
	}
	for _, segment := range entitykind.Segments() {
		if !isKnownEntityKind(segment) {
			t.Errorf("the capability tool's kind filter rejects %q, so an author working on that kind is told it does not exist", segment)
		}
		if !known[segment] {
			t.Errorf("the capability tool's kind vocabulary omits %q, so the diagnostic listing valid kinds is short", segment)
		}
	}
	if isKnownEntityKind("notakind") {
		t.Error("the capability tool's kind filter accepted a token that names no entity kind")
	}
}

// TestGuidancePointersNameOnlyPagesThatShip holds the guidance pointer surface to
// the declared vocabulary in both directions: every kind the vocabulary records
// as having a reference page gets a pointer naming that page, and every kind
// without one falls back to the reference index rather than naming a page that
// does not exist.
//
// The second direction is the load-bearing one. A pointer to a page that was
// never written reads as a broken reference to whoever follows it, which is worse
// than being sent to the index.
func TestGuidancePointersNameOnlyPagesThatShip(t *testing.T) {
	for _, e := range entitykind.Entries() {
		segment := entitykind.Segment(e.Kind)
		got := docPointerFor(segment)
		if e.ReferencePage {
			want := "language-reference/entities/" + segment + ".md"
			if got != want {
				t.Errorf("kind %s has a reference page but its guidance pointer is %q, want %q", e.Kind, got, want)
			}
			continue
		}
		if got != docIndex {
			t.Errorf("kind %s has no reference page, but its guidance pointer is %q rather than the reference index %q — a pointer to a page that does not ship reads as a broken reference", e.Kind, got, docIndex)
		}
	}
}

// TestPerToolKindFiltersUseRealSegments holds every tool's declared kind filter
// to the address vocabulary.
//
// Which kinds a given tool serves is that tool's own decision and is not derived
// here — the lint tool serves a different set from the build tool, and both are
// product choices rather than properties of a kind. What is not a choice is the
// spelling: a filter token that names no kind narrows nothing and silently
// removes the tool from every kind's capability listing.
func TestPerToolKindFiltersUseRealSegments(t *testing.T) {
	specs := append(append([]toolSpec{}, toolRegistry...), deferredToolRegistry...)
	if len(specs) == 0 {
		t.Fatal("no tool specs were collected; this assertion would be vacuous")
	}
	sawAny := false
	for _, spec := range specs {
		for _, token := range spec.kinds {
			sawAny = true
			if _, ok := entitykind.BySegment(token); !ok {
				t.Errorf("tool %q declares kind filter %q, which names no entity kind: the filter matches nothing, so the tool is absent from every kind's capability listing", spec.name, token)
			}
		}
	}
	if !sawAny {
		t.Fatal("no tool declared any kind filter; this assertion would be vacuous")
	}
}
