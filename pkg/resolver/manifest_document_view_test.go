// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The Manifest's document-stage presentation and the coordinate a rule is
// invoked once per.
//
// The assertions here are about the coordinate rather than about any rule: that
// a Manifest fact is answered from the presentation, that a rule reading it is
// invoked once per binding through the framework's own entry, and that what such
// a rule raises points at the Manifest the author edits. A rule enforcing
// something is a separate matter and none is declared here.

// manifestWithBindings composes a Manifest whose flat registry declares the
// given annotation keys in the order supplied, each routing to a Feature named
// after its key.
//
// The order is supplied rather than derived because author order is one of the
// properties under test: a presentation that sorted, or that ranged a Go map,
// would answer a different order per run and the enumeration would stop being a
// property of the document.
func manifestWithBindings(keys ...string) *types.Manifest {
	annotations := types.OrderedMap[types.AnnotationBinding]{}
	for _, k := range keys {
		annotations.Set(k, types.AnnotationBinding{
			Feature: "feature.example/" + k,
			Maps:    types.AnnotationBindingMaps{Values: []string{"host"}},
		})
	}
	man := &types.Manifest{}
	man.Spec.Mappings.Features = &types.MappingTier{Annotations: &annotations}
	return man
}

// A presented Manifest answers its binding keys in the order the author wrote
// them.
//
// The keys are deliberately not in sorted order, so a presentation that sorted
// would fail rather than coincidentally agree.
func TestManifestDocumentView_PresentsBindingKeysInAuthorOrder(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"
	view := newManifestDocumentView(document,
		manifestWithBindings("zeta-route", "alpha-route", "mid-route"))

	got := view.ManifestAnnotationBindingKeys(document)
	want := []string{"zeta-route", "alpha-route", "mid-route"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("binding keys: got %v, want %v", got, want)
	}
}

// The Feature fact tells an absent binding from a declared one.
//
// The second result is what carries that distinction, and it is asserted rather
// than assumed: a binding nobody declared and a declared binding naming the
// empty Feature are different findings, and the fact returning only a string
// would report the first as the second.
func TestManifestDocumentView_BindingFeatureSeparatesAbsentFromDeclared(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"

	empty := types.OrderedMap[types.AnnotationBinding]{}
	empty.Set("declared-empty", types.AnnotationBinding{Feature: ""})
	man := &types.Manifest{}
	man.Spec.Mappings.Features = &types.MappingTier{Annotations: &empty}

	view := newManifestDocumentView(document, man)

	if feature, declared := view.ManifestAnnotationBindingFeature(document, "declared-empty"); //
	feature != "" || !declared {
		t.Fatalf("a declared binding naming no Feature: got (%q, %v), want (%q, true)",
			feature, declared, "")
	}
	if feature, declared := view.ManifestAnnotationBindingFeature(document, "never-written"); //
	feature != "" || declared {
		t.Fatalf("a binding nobody declared: got (%q, %v), want (%q, false)",
			feature, declared, "")
	}
}

// A Manifest carrying no registry at all presents no keys, and does so without
// refusing.
//
// Absent and empty are the same answer here on purpose: a Manifest that declares
// no bindings has none, which is a truthful empty rather than a fact the document
// cannot carry. The refusals below are for the facts a Manifest genuinely cannot
// answer.
func TestManifestDocumentView_NoRegistryPresentsNoKeys(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"
	view := newManifestDocumentView(document, &types.Manifest{})

	if got := view.ManifestAnnotationBindingKeys(document); len(got) != 0 {
		t.Fatalf("a Manifest with no registry: got %v, want no keys", got)
	}
}

// An accessor asked about a document this view was not built for refuses instead
// of answering, because answering would hand back one document's fact under
// another document's name.
func TestManifestDocumentView_RefusesAnotherDocument(t *testing.T) {
	t.Parallel()

	view := newManifestDocumentView("composed/manifest.yaml", manifestWithBindings("route"))

	for _, tc := range []struct {
		name string
		call func()
	}{
		{"ManifestAnnotationBindingKeys", func() {
			view.ManifestAnnotationBindingKeys("some/other.yaml")
		}},
		{"ManifestAnnotationBindingFeature", func() {
			view.ManifestAnnotationBindingFeature("some/other.yaml", "route")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s answered about a document it does not present", tc.name)
				}
			}()
			tc.call()
		})
	}
}

// The facts a Manifest cannot carry refuse rather than returning a zero value.
//
// Returning empty would compile and read like an answer, and nothing downstream
// could tell it from the truthful empty a document that carries the fact would
// give. This is the same judgement the Aggregate's presentation makes about
// assertion names, asserted on the other side of the pair.
func TestManifestDocumentView_RefusesFactsAManifestCannotCarry(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"
	view := newManifestDocumentView(document, manifestWithBindings("route"))

	for _, tc := range []struct {
		name string
		call func()
	}{
		{"RawAggregateText", func() { view.RawAggregateText(document) }},
		{"DeclaredParameterKeys", func() { view.DeclaredParameterKeys(document) }},
		{"EntityAssertionNameSet", func() { view.EntityAssertionNameSet(document) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s answered a fact a Manifest does not carry", tc.name)
				}
			}()
			tc.call()
		})
	}
}

