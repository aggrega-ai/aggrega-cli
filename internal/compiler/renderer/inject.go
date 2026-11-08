// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"iter"
	"slices"
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
				if made, hit := resolveBoundaryScopeFREvaluation(boundaryFRState, consumerBoundaryPath, edge.OriginFeatureInstance); hit {
					merged := types.OrderedMap[string]{}
					for k, v := range targetProvides.All() {
						merged.Set(k, v)
					}
					for k, v := range made.Provides.All() {
						merged.Set(k, v)
					}
					targetProvides = merged
					ok = true
				}
			}
		}

		if !ok {
			continue
		}

		for mech, keymap := range deliveryMappingMechanisms(edge.Inject) {
			dest, keep := injectPipeMechanism(inject, mech)
			resolveInjectKeys(dest, keymap, targetProvides)
			keep()
		}
	}

	// Rebuild .all after resolution
	inject.All = buildInjectAll(*inject)
}

// typedInjectMechanisms are the mechanisms the pipe carries in fields of its own,
// in the order deliveryMappingMechanisms yields them. Every other mechanism name is custom.
var typedInjectMechanisms = [...]string{"env", "file", "arg"}

// deliveryMappingMechanisms yields each mechanism a resolved delivery mapping
// names — a uses edge's or an attachment's, keyed by mechanism to the local key and
// the provides key it reads — with that mechanism's key map, in the order the
// deliveries resolve them: env, file and arg, each only where the mapping names it,
// then every other mechanism in the order customInjectMechanisms gives.
//
// The edge delivery, the cross-boundary placeholder stamp and the attachment
// delivery all walk a mapping through it, so a mechanism the edge delivers is a
// mechanism the stamp reaches, whatever its name, and an attachment's claims come
// back in that one order.
func deliveryMappingMechanisms(mapping types.OrderedMap[types.OrderedMap[string]]) iter.Seq2[string, types.OrderedMap[string]] {
	return func(yield func(string, types.OrderedMap[string]) bool) {
		for _, mech := range typedInjectMechanisms {
			keymap, ok := mapping.Get(mech)
			if !ok {
				continue
			}
			if !yield(mech, keymap) {
				return
			}
		}
		for _, mech := range customInjectMechanisms(mapping) {
			keymap, _ := mapping.Get(mech)
			if !yield(mech, keymap) {
				return
			}
		}
	}
}

// injectPipeMechanism returns the pipe's delivery map for one mechanism, and keep,
// which records in the pipe what the caller left in that map. env, file and arg
// are the pipe's own fields, so the map is the field itself and keep does nothing.
// Any other mechanism lives in Additional under its own name, held by value: the
// map handed back is that value — empty when the pipe carries nothing under that
// name yet — and it shares its storage with the entry in the pipe rather than
// copying it, so a write to it is not wholly the pipe's until keep stores it back.
// A caller that writes calls keep; a caller that only reads, or finds nothing to
// change, does not, and the pipe is left exactly as it was.
func injectPipeMechanism(inject *types.InjectPipe, mechanism string) (dest *types.OrderedMap[string], keep func()) {
	switch mechanism {
	case "env":
		return &inject.Env, func() {}
	case "file":
		return &inject.File, func() {}
	case "arg":
		return &inject.Arg, func() {}
	}
	held, ok := inject.Additional.Get(mechanism)
	if !ok {
		held = types.OrderedMap[string]{}
	}
	return &held, func() { inject.Additional.Set(mechanism, held) }
}

// customInjectMechanisms names the mechanisms of an inject container that are
// not one of the three typed ones, in sorted order.
//
// It decides which names are custom by typedInjectMechanisms alone, so a
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
		if slices.Contains(typedInjectMechanisms[:], mech) {
			continue
		}
		custom = append(custom, mech)
	}
	sort.Strings(custom)
	return custom
}

func resolveInjectKeys(dest *types.OrderedMap[string], mechanismMap types.OrderedMap[string], targetProvides types.OrderedMap[string]) {
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

// normalizeInjectMechanism converts one mechanism of a component's own inject
// block to the pipe's starting shape: one key per entry, in the order the author
// wrote them, holding the entry as authored — its from: provides key, or else its
// value: text. The self-inject delivery replaces every one of those values with
// what the entry resolves to before anything reads the pipe, so what the pipe
// takes from here is its keys and their order.
//
// The entries are the delivery's own, read through normalizeSelfInjectEntries,
// and which half of an entry is read is [selfInjectEntry.providesKey]'s answer —
// so the pipe starts with exactly the keys the delivery resolves, in the order the
// author wrote them, and an entry carrying both halves is read the way it is
// delivered. A mechanism that is not a list yields an empty map.
func normalizeInjectMechanism(raw any) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	entries := normalizeSelfInjectEntries(raw)
	for in, entry := range entries.All() {
		if from, ok := entry.providesKey(); ok {
			out.Set(in, from)
			continue
		}
		out.Set(in, entry.value)
	}
	return out
}
