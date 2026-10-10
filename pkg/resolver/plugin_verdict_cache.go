// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// VerdictPluginTierSources is the caller-agnostic projection of the artifact
// tiers a verdict-socket cache-not-pin read resolves guest bytes through: the
// co-located catalog directories and the workspace content cache (workspace
// root plus cache directory). Each caller — the build pipeline, the publishing
// linter — fills it from its own workspace handles, so the shared cache-not-pin
// composition carries no caller-specific type. A zero field disables that tier
// exactly as it does on the underlying resolver.
type VerdictPluginTierSources struct {
	ColocatedDirs map[string]pluginpath.ColocatedDir
	WorkspaceRoot string
	CacheDir      string
}

// VerdictPluginTrust is what the record of the plugin identities trusted for the
// attachments that return an accept-or-reject verdict contributes to resolving
// one of their plugins from the content cache: the digests the record carries, and
// the signature requirement a plugin it records no signature for is held to.
//
// The two travel as one value because they are one record's contribution to one
// load. A surface supplied the digests without the requirement would hold a
// plugin's bytes and its recorded bundle while running one whose signature nothing
// records, and the gap would be invisible: an unset requirement is an empty field,
// and an empty field is also what a surface with no record to read supplies.
//
// The zero value holds a plugin to nothing beyond its cached manifest — the honest
// value where no record exists, and the value a transform-role resolution with no
// lockfile supplies, whose recorded identity is a lockfile pin rather than this.
type VerdictPluginTrust struct {
	// PinMaps carries the record's digests keyed by plugin identity: Pins and
	// PinVersions the digest the plugin's bytes are compared against, and
	// BundleDigests the digest of the cached signature bundle a recorded signature
	// names, which is re-hashed before the plugin's bytes are returned.
	pluginpath.PinMaps

	// HoldUnrecorded holds a plugin read from the content cache to the trust
	// configuration where the record carries no signature for it: given the
	// attachment's reference and the plugin's identity coordinate, it returns the
	// refusal the load halts with, or nil where a signature is recorded or none is
	// required. It is consulted before any of the plugin's bytes are read. nil holds
	// nothing.
	HoldUnrecorded func(uri, coordinate string) error
}

