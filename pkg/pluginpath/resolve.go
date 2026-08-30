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
//	3  registry   — an OCI pull (lock/pull time only; never at hermetic build)
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
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Tier names the source a plugin's bytes came from. It is a diagnostic tag: the
// loaded bytes are identical regardless of source when the digest pin matches, so
// nothing branches on it. Where a tag reaches a user it is rendered through
// Source, never printed raw, because these spellings are this resolver's internal
// vocabulary rather than words the reference teaches.
type Tier string

const (
	TierEmbed     Tier = "embed"     // 0a
	TierColocated Tier = "colocated" // 0b
	TierLocal     Tier = "local"     // 1
	TierCache     Tier = "cache"     // 2
	TierRegistry  Tier = "registry"  // 3
)

// resolutionCascade lists the sources the cascade consults, in precedence order.
// The diagnostic that reports exhaustion renders this slice rather than repeating
// the names, so the message cannot claim a cascade the resolver does not walk.
var resolutionCascade = []Tier{TierEmbed, TierColocated, TierLocal, TierCache, TierRegistry}

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
// The mapping is deliberately many-to-one: a .wasm laid down beside its entity
// and one named by an explicit path are distinct lookups here, but to a reader
// both mean the bytes were found locally, so both report the same word. That is
// the whole extent of the sharing — this cascade and the pull-side resolution
// tags are separate axes, over different subjects and at different granularity,
// and only their rendering is common.
func (t Tier) Source() closure.ResolutionSource {
	switch t {
	case TierEmbed:
		return closure.SourceInBinary
	case TierColocated, TierLocal:
		return closure.SourceLocal
	case TierCache:
		return closure.SourceCacheReuse
	case TierRegistry:
		return closure.SourceFetchedFresh
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
// how it assembles a dispatch group's per-node output, the artifact-template
// delimiter pair, and any source-file restriction. It carries no dependency on
// internal/wasm; the loader's caller maps it onto the wasm-side claim shape.
type ProviderContractClaim struct {
	// InputKinds is the authored input-kind claim.
	InputKinds PluginInputKindsClaim
	// BoundaryAssembly is the authored boundary-assembly mode ("none", "merge",
	// "nest", or empty).
	BoundaryAssembly string
	// ArtifactDelimiters is the authored artifact-template delimiter pair, or
	// nil when the plugin uses the default pair.
	ArtifactDelimiters *PluginArtifactDelimitersClaim
	// SourceFilter is the authored source-file restriction, or nil when the
	// plugin consumes every file in a supplied set.
	SourceFilter []string
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
}

// PluginAssemblyNameAnnotationClaim is an entity's authored assembly-name
// annotation in the neutral shape: the boundary annotation whose resolved value
// names the artifact the plugin assembles from a dispatch group, and whether
// every contributing boundary must agree on that value.
type PluginAssemblyNameAnnotationClaim struct {
	Annotation    string
	RequireUnique bool
}

// PluginInputKindsClaim is one entity's authored input-kind claim in the
// neutral shape: which of the three Recipe input kinds the plugin renders.
type PluginInputKindsClaim struct {
	Sourced             bool
	Synthesized         bool
	ArtifactsStandalone bool
}

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

var errPluginBundleDigestMismatch = cerrors.Register("pluginpath/plugin-bundle-digest-mismatch", cerrors.Meta{
	ShortCode: "E0144",
	Message:   "cached signature bundle does not match its recorded digest",
})

// Lookup resolves a plugin URI to its resolved bytes. It is the seam each
// socket binds: the render pipeline binds a build-configured Resolver's Resolve
// method; the linter binds a no-pins Resolver carrying its --plugin override.
type Lookup func(uri string) (ResolvedPlugin, error)

// Resolver carries the per-invocation resolution context the cascade consults.
// It is obtained only from NewResolver — its fields are unexported, so a caller
// in another package cannot hand-populate an under-wired copy; the sole
// construction path threads the tier sources and pin posture in one place. A
// Resolver built from a zero ResolutionContext under PinPostureNone resolves
// only the embed and local tiers (no colocated dirs, no cache, no registry, no
// pins) — the minimum a caller with no workspace context can do.
type Resolver struct {
	// colocatedDirs maps a plugin's artifact reference to the on-disk directory
	// holding its co-located .wasm — the module laid down beside its Plugin
	// entity in a catalog directory. It is the offline resolution path (Tier 0c)
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

	// pinVersions maps a plugin URI to its lockfile resolved version, used to
	// address the versioned cache slot (Tier 2). Absent leaves the version
	// empty, addressing the unversioned slot.
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

	// puller performs the Tier-3 OCI pull. nil disables Tier 3 — the hermetic
	// build path, where a cache miss is a loud halt rather than a network
	// fetch. The pull/lock path supplies a real puller.
	puller Puller

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

// Puller fetches a plugin artifact's bytes from a registry by URI. It is the
// Tier-3 capability, injected so this package makes no network call itself and
// the hermetic build path can leave it nil.
type Puller interface {
	PullPlugin(ctx context.Context, uri string) ([]byte, error)
}

// ociNotWiredHint is woven into a Tier-exhaustion error for an OCI URI when no
// puller is wired (the hermetic build path) so the message points at the fix —
// pull first, or point a local path at the artifact — rather than
// reading as an unexplained miss.
const ociNotWiredHint = "an OCI plugin URI requires a prior `aggrega pull --release <release>` (so its bytes are cached) " +
	"or an explicit local path; the hermetic build does not fetch from a registry"

// unsetMsg is the single-sourced diagnostic for the no-URI / no-override case.
// The substring "no plugin path configured" is load-bearing: callers and tests
// match on it.
const unsetMsg = "no plugin path configured. Either:\n" +
	"  - pass an explicit filesystem path\n" +
	"  - or set the plugin URI in .aggrega-config.yaml to a workspace-relative path"

// Resolve runs the tier cascade for uri and returns the resolved bytes. It
// satisfies the Lookup type when bound as a method value (r.Resolve).
//
// Precedence is embed (0a) → colocated (0b) → local (1) → cache (2) → registry
// (3); the first tier that produces bytes wins. An explicit Override short-
// circuits the cascade. When the URI carries a digest pin, the winning tier's
// bytes are verified against it; a mismatch halts with a wrapped
// oci.ErrFetchIntegrity.
func (r Resolver) Resolve(uri string) (ResolvedPlugin, error) {
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
			// fall through to Tier 3
		default:
			return ResolvedPlugin{}, err
		}
	}

	// 3 registry: an OCI pull. nil puller = hermetic build; a cache miss here
	// is a loud halt, not a network fetch.
	if r.puller != nil {
		b, err := r.puller.PullPlugin(context.Background(), uri)
		if err != nil {
			return ResolvedPlugin{}, fmt.Errorf("pull plugin %q: %w", uri, err)
		}
		return r.verified(uri, b, TierRegistry)
	}

	if workspaceconfig.LooksLikeOCIURI(uri) {
		return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable: %s: %w", uri, ociNotWiredHint, oci.ErrFetchNotFound())
	}
	return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable through any tier (%s): %w", uri, cascadeNames(), oci.ErrFetchNotFound())
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
// what to run to record them. It never fails a resolution: a workspace that has
// not been prepared yet is an ordinary state, and refusing to read a plugin for
// want of a record would make every surface that only reports on a document
// unusable until an operator prepared one.
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
func (r Resolver) noteUnrecordedIdentity(uri string, tier Tier) {
	if !r.expectsRecordedIdentity || tier == TierEmbed || uri == "" {
		return
	}
	if _, seen := advisedUnrecorded.LoadOrStore(uri, struct{}{}); seen {
		return
	}
	fmt.Fprintf(unrecordedIdentityWriter,
		"warning: plugin %q resolved from %s with no recorded digest to check it against, "+
			"so it runs unverified; run `aggrega pull --release <release>` to record it\n",
		uri, tier.Source())
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
