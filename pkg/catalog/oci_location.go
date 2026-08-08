// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "strings"

// OCILocation is the single rule mapping an entity identity to its OCI
// repository LOCATION: `<kindSegment>/<lower(identityPath)>`. A location is a
// coordinate derived from identity, not an identity itself: OCI repository names
// must be lowercase, while a catalog identity may carry case (PascalCase
// SchemaLibrary / MappingLibrary) and a scope segment (Recipe / FeatureRecipe
// identity is provider/name; Plugin identity is pluginType/name). So the location
// lowercases the whole path and, for recipes and plugins, encodes the scope
// segment — two providers may ship a recipe of the same name, and two sockets may
// hold a plugin of the same name; dropping the scope silently overwrites one with
// the other. The identity itself stays cased and scope-qualified everywhere it is
// an identity (metadata.name plus its scope, lockfile, cache key, closure).
//
// For a Plugin the scope segment is its pluginType, passed as provider: a plugin
// derives its location as `plugin/<lower(pluginType)>/<lower(name)>`, the same
// shape a recipe derives from its provider. pluginType is to a plugin what
// provider is to a recipe — a required non-empty scope segment.
//
// This is the single source of the location rule for both directions: push
// publishes an entity at `<registry>/<OCILocation>` and a registry-backed pull
// binds a bare identity to the same path, so push-location == pull-location holds
// by construction.
func OCILocation(kind, provider, name string) string {
	seg := KindPathSegment(kind)
	switch kind {
	case "Recipe", "FeatureRecipe", "Plugin", "Wasm":
		// provider is a required non-empty scope segment (a recipe's provider or a
		// plugin's pluginType); the caller (entityReference) rejects an empty scope
		// before reaching here.
		return seg + "/" + strings.ToLower(provider) + "/" + strings.ToLower(name)
	default:
		return seg + "/" + strings.ToLower(name)
	}
}

// KindPathSegment maps an entity kind to its OCI repository path segment.
//
// For an entity kind the segment is the kind's own lowercase path token, which is
// what the declared entity-kind vocabulary hands out; a test holds that no entity
// kind needs an exception here, so this stays a single rule rather than a table
// that could fall behind. The one alias is `Wasm`, which is not an entity kind at
// all — it is the raw plugin artifact, published beneath the same `plugin` prefix
// as the Plugin entity that pins it, per the plugin URI convention
// (`<registry>/plugin/<pluginType>/<name>`).
//
// A token that is neither an entity kind nor `Wasm` is lowercased unchanged, so a
// caller addressing something outside the vocabulary still gets a well-formed
// lowercase segment rather than an empty one.
func KindPathSegment(kind string) string {
	if kind == "Wasm" {
		return "plugin"
	}
	return strings.ToLower(kind)
}
