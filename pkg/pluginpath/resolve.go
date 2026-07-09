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
//	0b sidecar    — an on-disk plugin directory the operator points at
//	1  local      — an explicit ./ or ../ filesystem path (or an absolute path)
//	2  cache      — the workspace content cache (a previously-pulled artifact)
//	3  registry   — an OCI pull (lock/pull time only; never at hermetic build)
//
// Every tier yields the resolved bytes plus a source-tier tag. When the caller
// supplies a digest pin for the URI (the build path, reading the lockfile), the
// resolved bytes are verified against that pin at EVERY tier — the pinned
// digest, not the source tier, governs determinism: a sidecar artifact whose
// bytes match the pin resolves byte-identically to the registry artifact it
// overrides, and a mismatch halts. When no pin is supplied (the publish-time
// linter, which lints a not-yet-pinned proposed entity) verification is skipped.
//
// Trust at build is the digest pin alone: signature verification is a
// pull/lock-time, network-touching operation recorded as a lockfile fact, never
// re-run at build. This package performs no signature check.
package pluginpath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Tier names the resolution tier a plugin's bytes came from. It is a
// diagnostic tag — the loaded bytes are identical regardless of tier when the
// digest pin matches; the tag exists for logging and for the
// sidecar-overrides-embed notice.
type Tier string

const (
	TierEmbed    Tier = "embed"    // 0a
	TierSidecar  Tier = "sidecar"  // 0b
	TierLocal    Tier = "local"    // 1
	TierCache    Tier = "cache"    // 2
	TierRegistry Tier = "registry" // 3
)

