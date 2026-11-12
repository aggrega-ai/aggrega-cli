// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
//	                directory (the offline path when nothing is embedded), read
//	                only from inside the root that directory is held to
//	1  local      — an explicit ./ or ../ filesystem path (or an absolute path)
//	2  cache      — the workspace content cache (a previously-pulled artifact),
//	                read only from inside the root the cache's writes are held to
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
// re-hashed against its recorded digest and held to having been made for the
// artifact the bytes were read as — a byte and hash comparison over cached files,
// the same decision a build makes over every recorded fact it reads before
// anything renders, where it also refuses a reference a signing requirement covers
// that records no signature at all. The recorded digest comes from the lockfile
// row for a pinned plugin, and from the record of plugin identities beside the
// lockfile for a plugin attached at a verdict role. Signature verification itself —
// the network-touching, trust-root operation — is a pull/lock-time step recorded as
// one of those facts and is never re-run at build; this package performs no
// signature verification.
package pluginpath

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
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

	// TierRegistry tags bytes a caller fetched from a registry itself and holds
	// to a digest before it writes them where the cache tier reads them. The
	// cascade never yields it: every tier the cascade reads is on the machine.
	TierRegistry Tier = "registry"
)

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
	case TierRegistry:
		return closure.ResolutionPathRegistry
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
	// per-call execution), attached by the entity-fronted Lookup (EntityLookup)
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
// the per-call execution cap (in milliseconds). It carries no dependency on
// internal/wasm; the loader's caller maps it onto the load spec. A zero field
// means the entity declared no limit for that dimension — the loader substitutes
// the platform default cap, so a zero is "apply default", never "unbounded".
type PluginRuntimeLimits struct {
	// MaxMemoryPages caps the plugin's linear memory in Wasm pages (64 KiB per
	// page). Zero applies the platform default cap.
	MaxMemoryPages uint32

	// TimeoutMs caps how long the plugin's own code may execute during one call,
	// in milliseconds. Zero applies the platform default cap.
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

	// Detect is the importer recognition signatures the entity records, or nil
	// where it records none — in which case the load compares nothing about them.
	// It is the entity's own type rather than a neutral copy, because the claim
	// carries it unchanged; the loader's caller maps it onto the wasm-side shape.
	Detect *[]types.PluginDetectSignature
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
	// Omits is the authored omit-entries claim: whether the plugin leaves out of
	// its output every rendered unit an omit entry matches and reports one count
	// per entry. False when the plugin makes no such claim.
	Omits bool
	// BoundaryArtifacts is the authored shared-files claim: whether the plugin's
	// target accepts the files a Recipe contributes to the shared output of the
	// boundary it renders in. False when the plugin makes no such claim.
	BoundaryArtifacts bool
	// NestSegment is the authored directory each contributor's own sub-directory
	// sits under, for a plugin whose boundary assembly nests. Empty when the
	// plugin names none.
	NestSegment string
	// SourceIgnore is the authored record of the ignore rules the plugin declares
	// for a source it renders, or nil where the entity records none — in which
	// case the load compares nothing about them. It is the entity's own type
	// rather than a neutral copy, because the claim carries it unchanged.
	SourceIgnore *types.PluginSourceIgnore
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

// ErrPluginArtifactNotFound reports that a plugin's .wasm artifact resolved through
// none of the sources the cascade consults: it is not compiled into the binary, not
// beside its Plugin entity in a catalog directory, not at an explicit path, and not
// in the workspace content cache. Every one of those is a read of bytes already on
// the machine, so the condition says only that the bytes are absent; it states no
// fetch, because the cascade performs none.
//
// It is raised in three places, and all three report the same absence. A
// resolution that exhausts the cascade raises it wherever a plugin is loaded. A
// pull raises it earlier, before it writes anything, for a Plugin entity authored
// in a catalog directory whose artifact no tier holds (see ResolveCatalogArtifact):
// such an entity's artifact is read only from the machine, so no later pull can
// supply it, and the pull is the step that can name the file to place. And an
// import raises it for a core importer of the compiler's release that no tier
// holds and no registry could supply on that run — the workspace offline, no
// registry answering, or the registry holding no such module — naming the
// importer, its version and what puts the module on the machine.
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
	// colocatedDirs maps a plugin's artifact reference to where its co-located
	// .wasm is read — the module laid down beside its Plugin entity in a catalog
	// directory — and the root that read is held to. It is the offline resolution
	// path (Tier 0b) for a build that embeds no core guests: the .wasm is read from
	// the catalog directory the entity was indexed from, under the filename
	// PluginLeaf derives from the reference. Key is the artifact reference the
	// cascade resolves by, so the front's entity → artifact translation leaves
	// this tier's lookup unchanged. nil or empty disables the tier; a reference
	// absent from the map, or a missing .wasm file, is a clean miss that falls
	// through to the next tier. The tier sits after embed, so an embedded guest
	// always wins and this never shadows a built-in.
	colocatedDirs map[string]ColocatedDir

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
	// an entry, the recorded fact is held through the one decision whether a
	// recorded fact is accepted (oci.HoldRecordedSignature) before the resolved
	// bytes are returned — an offline byte and hash comparison over cached files,
	// never a signature re-verification and never a network touch. nil or a URI
	// absent from the map disables the check for that URI: an unsigned guest (no
	// signature fact was recorded), or the publish-time linter path where nothing
	// is pinned yet.
	bundleDigests map[string]string

	// recordedIn is the file the pin source records those facts in, named in a
	// refusal of one; empty names no file.
	recordedIn string

	// pinsHeldTo names, in the refusal of bytes that do not match their pin, what
	// the pins are; empty names them lockfile pins, the record most pins come from.
	pinsHeldTo string

	// release is the Release that file belongs to, whose pull a refusal of one of
	// those facts names as its remedy; empty names a placeholder.
	release string

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

	// unrecorded receives the advisory a resolution raises for a reference no
	// recorded digest governs; nil raises it to nobody. See noteUnrecordedIdentity.
	unrecorded func(UnrecordedIdentity)
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
// wrapped ErrPluginArtifactNotFound composed by notOnMachine. An explicit
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

	// 2 cache: a previously-pulled artifact in the workspace content cache. A slot
	// the read hold refuses — leading through a link out of the root the cache is
	// held to — is not a miss: it is returned below, so no later answer stands in
	// for bytes the cascade may not read.
	if r.workspaceRoot != "" || r.cacheDir != "" {
		version := r.pinVersions[uri]
		b, err := oci.ReadCached(r.workspaceRoot, r.cacheDir, oci.PluginArtifactKind(), uri, version)
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
	return ResolvedPlugin{}, r.notOnMachine(uri)
}

// notOnMachine is the refusal of uri, a plugin no source the cascade reads holds:
// the plugin-artifact-not-found condition, naming the plugin and, where the
// catalog folder holding its Plugin entity says, the version that entity is at
// and the file its module is read from there. It is the one composition of that
// sentence, so every surface that loads a plugin through the cascade — an import,
// a lint, a build — prints the same one.
//
// It names the places a module is read from in the words the reference teaches
// rather than by the cascade's own labels for them. Where the plugin's entity sits
// in a catalog folder, the module is read from beside it, and that is the one
// place left to put it: the cascade never fetches the module of such an entity.
// Otherwise the sentence says which of the places a module is read from this
// resolution looked in, and that none of them holds it. It names only the places
// this Resolver was given — the binary always; the catalog folders and the content
// cache a pull fetches into where the construction supplied them — so a surface
// that consults fewer, such as a lint reading no content cache, does not claim to
// have looked where it did not.
func (r Resolver) notOnMachine(uri string) error {
	at := r.colocatedDirs[uri]
	if at.Dir == "" {
		absent := []string{"the binary does not carry its module"}
		if len(r.colocatedDirs) > 0 {
			absent = append(absent, "no catalog folder holds its Plugin entity")
		}
		if r.workspaceRoot != "" || r.cacheDir != "" {
			absent = append(absent, "the content cache a pull fetches into does not hold it")
		}
		return fmt.Errorf("plugin %q is not on this machine: %s: %w", uri, inWords(absent), ErrPluginArtifactNotFound())
	}
	subject := fmt.Sprintf("plugin %q", uri)
	if at.Version != "" {
		subject += " at version " + at.Version
	}
	return fmt.Errorf("%s is not on this machine; place its module, named %s, beside its Plugin entity in %s: %w",
		subject, pluginLeaf(uri), at.Dir, ErrPluginArtifactNotFound())
}

// inWords joins clauses as a sentence lists them: "a", "a and b", "a, b and c".
func inWords(clauses []string) string {
	switch len(clauses) {
	case 0:
		return ""
	case 1:
		return clauses[0]
	}
	out := clauses[0]
	for _, c := range clauses[1 : len(clauses)-1] {
		out += ", " + c
	}
	return out + " and " + clauses[len(clauses)-1]
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
	dir := r.colocatedDirs[artifactRef].Dir
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
// holds the URI's recorded signature fact when it carries a recorded bundle
// digest, and returns the ResolvedPlugin. A content-pin mismatch halts with a
// wrapped oci.ErrFetchIntegrity naming the URI, the tier, the computed digest, and
// the pin. A cached-bundle mismatch halts with a wrapped
// oci.ErrSignatureBundleDigestMismatch, and a bundle made for another artifact
// than the one read with a wrapped oci.ErrSignatureForAnotherArtifact. With no pin
// and no bundle digest (the linter path) the bytes pass through unchecked.
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
		heldTo := r.pinsHeldTo
		if heldTo == "" {
			heldTo = "lockfile pin"
		}
		if err := holdContent(uri, tier, b, pin, heldTo); err != nil {
			return ResolvedPlugin{}, err
		}
	} else {
		r.noteUnrecordedIdentity(uri, tier)
	}
	if bundleDigest, ok := r.bundleDigests[uri]; ok && bundleDigest != "" {
		if err := r.verifyCachedBundle(uri, tier, bundleDigest, b); err != nil {
			return ResolvedPlugin{}, err
		}
	}
	return ResolvedPlugin{Bytes: b, Tier: tier, URI: uri, Version: r.pinVersions[uri]}, nil
}

