// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateComponentAndFeatureAssertions runs Step 4b: per-instance
// evaluation of every Component and Feature assertion in the IR graph.
// Walk order is deterministic-sorted-nodeID. Per node:
//
//  1. Resolve the bound Component via catalogLookup. If Component.Spec.Assertions
//     has entries, walk them in author order; each named assertion evaluates
//     against the per-node AssertionContext built from ExtractNodeBaseContext.
//     The first failing assertion halts.
//  2. For each Feature attached to the node (per IR FeatureValuesMapping),
//     resolve the Feature catalog entity; if Feature.Spec.Assertions has
//     entries, walk in author order; evaluate per-attachment with a context
//     populated from the Feature's per-attachment values/infra plus the
//     node-scope context. First fail halts.
//
// Per-instance semantics: a Component with N instances produces N
// evaluations (one per node), each with its own AssertionContext bound
// to that node's resolved state.
//
// Step 4b assertions cannot reference Step 5b-only fields (.provides,
// .locals, .recipe). The evaluator uses missingkey=error so accidental
// cross-phase references surface as no-such-key halts with the bad
// reference named.
//
// Returns nil on pass. Returns a non-nil error wrapping ErrAssertionFailed
// on the first failure; the error chain carries the failing entity kind,
// type, instance ID, and assertion name.
//
// What each entity declares sensitive is read here, through the catalog this
// stage already holds, so an assertion whose failure text could carry a declared
// value reports without it exactly as a Recipe's assertion does: a Component's
// against what the Component and the Features attached to the node declare, a
// Feature's against what that Feature declares for the attachment's own pillars.
func EvaluateComponentAndFeatureAssertions(
	graph *types.IRGraph,
	catalogLookup resolver.CatalogLookup,
) error {
	if graph == nil {
		return nil
	}
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	readers := newDeclarationReaders(catalogLookup)

	for _, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		base := ExtractNodeBaseContext(nodeID, graph)
		attachments, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
		declared := nodeDeclaration{
			component: readers.component(node.Component),
			own:       ownAttachmentKeys(attachments, readers.feature),
		}

		if catalogLookup.Component != nil {
			if comp, found := catalogLookup.Component(node.Component); found && comp.Spec.Assertions.Len() > 0 {
				ctx := AssertionContext{
					Values:   base.Values,
					Infra:    base.Infra,
					Uses:     base.Uses,
					Features: base.Features,
					Metadata: base.Metadata,
				}
				if err := walkAssertions("Component", node.Component, nodeID, comp.Spec.Assertions, ctx, declared.assertionKeys()); err != nil {
					return err
				}
			}
		}

		if catalogLookup.Feature == nil {
			continue
		}
		fvm, hasFeatures := graph.Spec.FeatureValuesMapping.Get(nodeID)
		if !hasFeatures || fvm.Len() == 0 {
			continue
		}
		for instanceName, entry := range fvm.All() {
			feat, found := catalogLookup.Feature(entry.Type)
			if !found || feat.Spec.Assertions.Len() == 0 {
				continue
			}
			ctx := AssertionContext{
				Values:   entry.Values.TemplateAlphabeticalMapDeep(),
				Infra:    entry.Infra.TemplateAlphabeticalMapDeep(),
				Uses:     base.Uses,
				Features: base.Features,
				Metadata: featureMetadata(base.Metadata, entry, instanceName),
			}
			if err := walkAssertions("Feature", entry.Type, nodeID, feat.Spec.Assertions, ctx,
				attachmentAssertionKeys(declared, readers.feature(entry.Type))); err != nil {
				return err
			}
		}
	}
	return nil
}

// attachmentAssertionKeys is the set for an assertion evaluated against one
// attachment's own values and infra, with the node's other pillars beside them:
// the attachment's Feature governs the values and infra, and the node's
// declarations govern everything else the context carries.
func attachmentAssertionKeys(node nodeDeclaration, leaves resolver.SensitiveLeaves) redact.Keys {
	keys := node.assertionKeys()
	keys.Values = leaves.Values
	keys.Infra = leaves.Infra
	return keys
}

// walkAssertions iterates the OrderedMap entries in author order and
// evaluates each named assertion. First failing assertion short-circuits.
// The returned error wraps ErrAssertionFailed with the failing entity
// kind, type, instance ID, and assertion name.
func walkAssertions(
	entityKind, entityType, instanceID string,
	assertions types.OrderedMap[string],
	ctx AssertionContext,
	withheld redact.Keys,
) error {
	for name, body := range assertions.All() {
		qualified := fmt.Sprintf("%s/%s/%s/%s", entityKind, entityType, instanceID, name)
		if err := EvaluateAssertion(qualified, body, ctx, withheld); err != nil {
			return fmt.Errorf("%s %q instance %q assertion %q: %w",
				entityKind, entityType, instanceID, name, err)
		}
	}
	return nil
}

// featureMetadata extends the node-scope metadata with Feature-specific
// identity (instance name + resolved version) for use in Feature assertion
// contexts.
func featureMetadata(base map[string]any, entry types.IRFeatureEntry, instanceName string) map[string]any {
	out := make(map[string]any, len(base)+2)
	for k, v := range base {
		out[k] = v
	}
	out["feature_instance"] = instanceName
	out["feature_type"] = entry.Type
	out["feature_resolved_version"] = entry.ResolvedVersion
	return out
}
