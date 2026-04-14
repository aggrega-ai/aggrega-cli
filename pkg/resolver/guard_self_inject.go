// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardSelfInject validates component-level self-inject:
//  1. If ComponentInstance.Inject is non-nil, the catalog ComponentSpec.Inject must be non-empty (White-Box)
//  2. Each mechanism used in the self-inject must be in the catalog allowlist
//  3. No delivery key collision between self-inject and uses-inject within the same mechanism
func guardSelfInject(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	compNames := sortedKeys(agg.Spec.Components)
	for _, compName := range compNames {
		comp := agg.Spec.Components[compName]
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

// collectInjectMechanisms returns sorted mechanism names present in an InjectBlock.
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
	for k := range inject.Additional {
		mechs = append(mechs, k)
	}
	sort.Strings(mechs)
	return mechs
}

// collectInjectDeliveryKeys extracts delivery key names per mechanism from an InjectBlock.
func collectInjectDeliveryKeys(inject *types.InjectBlock) map[string][]string {
	result := map[string][]string{}
	if inject == nil {
		return result
	}
	extractKeys := func(mech string, raw interface{}) {
		if raw == nil {
			return
		}
		switch v := raw.(type) {
		case map[string]interface{}:
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
	for k, v := range inject.Additional {
		extractKeys(k, v)
	}
	// Sort each mechanism's keys
	for mech := range result {
		sort.Strings(result[mech])
	}
	return result
}

// collectUsesInjectDeliveryKeys extracts delivery keys from all uses-level inject blocks.
func collectUsesInjectDeliveryKeys(uses map[string]interface{}) map[string][]string {
	result := map[string][]string{}
	if uses == nil {
		return result
	}
	for _, socketRaw := range uses {
		m, ok := socketRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, entryRaw := range m {
			entry, ok := entryRaw.(map[string]interface{})
			if !ok {
				continue
			}
			injectRaw, ok := entry["inject"]
			if !ok {
				continue
			}
			injectMap, ok := injectRaw.(map[string]interface{})
			if !ok {
				continue
			}
			for mech, mechRaw := range injectMap {
				mechMap, ok := mechRaw.(map[string]interface{})
				if !ok {
					continue
				}
				for key := range mechMap {
					result[mech] = append(result[mech], key)
				}
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