// ResolvedPlugin is the cascade's output: the plugin's .wasm bytes, the tier
// they were resolved from, and the URI they were resolved for. Bytes feeds the
// loader's PluginSpec.Bytes directly, so a tier with no stable filesystem
// location (embed) needs no path.
type ResolvedPlugin struct {
	Bytes []byte
	Tier  Tier
	URI   string

	// Capability is the entity's authored capability claim, attached by the
	// entity-fronted Lookup (EntityLookup) from the in-hand Plugin entity.
	// It is neutral — a pluginpath-owned shape, not the loader's — so this
	// package stays free of any internal/wasm dependency; the loader's caller
	// maps it onto the load spec. nil when the resolution is not entity-fronted
	// (the bare cascade, the linter's --plugin override) so the cross-check is
	// skipped.
	Capability *PluginCapabilityClaim

	// Limits is the entity's authored runtime resource limits (memory and
	// per-call wall time), attached by the entity-fronted Lookup (EntityLookup)
	// from the in-hand Plugin entity. Like Capability it is neutral — a
	// pluginpath-owned shape carrying no internal/wasm dependency; the loader's
	// caller maps it onto the load spec. Unlike Capability the limits are not
	// cross-checked against the plugin (a plugin does not self-report limits),
	// so they ride a sibling field rather than the capability claim. nil when
	// the resolution is not entity-fronted; the loader then applies the
	// platform default cap, so a load is never unbounded.
	Limits *PluginRuntimeLimits
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

// PluginCapabilityClaim is the neutral, pluginpath-owned shape of an entity's
// authored capability declaration: the per-export name/operates/scope triples
// the load gate cross-checks against the plugin's self-report. It carries no
// dependency on internal/wasm; the loader's caller maps it onto the wasm-side
// claim shape.
//
// The host-function surface is not part of the authored claim: it is
// machine-owned, derived from the .wasm import section at publish and stamped
// into the entity, so there is no authored list to cross-check at load. The
// loader registers exactly the derived set.
type PluginCapabilityClaim struct {
	// Exports is the authored per-export capability claim, in author order.
	Exports []PluginExportClaim

	// Capabilities is the authored plugin-level capability claim in the neutral
	// shape, or nil when the entity authors none. The loader's caller maps it
	// onto the wasm-side claim shape; the load gate cross-checks it against the
	// plugin's describe() self-report.
	Capabilities *PluginCapabilitiesClaim
}

// PluginCapabilitiesClaim is the neutral, pluginpath-owned shape of an entity's
// authored plugin-level capability claim: the input kinds the plugin renders,
// how it assembles a dispatch group's per-node output, the artifact-template
// delimiter pair, and any source-file restriction. It carries no dependency on
// internal/wasm; the loader's caller maps it onto the wasm-side claim shape.
type PluginCapabilitiesClaim struct {
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

// Lookup resolves a plugin URI to its resolved bytes. It is the seam each
// socket binds: the render pipeline binds a build-configured Resolver's Resolve
// method; the linter binds a no-pins Resolver carrying its --plugin override.
type Lookup func(uri string) (ResolvedPlugin, error)

// Resolver carries the per-invocation resolution context the cascade consults.
// A zero Resolver resolves only the embed and local tiers (no sidecar dir, no
// cache, no registry, no pins) — the minimum a caller with no workspace context
// can do. Callers populate the fields their socket has: the build path sets
// WorkspaceRoot/CacheDir/Pins (and a nil Puller, since build is hermetic); the
// pull path additionally sets a real Puller; the linter sets Override.
type Resolver struct {
	// SidecarDir is the on-disk plugin directory (Tier 0b), already resolved
	// to an absolute path. Empty disables the sidecar tier. Layout is flat:
	// a URI resolves to <SidecarDir>/<uri-leaf>.wasm.
	SidecarDir string

	// WorkspaceRoot and CacheDir address the workspace content cache (Tier 2).
	// Both empty disables the cache tier.
	WorkspaceRoot string
	CacheDir      string

	// Pins maps a plugin URI to its lockfile digest pin (a "sha256:..."
	// string). When a URI has a pin, the bytes resolved at EVERY tier are
	// verified against it and a mismatch halts. nil or a URI absent from the
	// map disables verification for that URI — the publish-time linter path,
	// where nothing is pinned yet.
	Pins map[string]string

	// PinVersions maps a plugin URI to its lockfile resolved version, used to
	// address the versioned cache slot (Tier 2). Absent leaves the version
	// empty, addressing the unversioned slot.
	PinVersions map[string]string

	// Puller performs the Tier-3 OCI pull. nil disables Tier 3 — the hermetic
	// build path, where a cache miss is a loud halt rather than a network
	// fetch. The pull/lock path supplies a real puller.
	Puller Puller

	// Override is an explicit filesystem path that wins over the cascade
	// entirely (the linter's --plugin flag). Returned as local-tier bytes.
	Override string

	// OverrideLog receives the loud notice when a sidecar artifact overrides an
	// embedded one of the same URI. nil discards the notice (tests, and any
	// caller that does not surface it). The production wiring points it at the
	// diagnostic stream.
	OverrideLog func(string)
}

// Puller fetches a plugin artifact's bytes from a registry by URI. It is the
// Tier-3 capability, injected so this package makes no network call itself and
// the hermetic build path can leave it nil.
type Puller interface {
	PullPlugin(ctx context.Context, uri string) ([]byte, error)
}

// ociNotWiredHint is woven into a Tier-exhaustion error for an OCI URI when no
// puller is wired (the hermetic build path) so the message points at the fix —
// pull first, or point a sidecar/local path at the artifact — rather than
// reading as an unexplained miss.
const ociNotWiredHint = "an OCI plugin URI requires a prior `aggrega pull` (so its bytes are cached) " +
	"or an explicit sidecar/local path; the hermetic build does not fetch from a registry"

// unsetMsg is the single-sourced diagnostic for the no-URI / no-override case.
// The substring "no plugin path configured" is load-bearing: callers and tests
// match on it.
const unsetMsg = "no plugin path configured. Either:\n" +
	"  - pass an explicit filesystem path\n" +
	"  - or set the plugin URI in .aggrega-config.yaml to a workspace-relative path"

// Resolve runs the tier cascade for uri and returns the resolved bytes. It
// satisfies the Lookup type when bound as a method value (r.Resolve).
//
// Precedence is embed (0a) → sidecar (0b) → local (1) → cache (2) → registry
// (3); the first tier that produces bytes wins. An explicit Override short-
// circuits the cascade. When the URI carries a digest pin, the winning tier's
// bytes are verified against it; a mismatch halts with a wrapped
// oci.ErrFetchIntegrity. A sidecar artifact that shadows an embedded one of the
// same URI emits a loud override notice (the pinned digest still governs output,
// so the override is determinism-safe when it matches the pin).
func (r Resolver) Resolve(uri string) (ResolvedPlugin, error) {
	if r.Override != "" {
		abs := r.Override
		if !filepath.IsAbs(abs) {
			a, err := filepath.Abs(abs)
			if err != nil {
				return ResolvedPlugin{}, fmt.Errorf("resolve plugin override %q: %w", r.Override, err)
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

	// 0a embed (looked up now; consumed after the sidecar tier so a sidecar
	// override of an embedded plugin can win and be logged).
	embedBytes, isEmbedded := embedded.Lookup(uri)

	// 0b sidecar.
	if r.SidecarDir != "" {
		b, ok, err := r.readSidecar(uri)
		if err != nil {
			return ResolvedPlugin{}, err
		}
		if ok {
			if isEmbedded {
				r.logOverride(fmt.Sprintf(
					"plugin %q: sidecar artifact (%s) overrides the embedded built-in plugin; "+
						"the pinned digest governs output, so the override is determinism-safe when it matches the pin",
					uri, filepath.Join(r.SidecarDir, sidecarLeaf(uri))))
			}
			return r.verified(uri, b, TierSidecar)
		}
	}

	// 0a embed: consume after a sidecar miss.
	if isEmbedded {
		return r.verified(uri, embedBytes, TierEmbed)
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
	if r.WorkspaceRoot != "" || r.CacheDir != "" {
		version := r.PinVersions[uri]
		b, err := oci.ReadCached(r.WorkspaceRoot, r.CacheDir, uri, version)
		switch {
		case err == nil:
			return r.verified(uri, b, TierCache)
		case errors.Is(err, oci.ErrFetchNotFound):
			// fall through to Tier 3
		default:
			return ResolvedPlugin{}, err
		}
	}

	// 3 registry: an OCI pull. nil puller = hermetic build; a cache miss here
	// is a loud halt, not a network fetch.
	if r.Puller != nil {
		b, err := r.Puller.PullPlugin(context.Background(), uri)
		if err != nil {
			return ResolvedPlugin{}, fmt.Errorf("pull plugin %q: %w", uri, err)
		}
		return r.verified(uri, b, TierRegistry)
	}

	if workspaceconfig.LooksLikeOCIURI(uri) {
		return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable: %s: %w", uri, ociNotWiredHint, oci.ErrFetchNotFound)
	}
	return ResolvedPlugin{}, fmt.Errorf("plugin %q not resolvable through any tier (embed, sidecar, local, cache, registry): %w", uri, oci.ErrFetchNotFound)
}

// verified checks bytes against the URI's digest pin when one is present and
// returns the ResolvedPlugin. A pin mismatch halts with a wrapped
// oci.ErrFetchIntegrity naming the URI, the tier, the computed digest, and the
// pin. With no pin (the linter path) the bytes pass through unverified.
func (r Resolver) verified(uri string, b []byte, tier Tier) (ResolvedPlugin, error) {
	if pin, ok := r.Pins[uri]; ok && pin != "" {
		if got := digest.ContentDigest(b); got != pin {
			return ResolvedPlugin{}, fmt.Errorf(
				"plugin %q resolved from %s tier: content digest %s does not match lockfile pin %s: %w",
				uri, tier, got, pin, oci.ErrFetchIntegrity)
		}
	}
	return ResolvedPlugin{Bytes: b, Tier: tier, URI: uri}, nil
}

// readSidecar reads the sidecar artifact for uri, returning (bytes, true, nil)
// on a hit, (nil, false, nil) on a clean miss (no such file), or a wrapped
// error on an unreadable present file.
func (r Resolver) readSidecar(uri string) ([]byte, bool, error) {
	path := filepath.Join(r.SidecarDir, sidecarLeaf(uri))
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read sidecar plugin %q at %s: %w", uri, path, err)
	}
	return b, true, nil
}

func (r Resolver) logOverride(msg string) {
	if r.OverrideLog != nil {
		r.OverrideLog(msg)
	}
}