// HoldToDigest refuses resolved unless its bytes are the module want names: their
// content digest must equal want, a sha256:<hex> content digest. heldTo names, in
// the refusal, what want is — the record or the release it was taken from — so a
// reader knows which statement of the module the bytes disagreed with.
//
// It is for bytes no Resolver read: a module a caller fetched from a registry
// itself (TierRegistry), held before anything else is done with it and before it
// is written where the cache tier reads it. Bytes a Resolver reads are held by the
// Resolver's own pin check, which a caller knowing the one module it accepts sets
// up through AcceptedDigest. It shares the cascade's own comparison, so a module
// held here and one held to a pin are refused under one condition — a wrapped
// oci.ErrFetchIntegrity naming the reference, the source the bytes were read
// from, the digest they carry and the one they had to.
func HoldToDigest(resolved ResolvedPlugin, want, heldTo string) error {
	return holdContent(resolved.URI, resolved.Tier, resolved.Bytes, want, heldTo)
}

// holdContent is the one comparison of a plugin's resolved bytes against a
// digest they must match, read by the cascade's pin check (verified) and by
// HoldToDigest alike. heldTo names what want is in the refusal.
func holdContent(uri string, tier Tier, b []byte, want, heldTo string) error {
	if got := digest.ContentDigest(b); got != want {
		return fmt.Errorf("plugin %q resolved from %s: content digest %s does not match %s %s: %w",
			uri, tier.Source(), got, heldTo, want, oci.ErrFetchIntegrity())
	}
	return nil
}

