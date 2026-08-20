// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// lintUsesSocketNearMiss reads each socket on a component's own `uses:` block
// whose name is not declared in its Component type's usesSchema but is within a
// small edit distance of one — the shape a socket-name typo takes.
//
// 🔴 It is NOT the production reading. The rule reaches a build through the
// declared check the advisory suite invokes; this is the second, independent
// implementation the equivalence cross-check compares that reading against, and
// it is retained for exactly that. A cross-check sharing an implementation with
// what it checks cannot see the two drift apart, so this must not be folded into
// the declared check or re-expressed in terms of it — and it must not be wired
// into a build, which would report one authoring fault twice. It is unexported
// so that no caller outside this package can restore it to a production path.
//
// What the two legitimately share is one definition of what counts as a near
// miss: both reach [nearMissCandidates]. What the cross-check compares are their
// conclusions.
//
// A socket name that appears in no usesSchema entry is legal on this surface:
// the Aggregate author owns the application's topology and may name a socket the
// catalog did not anticipate, and the edge is built and delivered. Catalog
// silence about a socket means the connection was not anticipated, not that it is
// forbidden, so the openness is deliberately not a halt.
//
// The cost of the openness is that a misspelled socket name silently becomes a
// connection of its own instead of the one the author meant, and — carrying no
// usesSchema entry — it carries no selector, no inject allowlist and no per-edge
// contract. Naming both spellings so the author can see the pair is the
// mitigation.
//
// Scope is the component's own `uses:` surface alone, which is the one surface
// where an undeclared socket name is accepted. An attachment's `uses:` and a
// Release's per-edge `uses:` may only name a declared socket, so a near miss
// there already halts and reporting it again would double-report it.
//
// Walk order. Component instances are walked in the order
// collectComponentInstances returns (sorted by instance name); within an
// instance, socket names are walked in [sort.Strings] order and each socket's
// candidate declarations are compared in [sort.Strings] order. That order is this
// implementation's own and is deliberately NOT the declared check's, which
// invokes in the order the document declares; the cross-check therefore compares
// the two findings as sets.
//
// Returns nil when agg is nil or when catalog.Component is nil — a catalog-less
// path has no socket declarations to compare against.
func lintUsesSocketNearMiss(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var out []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := catalogComponentFor(p.inst.Type, catalog)
		if comp == nil || comp.Spec.UsesSchema.Len() == 0 {
			continue
		}
		declaredSockets := comp.Spec.UsesSchema.Keys()
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)

		names := append([]string(nil), p.inst.Uses.Keys()...)
		sort.Strings(names)
		for _, name := range names {
			if _, declared := comp.Spec.UsesSchema.Get(name); declared {
				continue
			}
			for _, socket := range nearMissCandidates(name, declaredSockets) {
				out = append(out, &ResolverError{
					Entity: entity,
					Field:  fmt.Sprintf("%s.uses[%q]", base, name),
					Message: fmt.Sprintf(
						"socket %q on component %q is not declared in the usesSchema of type %q, but the type declares the socket %q — if %q was meant, this edge is currently an ad-hoc connection and carries no selector",
						name, p.name, p.inst.Type, socket, socket,
					),
					Err: ErrUsesSocketNearMiss(),
				})
			}
		}
	}

	return out
}
