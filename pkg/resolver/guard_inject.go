// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

// guardInjectKeys validates that every inject block's from-key references
// a key declared in the target Component's provides schema. Walks every
// feature and secret declaration on every Aggregate-level component,
// extracts the inject block's from-keys (compact and verbose forms),
// and confirms each from-key matches a key the target component declares
// as part of its provides contract.
//
// Emits ErrInvalidInjectKey per offending from-key, with the entity, the
// JSON-path-style field locator (component → feature/secret → inject →
// mechanism → delivery key), and a message naming the offending key plus
// the valid set. Errors sort by (Entity, Field) for byte-identical output.
//
// Returns nil when agg is nil or catalog.Component is nil. Component
// types not present in the catalog are silently skipped — unknown-type
// reporting belongs to other guards.
func guardInjectKeys(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components.All() {
		// Check inject blocks on features
		for featName, feat := range comp.Features.All() {
			if feat.Inject != nil {
				errs = append(errs, validateInjectBlock(
					entity,
					fmt.Sprintf(".spec.components[%q].features[%q].inject", compName, featName),
					*feat.Inject,
					comp.Type,
					catalog,
				)...)
			}
		}

		// Check inject blocks on secrets
		for secName, sec := range comp.Secrets.All() {
			if sec.Inject != nil {
				errs = append(errs, validateInjectBlock(
					entity,
					fmt.Sprintf(".spec.components[%q].secrets[%q].inject", compName, secName),
					*sec.Inject,
					comp.Type,
					catalog,
				)...)
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// validateInjectBlock validates one inject block against the target
// component's provides schema. Walks the standard mechanisms (env, file,
// arg) plus any additional mechanisms the catalog declares, and checks
// each mechanism's from-keys against the target's provides keys.
//
// Degraded modes:
//
//   - Unknown target type — silently returns nil (handled by other guards).
//   - Provides-schema extraction fails — emits exactly one
//     ErrInvalidInjectKey for the inject block carrying the constraint
//     message; from-key validation is skipped because the valid set cannot
//     be computed without the schema.
//   - Empty provides — silently returns nil (no keys to reference).
//
// Mechanisms are iterated in alphabetical order (additional mechanisms
// sorted) for deterministic error output.
func validateInjectBlock(entity, fieldPath string, inject types.InjectBlock, targetType string, catalog CatalogLookup) []*ResolverError {
	comp, found := catalog.Component(targetType)
	if !found {
		// Unknown type is another guard's concern; skip here.
		return nil
	}

	providesKeys, extractErrs := validator.ExtractProvidesKeys(comp.Spec.CueSchema)
	if len(extractErrs) > 0 {
		// cueSchema compiles with errors — surface once per inject block but don't
		// mask downstream inject-key issues (which we can't validate without the schema).
		return []*ResolverError{{
			Entity:  entity,
			Field:   fieldPath + ".cueSchema",
			Message: extractErrs[0].Constraint,
			Err:     ErrInvalidInjectKey,
		}}
	}
	if len(providesKeys) == 0 {
		// Target has no provides pillar — nothing to reference.
		return nil
	}
	validSet := make(map[string]bool, len(providesKeys))
	for _, k := range providesKeys {
		validSet[k] = true
	}

	var errs []*ResolverError
	for _, pair := range injectMechanisms(inject) {
		errs = append(errs, validateMechanismFromKeys(entity, fieldPath, pair.mech, pair.raw, validSet, providesKeys)...)
	}
	return errs
}

type mechanismRaw struct {
	mech string
	raw  any
}

// injectMechanisms returns the inject block's mechanisms paired with their
// raw payloads. The three standard mechanisms (arg, env, file) are emitted
// first in alphabetical order; any catalog-declared additional mechanisms
// follow, also alphabetically sorted. The ordering is for deterministic
// error output, not a semantic preference among mechanisms.
func injectMechanisms(inject types.InjectBlock) []mechanismRaw {
	out := []mechanismRaw{
		{"arg", inject.Arg}, // sorted alphabetically
		{"env", inject.Env},
		{"file", inject.File},
	}
	additionalNames := make([]string, 0, len(inject.Additional))
	for k := range inject.Additional {
		additionalNames = append(additionalNames, k)
	}
	sort.Strings(additionalNames)
	for _, k := range additionalNames {
		out = append(out, mechanismRaw{k, inject.Additional[k]})
	}
	return out
}

// validateMechanismFromKeys walks one mechanism's raw inject entries, extracts
// each entry's from-key, and validates that the from-key exists in validSet.
// Entries that look like literals (value, not from) are skipped.
func validateMechanismFromKeys(entity, fieldPath, mech string, raw any, validSet map[string]bool, validKeys []string) []*ResolverError {
	from := extractInjectFromKeys(raw)
	if len(from) == 0 {
		return nil
	}

	var errs []*ResolverError
	localKeys := make([]string, 0, len(from))
	for k := range from {
		localKeys = append(localKeys, k)
	}
	sort.Strings(localKeys)

	for _, localKey := range localKeys {
		fromKey := from[localKey]
		if fromKey == "" || validSet[fromKey] {
			continue
		}
		errs = append(errs, &ResolverError{
			Entity:  entity,
			Field:   fmt.Sprintf("%s.%s.%s", fieldPath, mech, localKey),
			Message: fmt.Sprintf("from-key %q does not match any provides key on target; valid keys: %s", fromKey, strings.Join(validKeys, ", ")),
			Err:     ErrInvalidInjectKey,
		})
	}
	return errs
}

// extractInjectFromKeys normalizes compact ({DB_URL: "host"}) and verbose
// ({DB_URL: {from: "host"}}) inject forms into {localKey: fromKey}. Empty
// fromKey means the entry is a literal value (verbose with "value:" only) and
// is skipped by the validator above.
func extractInjectFromKeys(raw any) map[string]string {
	out := map[string]string{}
	switch v := raw.(type) {
	case map[string]string:
		for k, val := range v {
			out[k] = val
		}
	case map[string]any:
		for k, val := range v {
			switch vv := val.(type) {
			case string:
				out[k] = vv
			case map[string]any:
				if fromStr, ok := vv["from"]; ok {
					if s, sok := fromStr.(string); sok {
						out[k] = s
					}
				}
			}
		}
	}
	return out
}
