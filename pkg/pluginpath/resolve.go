// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginpath is the single home for resolving a Wasm-plugin URI to the
// .wasm bytes the loader compiles. Every socket that loads a plugin — the
// publishing-linter and the render pipeline's per-step plugins — resolves
// through one cascade, so the resolution logic exists exactly once. Only the
// URI SOURCE differs per socket (workspace config for the linter; a recipe's
// spec.pipeline[].plugin for pipeline steps); that per-socket field-reading
// stays at each caller and feeds this resolver.
//
// Resolution is a strict tier cascade, highest precedence first:
//
//	0a embed      — bytes compiled into the CLI binary (core, version-locked)
//	0b colocated  — a .wasm laid down beside its Plugin entity in a catalog
//	                directory (the offline path when nothing is embedded)
//	1  local      — an explicit ./ or ../ filesystem path (or an absolute path)
//	2  cache      — the workspace content cache (a previously-pulled artifact)
//
// Every tier reads bytes already on the machine; the cascade fetches nothing. An
// artifact a registry serves reaches it only through the cache a pull filled, and a
// reference no tier answers halts with ErrPluginArtifactNotFound.
//
// Every tier yields the resolved bytes plus a source-tier tag. When the caller
// supplies a digest pin for the URI (the build path, reading the lockfile), the
// resolved bytes are verified against that pin at EVERY tier — the pinned
// digest, not the source tier, governs determinism: a co-located artifact whose
// bytes match the pin resolves byte-identically to the registry artifact it
// stands in for, and a mismatch halts. When no pin is supplied (the publish-time
// linter, which lints a not-yet-pinned proposed entity) verification is skipped.
//
// Trust at build is offline and free of any cryptographic re-verification: the
// .wasm content-digest pin governs the bytes, and for a guest that carried a
// verified signature fact when it was pulled, the cached signature bundle is
// re-hashed against its recorded digest — a byte/hash integrity check over the
// cached blob. Signature verification itself — the network-touching, trust-root
// operation — is a pull/lock-time step recorded as a lockfile fact and is never
// re-run at build; this package performs no signature verification.
package pluginpath

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Tier names the source a plugin's bytes came from. It is a provenance tag: the
// loaded bytes are identical regardless of source when the digest pin matches, so
// the cascade never branches on it. Where a tag reaches a user it is rendered
// through Source, never printed raw, because these spellings are this resolver's
// internal vocabulary rather than words the reference teaches; where a lock-time
// resolution records it, it is mapped onto the closure's own provenance tag.
type Tier string

const (
	TierEmbed     Tier = "embed"     // 0a
	TierColocated Tier = "colocated" // 0b
	TierLocal     Tier = "local"     // 1
	TierCache     Tier = "cache"     // 2
)

// resolutionCascade lists the sources the cascade consults, in precedence order.
// The diagnostic that reports exhaustion renders this slice rather than repeating
// the names, so the message cannot claim a cascade the resolver does not walk.
var resolutionCascade = []Tier{TierEmbed, TierColocated, TierLocal, TierCache}

