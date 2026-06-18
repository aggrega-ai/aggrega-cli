// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateSecretSlotSelectors runs the secretsSchema-slot selector checks at
// Step 4 — the credential analog of validateSelectors (usesSchema) and
// validateAppliesTo (Feature appliesTo), reusing pkg/selector:
//
//  1. Secret-kind constraint (structural, over every bound Component's and
//     attached Feature's slots): a slot whose `selector` is non-empty must
//     constrain its fill to secret-kind Features — its admitted set ⊆
//     {feature.aggrega.ai/kind=secret}. A present selector that does not pin
//     halts with ErrSecretSlotSelectorNotSecretKind. An absent selector is
//     fine: the secrets-pillar partition already guarantees a secret-kind fill;
//     a selector only narrows within that partition, never broadens it back out.
//  2. Fill satisfaction (per secret attachment): when the filled slot declares
//     a selector, the attached credential Feature's labels must satisfy it, or
//     the fill halts with ErrSecretSlotSelectorUnsatisfied.
//
// Catalog misses skip silently (Step 1's resolver guards cover the user-facing
// case), mirroring validateAppliesTo.
func validateSecretSlotSelectors(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) ([]AnalysisError, error) {
	var errs []AnalysisError

	// Check 1 — secret-kind constraint over every distinct bound entity's slots.
	checked := map[string]bool{} // entity type → done
	if catalogLookup.Component != nil {
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			node, ok := graph.Spec.Nodes.Get(nodeID)
			if !ok || checked[node.Component] {
				continue
			}
			checked[node.Component] = true
			if comp, found := catalogLookup.Component(node.Component); found {
				errs = append(errs, secretSlotKindErrors(comp.Spec.SecretsSchema, node.Component)...)
			}
		}
	}
	if catalogLookup.Feature != nil {
		for _, featType := range attachedFeatureTypes(graph) {
			if checked[featType] {
				continue
			}
			checked[featType] = true
			if feat, found := catalogLookup.Feature(featType); found {
				errs = append(errs, secretSlotKindErrors(feat.Spec.SecretsSchema, featType)...)
			}
		}
	}

	// Check 2 — fill satisfaction for each node's secret attachments.
	if catalogLookup.Component != nil && catalogLookup.Feature != nil {
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			node, ok := graph.Spec.Nodes.Get(nodeID)
			if !ok {
				continue
			}
			attachments, ok := graph.Spec.Secrets.Get(nodeID)
			if !ok || attachments.Len() == 0 {
				continue
			}
			comp, found := catalogLookup.Component(node.Component)
			if !found {
				continue
			}
			slotNames := attachments.Keys()
			sort.Strings(slotNames)
			for _, slotName := range slotNames {
				slot, slotOK := comp.Spec.SecretsSchema.Get(slotName)
				if !slotOK || slot.Selector == "" {
					continue
				}
				entry, _ := attachments.Get(slotName)
				feat, featFound := catalogLookup.Feature(entry.Type)
				if !featFound {
					continue
				}
				match, perr := selector.MatchString(slot.Selector, feat.Metadata.Labels)
				if perr != nil {
					errs = append(errs, AnalysisError{
						Source: nodeID, Target: slotName, Selector: slot.Selector,
						Message:  fmt.Sprintf("secret slot %q selector parse error: %v", slotName, perr),
						Sentinel: ErrSecretSlotSelectorUnsatisfied,
					})
					continue
				}
				if !match {
					errs = append(errs, AnalysisError{
						Source: nodeID, Target: slotName, Selector: slot.Selector,
						Message: fmt.Sprintf("secret slot %q on component %q: attached Feature %q labels do not satisfy the slot selector %q",
							slotName, node.Component, entry.Type, slot.Selector),
						Sentinel: ErrSecretSlotSelectorUnsatisfied,
					})
				}
			}
		}
	}
	return errs, nil
}

// attachedFeatureTypes returns the sorted, de-duplicated set of Feature type
// URIs attached anywhere in the graph (feature and secret attachments).
func attachedFeatureTypes(graph *types.IRGraph) []string {
	seen := map[string]bool{}
	collect := func(m types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]) {
		for _, inst := range m.All() {
			for _, e := range inst.All() {
				if e.Type != "" {
					seen[e.Type] = true
				}
			}
		}
	}
	collect(graph.Spec.FeatureValuesMapping)
	for _, secs := range graph.Spec.Secrets.All() {
		for _, e := range secs.All() {
			if e.Type != "" {
				seen[e.Type] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// secretSlotKindErrors checks each slot's declared selector against the
// secret-kind constraint, in slot-name order.
func secretSlotKindErrors(slots types.OrderedMap[types.SecretSlot], entityName string) []AnalysisError {
	var errs []AnalysisError
	names := slots.Keys()
	sort.Strings(names)
	for _, slotName := range names {
		slot, _ := slots.Get(slotName)
		if slot.Selector == "" {
			continue
		}
		reqs, perr := selector.Parse(slot.Selector)
		if perr != nil {
			errs = append(errs, AnalysisError{
				Source: entityName, Target: slotName, Selector: slot.Selector,
				Message:  fmt.Sprintf("secret slot %q selector parse error: %v", slotName, perr),
				Sentinel: ErrSecretSlotSelectorNotSecretKind,
			})
			continue
		}
		if !selectorPinsToSecretKind(reqs) {
			errs = append(errs, AnalysisError{
				Source: entityName, Target: slotName, Selector: slot.Selector,
				Message: fmt.Sprintf("secret slot %q selector %q admits non-secret Features; a slot selector must constrain its fill to feature.aggrega.ai/kind=secret",
					slotName, slot.Selector),
				Sentinel: ErrSecretSlotSelectorNotSecretKind,
			})
		}
	}
	return errs
}

// selectorPinsToSecretKind reports whether the parsed selector pins its fill to
// the secret kind: it carries a feature.aggrega.ai/kind requirement whose
// admitted set is exactly {secret} — an equality to "secret", or an `in` set of
// exactly {"secret"}. With AND semantics across clauses, any such clause pins
// the whole selector. A `kind!=`, bare `kind` (exists), `!kind` (doesnotexist),
// `in (secret, other)`, or a selector carrying no kind clause at all does not pin.
func selectorPinsToSecretKind(reqs []selector.Requirement) bool {
	for _, r := range reqs {
		if r.Key != types.FeatureKindLabel {
			continue
		}
		switch r.Operator {
		case selector.OpEquals, selector.OpIn:
			if len(r.Values) == 1 && r.Values[0] == types.FeatureKindSecret {
				return true
			}
		}
	}
	return false
}