// UnrecordedIdentity is the advisory a resolution raises for a plugin whose
// resolved bytes no recorded digest governs: the reference, and the source its
// bytes were read from. A construction receives it through
// [ResolutionContext.Unrecorded].
type UnrecordedIdentity struct {
	URI  string
	Tier Tier
}

// Message is the advisory's sentence: the reference, where its bytes came from,
// and what holds for that source.
//
// What it says after its first clause depends on the source, because the remedy
// is true of one source and false of another. Bytes read from the content cache
// are an acquisition a pull records, so the sentence names the pull that records
// them. Bytes read from beside a Plugin entity in a catalog directory are
// recorded by a pull only for a plugin attached at a transform role, whose bytes
// a pull pins because they shape the rendered output; for every other plugin
// authored there no pull records them, and telling the reader to run one would
// send them to a command that changes nothing. A resolution cannot tell which
// role a reference is attached at, so for those bytes the sentence says what
// holds either way — they are read from the catalog directory on every build,
// and a pull records them only for a transform role — and names the one thing
// that brings any other plugin under a recorded digest, publishing it and
// referencing the published entity.
func (a UnrecordedIdentity) Message() string {
	remedy := "run `aggrega pull --release <release>` to record it"
	if a.Tier == TierColocated {
		remedy = "it is read from beside its Plugin entity in a catalog directory on every build, and a pull " +
			"records its digest only for a plugin attached at a transform role; otherwise publish the plugin and " +
			"reference the published entity to have its digest recorded"
	}
	return fmt.Sprintf("plugin %q resolved from %s with no recorded digest to check it against, "+
		"so it runs unverified; %s", a.URI, a.Tier.Source(), remedy)
}

