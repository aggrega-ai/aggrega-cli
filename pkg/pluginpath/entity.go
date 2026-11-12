// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"fmt"
	"strings"

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
// reference names no Plugin entity, so the build halts here naming the offending
// reference. So does a well-formed reference that no catalog directory, the
// binary or the content cache holds, and a pull refuses that reference with this
// same condition before it writes a lockfile, so a pull and the build it feeds
// refuse one reference alike.
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
// does not depend on the Plugin entity's shape; resolving the entity once here yields the artifact
// reference, the claim, the limits, and the command mirror together, so the load
// path never re-resolves the entity. cascade is the artifact-resolving Lookup (a
// build-configured Resolver's Resolve method); it keys its pins and verification
// by the ARTIFACT reference, so translating before the cascade leaves the pin
// model unchanged. The claim is attached to the cascade's ResolvedPlugin so the
// load gate can cross-check it against the plugin's describe(); the limits are
// attached so the loader caps the plugin's memory and per-call execution; the
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
// A reference that does not match a Plugin entity halts with the refusal
// UnresolvedEntity composes for it — ErrPluginEntityNotFound, naming the
// offending reference, or, on the entity-only surface, the malformation of a
// version pin that names no version. A nil entityToArtifact disables the entity
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
			return ResolvedPlugin{}, entityNotFound(entityURI, policy)
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

// UnresolvedEntity returns the refusal for a plugin reference that no Plugin
// entity answers, on a surface governed by policy. It is the one composition of
// that refusal: the front EntityLookup returns raises it when its entity lookup
// finds nothing, and a caller asking the same entity lookup ahead of a build — a
// pull resolving the Plugin a pipeline step names — raises it too, so a pull and
// the build it feeds report one reference with one condition and one sentence.
//
// The reference's form chooses what is said about it. A form the surface refuses
// is reported as AcceptReferenceForm reports it, and on the entity-only surface a
// version pin that names no version is reported as that malformation. A
// well-formed `<pluginType>/<name>` reference, optionally versioned, names a
// Plugin entity that nothing holds, so it is reported as one, with the two ways to
// supply the entity. Any other reference is reported as one that must be written
// in that form.
func UnresolvedEntity(entityURI string, policy SurfacePolicy) error {
	if err := AcceptReferenceForm(entityURI, policy); err != nil {
		return err
	}
	return entityNotFound(entityURI, policy)
}

// entityNotFound is UnresolvedEntity past the form gate, for a caller that has
// already applied it.
//
// A permissive surface's reference is not judged for a malformed pin, because it
// may be an artifact reference whose last colon ends a digest, and its callers
// fall back on the not-found condition to resolve exactly those.
func entityNotFound(entityURI string, policy SurfacePolicy) error {
	if policy == SurfaceEntityOnly {
		if pe, malformed := types.ParseVersionPin(entityURI).Malformed(); malformed {
			return pe
		}
	}
	if scopedEntityURI(entityURI) {
		return fmt.Errorf(
			"%w: %q is a Plugin entity URI, but no catalog directory holds the entity it names, the binary does not carry it, "+
				"and no pull has fetched it into the content cache; place the Plugin entity in a catalog directory, "+
				"or publish it to a registry and run `aggrega pull`",
			ErrPluginEntityNotFound(), entityURI)
	}
	return fmt.Errorf(
		"%w: %q must be a plugin-type-scoped Plugin entity URI (`<pluginType>/<name>`, optionally versioned, as in `parser/yaml` or `parser/yaml:1.0.0`); "+
			"a bare `.wasm` reference or an artifact reference names no Plugin entity",
		ErrPluginEntityNotFound(), entityURI)
}

// scopedEntityURI reports whether a reference is written in the form a Plugin
// entity is named by: the entity form ClassifyReference reports, a base of
// exactly two non-empty segments — the plugin type and the name — and, where it
// carries a version pin, one that parses. It judges the text alone; whether
// anything holds the entity is the lookup's question.
func scopedEntityURI(uri string) bool {
	if ClassifyReference(uri) != FormEntityURI {
		return false
	}
	pin := types.ParseVersionPin(uri)
	if _, malformed := pin.Malformed(); malformed {
		return false
	}
	pluginType, name, ok := strings.Cut(pin.Base(), "/")
	return ok && pluginType != "" && name != "" && !strings.Contains(name, "/")
}
