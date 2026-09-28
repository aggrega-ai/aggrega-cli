// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrPluginEntityNotFound is the Loud Guardrail for a plugin reference that
// does not resolve to a Plugin entity. A plugin is referenced by its Plugin
// entity URI, always scoped by the plugin's own socket type —
// `<pluginType>/<name>` optionally versioned, as in `parser/yaml` or
// `parser/yaml:1.0.0`. The scope is mandatory rather than one form among
// several, because two sockets may hold a plugin of the same name and the scope
// is what makes the entity resolvable. The entity's .wasm artifact reference is
// derived from its bare identity. A bare .wasm reference or an artifact
// reference is no longer accepted: the build halts here naming the offending
// reference.
func ErrPluginEntityNotFound() error { return errPluginEntityNotFound }

var errPluginEntityNotFound = cerrors.Register("plugin-entity-not-found", cerrors.Meta{
	ShortCode: "E0145",
	Message:   "plugin reference is not a Plugin entity",
	RulePlace: cerrors.RuleNoStageReaches("Plugin resolution evaluates the rule, when a reference is checked against the form its surface " +
		"accepts and then translated to the Plugin entity it must name. Resolution loads the plugin a " +
		"socket will run, so it happens ahead of every declared stage and no stage holds the reference or " +
		"the entity index it is matched against."),
})

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
// the entity's authored runtime limits, and the entity's machine-owned command
// mirror, returning false when no Plugin entity matches the URI. The caller
// supplies it as a closure over the catalog's Plugin accessor, so this package
// stays ignorant of the Plugin entity's shape; resolving the entity once here yields the artifact
// reference, the claim, the limits, and the command mirror together, so the load
// path never re-resolves the entity. cascade is the artifact-resolving Lookup (a
// build-configured Resolver's Resolve method); it keys its pins and verification
// by the ARTIFACT reference, so translating before the cascade leaves the pin
// model unchanged. The claim is attached to the cascade's ResolvedPlugin so the
// load gate can cross-check it against the plugin's describe(); the limits are
// attached so the loader caps the plugin's memory and per-call wall time; the
// command mirror is attached so the load gate verifies it against the plugin's
// describe() commands. A nil command mirror (an entity carrying no commands
// field) leaves the mirror unattached, so the load gate's command check is
// skipped.
//
// policy is the per-surface acceptance policy applied to the reference FORM
// before any entity translation: SurfaceEntityOnly rejects a filesystem-path or
// artifact reference early — the pipeline-plugin surface — naming the offending
// reference; SurfacePermissive accepts every form and lets entity translation
// decide — the cross-cutting sockets. The form check layers with entity
// existence: a well-formed bare entity URI that names no Plugin entity passes the
// form gate and still halts below as not-found. Both the form rejection and the
// not-found halt wrap ErrPluginEntityNotFound, so a caller that falls back on
// that sentinel behaves identically whether the reference was rejected early for
// its form or late for naming no entity.
//
// A reference that does not match a Plugin entity halts with
// ErrPluginEntityNotFound, naming the offending reference and stating that a
// Plugin entity URI is required — except, on the entity-only surface, one whose
// version pin is malformed, which halts with that malformation instead. A nil entityToArtifact disables the entity
// translation entirely (every reference halts as not-an-entity); a nil cascade
// is a programming error and panics through the returned Lookup's nil call.
func EntityLookup(entityToArtifact func(uri string) (artifactURI string, claim *PluginContractClaim, limits *PluginRuntimeLimits, commands *[]PluginCommandMirror, ok bool), cascade Lookup, policy SurfacePolicy) Lookup {
	return func(entityURI string) (ResolvedPlugin, error) {
		if err := AcceptReferenceForm(entityURI, policy); err != nil {
			return ResolvedPlugin{}, err
		}
		var artifactURI string
		var claim *PluginContractClaim
		var limits *PluginRuntimeLimits
		var commands *[]PluginCommandMirror
		var ok bool
		if entityToArtifact != nil {
			artifactURI, claim, limits, commands, ok = entityToArtifact(entityURI)
		}
		if !ok {
			// On the entity-only surface the form gate above has already refused a
			// path and an artifact reference, so what reaches here is an entity URI,
			// and one whose version pin is malformed is reported as that
			// malformation: the pin names no version, so no Plugin entity could
			// answer it. A permissive surface is not asked, because its reference
			// may be an artifact reference whose last colon ends a digest, and its
			// callers fall back on the not-found condition to resolve exactly those.
			if policy == SurfaceEntityOnly {
				if pe, malformed := types.ParseVersionPin(entityURI).Malformed(); malformed {
					return ResolvedPlugin{}, pe
				}
			}
			return ResolvedPlugin{}, fmt.Errorf(
				"%w: %q must be a plugin-type-scoped Plugin entity URI (`<pluginType>/<name>`, optionally versioned, as in `parser/yaml` or `parser/yaml:1.0.0`); "+
					"bare-`.wasm` and artifact references are no longer accepted",
				ErrPluginEntityNotFound(), entityURI)
		}
		resolved, err := cascade(artifactURI)
		if err != nil {
			return ResolvedPlugin{}, err
		}
		resolved.Contract = claim
		resolved.Limits = limits
		resolved.Commands = commands
		return resolved, nil
	}
}
