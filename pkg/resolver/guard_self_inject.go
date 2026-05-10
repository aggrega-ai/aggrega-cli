// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
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
func guardSelfInject(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components.All() {
		if comp.Inject == nil {
			continue
		}

		catComp, found := catalog.Component(comp.Type)
		if !found {
			continue
		}

		// Check allowlist exists (Black-Box = no spec.inject)
		if len(catComp.Spec.Inject) == 0 {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.components[%q].inject", compName),
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
					Field:   fmt.Sprintf(".spec.components[%q].inject.%s", compName, mech),
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
						Field:   fmt.Sprintf(".spec.components[%q].inject.%s[%q]", compName, mech, key),
						Message: fmt.Sprintf("delivery key %q in mechanism %q is declared in both component self-inject and uses-inject", key, mech),
						Err:     ErrInjectKeyCollision,
					})
				}
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// collectInjectMechanisms returns the mechanism names present in an
// InjectBlock, sorted alphabetically for deterministic iteration. Treats
// nil sub-fields (Env, File, Arg) as absence; additional mechanisms are
// included by key. Does not mutate the input.
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
	sort.Strings(mechs)
	return mechs
}

// collectInjectDeliveryKeys returns delivery-key names indexed by
// mechanism name. Each per-mechanism slice is sorted alphabetically
// for deterministic iteration. Returns an empty map when inject is nil
// or carries no recognized shape. Does not mutate the input.
func collectInjectDeliveryKeys(inject *types.InjectBlock) map[string][]string {
	result := map[string][]string{}
	if inject == nil {
		return result
	}
	extractKeys := func(mech string, raw any) {
		if raw == nil {
			return
		}
		switch v := raw.(type) {
		case map[string]any:
			for k := range v {
				result[mech] = append(result[mech], k)
			}
		case map[string]string:
			for k := range v {
				result[mech] = append(result[mech], k)
			}
		}
	}
	extractKeys("env", inject.Env)
	extractKeys("file", inject.File)
	extractKeys("arg", inject.Arg)
	for k, v := range inject.Additional.All() {
		extractKeys(k, v)
	}
	// Sort each mechanism's keys
	for mech := range result {
		sort.Strings(result[mech])
	}
	return result
}

// collectUsesInjectDeliveryKeys returns delivery-key names indexed by
// mechanism name across every uses-level inject block on a component. Each
// per-mechanism slice is sorted alphabetically for deterministic
// iteration. Returns an empty map when uses is empty or no verbose-form
// socket carries inject directives. Does not mutate the input.
//
// Compact-form sockets (string arrays) carry no inject directives and are
// skipped. Verbose-form sockets walk the typed VerboseUsesEntry slice and
// extract delivery keys from each entry's optional Inject block.
func collectUsesInjectDeliveryKeys(uses types.OrderedMap[types.UsesSocketValue]) map[string][]string {
	result := map[string][]string{}
	if uses.Len() == 0 {
		return result
	}
	extractKeys := func(mech string, raw any) {
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
		}
	}
	for _, val := range uses.All() {
		if val.Verbose.Len() == 0 {
			continue
		}
		for _, entry := range val.Verbose.All() {
			if entry.Inject == nil {
				continue
			}
			extractKeys("env", entry.Inject.Env)
			extractKeys("file", entry.Inject.File)
			extractKeys("arg", entry.Inject.Arg)
			for mech, mechRaw := range entry.Inject.Additional.All() {
				extractKeys(mech, mechRaw)
			}
		}
	}
	for mech := range result {
		sort.Strings(result[mech])
	}
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