// ReportUnrecordedTo returns a receiver for [ResolutionContext.Unrecorded] that
// writes each advisory to w as one warning line, once per reference for as long
// as the receiver is held: a surface resolves the same plugin for every node it
// renders and every command it registers, and the condition is a property of the
// workspace rather than of any one resolution, so repeating it per resolution
// would bury it. It is for a surface whose reader looks at w and nowhere else; a
// surface with a structured advisory channel supplies a receiver that records
// there.
func ReportUnrecordedTo(w io.Writer) func(UnrecordedIdentity) {
	var mu sync.Mutex
	advised := map[string]struct{}{}
	return func(a UnrecordedIdentity) {
		mu.Lock()
		defer mu.Unlock()
		if _, seen := advised[a.URI]; seen {
			return
		}
		advised[a.URI] = struct{}{}
		fmt.Fprintf(w, "warning: %s\n", a.Message())
	}
}

// noteUnrecordedIdentity raises the advisory for a plugin whose resolved bytes no
// recorded digest governs, handing it to the receiver the construction supplied.
// It never fails a resolution: a workspace that has not been prepared yet is an
// ordinary state, and refusing to read a plugin for want of a record would make
// every surface that only reports on a document unusable until an operator
// prepared one.
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
// Where the advisory goes, and how often, is the receiver's to decide, because
// only the surface knows where its reader looks. A run of the compiler records it
// on the run's one advisory channel beside its display, once per reference per
// run, so a long-running process that serves an editor publishes it with the
// run's other advisories — each run reports what that run resolved unverified,
// and a run that no longer resolves it stops reporting it. A command reporting
// to a terminal writes it there, once per reference (see ReportUnrecordedTo).
// Either way the advisory returns nothing and cannot fail, so a resolution — and
// the verdict any surface reaches on it — is identical whether or not it was
// raised.
//
// The rule itself stays at this single site rather than at each caller, and the
// reason is the one that makes every pinned construction supply a receiver:
// honouring it where some plugins resolve and skipping it where others do leaves
// the skipped resolutions looking covered, so silence would mean two things at
// once — nothing went unrecorded, or this surface does not say — with no way for
// a reader to tell which. A construction that supplies no receiver raises the
// advisory to nobody.
func (r Resolver) noteUnrecordedIdentity(uri string, tier Tier) {
	if !r.expectsRecordedIdentity || tier == TierEmbed || uri == "" || r.unrecorded == nil {
		return
	}
	r.unrecorded(UnrecordedIdentity{URI: uri, Tier: tier})
}

// verifyCachedBundle holds a signed guest's recorded signature fact — its lockfile
// row's, or its entry's in the record of plugin identities beside the lockfile —
// before the guest loads, through the one decision whether a recorded fact is
// accepted (oci.HoldRecordedSignature): the cached bundle is re-hashed against the
// digest the fact records, and the bundle must have been made for the artifact
// these bytes were read as. That is an offline byte and hash comparison — no
// signature re-verification, no network, no trust root — so it holds under the
// hermetic build. A missing or unreadable blob, or a hash that differs from the
// recorded digest, halts with a wrapped oci.ErrSignatureBundleDigestMismatch, and
// a bundle made for another artifact with a wrapped
// oci.ErrSignatureForAnotherArtifact, each naming the plugin and where its bytes
// came from. The blob is read under the same workspace root the cached .wasm is
// read under, so the slot checked here is the slot the pull path wrote whatever
// directory either was run from.
//
// Which artifact the bytes were read as is the manifest heldManifest reads beside
// them, never a field of the record carrying the fact (see heldManifest).
//
// A build runs the same decision over every recorded fact it reads before anything
// renders, so a build reaches here only with a fact it already accepted. This site
// is kept because it is where a plugin's bytes are resolved on every surface — a
// validation, a preview, a lint, an extension command's registration — and the
// surfaces that load a plugin outside a build pass no other.
//
// A read-hold refusal — a cache directory, or the blob itself, leading through a
// link out of the root the pull's write of it is held to — is returned as
// itself, naming the link, rather than as a bundle that does not match.
//
// For bytes read from the content cache, a refusal of either condition names the
// pull of the Release the pin source belongs to as its remedy (oci.WithPullRemedy):
// the next pull verifies again the signature of a cache-served plugin whose
// recorded fact the decision refuses, and rewrites a cached bundle that no longer
// matches its digest. Bytes read from any other tier are never asked about at a
// registry, so a pull leaves a fact recorded for them as it is, and their refusal
// names no pull.
func (r Resolver) verifyCachedBundle(uri string, tier Tier, bundleDigest string, b []byte) error {
	madeFor, err := r.heldManifest(uri, tier, b)
	if err != nil {
		return err
	}
	recorded := oci.RecordedBundle{Digest: bundleDigest, RecordedIn: r.recordedIn}
	if _, err := oci.HoldRecordedSignature(r.workspaceRoot, r.cacheDir, recorded, madeFor); err != nil {
		if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) || errors.Is(err, oci.ErrSignatureForAnotherArtifact()) {
			if tier == TierCache {
				err = oci.WithPullRemedy(err, r.release, r.recordedIn)
			}
			return fmt.Errorf("plugin %q resolved from %s: %w", uri, tier.Source(), err)
		}
		return err
	}
	return nil
}

