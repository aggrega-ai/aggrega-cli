// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardRequiredUsesSockets enforces that every ComponentInstance in the Aggregate
// populates each `required: true` socket declared by its Component's usesSchema.
// Emits ErrMissingRequiredSocket per missing or empty socket. Walks the Aggregate
// top-level components plus all ComponentInstances reachable through Subsystems.
func guardRequiredUsesSockets(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	var errs []*ResolverError
	instances := collectComponentInstances(na)
	for _, pair := range instances {
		comp, ok := catalog.Component(pair.inst.Type)
		if !ok {
			// A resolved bare-token catalog ref absent here is a view
			// inconsistency (loud); a Tier-1 relative-path type legitimately has
			// no catalog entity (skip).
			if verr := guardEntityViewInconsistency(
				fmt.Sprintf("Aggregate %s", agg.Metadata.Name),
				componentFieldPath(pair.subsystemPath, pair.name, pair.synthesized),
				pair.inst.Type,
			); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		if comp.Spec.UsesSchema.Len() == 0 {
			continue
		}

		// Iterate in author order from the catalog entity (UsesSchema is OrderedMap).
		for socketName, entry := range comp.Spec.UsesSchema.All() {
			if !entry.Required {
				continue
			}
			if socketIsPopulated(pair.inst.Uses, socketName) {
				continue
			}
			errs = append(errs, &ResolverError{
				Entity:  fmt.Sprintf("Component %q (instance %q)", comp.Metadata.Name, pair.name),
				Field:   fmt.Sprintf("%s.uses[%q]", componentFieldPath(pair.subsystemPath, pair.name, pair.synthesized), socketName),
				Message: fmt.Sprintf("required uses socket %q has no entries in component %q", socketName, pair.name),
				Err:     ErrMissingRequiredSocket,
			})
		}
	}
	return errs
}

// socketIsPopulated returns true when the Aggregate's uses block has at
// least one entry for the given socket. Accepts both the compact list
// form (`socket: [target1, target2]`) and the verbose alias-keyed map
// form (`socket: {alias: {target: ..., ...}}`). Empty strings in the
// compact list and empty maps in the verbose form count as not-populated.
func socketIsPopulated(uses types.OrderedMap[types.UsesSocketValue], socket string) bool {
	val, ok := uses.Get(socket)
	if !ok {
		return false
	}
	for _, s := range val.Compact {
		if s != "" {
			return true
		}
	}
	return val.Verbose.Len() > 0
}
