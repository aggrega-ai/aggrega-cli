// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAConvertedFindingStaysMatchableByItsSentinel is the property every caller
// that reads a rule's identity depends on, and it is asserted over an objection
// a real check raised rather than over a hand-built one.
//
// Two callers read a finding by identity rather than by text. One drops a single
// rule's findings out of a halt set so the build proceeds with that condition
// reported instead of fatal; the other matches a rule through the aggregate
// error a failed resolve returns. Both ask the same question — does this finding
// wrap that sentinel — so both hold exactly when the conversion carries the
// sentinel through.
//
// The objection is produced by invoking a declared rule through the framework's
// own traversal over the coordinate set, because an objection cannot be
// constructed any other way: the fields are unexported and the only producer is
// a subject the framework supplies while it invokes a check. That makes this a
// statement about the real path rather than about a fixture shaped to pass.
//
// INVARIANT: a converted finding is matchable by the sentinel of the rule that
// raised it, both on its own and through the aggregate error carrying it.
// CONSEQUENCE: were the sentinel not carried, the tolerant path would stop
// recognising the condition it exists to tolerate and would halt a build it is
// supposed to let through — and no text comparison would notice, because the
// message would still read correctly.
func TestAConvertedFindingStaysMatchableByItsSentinel(t *testing.T) {
	t.Parallel()

	// The type is a catalog identity the catalog holds no entry for, which is
	// exactly the condition the rule below reports.
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "conversion"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, CatalogLookup{}, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule raised nothing for a type absent from the catalog.\n" +
			"INVARIANT: the fixture violates the rule, so there is a real objection to convert.\n" +
			"CONSEQUENCE: the assertions below run over an empty set and report the " +
			"conversion as correct without converting anything.")
	}

	findings := FindingsFromObjections(objections, "Aggregate conversion")
	if len(findings) != len(objections) {
		t.Fatalf("the conversion produced %d findings from %d objections.\n"+
			"INVARIANT: one finding per objection, none dropped.\n"+
			"CONSEQUENCE: a dropped objection is a violation the build never reports.",
			len(findings), len(objections))
	}

	sentinel := ErrGuardEntityViewInconsistency()

	// The per-finding match: what the tolerant path's filter asks of each halt.
	for _, f := range findings {
		if !errors.Is(f, sentinel) {
			t.Fatalf("a converted finding does not match the sentinel of the rule that raised it.\n" +
				"INVARIANT: the conversion carries the sentinel onto the finding it builds.\n" +
				"CONSEQUENCE: the path that drops this rule's findings out of a halt set " +
				"stops recognising them, and halts a build it exists to let through.")
		}
	}

	// The aggregate match: what a caller asks of the error a failed resolve
	// returns, which carries the findings rather than re-wrapping them.
	aggregated := &MultiResolverError{
		Entity:     "resolution",
		Summary:    "guard violations",
		Violations: findings,
	}
	if !errors.Is(aggregated, sentinel) {
		t.Fatal("the aggregate error does not match a sentinel one of its findings wraps.\n" +
			"INVARIANT: the aggregate unwraps to its findings, so a caller matches a " +
			"specific rule through it.\n" +
			"CONSEQUENCE: a caller that branches on a particular rule stops seeing it and " +
			"falls through to generic handling.")
	}

	// A rule the findings did NOT raise must not match, or the two assertions
	// above would hold for any sentinel and would be measuring nothing.
	if errors.Is(aggregated, ErrSchemaConformance()) {
		t.Fatal("the aggregate error matched a sentinel none of its findings raised.\n" +
			"INVARIANT: identity matching distinguishes rules.\n" +
			"CONSEQUENCE: the positive assertions above pass for every sentinel, so they " +
			"prove nothing about the one that was actually raised.")
	}
}

// TestTheConversionAttributesFindingsToTheDocumentItIsGiven pins the entity the
// conversion names, which is the half a caller supplies.
//
// The rules reporting through this conversion span both authoring documents, so
// the attribution cannot be derived from the objection: a rule over what the
// deployment wrote is not the application's fault, and naming one document for
// everything would send an operator to a file they did not write in.
//
// INVARIANT: findings carry the entity the caller named.
// CONSEQUENCE: were the entity fixed to one document, every finding about the
// other would be attributed to a file its author never edited.
func TestTheConversionAttributesFindingsToTheDocumentItIsGiven(t *testing.T) {
	t.Parallel()

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: componentTypeURI})
	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "attribution"},
		Spec:     types.AggregateSpec{Components: components},
	})

	view := newNormalizedView(na, nil, CatalogLookup{}, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node traversal returned an error: %v", err)
	}
	if len(objections) == 0 {
		t.Fatal("the rule raised nothing, so there is no finding to attribute.")
	}

	for _, entity := range []string{"Aggregate attribution", "Release prod"} {
		for _, f := range FindingsFromObjections(objections, entity) {
			if f.Entity != entity {
				t.Errorf("a finding was attributed to %q; want %q.\n"+
					"INVARIANT: the conversion names the document the caller gave it.\n"+
					"CONSEQUENCE: a finding about one document is reported against the other.",
					f.Entity, entity)
			}
		}
	}
}
