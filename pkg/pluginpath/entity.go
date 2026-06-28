// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"fmt"
)

// ErrWasmPluginEntityNotFound is the Loud Guardrail for a plugin reference that
// does not resolve to a WasmPlugin entity. A plugin is referenced by its
// WasmPlugin entity URI (`name:version` or `provider/name:version`); the entity
// carries the .wasm artifact reference in spec.plugin. A bare .wasm reference or
// an artifact reference is no longer accepted: the build halts here naming the
// offending reference.
var ErrWasmPluginEntityNotFound = errors.New("plugin reference is not a WasmPlugin entity")

// EntityLookup adapts the artifact-resolving cascade into an entity-fronted
// Lookup: a plugin reference is a WasmPlugin entity URI, and resolution is a
// two-step — translate the entity URI to its .wasm artifact reference, then run
// the cascade on the artifact. This is the single home of the
// entity → spec.plugin → cascade two-step; every socket that loads a pipeline
// plugin by entity URI (the build-load pipeline executor and the
// workspace-config linter path) binds the Lookup this returns, so the linkage
// exists exactly once.
//
// entityToArtifact translates a WasmPlugin entity URI to its spec.plugin
// artifact reference, returning false when no WasmPlugin entity matches the URI.
// The caller supplies it as a closure over the catalog's WasmPlugin accessor
// reading .Spec.Plugin, so this package stays ignorant of pkg/types. cascade is
// the artifact-resolving Lookup (a build-configured Resolver's Resolve method);
// it keys its pins and verification by the ARTIFACT reference, so translating
// before the cascade leaves the pin model unchanged.
//
// A reference that does not match a WasmPlugin entity halts with
// ErrWasmPluginEntityNotFound, naming the offending reference and stating that a
// WasmPlugin entity URI is required. A nil entityToArtifact disables the entity
// translation entirely (every reference halts as not-an-entity); a nil cascade
// is a programming error and panics through the returned Lookup's nil call.
func EntityLookup(entityToArtifact func(uri string) (artifactURI string, ok bool), cascade Lookup) Lookup {
	return func(entityURI string) (ResolvedPlugin, error) {
		var artifactURI string
		var ok bool
		if entityToArtifact != nil {
			artifactURI, ok = entityToArtifact(entityURI)
		}
		if !ok {
			return ResolvedPlugin{}, fmt.Errorf(
				"%w: %q must be a WasmPlugin entity URI (e.g. `name:version` or `provider/name:version`); "+
					"bare-`.wasm` and artifact references are no longer accepted",
				ErrWasmPluginEntityNotFound, entityURI)
		}
		return cascade(artifactURI)
	}
}
