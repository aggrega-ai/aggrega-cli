// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// BuildInjectPipe constructs the dual-state injection context for a node.
// - injectRaw: the immutable developer intent (nil if omitted)
// - inject: pre-hydrated with guaranteed env/file/arg empty maps + .all merge
//
// The inject pipe starts with base values resolved from developer inject → target provides
// (populated by Pass 2 in the 3-pass renderer). This function builds the initial
// structure with guaranteed keys for null-pointer safety.
func BuildInjectPipe(rawInject *types.InjectBlock) (inject types.InjectPipe, injectRaw any) {
	inject = types.InjectPipe{
		Env:  types.OrderedMap[string]{},
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}

	if rawInject == nil {
		return inject, nil
	}

	// Preserve raw as immutable snapshot
	injectRaw = rawInject

	// Populate known mechanisms from raw inject
	if env := normalizeInjectMechanism(rawInject.Env); env.Len() > 0 {
		inject.Env = env
	}
	if file := normalizeInjectMechanism(rawInject.File); file.Len() > 0 {
		inject.File = file
	}
	if arg := normalizeInjectMechanism(rawInject.Arg); arg.Len() > 0 {
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
		if edge.Source != nodeID || edge.Inject.Len() == 0 {
			continue
		}

		targetProvides, ok := allProvides[edge.Target]
		if !ok {
			continue
		}

		// Resolve env inject keys
		resolveInjectKeys(&inject.Env, edge.Inject, "env", targetProvides)
		resolveInjectKeys(&inject.File, edge.Inject, "file", targetProvides)
		resolveInjectKeys(&inject.Arg, edge.Inject, "arg", targetProvides)
	}

	// Rebuild .all after resolution
	inject.All = buildInjectAll(*inject)
}

func resolveInjectKeys(dest *types.OrderedMap[string], edgeInject types.OrderedMap[types.OrderedMap[string]], mechanism string, targetProvides types.OrderedMap[string]) {
	mechanismMap, ok := edgeInject.Get(mechanism)
	if !ok {
		return
	}
	for localKey, providesKey := range mechanismMap.All() {
		if resolved, ok := targetProvides.Get(providesKey); ok {
			dest.Set(localKey, resolved)
		}
	}
}

// buildInjectAll merges all inject mechanisms into a single OrderedMap.
// Keys from arg, env, file (alphabetical mechanism names for stability), then
// custom mechanisms in sorted order.
func buildInjectAll(inject types.InjectPipe) types.OrderedMap[string] {
	all := types.OrderedMap[string]{}

	// Merge in deterministic order: arg, env, file (alphabetical mechanism names)
	mergeSorted(&all, inject.Arg)
	mergeSorted(&all, inject.Env)
	mergeSorted(&all, inject.File)

	// Custom mechanisms via Additional
	if inject.Additional.Len() > 0 {
		mechNames := make([]string, 0, inject.Additional.Len())
		for k := range inject.Additional.All() {
			mechNames = append(mechNames, k)
		}
		sort.Strings(mechNames)
		for _, mech := range mechNames {
			if val, ok := inject.Additional.Get(mech); ok {
				mergeSorted(&all, val)
			}
		}
	}

	return all
}

func mergeSorted(dest *types.OrderedMap[string], src types.OrderedMap[string]) {
	// Merge in author order — the underlying OrderedMap already carries it.
	for k, v := range src.All() {
		dest.Set(k, v)
	}
}

// normalizeInjectMechanism converts the polymorphic inject mechanism value
// (map[string]string, map[string]any, *types.OrderedMap[any], or []any
// schema-verbose-form entries) to OrderedMap[string]. The
// *types.OrderedMap[any] shape arrives from round-trip-fidelity decoding
// when the inject block is reached via an OrderedMap[any]-routed authoring
// path; iteration via .All() preserves author order. Unsorted plain-map
// shapes lose authoring order at decode and are sorted alphabetically here
// for stability. The schema-verbose-form []any (elements
// {in, from?, value?}) iterates in source order; value: wins over from: if
// both are present at an entry.
func normalizeInjectMechanism(raw any) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	if raw == nil {
		return out
	}
	switch v := raw.(type) {
	case *types.OrderedMap[any]:
		if v == nil {
			return out
		}
		for k, val := range v.All() {
			if s, ok := val.(string); ok {
				out.Set(k, s)
			}
		}
	case map[string]string:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Set(k, v[k])
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := v[k].(string); ok {
				out.Set(k, s)
			}
		}
	case []any:
		for _, entry := range v {
			in, value, from, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok {
				continue
			}
			if hasValue {
				out.Set(in, value)
				continue
			}
			if hasFrom {
				out.Set(in, from)
			}
		}
	}
	return out
}
