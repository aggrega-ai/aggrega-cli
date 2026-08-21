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
	ColocatedDirs map[string]string
	WorkspaceRoot string
	CacheDir      string
}

// ResolveVerdictPluginFromCache resolves a verdict-socket (policy / linter)
// plugin from the pull-populated cache by its entity URI, with no lockfile pin.
// It reads the Plugin entity from the cache (manifest root of trust) to obtain
// the entity's authored contract and runtime limits and the .wasm layer content
// digest, then resolves the executable through the shared artifact cascade keyed
// by that digest — the same verified content-cache read the pinned path uses, so
// the .wasm is digest-checked exactly as a pinned plugin's is. The entity axis is
// never pinned or drift-gated; only its bytes are integrity-checked.
//
// trusted carries the digests recorded as trusted for verdict-returning plugins,
// keyed by plugin identity. The caller derives them from whatever records
// them — this package stays unaware of where a pin originates, which is what the
// pin-map type is for. An empty set is the ordinary case and changes nothing.
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
// pluginFromCache reads a Plugin entity from the content cache by URI, returning
// the parsed entity, the .wasm layer content digest, a found flag, and any read
// error; the caller supplies it as a closure over its own cache-backed catalog
// view. A nil pluginFromCache, or a clean cache miss, halts with a message
// naming the plugin and wrapping oci.ErrFetchNotFound.
func ResolveVerdictPluginFromCache(sources VerdictPluginTierSources, trusted pluginpath.PinMaps, pluginFromCache func(uri string) (types.Plugin, string, bool, error), uri string) (pluginpath.ResolvedPlugin, error) {
	if pluginFromCache == nil {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve verdict-socket plugin %q: no cache-backed catalog lookup: %w", uri, oci.ErrFetchNotFound())
	}
	entity, wasmDigest, ok, err := pluginFromCache(uri)
	if err != nil {
		return pluginpath.ResolvedPlugin{}, err
	}
	if !ok {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve verdict-socket plugin %q: %s: %w",
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
				"resolve verdict-socket plugin %q: the digest recorded as trusted for it is %s, but the plugin manifest in the content cache reports %s; "+
					"review what changed and re-record the trusted digest, or restore the recorded artifact: %w",
				artifactRef, recorded, wasmDigest, ErrVerdictPluginTrustMismatch())
		}
		// Equal by the check just above, so which one is threaded is behaviourally
		// identical — the recorded one goes in to make the recorded identity the
		// pin of record rather than a fact that was merely consulted.
		pin = recorded
	}
	// A fresh resolver keyed only by that pin: it governs the bytes
	// (verify-on-read) without a lockfile row, so the gate plugin's .wasm resolves
	// and is integrity-checked whether or not anything recorded a digest for it.
	r := pluginpath.NewResolver(pluginpath.ResolutionContext{
		ColocatedDirs: sources.ColocatedDirs,
		WorkspaceRoot: sources.WorkspaceRoot,
		CacheDir:      sources.CacheDir,
	}, pluginpath.PinPostureVerifyOnRead, pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: pin},
		PinVersions: map[string]string{artifactRef: pin},
	})
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
// Only a clean not-found miss (a wrapped pluginpath.ErrPluginEntityNotFound or
// oci.ErrFetchNotFound) falls through; any other error from base — a drifted
// pin, a load fault — is real and halts without consulting the cache. When both
// base and cacheResolve miss, cacheResolve's error is returned.
func VerdictLookup(base pluginpath.Lookup, cacheResolve func(uri string) (pluginpath.ResolvedPlugin, error)) pluginpath.Lookup {
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		resolved, err := base(uri)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) && !errors.Is(err, oci.ErrFetchNotFound()) {
			return pluginpath.ResolvedPlugin{}, err
		}
		return cacheResolve(uri)
	}
}
