// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"fmt"
)

// ErrPluginEntityNotFound is the Loud Guardrail for a plugin reference that
// does not resolve to a Plugin entity. A plugin is referenced by its
// Plugin entity URI (`name:version` or `provider/name:version`); the entity's
// .wasm artifact reference is derived from its bare identity. A bare .wasm
// reference or an artifact reference is no longer accepted: the build halts here
// naming the offending reference.
var ErrPluginEntityNotFound = errors.New("plugin reference is not a Plugin entity")

// EntityLookup adapts the artifact-resolving cascade into an entity-fronted
// Lookup: a plugin reference is a Plugin entity URI, and resolution is a
// two-step — translate the entity URI to its .wasm artifact reference, then run
// the cascade on the artifact. This is the single home of the
// entity → artifact-reference → cascade two-step; every socket that loads a
// pipeline plugin by entity URI (the build-load pipeline executor and the
// workspace-config linter path) binds the Lookup this returns, so the linkage
// exists exactly once.
//
// entityToArtifact translates a Plugin entity URI to its .wasm artifact
// reference (the entity's bare identity), the entity's authored capability claim,
// and the entity's authored runtime limits, returning false when no Plugin entity
// matches the URI. The caller supplies it as a closure over the catalog's Plugin
// accessor, so this package stays ignorant of pkg/types; resolving the entity
// once here yields the artifact reference, the claim, and the limits together, so
// the load path never re-resolves the entity. cascade is the artifact-resolving
// Lookup (a
// build-configured Resolver's Resolve method); it keys its pins and verification
// by the ARTIFACT reference, so translating before the cascade leaves the pin
// model unchanged. The claim is attached to the cascade's ResolvedPlugin so the
// load gate can cross-check it against the plugin's describe(); the limits are
// attached so the loader caps the plugin's memory and per-call wall time.
//
// A reference that does not match a Plugin entity halts with
// ErrPluginEntityNotFound, naming the offending reference and stating that a
// Plugin entity URI is required. A nil entityToArtifact disables the entity
// translation entirely (every reference halts as not-an-entity); a nil cascade
// is a programming error and panics through the returned Lookup's nil call.
func EntityLookup(entityToArtifact func(uri string) (artifactURI string, claim *PluginCapabilityClaim, limits *PluginRuntimeLimits, ok bool), cascade Lookup) Lookup {
	return func(entityURI string) (ResolvedPlugin, error) {
		var artifactURI string
		var claim *PluginCapabilityClaim
		var limits *PluginRuntimeLimits
		var ok bool
		if entityToArtifact != nil {
			artifactURI, claim, limits, ok = entityToArtifact(entityURI)
		}
		if !ok {
			return ResolvedPlugin{}, fmt.Errorf(
				"%w: %q must be a Plugin entity URI (e.g. `name:version` or `provider/name:version`); "+
					"bare-`.wasm` and artifact references are no longer accepted",
				ErrPluginEntityNotFound, entityURI)
		}
		resolved, err := cascade(artifactURI)
		if err != nil {
			return ResolvedPlugin{}, err
		}
		resolved.Capability = claim
		resolved.Limits = limits
		return resolved, nil
	}
}
