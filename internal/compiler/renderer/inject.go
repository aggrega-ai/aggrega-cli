// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// BuildInjectPipe constructs the dual-state injection context for a node.
// - injectRaw: the immutable developer intent (nil if omitted)
// - inject: pre-hydrated with guaranteed env/file/arg empty maps + .all merge
//
// The inject pipe starts with base values resolved from developer inject → target provides
// (populated by Pass 2 in the 3-pass renderer). This function builds the initial
// structure with guaranteed keys for null-pointer safety.
func BuildInjectPipe(rawInject *types.InjectBlock) (inject types.InjectPipe, injectRaw interface{}) {
	inject = types.InjectPipe{
		Env:  make(map[string]string),
		File: make(map[string]string),
		Arg:  make(map[string]string),
		All:  make(map[string]string),
	}

	if rawInject == nil {
		return inject, nil
	}

	// Preserve raw as immutable snapshot
	injectRaw = rawInject

	// Populate known mechanisms from raw inject
	if env := normalizeInjectMechanism(rawInject.Env); env != nil {
		inject.Env = env
	}
	if file := normalizeInjectMechanism(rawInject.File); file != nil {
		inject.File = file
	}
	if arg := normalizeInjectMechanism(rawInject.Arg); arg != nil {
		inject.Arg = arg
	}

	// Build .all merge
	inject.All = buildInjectAll(inject)

	return inject, injectRaw
}

// RehydrateInjectFromProvides resolves developer inject keys against target provides.
// For each edge's inject block, the developer wrote {env: {DB_URL: "host"}} meaning
// "resolve key 'host' from the target's provides dictionary."
func RehydrateInjectFromProvides(
	inject *types.InjectPipe,
	edges []types.IREdge,
	nodeID string,
	allProvides NodeProvides,
) {
	for _, edge := range edges {
		if edge.Source != nodeID || edge.Inject == nil {
			continue
		}

		targetProvides := allProvides[edge.Target]
		if targetProvides == nil {
			continue
		}

		// Resolve env inject keys
		resolveInjectKeys(inject.Env, edge.Inject, "env", targetProvides)
		resolveInjectKeys(inject.File, edge.Inject, "file", targetProvides)
		resolveInjectKeys(inject.Arg, edge.Inject, "arg", targetProvides)
	}

	// Rebuild .all after resolution
	inject.All = buildInjectAll(*inject)
}

func resolveInjectKeys(dest map[string]string, edgeInject map[string]map[string]string, mechanism string, targetProvides map[string]string) {
	mechanismMap, ok := edgeInject[mechanism]
	if !ok {
		return
	}
	for localKey, providesKey := range mechanismMap {
		if resolved, ok := targetProvides[providesKey]; ok {
			dest[localKey] = resolved
		}
	}
}

// buildInjectAll merges all inject mechanisms into a single flat map.
// Keys from env, file, arg (and custom mechanisms) are merged with sorted iteration.
func buildInjectAll(inject types.InjectPipe) map[string]string {
	all := make(map[string]string)

	// Merge in deterministic order: arg, env, file (alphabetical mechanism names)
	mergeSorted(all, inject.Arg)
	mergeSorted(all, inject.Env)
	mergeSorted(all, inject.File)

	// Custom mechanisms via Additional
	if inject.Additional != nil {
		mechNames := make([]string, 0, len(inject.Additional))
		for k := range inject.Additional {
			mechNames = append(mechNames, k)
		}
		sort.Strings(mechNames)
		for _, mech := range mechNames {
			mergeSorted(all, inject.Additional[mech])
		}
	}

	return all
}

func mergeSorted(dest, src map[string]string) {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dest[k] = src[k]
	}
}

// normalizeInjectMechanism converts the polymorphic inject mechanism value
// (either map[string]string or map[string]interface{}) to map[string]string.
func normalizeInjectMechanism(raw interface{}) map[string]string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}