// heldManifest reports the artifact a plugin's bytes were read as, for holding a
// recorded signature fact to: the digest of the manifest persisted beside the
// bytes in the content-cache slot they were read from, once the bytes are held to
// it — the manifest the pull that wrote the slot fetched with them, and the one a
// signature on the plugin was made for.
//
// Bytes read from the binary, from beside a Plugin entity or from an explicit path
// come with no manifest, so a fact recorded for them is held to the re-hash alone.
// So is a cache slot holding no manifest beside its bytes, which a pull never
// leaves: it writes the two together. A manifest that is there and does not
// describe the bytes is refused rather than used, because a manifest that is not
// the bytes' own would say nothing about which artifact they are.
func (r Resolver) heldManifest(uri string, tier Tier, b []byte) (oci.MadeFor, error) {
	if tier != TierCache {
		return oci.NoManifestRead(), nil
	}
	manifest, err := oci.ReadCachedManifest(r.workspaceRoot, r.cacheDir, oci.PluginArtifactKind(), uri, r.pinVersions[uri])
	switch {
	case err == nil && len(manifest) > 0:
	case err == nil, errors.Is(err, oci.ErrFetchNotFound()):
		return oci.NoManifestRead(), nil
	default:
		return oci.MadeFor{}, err
	}
	if err := oci.VerifyContentAgainstManifest(manifest, b); err != nil {
		return oci.MadeFor{}, fmt.Errorf("plugin %q resolved from %s: the manifest beside its bytes: %w", uri, tier.Source(), err)
	}
	return oci.MadeForManifest(digest.ContentDigest(manifest)), nil
}

// readColocated reads the co-located artifact for uri — the .wasm beside its
// Plugin entity in the catalog directory keyed by the URI's artifact reference —
// returning (bytes, true, nil) on a hit, (nil, false, nil) on a clean miss (no
// mapped directory, or no such file), or a wrapped error on an unreadable
// present file. The filename is the shared PluginLeaf mapping, which carries the
// reference's socket and — where the reference pins one — its version, so two
// plugins sharing a bare name are read from two files rather than one.
//
// The file is workspace content the product reads on its own account, so it is
// judged before it is opened against the root the entity's catalog directory picks
// (readscope.CheckBesideEntityRead): one that leads through a link out of that root
// is not read, and the refusal is returned rather than a miss, because a miss would
// fall through to the next tier and could load another artifact under the same
// reference in its place. A link leading nowhere carries nothing to read and is a
// clean miss, as a missing file is.
func (r Resolver) readColocated(uri string) ([]byte, bool, error) {
	at, ok := r.colocatedDirs[uri]
	if !ok {
		return nil, false, nil
	}
	path, missed, err := at.besideEntity(uri)
	if err != nil {
		return nil, false, err
	}
	if missed {
		return nil, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read colocated plugin %q at %s: %w", uri, path, err)
	}
	return b, true, nil
}

// HoldsModule reports whether the co-located tier finds a module of uri at at:
// whether a read of it there would return the module or refuse it, rather than
// miss and fall through to the next tier. It is the tier's own judgement
// (besideEntity), so a caller deciding by it decides exactly as the read would.
func (at ColocatedDir) HoldsModule(uri string) bool {
	_, missed, err := at.besideEntity(uri)
	return err != nil || !missed
}

// besideEntity is the one judgement of the module of uri beside the entity at
// at: the file it is read from — the one PluginLeaf names for uri, in Dir — and
// whether the tier misses it there. It misses where at names no directory, and
// where nothing is at the path, a link leading nowhere included. A path the read
// may not reach — a link leading out of the root the entity's catalog directory
// picks (readscope.CheckBesideEntityRead) — is refused with the error. Anything
// else at the path is there: the read returns it, or reports why it cannot.
func (at ColocatedDir) besideEntity(uri string) (path string, missed bool, err error) {
	if at.Dir == "" {
		return "", true, nil
	}
	path = filepath.Join(at.Dir, pluginLeaf(uri))
	if err := readscope.CheckBesideEntityRead(at.Root, path); err != nil {
		return path, false, err
	}
	if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
		return path, true, nil
	}
	return path, false, nil
}
