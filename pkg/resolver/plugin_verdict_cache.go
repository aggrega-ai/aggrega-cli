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
// operator sidecar directory, the co-located catalog directories, and the
// workspace content cache (workspace root plus cache directory). Each caller —
// the build pipeline, the publishing linter — fills it from its own workspace
// handles, so the shared cache-not-pin composition carries no caller-specific
// type. A zero field disables that tier exactly as it does on the underlying
// resolver.
type VerdictPluginTierSources struct {
	SidecarDir    string
	ColocatedDirs map[string]string
	WorkspaceRoot string
	CacheDir      string
}

// ResolveVerdictPluginFromCache resolves a verdict-socket (policy / linter)
// plugin from the pull-populated cache by its entity URI, with no lockfile pin.
// It reads the Plugin entity from the cache (manifest root of trust) to obtain
// the entity's authored contract and runtime limits and the .wasm layer content
// digest, then resolves the executable through the shared artifact cascade keyed
// by that manifest-derived digest — the same verified content-cache read the
// pinned path uses, so the .wasm is digest-checked exactly as a pinned plugin's
// is. The entity axis is never pinned or drift-gated; only its bytes are
// integrity-checked.
//
// pluginFromCache reads a Plugin entity from the content cache by URI, returning
// the parsed entity, the .wasm layer content digest, a found flag, and any read
// error; the caller supplies it as a closure over its own cache-backed catalog
// view. A nil pluginFromCache, or a clean cache miss, halts with a message
// naming the plugin and wrapping oci.ErrFetchNotFound.
func ResolveVerdictPluginFromCache(sources VerdictPluginTierSources, pluginFromCache func(uri string) (types.Plugin, string, bool, error), uri string) (pluginpath.ResolvedPlugin, error) {
	if pluginFromCache == nil {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve verdict-socket plugin %q: no cache-backed catalog lookup: %w", uri, oci.ErrFetchNotFound)
	}
	entity, wasmDigest, ok, err := pluginFromCache(uri)
	if err != nil {
		return pluginpath.ResolvedPlugin{}, err
	}
	if !ok {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("resolve verdict-socket plugin %q: %s: %w",
			uri, "not present in the content cache; run `aggrega pull` first", oci.ErrFetchNotFound)
	}
	artifactRef := entity.ArtifactReference()
	// A fresh resolver keyed only by the manifest-derived .wasm digest: the pin
	// governs the bytes (verify-on-read) but is synthesized from the cached
	// manifest, not read from the lockfile, so the gate plugin's .wasm resolves and
	// is integrity-checked without a lockfile row.
	r := pluginpath.NewResolver(pluginpath.ResolutionContext{
		SidecarDir:    sources.SidecarDir,
		ColocatedDirs: sources.ColocatedDirs,
		WorkspaceRoot: sources.WorkspaceRoot,
		CacheDir:      sources.CacheDir,
	}, pluginpath.PinPostureVerifyOnRead, pluginpath.PinMaps{
		Pins:        map[string]string{artifactRef: wasmDigest},
		PinVersions: map[string]string{artifactRef: wasmDigest},
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
// sidecar override, or an embedded built-in), and on a not-found miss ONLY falls
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
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound) && !errors.Is(err, oci.ErrFetchNotFound) {
			return pluginpath.ResolvedPlugin{}, err
		}
		return cacheResolve(uri)
	}
}
