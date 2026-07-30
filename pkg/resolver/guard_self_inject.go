// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardSelfInject validates component-level self-inject declarations
// against three rules:
//
//  1. The catalog component's spec.inject allowlist must be non-empty.
//     A component type with empty spec.inject is a Black-Box (Packaged
//     Compute) — its Recipe handles internal wiring via valuesMapping
//     and self-inject is structurally forbidden. A component type with
//     non-empty spec.inject is a White-Box (Custom Code) where the
//     Developer must dictate delivery, and self-inject is permitted up
//     to the allowlist.
//  2. Each mechanism used in the self-inject (env, file, arg, or
//     additional mechanisms) must be a member of the allowlist.
//  3. No delivery-key collision between component-level self-inject and
//     uses-level inject within the same mechanism — the same key cannot
//     be claimed by both delivery sources.
//
// Emits ErrSelfInjectForbidden for rule-1 and rule-2 violations and
// ErrInjectKeyCollision for rule-3 violations. Errors sort by
// (Entity, Field) for byte-identical output. Returns nil when agg is
// nil or catalog.Component is nil. Components whose Inject field is nil
// are skipped (no self-inject to validate); component types not in the
// catalog are silently skipped — unknown-type reporting belongs to
// other guards.
func guardSelfInject(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := p.inst
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		if comp.Inject == nil {
			continue
		}

		catComp, found := catalog.Component(comp.Type)
		if !found {
			if verr := guardEntityViewInconsistency(entity, base, comp.Type); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}

		// Check allowlist exists (Black-Box = no spec.inject)
		if len(catComp.Spec.Inject) == 0 {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s.inject", base),
				Message: fmt.Sprintf("component type %q has no spec.inject allowlist (Black-Box pattern) — self-inject is not allowed", comp.Type),
				Err:     ErrSelfInjectForbidden,
			})
			continue
		}

		// Build allowlist set
		allowed := make(map[string]bool, len(catComp.Spec.Inject))
		for _, mech := range catComp.Spec.Inject {
			allowed[mech] = true
		}

		// Check each mechanism used in self-inject is in the allowlist
		mechanisms := collectInjectMechanisms(comp.Inject)
		for _, mech := range mechanisms {
			if !allowed[mech] {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf("%s.inject.%s", base, mech),
					Message: fmt.Sprintf("mechanism %q not in component type %q spec.inject allowlist %v", mech, comp.Type, catComp.Spec.Inject),
					Err:     ErrSelfInjectForbidden,
				})
			}
		}

		// Check for key collisions between self-inject and uses-inject
		selfKeys := collectInjectDeliveryKeys(comp.Inject)
		usesKeys := collectUsesInjectDeliveryKeys(comp.Uses)
		for mech, keys := range selfKeys {
			for _, key := range keys {
				if contains(usesKeys[mech], key) {
					errs = append(errs, &ResolverError{
						Entity:  entity,
						Field:   fmt.Sprintf("%s.inject.%s[%q]", base, mech, key),
						Message: fmt.Sprintf("delivery key %q in mechanism %q is declared in both component self-inject and uses-inject", key, mech),
						Err:     ErrInjectKeyCollision,
					})
				}
			}
		}

		// Check for cross-mechanism collisions within the self-inject block:
		// the same delivery key declared on more than one mechanism (e.g.,
		// inject.ssm_param.DB_URL and inject.env.DB_URL) is a contradiction
		// because the runtime cannot deliver the same key from two different
		// sources simultaneously. Symmetric to the rule-3 self-vs-uses check
		// above; same emission shape (ErrInjectKeyCollision).
		mechsByKey := map[string][]string{}
		for mech, keys := range selfKeys {
			for _, key := range keys {
				mechsByKey[key] = append(mechsByKey[key], mech)
			}
		}
		collisionKeys := make([]string, 0, len(mechsByKey))
		for key, mechs := range mechsByKey {
			if len(mechs) > 1 {
				collisionKeys = append(collisionKeys, key)
			}
		}
		sort.Strings(collisionKeys)
		for _, key := range collisionKeys {
			mechs := mechsByKey[key]
			sort.Strings(mechs)
			for _, mech := range mechs {
				others := make([]string, 0, len(mechs)-1)
				for _, other := range mechs {
					if other != mech {
						others = append(others, other)
					}
				}
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf("%s.inject.%s[%q]", base, mech, key),
					Message: fmt.Sprintf("delivery key %q in mechanism %q collides with declaration in mechanism(s) %v", key, mech, others),
					Err:     ErrInjectKeyCollision,
				})
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// collectInjectMechanisms returns the mechanism names present in an
// InjectBlock. Core mechanisms (env, file, arg) populate first in their
// struct-field order when their sub-field is non-nil; custom mechanisms
// from Additional follow in author order via OrderedMap.All(). User-facing
// error emission is canonicalised by sortResolverErrors at the resolver
// boundary; this helper's slice ordering is semantic-only. Does not mutate
// the input.
func collectInjectMechanisms(inject *types.InjectBlock) []string {
	var mechs []string
	if inject.Env != nil {
		mechs = append(mechs, "env")
	}
	if inject.File != nil {
		mechs = append(mechs, "file")
	}
	if inject.Arg != nil {
		mechs = append(mechs, "arg")
	}
	for k := range inject.Additional.All() {
		mechs = append(mechs, k)
	}
	return mechs
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
// The recognised shapes are the ordered mapping form (which preserves author
// order), the two plain-map forms, and the array form whose elements carry the
// delivery key under `in`. An unrecognised shape contributes nothing. Does not
// mutate the payload.
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
		for k := range v {
			result[mech] = append(result[mech], k)
		}
	case map[string]string:
		for k := range v {
			result[mech] = append(result[mech], k)
		}
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
