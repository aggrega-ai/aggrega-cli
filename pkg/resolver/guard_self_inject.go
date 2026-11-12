// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// InjectMechanismNames returns the delivery-mechanism names an inject block
// dictates. Core mechanisms (env, file, arg) populate first in their struct-field
// order when their sub-field is non-nil; custom mechanisms from Additional follow
// in author order via OrderedMap.All(). Returns nil for a nil block. Does not
// mutate the input.
//
// A mechanism counts as dictated when its key is PRESENT, whatever its payload
// resolves to. That is deliberate and it is what makes this usable by an allowlist
// check: the conversions further down the build drop a payload they cannot express
// as one string per delivery key, so a check reading mechanism names off a
// converted structure would silently miss a dictation whose payload was dropped
// and report the edge as dictating nothing.
//
// It is exported because it is the one enumeration of that question, and the
// allowlist checks on both delivery surfaces — a component's own inject block and
// an edge's — must not disagree about which mechanisms an author dictated.
// User-facing error emission is canonicalised at each emitting boundary; this
// helper's slice ordering is semantic-only.
func InjectMechanismNames(inject *types.InjectBlock) []string {
	payloads := InjectMechanismPayloads(inject)
	if payloads.Len() == 0 {
		return nil
	}
	return payloads.Keys()
}

// InjectMechanismPayloads pairs every mechanism an injection block dictates with
// the payload the author wrote under it, in the order above: the core mechanisms
// in struct-field order when their sub-field is non-nil, then custom mechanisms
// in author order.
//
// It is the one enumeration of an injection block's mechanisms, which is why the
// name reading sits on top of it rather than beside it, and why it is exported:
// a reader in another package that walked the block itself would be that second
// statement across a package boundary, where nothing at all compares the two. A reader that walked the
// block itself would be a second statement of which names the block carries as
// fields, and the two would be free to disagree the moment either moved — a
// disagreement nothing would catch, because both would still compile and both
// would still return mechanism names.
//
// Presence is the key's, not the payload's, for the reason the name reading
// records: the conversions further down the build drop a payload they cannot
// express as one string per delivery key, so counting a mechanism only when its
// payload survives would silently miss a dictation.
func InjectMechanismPayloads(inject *types.InjectBlock) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	if inject == nil {
		return out
	}
	if inject.Env != nil {
		out.Set("env", inject.Env)
	}
	if inject.File != nil {
		out.Set("file", inject.File)
	}
	if inject.Arg != nil {
		out.Set("arg", inject.Arg)
	}
	for k, v := range inject.Additional.All() {
		out.Set(k, v)
	}
	return out
}

// appendInjectDeliveryKeys appends the delivery-key names carried by one
// mechanism payload onto result[mech]. It is the single place either
// delivery-key reading opens a mechanism value — a component's own injection
// block on one side, a connection's authored delivery payload on the other — so
// the two cannot read it differently. That matters because both feed a
// collision rule, and two readings that disagreed about which keys a payload
// carries would let the rule approve a declaration one of them never examined.
//
// A mechanism is the list of entries the inject_delivery definition describes,
// each carrying its delivery key under `in`, and the keys are contributed in the
// order the author wrote them, which the keys reaching a diagnostic sequence
// depend on. An entry whose `in` does not decode is skipped, and a payload that
// is not a list contributes nothing. Does not mutate the payload.
func appendInjectDeliveryKeys(result map[string][]string, mech string, raw any) {
	entries, ok := raw.([]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		in, _, _, _, _, ok := types.DecodeVerboseInjectEntry(entry)
		if !ok {
			continue
		}
		result[mech] = append(result[mech], in)
	}
}
