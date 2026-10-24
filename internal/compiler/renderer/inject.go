// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
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
// For each edge's inject block, the developer wrote {env: [{in: DB_URL, from: host}]}
// meaning "resolve key 'host' from the target's provides dictionary."
//
// An edge whose OriginFeature names a boundary-scope FeatureRecipe resolves its
// provided values from boundaryFRState rather than the per-component allProvides
// bag: a boundary-scope FR renders once at its boundary, so its provides live in
// BoundaryFRState keyed by (owning-boundary-path, instance), located via the
// consumer node's boundary ancestry and the edge's OriginFeatureInstance (which
// disambiguates two attachments of the same FR type on one boundary). Those
// provides augment the per-component targetProvides; non-boundary edges keep the
// exact allProvides[edge.Target] path. The pre-loop PrepopulateBoundaryScopeProvides
// pass guarantees the boundary-scope provides exist by the time this runs.
func RehydrateInjectFromProvides(
	inject *types.InjectPipe,
	edges []types.IREdge,
	nodeID string,
	allProvides NodeProvides,
	graph *types.IRGraph,
	recipeLookup RecipeLookup,
	boundaryFRState *BoundaryFRState,
) {
	consumerBoundaryPath := ""
	if graph != nil {
		if n, ok := graph.Spec.Nodes.Get(nodeID); ok {
			consumerBoundaryPath = n.BoundaryPath
		}
	}

	for _, edge := range edges {
		if edge.Source != nodeID || edge.Inject.Len() == 0 {
			continue
		}

		targetProvides, ok := allProvides[edge.Target]

		// Boundary-scope FR provides augmentation. When the edge originated
		// from a Feature whose FeatureRecipe is boundary-scope, its provides
		// are not in the per-component allProvides bag — resolve them from the
		// boundary accumulator by instance and merge over targetProvides.
		if edge.OriginFeature != "" && edge.OriginFeatureInstance != "" && boundaryFRState != nil && recipeLookup.FeatureRecipe != nil {
			if fr, found := recipeLookup.FeatureRecipe(edge.OriginFeatureInstance, edge.OriginFeature); found && fr.Spec.ExecutionScope == executionScopeBoundary {
				if bprov, hit := resolveBoundaryScopeFRProvides(boundaryFRState, consumerBoundaryPath, edge.OriginFeatureInstance); hit {
					merged := types.OrderedMap[string]{}
					for k, v := range targetProvides.All() {
						merged.Set(k, v)
					}
					bkeys := make([]string, 0, len(bprov))
					for k := range bprov {
						bkeys = append(bkeys, k)
					}
					sort.Strings(bkeys)
					for _, k := range bkeys {
						merged.Set(k, fmt.Sprint(bprov[k]))
					}
					targetProvides = merged
					ok = true
				}
			}
		}

		if !ok {
			continue
		}

		// Resolve env inject keys
		resolveInjectKeys(&inject.Env, edge.Inject, "env", targetProvides)
		resolveInjectKeys(&inject.File, edge.Inject, "file", targetProvides)
		resolveInjectKeys(&inject.Arg, edge.Inject, "arg", targetProvides)

		// A mechanism the edge names that is not one of the three typed ones
		// resolves through the same per-key resolver and lands under its own
		// name. The resolver is already mechanism-parametric, so the mechanism
		// name is the only thing that differs from the three calls above.
		for _, mech := range customInjectMechanisms(edge.Inject) {
			dst, ok := inject.Additional.Get(mech)
			if !ok {
				dst = types.OrderedMap[string]{}
			}
			resolveInjectKeys(&dst, edge.Inject, mech, targetProvides)
			inject.Additional.Set(mech, dst)
		}
	}

	// Rebuild .all after resolution
	inject.All = buildInjectAll(*inject)
}

// customInjectMechanisms names the mechanisms of an inject container that are
// not one of the three typed ones, in sorted order.
//
// It is the one place that decides which mechanism names are typed, so a
// delivery site cannot disagree with another about whether a name is custom;
// and it is generic over the payload because the containers differ by
// authoring surface — a component's own inject block carries an undecoded
// payload per mechanism, an edge's and an attachment's carry resolved
// delivery maps. Sorted rather than author order because the union view and
// every other delivery site already emit custom mechanisms sorted, and one
// deterministic order across them all is what makes the emission comparable.
func customInjectMechanisms[V any](container types.OrderedMap[V]) []string {
	var custom []string
	for mech := range container.All() {
		if mech == "env" || mech == "file" || mech == "arg" {
			continue
		}
		custom = append(custom, mech)
	}
	sort.Strings(custom)
	return custom
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

// normalizeInjectMechanism converts one inject mechanism — the list of
// {in, from?, value?} entries the inject_delivery definition describes — to an
// OrderedMap[string] in the order the author wrote the entries. value: wins
// over from: where an entry carries both, and an entry whose in: does not
// decode is skipped. A mechanism that is not a list yields an empty map.
func normalizeInjectMechanism(raw any) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	entries, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, entry := range entries {
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
	return out
}
