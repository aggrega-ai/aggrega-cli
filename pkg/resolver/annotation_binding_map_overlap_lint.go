// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// LintAnnotationBindingMaps asks the one-pillar-per-path rule of every
// annotation binding the resolved Manifest declares, and returns one advisory
// per path a binding declares under both of its pillar lists.
//
// It presents the document and names the rule rather than walking the registry
// itself, for the reason the Manifest's other binding readings state: the rule
// is declared in this package as a constraint, so where it runs follows from the
// facts it reads, and the provenance its findings carry — the locator above all
// — is supplied by the framework rather than assembled here.
//
// The findings are returned rather than printed, so they join the one advisory
// collection every other advisory flows through: the build renders that
// collection to a terminal and the compiler-as-service daemon maps the same
// findings to editor warnings positioned on the offending binding key. A finding
// written straight to a stream reaches only the first of those two, and reaches
// it under a shape no consumer can match.
//
// It reports EVERY offending path on every offending binding rather than the
// first — a contract of the rule rather than a property of how it happens to be
// written. The rule is invoked once per declared binding and the framework
// accumulates what each invocation raised, so an author fixing a Manifest with
// several overlaps sees all of them in one build instead of one per build.
//
// Walk order. Bindings are visited in the order the registry declares them,
// which is author order; within one binding the rule raises in the order the
// values pillar declares its paths. Both orders are fixed by what the framework
// and the presentation already guarantee, so repeated runs over identical input
// report an identical sequence.
//
// Nothing here halts. An author who accepts the overlap keeps the deterministic
// routing the tie-break performs; the halt that catches an Aggregate-side
// annotation reaching the dropped pillar remains the load-bearing safety net.
//
// Returns nil when no Manifest was resolved or when no binding declares an
// overlapping path.
func LintAnnotationBindingMaps(result *ResolveResult) []*ResolverError {
	if result == nil || result.Manifest == nil {
		return nil
	}
	document := manifestDocumentOf(result)
	var presented constraint.DocumentView = newManifestDocumentView(document, result.Manifest)
	return findingsAtTheirOwnDocument(
		constraint.RunOnDocumentAnnotationBindings(
			document,
			presented.ManifestAnnotationBindingKeys(document),
			presented,
			annotationBindingPathRoutesThroughOnePillar,
		),
		"Manifest",
		"Manifest",
	)
}
