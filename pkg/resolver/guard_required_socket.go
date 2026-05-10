// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardRequiredUsesSockets enforces that every ComponentInstance in the Aggregate
// populates each `required: true` socket declared by its Component's usesSchema.
// Emits ErrMissingRequiredSocket per missing or empty socket. Walks the Aggregate
// top-level components plus all ComponentInstances reachable through Subsystems.
func guardRequiredUsesSockets(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	var errs []*ResolverError
	instances := collectComponentInstances(agg)
	for _, pair := range instances {
		comp, ok := catalog.Component(pair.inst.Type)
		if !ok {
			continue // unknown-type errors handled by a different guard
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
				Field:   fmt.Sprintf(".components.%s.uses.%s", pair.name, socketName),
				Message: fmt.Sprintf("required uses socket %q has no entries in component %q", socketName, pair.name),
				Err:     ErrMissingRequiredSocket,
			})
		}
	}
	return errs
}

// socketIsPopulated returns true when the Aggregate's uses block has at least
// one entry for the given socket. Accepts both the compact list form
// (`socket: [target1, target2]`) and the verbose map form
// (`socket: {alias: {target: ..., ...}}`). Empty strings in a list and empty
// maps count as not-populated.
func socketIsPopulated(uses types.OrderedMap[any], socket string) bool {
	raw, ok := uses.Get(socket)
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				return true
			}
		}
		return false
	case []string:
		for _, s := range v {
			if s != "" {
				return true
			}
		}
		return false
	case *types.OrderedMap[any]:
		return v.Len() > 0
	case *types.OrderedMap[*types.OrderedMap[any]]:
		return v.Len() > 0
	case map[string]any:
		// Defensive: in-memory test fixtures may construct map shapes
		// that bypass the OrderedMap unmarshal path.
		return len(v) > 0
	case map[string]map[string]any:
		// Defensive: same rationale.
		return len(v) > 0
	default:
		// Unknown shapes aren't counted as populated — guard defers to the
		// schema-validation guard for shape errors.
		return false
	}
}
