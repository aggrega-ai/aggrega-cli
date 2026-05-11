// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"log"

	"github.com/cognitio/aggrega-cli/pkg/types"
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

// ValidateValuesAgainstBaseSchema is the transitional stub during the
// JSON Schema migration. The validator pipeline no longer processes
// CUE source strings; provider baseSchema fields authored in CUE do
// not get validated until the schema-authoring phase migrates them
// to JSON Schema 2020-12. When a provider carries a non-empty
// Spec.BaseSchema, this function logs a deprecation note once per
// provider and returns nil (skip).
//
// Returns nil on success or when validation is skipped. If the
// provider has no baseSchema, validation is skipped silently (the
// field is optional).
func ValidateValuesAgainstBaseSchema(
	providerName string,
	providerLookup ProviderLookup,
	_ map[string]any,
	_ string,
) error {
	if providerLookup == nil {
		return nil
	}
	p, found := providerLookup(providerName)
	if !found {
		return nil
	}
	if p.Spec.BaseSchema == "" {
		return nil
	}
	logBaseSchemaSkipOnce(providerName)
	return nil
}

// loggedBaseSchemaSkips tracks per-provider skip notifications so the
// deprecation log fires once per provider rather than once per
// validation call. The package-level map is not safe for concurrent
// mutation; renderer invocations are single-threaded per pipeline run.
var loggedBaseSchemaSkips = map[string]bool{}

func logBaseSchemaSkipOnce(providerName string) {
	if loggedBaseSchemaSkips[providerName] {
		return
	}
	loggedBaseSchemaSkips[providerName] = true
	log.Printf("warning: provider %q authors Spec.BaseSchema; baseSchema validation skipped pending JSON Schema migration",
		providerName)
}
