// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

// ProviderLookup resolves a provider by name (e.g. "helm", "helm-chart").
type ProviderLookup func(name string) (types.Provider, bool)

// isSourceless reports whether a Recipe is in generative (sourceless) mode.
// Source absent/nil → sourceless. Source non-empty string/object → sourced.
// Empty-string source is a fatal halt condition handled elsewhere (ErrEmptySource).
func isSourceless(recipe *types.Recipe) bool {
	if recipe == nil {
		return true
	}
	if recipe.Spec.Source == nil {
		return true
	}
	if s, ok := recipe.Spec.Source.(string); ok {
		return s == "" // empty string → treat as sourceless for validation purposes;
		// the dedicated helm provider (WI-5) will reject empty-string source with ErrEmptySource.
	}
	return false
}

// ValidateValuesAgainstBaseSchema validates a valuesMapping output against the
// provider's baseSchema CUE definition. Used in sourceless (generative) mode to
// ensure the Recipe's valuesMapping output conforms to the provider's contract.
//
// Returns nil on success. If the provider has no baseSchema, validation is
// skipped (baseSchema is optional).
func ValidateValuesAgainstBaseSchema(
	providerName string,
	providerLookup ProviderLookup,
	values map[string]interface{},
	entityForErrors string,
) error {
	if providerLookup == nil {
		return nil
	}
	p, found := providerLookup(providerName)
	if !found {
		return nil // provider not registered in catalog — not a validation concern here
	}
	if p.Spec.BaseSchema == "" {
		return nil
	}
	errs := validator.ValidateValues(p.Spec.BaseSchema, values, entityForErrors)
	if len(errs) == 0 {
		return nil
	}
	// Return a compact error message listing all violations
	msg := fmt.Sprintf("provider %q baseSchema validation failed", providerName)
	for _, e := range errs {
		msg += "\n  - " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}
