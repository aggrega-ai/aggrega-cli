// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"sort"

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
	payloads := injectMechanismPayloads(inject)
	if payloads.Len() == 0 {
		return nil
	}
	return payloads.Keys()
}

// injectMechanismPayloads pairs every mechanism an injection block dictates with
// the payload the author wrote under it, in the order above: the core mechanisms
// in struct-field order when their sub-field is non-nil, then custom mechanisms
// in author order.
//
// It is the one enumeration of an injection block's mechanisms, which is why the
// name reading sits on top of it rather than beside it. A reader that walked the
// block itself would be a second statement of which names the block carries as
// fields, and the two would be free to disagree the moment either moved — a
// disagreement nothing would catch, because both would still compile and both
// would still return mechanism names.
//
// Presence is the key's, not the payload's, for the reason the name reading
// records: the conversions further down the build drop a payload they cannot
// express as one string per delivery key, so counting a mechanism only when its
// payload survives would silently miss a dictation.
func injectMechanismPayloads(inject *types.InjectBlock) types.OrderedMap[any] {
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
// delivery-key collector knows the payload shapes a mechanism value can arrive
// in, so the two cannot recognise different shapes: a payload shape one
// collector walks is a shape both walk. That matters because the collectors
// feed a collision check, and a collector that returns no keys for a shape it
// does not recognise makes the check approve a declaration it never examined —
// indistinguishable, from the outside, from a check that examined it and found
// nothing wrong.
//
// The recognised shapes are the ordered mapping form, the two plain-map forms,
// and the array form whose elements carry the delivery key under `in`. An
// unrecognised shape contributes nothing. Does not mutate the payload.
//
// Order follows the shape, because only some shapes carry one. The ordered
// mapping form and the array form contribute their keys in the order the author
// wrote them. A plain map carries no author order to preserve — ranging one
// yields a sequence that varies between reads of the very same payload — so
// those two arms contribute their keys alphabetically. That is what makes a walk
// over the collected keys repeatable whatever shape the payload arrived in,
// which the keys reaching a diagnostic sequence depend on.
func appendInjectDeliveryKeys(result map[string][]string, mech string, raw any) {
	if raw == nil {
		return
	}
	switch v := raw.(type) {
	case *types.OrderedMap[any]:
		if v == nil {
			return
		}
		for k := range v.All() {
			result[mech] = append(result[mech], k)
		}
	case map[string]any:
		result[mech] = append(result[mech], sortedKeysOf(v)...)
	case map[string]string:
		result[mech] = append(result[mech], sortedKeysOf(v)...)
	case []any:
		for _, entry := range v {
			in, _, _, _, _, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok {
				continue
			}
			result[mech] = append(result[mech], in)
		}
	}
}

// sortDeliveryKeys sorts each mechanism's key slice so downstream walks over
// the collected keys iterate deterministically.
func sortDeliveryKeys(result map[string][]string) {
	for mech := range result {
		sort.Strings(result[mech])
	}
}

// collectInjectDeliveryKeys returns delivery-key names indexed by mechanism
// name. The outer iteration walks core mechanisms (env, file, arg) followed
// by custom mechanisms from Additional in author order via OrderedMap.All();
// the per-mechanism inner delivery-key slice is sorted alphabetically for
// deterministic iteration of the keys downstream callers walk. Returns an
// empty map when inject is nil or carries no recognized shape. Does not
// mutate the input.
func collectInjectDeliveryKeys(inject *types.InjectBlock) map[string][]string {
	result := map[string][]string{}
	if inject == nil {
		return result
	}
	appendInjectDeliveryKeys(result, "env", inject.Env)
	appendInjectDeliveryKeys(result, "file", inject.File)
	appendInjectDeliveryKeys(result, "arg", inject.Arg)
	for k, v := range inject.Additional.All() {
		appendInjectDeliveryKeys(result, k, v)
	}
	sortDeliveryKeys(result)
	return result
}

// collectUsesInjectDeliveryKeys returns delivery-key names indexed by
// mechanism name across every uses-level inject block on a component. The
// outer iteration walks core mechanisms (env, file, arg) followed by custom
// mechanisms from Additional in author order via OrderedMap.All(); the
// per-mechanism inner delivery-key slice is sorted alphabetically for
// deterministic iteration. Returns an empty map when uses is empty or no
// verbose-form socket carries inject directives. Does not mutate the input.
//
// Compact-form sockets (string arrays) carry no inject directives and are
// skipped. Verbose-form sockets walk the typed VerboseUsesEntry slice and
// extract delivery keys from each entry's optional Inject block.
func collectUsesInjectDeliveryKeys(uses types.OrderedMap[types.UsesSocketValue]) map[string][]string {
	result := map[string][]string{}
	if uses.Len() == 0 {
		return result
	}
	for _, val := range uses.All() {
		if val.Verbose.Len() == 0 {
			continue
		}
		for _, entry := range val.Verbose.All() {
			if entry.Inject == nil {
				continue
			}
			appendInjectDeliveryKeys(result, "env", entry.Inject.Env)
			appendInjectDeliveryKeys(result, "file", entry.Inject.File)
			appendInjectDeliveryKeys(result, "arg", entry.Inject.Arg)
			for mech, mechRaw := range entry.Inject.Additional.All() {
				appendInjectDeliveryKeys(result, mech, mechRaw)
			}
		}
	}
	sortDeliveryKeys(result)
	return result
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
