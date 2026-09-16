// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ValidateAuthoredAnnotations asks the rules over every annotation entry either
// authoring document declares, and refuses the build when any of them routes
// through a pillar its own document may not use, or carries a value the Feature
// its binding targets does not admit.
//
// It presents the documents and names each rule rather than walking the
// annotations itself: the rules are declared in this package as constraints, so
// where they run follows from the facts they read, and the provenance their
// findings carry is supplied by the framework rather than assembled here.
//
// It is asked BEFORE annotation expansion, and that position is load-bearing
// rather than incidental. Expansion consumes the entry: a value the target
// Feature refuses would be written into the synthetic attachment's pillar and
// reported downstream against a block no author wrote, and a value routed to the
// other side's pillar would simply be dropped, leaving an author who wrote
// something with no sign that nothing was done with it. Asking first keeps each
// finding on the line the author typed.
//
// It runs the rules once per authoring document rather than once over both, and
// the split is attribution rather than scope. A finding is reported against the
// document its entry was written in, so an operator is sent to the Release
// they wrote and a developer to the Aggregate — and the two passes together
// reach every entry exactly once, because each entry belongs to one document.
//
// It reports EVERY offending entry rather than the first, which is the
// framework's accumulation and not a property of how this is written: each rule
// is invoked once per entry and what each invocation raised is collected, so an
// author fixing several sees them all in one build.
//
// Unlike the binding-target rule beside it, it does NOT return early on a build
// resolving against no catalog. One of the two rules reads no contract at all —
// the pillar comes from the binding and the side from the document — so
// returning early would lose a finding that is fully decidable without a
// catalog. The rule that does read a contract ends on its own where the
// contract cannot be read.
//
// The findings are joined, so a caller matching the refusal by identity keeps
// matching however many entries offended.
//
// cancelled is the signal both walks read once per entry: a caller that has given
// up on this build stops paying for the rest of it, and a walk that stops hands
// back nothing rather than a partial set that would read as documents satisfying
// rules nobody asked.
func ValidateAuthoredAnnotations(
	cancelled constraint.Cancellation,
	result *ResolveResult,
	catalogLookup CatalogLookup,
) error {
	if result == nil || result.Manifest == nil {
		return nil
	}
	inner := newNormalizedView(
		normalizedAggregateOf(result), result.Release, catalogLookup, result.Parameters)
	presented := newManifestNormalizedView(
		manifestDocumentOf(result), result.Manifest, inner, catalogLookup)

	var findings []*ResolverError
	collect := func(entity string, objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		findings = append(findings, FindingsFromObjections(objections, entity)...)
		return nil
	}
	facts := constraint.NormalizedView(presented)
	for _, side := range []struct {
		document string
		entity   string
	}{
		{
			document: constraint.AuthoringDocumentRelease,
			entity:   releaseDocumentName(result.Release),
		},
		{
			document: constraint.AuthoringDocumentAggregate,
			entity:   aggregateDocumentName(result.Aggregate),
		},
	} {
		entries := annotationsWrittenIn{
			document: side.document,
			entries:  inner.annotations,
		}
		// The entity is bound ahead of the calls rather than passed beside each,
		// because a traversal returns two values and Go spreads a two-valued call
		// only into a call taking exactly those two. Binding it is what lets each
		// call below name its rule as a literal argument — a constraint of the
		// framework rather than a style, since a call written any other way makes
		// the rule it invokes underivable and leaves it indistinguishable from a
		// rule nothing invokes at all.
		rule := func(objections []constraint.Objection, err error) error {
			return collect(side.entity, objections, err)
		}
		// The side rule declares the narrower view, because its predicate reads only
		// what a presented document carries. A later presentation contains an
		// earlier one, so the same value serves both calls and the narrowing is a
		// statement about where the rule COULD run rather than a second
		// presentation.
		if err := rule(constraint.RunOnAuthoredAnnotations(
			cancelled, entries, constraint.DocumentView(presented),
			annotationRoutesThroughItsAuthoringSidesPillar)); err != nil {
			return err
		}
		if err := rule(constraint.RunOnAuthoredAnnotations(
			cancelled, entries, facts,
			annotationSubkeyConformsToTheTargetFeature)); err != nil {
			return err
		}
	}

	if len(findings) == 0 {
		return nil
	}
	joined := make([]error, 0, len(findings))
	for _, f := range findings {
		joined = append(joined, f)
	}
	return errors.Join(joined...)
}

// annotationsWrittenIn narrows the presentation's annotation axis to the entries
// one authoring document declares, preserving the order they were recorded in.
//
// It is a narrowing of one recorded set rather than a second reading of the
// documents, which is what keeps the coordinate a rule is invoked for and the
// value it then reads coming from one walk. The narrowing exists so a finding can
// be attributed to the document its entry was written in: the conversion at this
// boundary names one document for a whole set of objections, and a rule spanning
// both documents would otherwise send half its readers to a file they did not
// write in.
type annotationsWrittenIn struct {
	document string
	entries  []constraint.AuthoredAnnotationInstance
}

// AuthoredAnnotations returns the entries the named document declares, in the
// order the presentation recorded them.
func (a annotationsWrittenIn) AuthoredAnnotations() []constraint.AuthoredAnnotationInstance {
	var out []constraint.AuthoredAnnotationInstance
	for _, entry := range a.entries {
		if entry.Document == a.document {
			out = append(out, entry)
		}
	}
	return out
}

// aggregateDocumentName renders the Aggregate document a finding names. A
// resolve holding none names the kind alone, on the same terms its Release
// counterpart does: nothing it could be asked about exists, so no rule attributed
// to it raises.
func aggregateDocumentName(aggregate *types.Aggregate) string {
	if aggregate == nil {
		return "Aggregate"
	}
	return fmt.Sprintf("Aggregate %s", aggregate.Metadata.Name)
}
