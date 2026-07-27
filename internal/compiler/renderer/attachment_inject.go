// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ResolveAttachmentInject delivers authored attachment-level inject into the
// node's delivery pipe.
//
// An attachment-level inject value is a provides-key reference resolved
// against the attachment's OWN resolved provides — its FeatureRecipe's provides
// evaluated against the attachment's values/infra/locals — delivered into the
// host component's .context.inject under the local delivery key. This is
// distinct from uses[].inject (a named dependency target's provides, resolved
// in Pass 2b) and from component.inject (the host's own provides / literals,
// resolved in Pass 2a).
//
// The pipe already carries the Pass 2a (self) and Pass 2b (uses) delivery keys
// when this runs, so a delivery key already present is a duplicate across
// developer-authored sources and halts with ErrAttachmentInjectKeyCollision. The
// FeatureRecipe injectMapping interception in Phase A is a later, last-wins
// overlay and is intentionally not a collision. Attachments are processed in
// IR (author) order for deterministic emission.
func ResolveAttachmentInject(
	inject *types.InjectPipe,
	graph *types.IRGraph,
	nodeID string,
	recipeLookup RecipeLookup,
) error {
	if recipeLookup.FeatureRecipe == nil {
		return nil
	}
	entries, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		inject.All = buildInjectAll(*inject)
		return nil
	}
	for instanceName, entry := range entries.All() {
		if entry.Inject.Len() == 0 {
			continue
		}
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
		if !found {
			continue
		}
		provided, err := evalAttachmentFRProvides(fr, instanceName, nodeID, entry)
		if err != nil {
			return err
		}
		if err := deliverAttachmentInjectKeys(inject, instanceName, entry.Inject, provided, nodeID); err != nil {
			return err
		}
	}
	inject.All = buildInjectAll(*inject)
	return nil
}

// evalAttachmentFRProvides evaluates the attachment FeatureRecipe's provides
// against the attachment's own scope (instance_id + values + infra + the FR's
// own evaluated locals), mirroring the boundary-scope FR provides evaluation.
// The resolved provides are the source the authored inject's provides-key
// references resolve against.
//
// The locals resolve first, through evaluateLocalsInOrder — the one home of the
// locals contract — so an entry may read an earlier sibling and a forward or
// unknown sibling reference halts. The provides then evaluate against the same
// scope carrying the fully resolved block, which is what lets a provides template
// read .locals and receive resolved values; a provides template naming a local the
// block does not declare halts through the same shared refusal.
func evalAttachmentFRProvides(fr *types.FeatureRecipe, instanceName, nodeID string, entry types.IRFeatureEntry) (types.OrderedMap[string], error) {
	resolvedLocals, err := evaluateLocalsInOrder(
		fr.Spec.Locals,
		func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("node %q attachment %q locals[%q]", nodeID, instanceName, key),
				File:  fr.Metadata.Name,
				Key:   key,
			}
		},
		func(key string) string { return fmt.Sprintf("attach-inject-local[%s.%s]", instanceName, key) },
		func(resolved types.OrderedMap[string]) any {
			return ProvidesContext{
				Context:        ProvidesContextBlock{InstanceID: nodeID},
				Values:         entry.Values,
				Infra:          entry.Infra,
				Locals:         resolved,
				AttachmentName: instanceName,
			}
		},
	)
	if err != nil {
		return types.OrderedMap[string]{}, err
	}
	if err := checkProvidesLocalsReferences(fr.Spec.Provides, resolvedLocals, func(key string) localsRefSite {
		return localsRefSite{
			Where: fmt.Sprintf("node %q attachment %q provides[%q]", nodeID, instanceName, key),
			File:  fr.Metadata.Name,
		}
	}); err != nil {
		return types.OrderedMap[string]{}, err
	}
	scope := ProvidesContext{
		Context:        ProvidesContextBlock{InstanceID: nodeID},
		Values:         entry.Values,
		Infra:          entry.Infra,
		Locals:         resolvedLocals,
		AttachmentName: instanceName,
	}
	provided := types.OrderedMap[string]{}
	for k, tmplText := range fr.Spec.Provides.All() {
		out, err := EvalTemplate(fmt.Sprintf("attach-inject-provides[%s.%s]", instanceName, k), tmplText, scope)
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("node %q attachment %q provides[%q]", nodeID, instanceName, k))
		}
		provided.Set(k, strings.TrimSpace(out))
	}
	return provided, nil
}

// enforceProvidesContract halts when a realizer's produced provides bag omits
// any key the abstract entity declares as its output contract. The contract is
// structural key-presence — subset-required, superset-permitted: every declared
// key must appear in produced, while extra produced keys are tolerated. Produced
// values are never inspected (they are opaque runtime pointers). Declared keys
// are checked in sorted order so the first missing key reported, and the halt,
// are byte-identical across runs. contextPrefix locates the binding (e.g.
// `node "x" attachment "y"`); realizer and abstract are the pre-quoted entity
// descriptors (e.g. `Recipe "z"`, `Component "core/service"`) the diagnostic
// names. nodeID sets the halt's source location. A single home for the one
// realizing-side fulfilment semantic shared by every realizer/abstract pair.
func enforceProvidesContract(declared map[string]bool, produced types.OrderedMap[string], nodeID, contextPrefix, realizer, abstract string) error {
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, has := produced.Get(key); !has {
			return cerrors.Wrap(ErrProvidesContractUnfulfilled, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("%s: %s does not publish provides key %q declared by %s's schema.provides contract", contextPrefix, realizer, key, abstract))
		}
	}
	return nil
}