// The Manifest axis is presented through the same contract its sibling document
// axes are, rather than through one written beside it.
//
// This is asserted structurally rather than by reading the two files: both
// presentations are held to the one generated document view, so the Manifest's
// facts are accessors on the same interface the Aggregate's facts are accessors
// on. A parallel Manifest-only view would satisfy no such comparison, and a check
// would then have to know which of two contracts it had been handed.
func TestManifestPresentationSharesTheSiblingDocumentContract(t *testing.T) {
	t.Parallel()

	contract := reflect.TypeOf((*constraint.DocumentView)(nil)).Elem()

	for _, tc := range []struct {
		name string
		impl reflect.Type
	}{
		{"aggregateDocumentView", reflect.TypeOf(aggregateDocumentView{})},
		{"manifestDocumentView", reflect.TypeOf(manifestDocumentView{})},
	} {
		if !tc.impl.Implements(contract) {
			t.Fatalf("%s does not satisfy the document view contract", tc.name)
		}
	}

	// The Manifest facts are declared on that shared contract and not somewhere
	// beside it. Naming them rather than counting the method set is deliberate: a
	// count moves whenever a fact is declared and would then read as evidence
	// after it stopped being true.
	for _, accessor := range []string{
		"ManifestAnnotationBindingKeys",
		"ManifestAnnotationBindingFeature",
	} {
		if _, ok := contract.MethodByName(accessor); !ok {
			t.Fatalf("%s is not declared on the shared document view contract", accessor)
		}
	}
}

// A rule invoked over the Manifest coordinate is invoked once per declared
// binding, reads a Manifest fact in its predicate, and raises a finding whose
// locator names the binding's own authored site in the Manifest.
//
// It goes through the framework's traversal entry rather than calling the rule's
// Run directly, because the invocation is half of what is under test: a rule
// handed the wrong subject, or an entry that ignored the keys it was given, would
// both pass a direct call.
//
// The rule declared here enforces nothing. It objects to whichever binding routes
// to a Feature the fixture marks as the offending one, which is enough to make a
// raise happen at a known coordinate and no more than that.
func TestDocumentAnnotationBindingRule_LocatorNamesTheManifestAuthoredSite(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"

	probe := constraint.Check[
		constraint.DocumentView,
		constraint.DocumentAnnotationBindingSubject,
	]{
		Sentinel: ErrManifestReservedPrefix(),
		Severity: constraint.SeverityWarning,
		Run: func(
			v constraint.DocumentView,
			s constraint.DocumentAnnotationBindingSubject,
		) []constraint.Objection {
			feature, declared := v.ManifestAnnotationBindingFeature(
				s.SourceFile(), s.AnnotationBindingKey())
			if !declared || feature != "feature.example/offending-route" {
				return nil
			}
			return []constraint.Objection{
				s.Raise(ErrManifestReservedPrefix(), s.AnnotationBindingKey()),
			}
		},
	}

	var presented constraint.DocumentView = newManifestDocumentView(document,
		manifestWithBindings("clean-route", "offending-route"))

	objections := constraint.RunOnDocumentAnnotationBindings(
		document,
		presented.ManifestAnnotationBindingKeys(document),
		presented,
		probe,
	)

	if len(objections) != 1 {
		t.Fatalf("objections: got %d, want exactly the offending binding's one", len(objections))
	}
	got := objections[0]

	if got.SourceFile() != document {
		t.Fatalf("source file: got %q, want the Manifest %q", got.SourceFile(), document)
	}
	const wantLocator = ".spec.mappings.features.annotations.offending-route"
	if got.Locator() != wantLocator {
		t.Fatalf("locator: got %q, want %q — a locator naming anything but the Manifest's own "+
			"authored site sends the reader to the wrong document", got.Locator(), wantLocator)
	}
}

// The rule above is capable of raising nothing, so its single finding above is a
// verdict rather than a constant.
//
// Without this, a rule that raised on every binding would satisfy the assertion
// above just as well, and the locator it reported would be attributable to the
// traversal rather than to the predicate.
func TestDocumentAnnotationBindingRule_RaisesNothingOverACleanRegistry(t *testing.T) {
	t.Parallel()

	const document = "composed/manifest.yaml"

	probe := constraint.Check[
		constraint.DocumentView,
		constraint.DocumentAnnotationBindingSubject,
	]{
		Sentinel: ErrManifestReservedPrefix(),
		Severity: constraint.SeverityWarning,
		Run: func(
			v constraint.DocumentView,
			s constraint.DocumentAnnotationBindingSubject,
		) []constraint.Objection {
			feature, declared := v.ManifestAnnotationBindingFeature(
				s.SourceFile(), s.AnnotationBindingKey())
			if !declared || feature != "feature.example/offending-route" {
				return nil
			}
			return []constraint.Objection{
				s.Raise(ErrManifestReservedPrefix(), s.AnnotationBindingKey()),
			}
		},
	}

	var presented constraint.DocumentView = newManifestDocumentView(document,
		manifestWithBindings("clean-route", "another-clean-route"))

	if objections := constraint.RunOnDocumentAnnotationBindings(
		document,
		presented.ManifestAnnotationBindingKeys(document),
		presented,
		probe,
	); len(objections) != 0 {
		t.Fatalf("objections over a clean registry: got %d, want none", len(objections))
	}
}
