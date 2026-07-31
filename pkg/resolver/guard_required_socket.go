// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardRequiredUsesSockets enforces that each `required: true` socket declared
// by a Component's usesSchema is populated for every ComponentInstance of that
// Component. Emits ErrMissingRequiredSocket per missing or empty socket. Walks
// every ComponentInstance reachable through the Aggregate's Subsystems, which is
// all of them.
//
// Population is read from BOTH authoring documents, because both can bind such a
// socket: the Aggregate's own `uses` block, and the Release's per-edge `uses`
// surface, which may wire a declared-but-unbound socket to an existing
// component. A socket only the Release binds is populated in the graph that
// gets rendered, so reading the Aggregate alone would reject a document the
// operator edge surface expressly permits.
//
// No other surface can populate one of these sockets. A Feature attachment's
// `uses` sockets are the FEATURE's declared sockets, a separate socket set on a
// separate catalog entity, and a Feature-contributed edge is deliberately kept
// out of the consuming Component's apparent edge set — so an attachment cannot
// satisfy a Component's socket, and the rule's inputs are complete once both
// documents are parsed.
func guardRequiredUsesSockets(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	releaseBound := releaseBoundSockets(release)

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

		populated := map[string]bool{}
		for socketName := range comp.Spec.UsesSchema.All() {
			if socketIsPopulated(pair.inst.Uses, socketName) {
				populated[socketName] = true
				continue
			}
			if _, bound := releaseBound[releaseSocketKey{node: pair.name, socket: socketName}]; bound {
				populated[socketName] = true
			}
		}
		errs = append(errs, ValidateRequiredSockets(
			comp, pair.name,
			componentFieldPath(pair.subsystemPath, pair.name, pair.synthesized),
			populated,
		)...)
	}
	return errs
}

// ValidateRequiredSockets reports every socket comp's usesSchema declares
// `required: true` that populated does not mark as bound, one
// ErrMissingRequiredSocket per unbound socket. instanceName identifies the
// instance in the diagnostic and componentField is its authoring locator.
//
// The rule is the same wherever it is asked; only the evidence for "bound"
// differs by the stage that asks. Before flattening, the evidence is the two
// authored surfaces that can bind a socket — the component's own `uses` and the
// Release's per-edge `uses`. After flattening, for a component that exists only
// then, the evidence is the resolved edge set, which is the same answer arrived at
// downstream. Exported so both stages ask through one implementation rather than
// two that agree until one is edited.
//
// Iteration is the catalog entity's author order (usesSchema is ordered), so the
// emitted set is byte-identical across runs of the same input.
func ValidateRequiredSockets(comp types.Component, instanceName, componentField string, populated map[string]bool) []*ResolverError {
	if comp.Spec.UsesSchema.Len() == 0 {
		return nil
	}
	var errs []*ResolverError
	for socketName, entry := range comp.Spec.UsesSchema.All() {
		if !entry.Required || populated[socketName] {
			continue
		}
		errs = append(errs, &ResolverError{
			Entity:  fmt.Sprintf("Component %q (instance %q)", comp.Metadata.Name, instanceName),
			Field:   fmt.Sprintf("%s.uses[%q]", componentField, socketName),
			Message: fmt.Sprintf("required uses socket %q has no entries in component %q", socketName, instanceName),
			Err:     ErrMissingRequiredSocket,
		})
	}
	return errs
}

// releaseSocketKey identifies one socket on one consuming node, so a
// Release-side binding can be looked up per (node, socket) rather than
// per-node — a Release wiring one socket says nothing about a sibling socket on
// the same component.
type releaseSocketKey struct {
	node   string
	socket string
}

// releaseBoundSockets returns the set of sockets a Release binds by CREATING an
// edge. Only an entry naming a target creates one: a targetless entry
// configures an edge that must already exist, so it cannot populate a socket
// nothing else bound.
//
// The shared [collectReleaseEdgeEntries] walk supplies the entries, but the
// target-presence test itself is a SECOND statement of the rule the graph-build
// pass applies when it decides whether to draw an edge. The two agree by
// construction rather than by sharing a predicate, so they have to change
// together: were they to diverge, a socket omitted from this set is one the
// required-socket rule rejects even though an edge does get drawn for it.
//
// Node identity is the consuming node ID as the Release authored it, matched
// against the resolver-visible instance name — the convention every Step-1
// guard that reads the Release edge surface already uses, via the shared
// [collectReleaseEdgeEntries] walk. A node the resolver cannot see is therefore
// invisible here exactly as it is to the socket-allowlist and per-edge
// conformance gates that validate the very same entries.
func releaseBoundSockets(release *types.Release) map[releaseSocketKey]struct{} {
	if release == nil {
		return nil
	}
	bound := map[releaseSocketKey]struct{}{}
	for _, ref := range collectReleaseEdgeEntries(release) {
		if ref.entry.Target == "" {
			continue
		}
		bound[releaseSocketKey{node: ref.nodeID, socket: ref.socket}] = struct{}{}
	}
	return bound
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