// cascadeNames renders the consulted sources for the exhaustion diagnostic.
func cascadeNames() string {
	names := make([]string, len(resolutionCascade))
	for i, t := range resolutionCascade {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

// Source maps this resolver's source tag to the word user-facing output prints
// for it, drawn from the shared provenance vocabulary so that a plugin's
// resolution and a pulled entity's resolution name the same source identically.
//
// The word is read through the provenance tag the tier corresponds to rather than
// chosen here, so the two cannot name one source differently.
func (t Tier) Source() closure.ResolutionSource {
	source, _ := closure.ResolutionSourceFor(t.resolutionPath())
	return source
}

// resolutionPath maps this resolver's source tag onto the provenance tag a closure
// resolution records for bytes found the same way: the binary's copy, a local file,
// or the content cache. It is the one correspondence between the two, read both by
// Source and by ResolveCatalogArtifact, whose resolution a pull records.
//
// The mapping is deliberately many-to-one: a .wasm laid down beside its entity and
// one named by an explicit path are distinct lookups here, but both are bytes found
// on the local filesystem, which is what the catalog tag records. A tier with no
// correspondence maps to the empty tag, which names no source.
func (t Tier) resolutionPath() string {
	switch t {
	case TierEmbed:
		return closure.ResolutionPathEmbedded
	case TierColocated, TierLocal:
		return closure.ResolutionPathCatalog
	case TierCache:
		return closure.ResolutionPathCache
	}
	return ""
}

// ResolvedPlugin is the cascade's output: the plugin's .wasm bytes, the tier
// they were resolved from, and the URI they were resolved for. Bytes feeds the
// loader's PluginSpec.Bytes directly, so a tier with no stable filesystem
// location (embed) needs no path.
type ResolvedPlugin struct {
	Bytes []byte
	Tier  Tier
	URI   string

	// Version is the lockfile-resolved version the URI was pinned to at
	// resolution (the same value that addresses the versioned cache slot), or
	// empty when no pin supplied one — the publish-time linter path, the bare
	// cascade. It rides alongside URI as the resolved plugin's identity so a
	// caller can record which artifact a set of guest bytes came from. It is a
	// plain scalar, so this package stays free of any internal/wasm dependency.
	Version string

	// Contract is the entity's authored capability claim, attached by the
	// entity-fronted Lookup (EntityLookup) from the in-hand Plugin entity.
	// It is neutral — a pluginpath-owned shape, not the loader's — so this
	// package stays free of any internal/wasm dependency; the loader's caller
	// maps it onto the load spec. nil when the resolution is not entity-fronted
	// (the bare cascade, the linter's --plugin override) so the cross-check is
	// skipped.
	Contract *PluginContractClaim

	// Limits is the entity's authored runtime resource limits (memory and
	// per-call wall time), attached by the entity-fronted Lookup (EntityLookup)
	// from the in-hand Plugin entity. Like Contract it is neutral — a
	// pluginpath-owned shape carrying no internal/wasm dependency; the loader's
	// caller maps it onto the load spec. Unlike Contract the limits are not
	// cross-checked against the plugin (a plugin does not self-report limits),
	// so they ride a sibling field rather than the capability claim. nil when
	// the resolution is not entity-fronted; the loader then applies the
	// platform default cap, so a load is never unbounded.
	Limits *PluginRuntimeLimits

	// Commands is the entity's machine-owned CLI-command mirror, attached by the
	// entity-fronted Lookup (EntityLookup) from the in-hand Plugin entity. It is
	// neutral — a pluginpath-owned shape carrying no internal/wasm dependency; the
	// loader's caller maps it onto the load spec, where a non-nil value drives the
	// load gate's full-equality command check. It is a pointer so a resolution can
	// distinguish an entity that supplied its mirror — even an explicit empty one —
	// from one that carries no command record: nil means the resolution is not
	// entity-fronted or the entity records no commands field, and the check is
	// skipped; a non-nil value (possibly empty) means the entity supplied the
	// mirror and the check runs.
	Commands *[]PluginCommandMirror
}

// PluginCommandMirror is one entry of an entity's machine-owned CLI-command
// mirror in the neutral, pluginpath-owned shape: the command name, its one-line
// short help, and its declared flags in order. It carries no dependency on
// internal/wasm; the loader's caller maps it onto the wasm-side command shape.
type PluginCommandMirror struct {
	Name        string
	Description string
	Flags       []PluginCommandFlagMirror
}

// PluginCommandFlagMirror is one flag of a command's machine-owned flag mirror
// in the neutral, pluginpath-owned shape: the flag's long name and its optional
// short alias, help text, value type, and default. It carries no dependency on
// internal/wasm; the loader's caller maps it onto the wasm-side flag shape.
type PluginCommandFlagMirror struct {
	Name        string
	Shorthand   string
	Description string
	Type        string
	Default     string
}

// PluginRuntimeLimits is the neutral, pluginpath-owned shape of an entity's
// authored runtime resource limits: the linear-memory cap (in Wasm pages) and
// the per-call wall-time cap (in milliseconds). It carries no dependency on
// internal/wasm; the loader's caller maps it onto the load spec. A zero field
// means the entity declared no limit for that dimension — the loader substitutes
// the platform default cap, so a zero is "apply default", never "unbounded".
type PluginRuntimeLimits struct {
	// MaxMemoryPages caps the plugin's linear memory in Wasm pages (64 KiB per
	// page). Zero applies the platform default cap.
	MaxMemoryPages uint32

	// TimeoutMs caps per-call execution wall time in milliseconds. Zero applies
	// the platform default cap.
	TimeoutMs uint64
}

// PluginContractClaim is the neutral, pluginpath-owned shape of an entity's
// authored contract declaration: the per-export name/operates/scope triples
// the load gate cross-checks against the plugin's self-report. It carries no
// dependency on internal/wasm; the loader's caller maps it onto the wasm-side
// claim shape.
//
// The host-function surface is not part of the authored claim: it is
// machine-owned, derived from the .wasm import section at publish and stamped
// into the entity, so there is no authored list to cross-check at load. The
// loader registers exactly the derived set.
type PluginContractClaim struct {
	// Exports is the authored per-export capability claim, in author order.
	Exports []PluginExportClaim

	// Contract is the authored provider render-behavior contract in the neutral
	// shape, or nil when the entity authors none. The loader's caller maps it
	// onto the wasm-side claim shape; the load gate cross-checks it against the
	// plugin's describe() self-report.
	Contract *ProviderContractClaim
}

// ProviderContractClaim is the neutral, pluginpath-owned shape of an entity's
// authored provider render-behavior contract: the input kinds the plugin renders,
// how it assembles a dispatch group's per-node output and which directories that
// output sits under, and the artifact-template delimiter pair. It carries no
// dependency on internal/wasm; the loader's caller maps it onto the wasm-side
// claim shape.
type ProviderContractClaim struct {
	// InputKinds is the authored input-kind claim.
	InputKinds PluginInputKindsClaim
	// BoundaryAssembly is the authored boundary-assembly mode ("none", "merge",
	// "nest", or empty).
	BoundaryAssembly string
	// ArtifactDelimiters is the authored artifact-template delimiter pair, or
	// nil when the plugin uses the default pair.
	ArtifactDelimiters *PluginArtifactDelimitersClaim
	// AssemblyNameAnnotation is the authored assembly-name annotation, or nil
	// when the plugin names its assembly from the dispatch path alone.
	AssemblyNameAnnotation *PluginAssemblyNameAnnotationClaim
	// ReusableInstance is the authored reuse claim: whether one loaded plugin
	// instance may serve every crossing of a build rather than a fresh instance
	// per crossing. False when the plugin holds state across crossings.
	ReusableInstance bool
	// CrossBoundaryLinking is the authored cross-boundary-linking claim: whether
	// the plugin wires a uses edge whose ends assemble into different dispatch
	// groups through its paired link_publish and link_resolve exports. False when
	// the plugin wires no cross-boundary edges.
	CrossBoundaryLinking bool
	// NestSegment is the authored directory each contributor's own sub-directory
	// sits under, for a plugin whose boundary assembly nests. Empty when the
	// plugin names none.
	NestSegment string
}

// PluginAssemblyNameAnnotationClaim is an entity's authored assembly-name
// annotation in the neutral shape: the boundary annotation whose resolved value
// names the artifact the plugin assembles from a dispatch group, and whether
// every contributing boundary must agree on that value.
type PluginAssemblyNameAnnotationClaim struct {
	Annotation    string
	RequireUnique bool
}

// PluginInputKindsClaim is one entity's authored input-kind claim: which of the
// Recipe input kinds the plugin renders.
//
// It is an ALIAS rather than a distinct struct, and deliberately so. "Which input
// kinds does this provider render" is one fact about one provider, and
// [types.PluginInputKinds] is its one home; an alias carries the same type
// identity, so an entity claim crossing into this package needs no field copy and
// cannot silently lose a field on the way. The name is kept because it is part of
// this package's published surface.
//
// Adding a kind means adding a field to the home type. Do not declare a second
// three-boolean struct here or anywhere else: nothing makes that fail to compile,
// so the only thing standing between a new shape and a silently dropped
// capability is the field-set guard in internal/wasm and this comment.
type PluginInputKindsClaim = types.PluginInputKinds

// Compiles only while PluginInputKindsClaim really is an alias. Two DISTINCT named
// struct types are never assignable to one another in Go however identical their
// fields, so re-expanding the alias into a struct of its own breaks the build here
// — which makes this the one rung on this path that a reader cannot walk past.
var _ PluginInputKindsClaim = types.PluginInputKinds{}

// PluginArtifactDelimitersClaim is an entity's authored artifact-template
// delimiter pair in the neutral shape.
type PluginArtifactDelimitersClaim struct {
	Left  string
	Right string
}

// PluginExportClaim is one export's authored capability in the neutral shape:
// the export name and the operates/scope fields cross-checked against the
// plugin's describe(). The selector is intentionally absent — it is free-form
// documentation, not a structurally cross-checkable claim.
type PluginExportClaim struct {
	Name     string
	Operates string
	Scope    string
}

// ErrPluginBundleDigestMismatch reports that a signed guest's cached signature
// bundle is missing, unreadable, or its bytes hash to a value other than the
// digest the lockfile recorded for it. Before a guest that carried a verified
// signature fact loads, the build re-hashes its cached signature-bundle blob and
// compares the hash to the recorded digest; a drifted or absent blob halts the
// load with this sentinel. The check is a byte/hash comparison — never a
// cryptographic signature re-verification, and never a network or trust-root
// touch — so it holds under the hermetic, air-gapped build.
func ErrPluginBundleDigestMismatch() error { return errPluginBundleDigestMismatch }

var errPluginBundleDigestMismatch = cerrors.Register("plugin-bundle-digest-mismatch", cerrors.Meta{
	ShortCode: "E0144",
	Message:   "cached signature bundle does not match its recorded digest",
	RulePlace: cerrors.NotARule("A signed guest's cached signature bundle is missing, unreadable, or hashes to something " +
		"other than the digest the lockfile recorded. The blob is read and re-hashed when the " +
		"comparison runs, so what it reports is an integrity comparison that did not hold."),
})

// ErrPluginArtifactNotFound reports that a plugin's .wasm artifact resolved through
// none of the sources the cascade consults: it is not compiled into the binary, not
// beside its Plugin entity in a catalog directory, not at an explicit path, and not
// in the workspace content cache. Every one of those is a read of bytes already on
// the machine, so the condition says only that the bytes are absent; it states no
// fetch, because the cascade performs none.
//
// It is raised in two places, and both report the same absence. A resolution that
// exhausts the cascade raises it wherever a plugin is loaded. A pull raises it
// earlier, before it writes anything, for a Plugin entity authored in a catalog
// directory whose artifact no tier holds (see ResolveCatalogArtifact): such an
// entity's artifact is read only from the machine, so no later pull can supply it,
// and the pull is the step that can name the file to place.
func ErrPluginArtifactNotFound() error { return errPluginArtifactNotFound }

var errPluginArtifactNotFound = cerrors.Register("plugin-artifact-not-found", cerrors.Meta{
	ShortCode: "E0306",
	Message:   "plugin artifact not found on this machine",
	RulePlace: cerrors.RuleNoStageReaches("Plugin resolution evaluates the rule, when a socket loads a plugin and when a pull " +
		"resolves a Plugin entity authored in a catalog directory: the artifact the entity names has to be " +
		"readable from the binary, beside the entity, at an explicit path, or from the content cache. Both " +
		"moments come ahead of every declared stage, so no stage holds the reference or the sources it is " +
		"read from."),
})

// Lookup resolves a plugin URI to its resolved bytes. It is the seam each
// socket binds: the render pipeline binds a build-configured Resolver's Resolve
// method; the linter binds a no-pins Resolver carrying its --plugin override.
//
// The seam carries no cancellation parameter, so a resolution reached through it
// cannot be ended by the caller that started it. That bound costs nothing: every
// tier reads bytes already on the machine — from the binary, from a catalog
// directory, from an explicit path, from the content cache — and none of them is
// an operation there is anything to stop.
type Lookup func(uri string) (ResolvedPlugin, error)

// Resolver carries the per-invocation resolution context the cascade consults.
// It is obtained only from NewResolver — its fields are unexported, so a caller
// in another package cannot hand-populate an under-wired copy; the sole
// construction path threads the tier sources and pin posture in one place. A
// Resolver built from a zero ResolutionContext under PinPostureNone resolves
// only the embed and local tiers (no colocated dirs, no cache, no pins) — the
// minimum a caller with no workspace context can do.
type Resolver struct {
	// colocatedDirs maps a plugin's artifact reference to the on-disk directory
	// holding its co-located .wasm — the module laid down beside its Plugin
	// entity in a catalog directory. It is the offline resolution path (Tier 0b)
	// for a build that embeds no core guests: the .wasm is read from the catalog
	// directory the entity was indexed from, under the filename PluginLeaf
	// derives from the reference. Key is the artifact reference the
	// cascade resolves by, so the front's entity → artifact translation leaves
	// this tier's lookup unchanged. nil or empty disables the tier; a reference
	// absent from the map, or a missing .wasm file, is a clean miss that falls
	// through to the next tier. The tier sits after embed, so an embedded guest
	// always wins and this never shadows a built-in.
	colocatedDirs map[string]string

	// workspaceRoot and cacheDir address the workspace content cache (Tier 2).
	// Both empty disables the cache tier.
	workspaceRoot string
	cacheDir      string

	// pins maps a plugin URI to its lockfile digest pin (a "sha256:..."
	// string). When a URI has a pin, the bytes resolved at EVERY tier are
	// verified against it and a mismatch halts. nil or a URI absent from the
	// map disables verification for that URI — the publish-time linter path,
	// where nothing is pinned yet.
	pins map[string]string

	// pinVersions maps a plugin URI to its resolved version, used to address the
	// versioned cache slot (Tier 2). Absent leaves the version empty, addressing
	// the unversioned slot. The value is whatever the caller recorded as that
	// URI's resolved version, and the lockfile is only one of the records it can
	// come from: a record addressing a plugin by content digest supplies that
	// digest, which for a .wasm artifact names the same content the version does.
	pinVersions map[string]string

	// bundleDigests maps a plugin URI to the recorded digest of its cached
	// signature-bundle blob (a "sha256:..." string), present only for a guest
	// that carried a verified signature fact when it was pulled. When a URI has
	// an entry, the cached bundle blob under cacheDir is re-hashed and compared
	// to it before the resolved bytes are returned — an offline byte/hash
	// integrity check over the cached blob, never a signature re-verification and
	// never a network touch. nil or a URI absent from the map disables the check
	// for that URI: an unsigned guest (no signature fact was recorded), or the
	// publish-time linter path where nothing is pinned yet.
	bundleDigests map[string]string

	// override is an explicit filesystem path that wins over the cascade
	// entirely (the linter's --plugin flag). Returned as local-tier bytes.
	override string

	// expectsRecordedIdentity reports whether this construction consumes plugins a
	// recorded digest is meant to govern, so a resolution that finds none for a
	// reference is worth telling the reader about. It is set from the construction's
	// declared pin posture and read only by the advisory — never by the cascade,
	// which still treats a populated or empty pin map identically whatever the
	// posture was. False for the pin-free base, whose two callers resolve something
	// no record was ever going to stand at.
	expectsRecordedIdentity bool
}

// ociReferenceHint is woven into the exhaustion error for a reference shaped like
// a registry location, so the message points at the fix — pull first, or point a
// local path at the artifact — rather than reading as an unexplained miss.
const ociReferenceHint = "an OCI plugin URI requires a prior `aggrega pull --release <release>` (so its bytes are cached) " +
	"or an explicit local path; plugin resolution does not fetch from a registry"

// unsetMsg is the single-sourced diagnostic for the no-URI / no-override case.
// The substring "no plugin path configured" is load-bearing: callers and tests
// match on it.
const unsetMsg = "no plugin path configured. Either:\n" +
	"  - pass an explicit filesystem path\n" +
	"  - or set the plugin URI in .aggrega-config.yaml to a workspace-relative path"

// Resolve runs the tier cascade for uri and returns the resolved bytes. It
// satisfies the Lookup type when bound as a method value (r.Resolve).
//
// Precedence is embed (0a) → colocated (0b) → local (1) → cache (2); the first
// tier that produces bytes wins, and a reference no tier answers halts with a
// wrapped ErrPluginArtifactNotFound naming the tiers consulted. An explicit
// Override short-circuits the cascade. When the URI carries a digest pin, the
// winning tier's bytes are verified against it; a mismatch halts with a wrapped
// oci.ErrFetchIntegrity.
//
// This form takes no cancellation parameter, so a resolution it starts runs to
// its own completion. It is the form the Lookup seam binds, and it is exactly
// right for that seam: every tier reads bytes already on the machine.
func (r Resolver) Resolve(uri string) (ResolvedPlugin, error) {
	return r.ResolveContext(context.Background(), uri)
}

// ResolveContext runs the tier cascade for uri under the caller's context and
// returns the resolved bytes. It is the cascade's whole implementation; Resolve
// is the seam-facing form that delegates here.
//
// Precedence, the Override short-circuit and the digest-pin verification are
// exactly as Resolve describes them — ctx changes which work is started, never
// which bytes win.
//
// Every tier reads bytes already on the machine and completes without consulting
// ctx. A caller that has already withdrawn is answered here, before any tier is
// consulted, so a resolution that was never going to be used does not read a file
// first.
func (r Resolver) ResolveContext(ctx context.Context, uri string) (ResolvedPlugin, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedPlugin{}, fmt.Errorf("resolve plugin %q: %w", uri, err)
	}
	if r.override != "" {
		abs := r.override
		if !filepath.IsAbs(abs) {
			a, err := filepath.Abs(abs)
			if err != nil {
				return ResolvedPlugin{}, fmt.Errorf("resolve plugin override %q: %w", r.override, err)
			}
			abs = a
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return ResolvedPlugin{}, fmt.Errorf("read plugin override %q: %w", abs, err)
		}
		return r.verified(uri, b, TierLocal)
	}

	if uri == "" {
		return ResolvedPlugin{}, errors.New(unsetMsg)
	}

	// 0a embed: bytes compiled into the CLI binary; an embedded guest always
	// wins.
	if embedBytes, isEmbedded := embedded.Lookup(uri); isEmbedded {
		return r.verified(uri, embedBytes, TierEmbed)
	}

	// 0b colocated: a .wasm beside its Plugin entity in a catalog directory —
	// the offline path when the binary embeds no core guests. It sits after the
	// embed consume so an embedded guest always wins.
	if len(r.colocatedDirs) > 0 {
		b, ok, err := r.readColocated(uri)
		if err != nil {
			return ResolvedPlugin{}, err
		}
		if ok {
			return r.verified(uri, b, TierColocated)
		}
	}

	// 1 local: an explicit ./ ../ or absolute filesystem path.
	if isLocalPathRef(uri) {
		abs := uri
		if !filepath.IsAbs(abs) {
			a, err := filepath.Abs(abs)
			if err != nil {
				return ResolvedPlugin{}, fmt.Errorf("resolve local plugin %q: %w", uri, err)
			}
			abs = a
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return ResolvedPlugin{}, fmt.Errorf("read local plugin %q: %w", abs, err)
		}
		return r.verified(uri, b, TierLocal)
	}

	// 2 cache: a previously-pulled artifact in the workspace content cache.
	if r.workspaceRoot != "" || r.cacheDir != "" {
		version := r.pinVersions[uri]
		b, err := oci.ReadCached(r.workspaceRoot, r.cacheDir, uri, version)
		switch {
		case err == nil:
			return r.verified(uri, b, TierCache)
		case errors.Is(err, oci.ErrFetchNotFound()):
			// A clean cache miss: no tier holds the bytes.
		default:
			return ResolvedPlugin{}, err
		}
	}

	if workspaceconfig.LooksLikeOCIURI(uri) {
		return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable: %s: %w", uri, ociReferenceHint, ErrPluginArtifactNotFound())
	}
	return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable through any tier (%s): %w", uri, cascadeNames(), ErrPluginArtifactNotFound())
}

// ResolveCatalogArtifact resolves, through the cascade, the artifact a Plugin entity
// authored in a catalog directory names, and refuses when no tier holds it.
//
// It is the lock-time question about such an entity, answered by the same cascade a
// build loads the plugin through rather than by a second reading of the catalog
// directory: an artifact the binary carries, one beside the entity, one at an
// explicit path and one already in the content cache all satisfy it, exactly as
// they satisfy the build. Every one of those tiers reads bytes already on the
// machine, so the artifact of an entity authored in a catalog directory is never
// fetched from a registry; refusing at the pull names the fix at the step that can
// still take it.
//
// When the artifact resolves, the result describes the bytes read: their content
// digest, as both the digest and the version, the way a pinned plugin artifact
// records its content; the source they were read from; and no manifest digest,
// because they came through no registry manifest. A pull pins that description for
// an attachment whose bytes shape the rendered output, and a build reconstructing
// the lockfile asks the same question and compares what it gets.
//
// When no tier holds the artifact, it returns an occurrence of
// ErrPluginArtifactNotFound naming the entity, the file the co-located tier reads
// and the directory it reads it in, with the cascade's own exhaustion as the cause.
// Any other resolution failure — an unreadable file beside the entity, bytes that do
// not match a recorded pin — is returned as the cascade raised it.
func (r Resolver) ResolveCatalogArtifact(entityURI, artifactRef string) (closure.Resolution, error) {
	resolved, err := r.Resolve(artifactRef)
	if err == nil {
		content := digest.ContentDigest(resolved.Bytes)
		return closure.Resolution{
			ResolvedVersion: content,
			Digest:          content,
			ResolutionPath:  resolved.Tier.resolutionPath(),
		}, nil
	}
	if !errors.Is(err, ErrPluginArtifactNotFound()) {
		return closure.Resolution{}, err
	}
	leaf := pluginLeaf(artifactRef)
	dir := r.colocatedDirs[artifactRef]
	values := []string{fmt.Sprintf("Plugin entity %q is authored in a catalog directory", entityURI)}
	where := cerrors.SourceLocation{File: entityURI}
	advice := "Place the built .wasm beside the entity under that name, or publish the plugin and reference the " +
		"published Plugin entity instead of the one in the catalog directory"
	if dir != "" {
		values = append(values, fmt.Sprintf("expected %s in %s", leaf, dir))
		where = cerrors.SourceLocation{File: dir}
	} else {
		values = append(values, fmt.Sprintf("expected %s beside the entity", leaf))
	}
	return closure.Resolution{}, cerrors.Wrap(err, cerrors.CatalogError, where, cerrors.ComposeMessage(ErrPluginArtifactNotFound(), values, advice))
}

// verified checks bytes against the URI's digest pin when one is present, then
// applies the offline signature-bundle integrity gate when the URI carries a
// recorded bundle digest, and returns the ResolvedPlugin. A content-pin mismatch
// halts with a wrapped oci.ErrFetchIntegrity naming the URI, the tier, the
// computed digest, and the pin. A cached-bundle mismatch halts with a wrapped
// ErrPluginBundleDigestMismatch. With no pin and no bundle digest (the linter
// path) the bytes pass through unchecked.
//
// This is the single site that decides whether a plugin's bytes are held to a
// recorded identity, and it is therefore where the rule lives: honour a recorded
// digest when one is reachable, and where none is, resolve anyway and say so. The
// rule sits here rather than at each surface that runs a plugin because a plugin
// can be loaded before the CLI knows which command it was asked for — the
// configured extension commands are registered while the command tree is being
// built, and registering one describes its guest on a cold describe cache. A rule
// written per command cannot see that case at all; a rule at the moment bytes are
// resolved sees every case, because every guest the compiler runs gets its bytes
// from here.
func (r Resolver) verified(uri string, b []byte, tier Tier) (ResolvedPlugin, error) {
	if pin, ok := r.pins[uri]; ok && pin != "" {
		if got := digest.ContentDigest(b); got != pin {
			return ResolvedPlugin{}, fmt.Errorf(
				"plugin %q resolved from %s: content digest %s does not match lockfile pin %s: %w",
				uri, tier.Source(), got, pin, oci.ErrFetchIntegrity())
		}
	} else {
		r.noteUnrecordedIdentity(uri, tier)
	}
	if bundleDigest, ok := r.bundleDigests[uri]; ok && bundleDigest != "" {
		if err := r.verifyCachedBundle(uri, tier, bundleDigest); err != nil {
			return ResolvedPlugin{}, err
		}
	}
	return ResolvedPlugin{Bytes: b, Tier: tier, URI: uri, Version: r.pinVersions[uri]}, nil
}

// unrecordedIdentityWriter receives the advisory raised when a plugin resolves
// with no recorded digest to check it against. It defaults to standard error —
// the stream every other compile-time advisory is written to, and the one a
// daemon redirects at the process boundary — and is redirected by tests to
// observe the advisory.
var unrecordedIdentityWriter io.Writer = os.Stderr

// advisedUnrecorded remembers the plugin references already advised on in this
// process, so the advisory is raised once per reference rather than once per
// resolution. A render resolves the same provider for every node it renders, and
// a long-running daemon resolves the same attachment on every request; the
// condition is a property of the workspace rather than of any one resolution, so
// repeating it per resolution would bury it.
var advisedUnrecorded sync.Map

// noteUnrecordedIdentity raises the advisory for a plugin whose resolved bytes no
// recorded digest governs, naming the reference, where the bytes came from, and
// what holds for that source. It never fails a resolution: a workspace that has
// not been prepared yet is an ordinary state, and refusing to read a plugin for
// want of a record would make every surface that only reports on a document
// unusable until an operator prepared one.
//
// What the advisory says after its first clause depends on the source, because
// the remedy is true of one source and false of another. Bytes read from the
// content cache are an acquisition a pull records, so the advisory names the pull
// that records them. Bytes read from beside a Plugin entity in a catalog directory
// are recorded by a pull only for a plugin attached at a transform role, whose
// bytes a pull pins because they shape the rendered output; for every other plugin
// authored there no pull records them, and telling the reader to run one would send
// them to a command that changes nothing. This resolver cannot tell which role a
// reference is attached at, so for those bytes the advisory says what holds either
// way — they are read from the catalog directory on every build, and a pull records
// them only for a transform role — and names the one thing that brings any other
// plugin under a recorded digest, publishing it and referencing the published
// entity.
//
// Two resolutions raise nothing, and each is a case where no record could exist
// rather than one where a record is missing:
//
//   - Bytes that ship inside the compiler binary. Nothing the compiler records
//     ever stands at a built-in's identity, because a resolution records what it
//     acquired and there is nothing to acquire for bytes that are already here.
//     The binary is those bytes' identity, so there is nothing an operator could
//     run to improve it.
//   - A resolution whose construction declares that no recorded digest
//     participates (see PinPosture). Two constructions declare that deliberately
//     and record why at the call: the explicit-artifact escape hatch, which points
//     at a file rather than consuming a recorded one, and the surface that
//     inspects the very artifact being checked, for which a record of the
//     identities trusted for the checkers holds nothing addressed to it.
//
// The bound is worth stating rather than leaving to be discovered: a construction
// that names the pin-free posture raises no advisory even where a digest could in
// principle have been recorded for what it resolved.
//
// The rule holds on a long-running process that serves an editor, which resolves
// plugins as a document is edited: it raises this advisory like any other caller,
// and that is deliberate. Three properties let an advisory sit there where a
// diagnostic could not, and all three belong to this function rather than to that
// surface. advisedUnrecorded holds it to one line per plugin reference for the
// life of the process, so it does not repeat at editing cadence. It is written to
// unrecordedIdentityWriter, which is standard error, and to nothing else, so it
// cannot enter the stream an editor reads its diagnostics from. And it returns
// nothing and cannot fail, so a resolution — and the verdict any surface reaches
// on it — is identical whether or not the advisory was raised. A diagnostic has
// none of the three: it would re-render on every edit and would claim a place in
// the editor's own report on the buffer.
//
// Silencing it for that one surface is therefore the wrong repair, and the reason
// is the one that puts the rule at this single site rather than at each caller.
// Honouring it where some plugins resolve and skipping it where others do leaves
// the skipped resolutions looking covered: silence would then mean two things at
// once — nothing went unrecorded, or this surface does not say — with no way for
// a reader to tell which, and that is worse than raising nothing anywhere.
func (r Resolver) noteUnrecordedIdentity(uri string, tier Tier) {
	if !r.expectsRecordedIdentity || tier == TierEmbed || uri == "" {
		return
	}
	if _, seen := advisedUnrecorded.LoadOrStore(uri, struct{}{}); seen {
		return
	}
	remedy := "run `aggrega pull --release <release>` to record it"
	if tier == TierColocated {
		remedy = "it is read from beside its Plugin entity in a catalog directory on every build, and a pull " +
			"records its digest only for a plugin attached at a transform role; otherwise publish the plugin and " +
			"reference the published entity to have its digest recorded"
	}
	fmt.Fprintf(unrecordedIdentityWriter,
		"warning: plugin %q resolved from %s with no recorded digest to check it against, "+
			"so it runs unverified; %s\n",
		uri, tier.Source(), remedy)
}

// verifyCachedBundle re-hashes the cached signature-bundle blob recorded for a
// signed guest and compares it to the recorded digest, before the guest loads.
// It is an offline byte/hash integrity gate over the cached blob — no signature
// re-verification, no network, no trust-root — so it holds under the hermetic
// build. A missing or unreadable blob, or a hash that differs from the recorded
// digest, halts with a wrapped ErrPluginBundleDigestMismatch naming the plugin.
// The blob location is spelled once in oci.SignatureBundlePath, so the slot
// checked here is the slot the pull path wrote; the re-hash uses the single
// content-address spelling in digest.ContentDigest, so an intact bundle matches.
func (r Resolver) verifyCachedBundle(uri string, tier Tier, bundleDigest string) error {
	path := oci.SignatureBundlePath(r.cacheDir, bundleDigest)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf(
			"plugin %q resolved from %s: cached signature bundle %s is unreadable: %w",
			uri, tier.Source(), path, ErrPluginBundleDigestMismatch())
	}
	if got := digest.ContentDigest(b); got != bundleDigest {
		return fmt.Errorf(
			"plugin %q resolved from %s: cached signature bundle %s has digest %s, recorded digest is %s: %w",
			uri, tier.Source(), path, got, bundleDigest, ErrPluginBundleDigestMismatch())
	}
	return nil
}

// readColocated reads the co-located artifact for uri — the .wasm beside its
// Plugin entity in the catalog directory keyed by the URI's artifact reference —
// returning (bytes, true, nil) on a hit, (nil, false, nil) on a clean miss (no
// mapped directory, or no such file), or a wrapped error on an unreadable
// present file. The filename is the shared PluginLeaf mapping, which carries the
// reference's socket and — where the reference pins one — its version, so two
// plugins sharing a bare name are read from two files rather than one.
func (r Resolver) readColocated(uri string) ([]byte, bool, error) {
	dir, ok := r.colocatedDirs[uri]
	if !ok || dir == "" {
		return nil, false, nil
	}
	path := filepath.Join(dir, pluginLeaf(uri))
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read colocated plugin %q at %s: %w", uri, path, err)
	}
	return b, true, nil
}
