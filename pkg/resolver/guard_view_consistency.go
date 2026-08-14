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
// Every guard that would otherwise skip silently on a missing catalog entity
// routes its miss through this one helper, so the classification and the
// diagnostic are identical across them, and stay identical when the lookup
// backing changes underneath. The callers are the statement of how many there
// are; a number written here beside them would be a second copy that reads as
// current while lagging the set it counts.
func guardEntityViewInconsistency(entity, field, typeRef string) *ResolverError {
	if isRelativePathRef(typeRef) {
		return nil
	}
	return &ResolverError{
		Entity:  entity,
		Field:   field,
		Message: fmt.Sprintf("type %q was resolved as a catalog reference but is absent from the guard's catalog view (build resolved-set / guard view divergence)", typeRef),
		Err:     ErrGuardEntityViewInconsistency(),
	}
}

// GuardEntityViewInconsistency is the exported entry point to the catalog-entity
// view-inconsistency classifier, for callers outside this package — the topology
// sub-aggregate conformance guard, which validates flattened sub-aggregate nodes
// that the resolution guards do not reach. Same classification as the in-package
// guards: a Tier-1 relative-path type is a legitimate skip (nil); a bare catalog
// reference absent from the guard's view is the loud
// [ErrGuardEntityViewInconsistency] halt.
func GuardEntityViewInconsistency(entity, field, typeRef string) *ResolverError {
	return guardEntityViewInconsistency(entity, field, typeRef)
}
