// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import "fmt"

// guardEntityViewInconsistency classifies a guard's catalog-entity miss
// (`found == false` for a component type) and returns the loud halt for the
// inconsistent case, or nil for the legitimate skip.
//
// At a guard, a missing catalog entity is never a dangling user reference — a
// genuinely unresolvable reference halts at resolution (NotFound) before the
// guards run. So a miss is one of two things, told apart by resolution's own
// Tier-1 discriminator [isRelativePathRef] (the three-tier entity-resolution
// rule: `./…`/`../…` is a local-path file, a bare token is a catalog identity),
// NOT by an ad-hoc string check:
//
//   - a Tier-1 relative-path type (`./…` / `../…`): a genuinely non-catalog
//     local file with no catalog entity — the caller's existing skip is correct,
//     so this returns nil;
//   - a bare-token catalog reference resolution resolved but that is absent from
//     the guard's view: an internal inconsistency between the build's resolved
//     set and the guard's catalog view — this returns an
//     [ErrGuardEntityViewInconsistency] ResolverError for the caller to append.
//
// The five guards that previously skipped silently on a missing catalog entity
// route their miss through this one helper, so the classification and diagnostic
// are identical across them (and inherited unchanged when the lookup backing
// changes underneath).
func guardEntityViewInconsistency(entity, field, typeRef string) *ResolverError {
	if isRelativePathRef(typeRef) {
		return nil
	}
	return &ResolverError{
		Entity:  entity,
		Field:   field,
		Message: fmt.Sprintf("type %q was resolved as a catalog reference but is absent from the guard's catalog view (build resolved-set / guard view divergence)", typeRef),
		Err:     ErrGuardEntityViewInconsistency,
	}
}