// ResolveAttachmentProvides resolves every attachment in one bucket against its
// bound FeatureRecipe's `provides` ONCE — before the consumer renders — keyed by
// attachment instance name, and enforces the producer-fulfilment contract: the
// bound FeatureRecipe must publish every provides key the attachment's Feature
// declares in schema.provides; a missing key halts with
// ErrProvidesContractUnfulfilled (structural key-presence, never a judgement of
// the value).
//
// This is the one home for resolving an attachment's provides. Every attachment
// routes through it, so the published output a consumer reads at
// .features.<name>.provides, the contract check, and the attachment
// FeatureRecipe's own .provides at Phase B.6 all derive from a single
// evaluation and cannot drift. An attachment with no bound FeatureRecipe is
// skipped; instances are walked in sorted order so the first-reported shortfall
// is deterministic.
func ResolveAttachmentProvides(attachments types.OrderedMap[types.IRFeatureEntry], nodeID string, recipeLookup RecipeLookup, catalogLookup resolver.CatalogLookup) (map[string]types.OrderedMap[string], error) {
	if recipeLookup.FeatureRecipe == nil || attachments.Len() == 0 {
		return nil, nil
	}
	out := map[string]types.OrderedMap[string]{}
	instances := attachments.Keys()
	sort.Strings(instances)
	for _, instanceName := range instances {
		entry, _ := attachments.Get(instanceName)
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
		if !found || fr == nil {
			continue
		}
		provided, err := evalAttachmentFRProvides(fr, instanceName, nodeID, entry)
		if err != nil {
			return nil, err
		}
		// Producer-fulfilment: the bound FeatureRecipe must publish every provides
		// key the attachment's Feature declares as its output contract.
		if catalogLookup.Feature != nil {
			if feat, ffound := catalogLookup.Feature(entry.Type); ffound {
				contract := resolver.FeatureProvidesKeys(feat, catalogLookup.SchemaLibraries)
				if err := enforceProvidesContract(contract, provided, nodeID,
					fmt.Sprintf("node %q attachment %q", nodeID, instanceName),
					fmt.Sprintf("FeatureRecipe %q", fr.Metadata.Name),
					fmt.Sprintf("Feature %q", entry.Type)); err != nil {
					return nil, err
				}
			}
		}
		out[instanceName] = provided
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// deliverAttachmentInjectKeys resolves each delivery key's provides-key
// reference against the attachment's resolved provides and sets the resolved
// value into the pipe under the local delivery key. A delivery key already
// present in the pipe (from self-inject, uses-inject, or a prior attachment)
// halts with ErrAttachmentInjectKeyCollision. An unresolved provides-key (absent from the
// attachment's provides) delivers nothing (missingkey=zero semantics).
// Mechanisms are processed env, file, arg, then custom mechanisms in sorted
// order for deterministic emission.
func deliverAttachmentInjectKeys(inject *types.InjectPipe, instanceName string, entryInject types.OrderedMap[types.OrderedMap[string]], provided types.OrderedMap[string], nodeID string) error {
	set := func(dest *types.OrderedMap[string], mech, localKey, providesKey string) error {
		if _, exists := dest.Get(localKey); exists {
			return cerrors.Wrap(
				fmt.Errorf("%w: key %q in mechanism %q (attachment %q)", ErrAttachmentInjectKeyCollision, localKey, mech, instanceName),
				cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q attachment %q inject delivery-key collision", nodeID, instanceName))
		}
		val, ok := provided.Get(providesKey)
		if !ok {
			return nil
		}
		dest.Set(localKey, val)
		return nil
	}
	for _, mt := range []struct {
		name string
		dest *types.OrderedMap[string]
	}{{"env", &inject.Env}, {"file", &inject.File}, {"arg", &inject.Arg}} {
		mechMap, ok := entryInject.Get(mt.name)
		if !ok {
			continue
		}
		for localKey, providesKey := range mechMap.All() {
			if err := set(mt.dest, mt.name, localKey, providesKey); err != nil {
				return err
			}
		}
	}
	var customMechs []string
	for mech := range entryInject.All() {
		if mech == "env" || mech == "file" || mech == "arg" {
			continue
		}
		customMechs = append(customMechs, mech)
	}
	sort.Strings(customMechs)
	for _, mech := range customMechs {
		mechMap, _ := entryInject.Get(mech)
		dst, ok := inject.Additional.Get(mech)
		if !ok {
			dst = types.OrderedMap[string]{}
		}
		for localKey, providesKey := range mechMap.All() {
			if err := set(&dst, mech, localKey, providesKey); err != nil {
				return err
			}
		}
		inject.Additional.Set(mech, dst)
	}
	return nil
}
