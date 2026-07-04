// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "strings"

// OCILocation is the single rule mapping an entity identity to its OCI
// repository LOCATION: `<kindSegment>/<lower(identityPath)>`. A location is a
// coordinate derived from identity, not an identity itself: OCI repository names
// must be lowercase, while a catalog identity may carry case (PascalCase
// SchemaLibrary / MappingLibrary) and a provider scope (Recipe / FeatureRecipe
// identity is provider/name). So the location lowercases the whole path and, for
// recipes, encodes the provider — two providers may ship a recipe of the same
// name, and dropping the provider silently overwrites one with the other. The
// identity itself stays cased and provider-scoped everywhere it is an identity
// (metadata.name, lockfile, cache key, closure).
//
// This is the single source of the location rule for both directions: push
// publishes an entity at `<registry>/<OCILocation>` and a registry-backed pull
// binds a bare identity to the same path, so push-location == pull-location holds
// by construction.
func OCILocation(kind, provider, name string) string {
	seg := KindPathSegment(kind)
	switch kind {
	case "Recipe", "FeatureRecipe":
		// provider is a required non-empty scope for recipe locations; the caller
		// (entityReference) rejects an empty provider before reaching here.
		return seg + "/" + strings.ToLower(provider) + "/" + strings.ToLower(name)
	default:
		return seg + "/" + strings.ToLower(name)
	}
}

// KindPathSegment maps an entity kind to its OCI repository path segment.
// Default behavior is `strings.ToLower(kind)`; the Plugin kind aliases to
// the canonical `wasm` path prefix per the plugin URI convention
// (`<registry>/wasm/<socket>/<name>`).
func KindPathSegment(kind string) string {
	switch kind {
	case "Plugin", "Wasm":
		return "wasm"
	default:
		return strings.ToLower(kind)
	}
}