// ResolveVerdictPluginFromCache resolves a plugin from the pull-populated cache
// by its entity URI, with no lockfile pin. It reads the Plugin entity from the
// cache (manifest root of trust) to obtain
// the entity's authored contract and runtime limits and the .wasm layer content
// digest, then resolves the executable through the shared artifact cascade keyed
// by that digest — the same verified content-cache read the pinned path uses, so
// the .wasm is digest-checked exactly as a pinned plugin's is. The entity axis is
// never pinned or drift-gated; only its bytes are integrity-checked.
//
// It serves every socket that must resolve a guest without a lockfile pin: the
// gates that return an accept-or-reject verdict, which are cached at pull and
// never pinned; and a transform role on a pass that has no lockfile to read
// because it is the pass that writes one. The name carries the verdict tier's
// vocabulary because that tier was its first caller; the resolution itself is
// tier-neutral, and what differs between callers is only which record, if any,
// they supply as trusted.
//
// trusted carries the digests recorded as trusted for verdict-returning plugins,
// keyed by plugin identity, and the signature requirement a plugin they record
// no signature for is held to. The caller derives them from whatever records
// them — this package stays unaware of where a pin originates, which is what the
// pin-map type is for. An empty set is the ordinary case and changes nothing.
//
// # The recorded signature, at the moment the plugin loads
//
// Where the record carries a verified signature for the plugin, the cached
// signature bundle that signature names is re-hashed against the digest it
// recorded, by the same resolver step that re-hashes a lockfile-pinned plugin's
// bundle (pluginpath.Resolver), before the plugin's bytes are returned: a missing,
// unreadable or altered bundle halts the load. Where the record carries none,
// trusted.HoldUnrecorded decides, before any byte is read, whether the trust
// configuration requires one; a required signature nothing records halts the load.
// So every surface that runs the plugin holds it as a build does, whichever command
// asked for it.
//
// # What a recorded digest adds, and what it does not
//
// Where a digest IS recorded for the plugin being resolved, it is compared
// against the digest the cached manifest reports, and a disagreement halts with
// ErrVerdictPluginTrustMismatch. That comparison is the whole point: without it
// the pin is derived from the same manifest whose bytes it then validates, which
// is self-consistent by construction and so cannot notice that the entire
// artifact has been replaced. The recorded digest is an opinion from outside the
// artifact, and a disagreement between the two is precisely the case worth
// halting on.
//
// The comparison never REPLACES the entity read. The entity supplies the
// version, the capability claim and the runtime limits, and it is what the
// resolved config is verified against; substituting a recorded digest for it
// would trade one check for another rather than add one.
//
// Where no digest is recorded, resolution is exactly as it is without this
// parameter: the manifest-derived digest governs the bytes. An absent record is a
// first-run state, not a fault, and never a pin of zero.
//
// The bound on all of that is the resolution tier, not the plugin: the
// comparison governs the cache-not-pin read this function performs, and no
// resolution outside it. A verdict attachment whose plugin the entity-fronted
// cascade surfaces first — an embedded guest, a co-located artifact, an explicit
// path, or a lockfile-pinned row — never reaches this function at all, because
// VerdictLookup falls through to it only on a clean not-found miss, so it is
// governed by whatever governs that resolution instead. Note that a lockfile pin
// is not itself a tier: Resolver.verified applies it to whichever tier produced
// the bytes, so "pinned" and "served from the cache" are not alternatives on one
// axis. The record's
// other half — refusing a configuration that no longer attaches a recorded
// gate — is not tier-bound: it compares the configuration against the record
// before any resolution runs.
//
// pluginFromCache reads a Plugin entity from the content cache by URI, returning
// the parsed entity, the .wasm layer content digest, a found flag, and any read
// error; the caller supplies it as a closure over its own cache-backed catalog
// view. A nil pluginFromCache, or a clean cache miss, halts with a message
// naming the plugin and wrapping oci.ErrFetchNotFound.
func ResolveVerdictPluginFromCache(sources VerdictPluginTierSources, trusted VerdictPluginTrust, pluginFromCache func(uri string) (types.Plugin, string, bool, error), uri string) (pluginpath.ResolvedPlugin, error) {
	if pluginFromCache == nil {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve cached plugin %q: no cache-backed catalog lookup: %w", uri, oci.ErrFetchNotFound())
	}
	entity, wasmDigest, ok, err := pluginFromCache(uri)
	if err != nil {
		return pluginpath.ResolvedPlugin{}, err
	}
	if !ok {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve cached plugin %q: %s: %w",
			uri, "not present in the content cache; run `aggrega pull --release <release>` first", oci.ErrFetchNotFound())
	}
	// A Plugin entity's artifact reference IS its identity coordinate, which is
	// the address a trusted digest is recorded under, so the recorded digest for
	// this plugin is found under the same key the pin maps are built on.
	artifactRef := entity.ArtifactReference()
	pin := wasmDigest
	if recorded, isRecorded := trusted.Pins[artifactRef]; isRecorded && recorded != "" {
		if recorded != wasmDigest {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf(
				"resolve cached plugin %q: the digest recorded as trusted for it is %s, but the plugin manifest in the content cache reports %s; "+
					"review what changed and re-record the trusted digest, or restore the recorded artifact: %w",
				artifactRef, recorded, wasmDigest, ErrVerdictPluginTrustMismatch())
		}
		// Equal by the check just above, so which one is threaded is behaviourally
		// identical — the recorded one goes in to make the recorded identity the
		// pin of record rather than a fact that was merely consulted.
		pin = recorded
	}
	if trusted.HoldUnrecorded != nil {
		if err := trusted.HoldUnrecorded(uri, artifactRef); err != nil {
			return pluginpath.ResolvedPlugin{}, err
		}
	}
	// A fresh resolver keyed only by that pin: it governs the bytes
	// (verify-on-read) without a lockfile row, so the gate plugin's .wasm resolves
	// and is integrity-checked whether or not anything recorded a digest for it.
	// A bundle digest the record carries for the plugin travels with it, so the
	// resolver re-hashes the cached bundle before returning the bytes.
	pins := pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: pin},
		PinVersions: map[string]string{artifactRef: pin},
	}
	if bundle := trusted.BundleDigests[artifactRef]; bundle != "" {
		pins.BundleDigests = map[string]string{artifactRef: bundle}
	}
	r := pluginpath.NewResolver(pluginpath.ResolutionContext{
		ColocatedDirs: sources.ColocatedDirs,
		WorkspaceRoot: sources.WorkspaceRoot,
		CacheDir:      sources.CacheDir,
	}, pluginpath.PinPostureVerifyOnRead, pins)
	resolved, err := r.Resolve(artifactRef)
	if err != nil {
		return pluginpath.ResolvedPlugin{}, err
	}
	resolved.Version = entity.Metadata.Version
	resolved.Contract = PluginContractClaim(entity)
	resolved.Limits = PluginRuntimeLimits(entity)
	return resolved, nil
}

// VerdictLookup composes a verdict-socket's cache-not-pin resolution: it tries
// base first (the pinned / Tier-1 entity-fronted cascade, honoring a pin, a
// co-located artifact, or an embedded built-in), and on a not-found miss ONLY falls
// through to cacheResolve (the pull-populated cache read by URI). A verdict
// socket's plugin is cached at pull but not pinned, so a gate plugin the pinned
// path does not surface still resolves from the cache.
//
// Only a clean not-found miss falls through: a wrapped
// pluginpath.ErrPluginEntityNotFound (no Plugin entity matches the reference), a
// wrapped pluginpath.ErrPluginArtifactNotFound (the entity's artifact resolved
// through none of the cascade's tiers), or a wrapped oci.ErrFetchNotFound (a
// base whose own miss is a fetch that found nothing). Any other error from base — a
// drifted pin, a load fault — is real and halts without consulting the cache.
// When both base and cacheResolve miss, cacheResolve's error is returned.
func VerdictLookup(base pluginpath.Lookup, cacheResolve func(uri string) (pluginpath.ResolvedPlugin, error)) pluginpath.Lookup {
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		resolved, err := base(uri)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) &&
			!errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) &&
			!errors.Is(err, oci.ErrFetchNotFound()) {
			return pluginpath.ResolvedPlugin{}, err
		}
		return cacheResolve(uri)
	}
}
