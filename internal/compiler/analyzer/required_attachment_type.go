// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateRequiredAttachmentTypes enforces every presence mandate a node's
// Component catalog entity declares — each
// features.aggrega.ai/requiredType.<label> annotation — against the node's
// final attachment set.
//
// This is the single home of the rule "a required attachment type is absent".
// It runs over the finalised graph because that is the one stage where both
// halves of the rule are in hand: the resolved Component entity's own
// annotations (projected onto the node as its catalog annotation bucket) and
// the node's merged attachment set after the whole attachment cascade — every
// tier, every scope, plus attachments produced by annotation-driven expansion
// — has been folded. Checking earlier means re-deriving an approximation of
// that set from the authored Aggregate and a hand-walked Release, which is
// blind to flattened sub-aggregate nodes and to scope precedence.
//
// The satisfying set is the union of attachment types across the node's two
// attachment buckets, and a mandate matches by exact type equality. Presence
// is all the rule tests: it does not care which scope supplied the attachment,
// whether a slot declared the name, or how many attachments of the type are
// present. Cardinality is at-least-one.
//
// A mandate whose declaration is broken — a key with no label, or a value that
// is not a Feature type URI — is reported as a malformed declaration and its
// requirement is NOT tested, so a typo can never read as a satisfied
// requirement. The two outcomes carry different sentinels because they have
// different remediations: fix the catalog entity, versus attach the feature.
//
// Nodes iterate in sorted node order and mandates in the annotation map's
// author order, so the emitted violation set is byte-identical across runs of
// the same input. A component absent from the catalog is skipped: Step 1's
// guards already reject an unresolvable component reference.
func validateRequiredAttachmentTypes(ctx context.Context, graph *types.IRGraph) ([]AnalysisError, error) {
	var errs []AnalysisError

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
		mandates := types.RequiredAttachmentTypes(node.CatalogAnnotations)
		if len(mandates) == 0 {
			continue
		}

		attached := attachedTypes(graph, nodeID)
		field := componentInstanceFieldPath(nodeID, node.SubsystemPath, node.SynthesizedSubsystem)
		origin := sourceFileFor(graph, nodeID)

		for _, m := range mandates {
			switch {
			case m.Malformed != "":
				errs = append(errs, AnalysisError{
					Kind:   AnalysisErrorAttachmentRequirement,
					Source: nodeID, Target: m.Key,
					OriginFile: origin,
					Field:      field,
					Message: fmt.Sprintf(
						"component type %q declares a malformed attachment requirement in annotation %q: %s",
						node.Component, m.Key, m.Malformed),
					Sentinel: ErrMalformedAttachmentRequirement,
				})
			case !attached[m.Type]:
				errs = append(errs, AnalysisError{
					Kind:   AnalysisErrorAttachmentRequirement,
					Source: nodeID, Target: m.Key,
					OriginFile: origin,
					Field:      field,
					Message: fmt.Sprintf(
						"component type %q requires an attachment of type %q for %q, but none is attached; "+
							"attach a Feature of that type to the component, or at a scope it inherits from",
						node.Component, m.Type, m.Label),
					Sentinel: resolver.ErrMissingRequiredFeature,
				})
			}
		}
	}
	return errs, nil
}

// attachedTypes returns the node's satisfying set: the non-empty attachment
// types across both of the node's attachment buckets. The map is a
// presence-set; values are always true.
func attachedTypes(graph *types.IRGraph, nodeID string) map[string]bool {
	attached := map[string]bool{}
	if feats, ok := graph.Spec.FeatureValuesMapping.Get(nodeID); ok {
		for _, entry := range feats.All() {
			if entry.Type != "" {
				attached[entry.Type] = true
			}
		}
	}
	if secrets, ok := graph.Spec.Secrets.Get(nodeID); ok {
		for _, entry := range secrets.All() {
			if entry.Type != "" {
				attached[entry.Type] = true
			}
		}
	}
	return attached
}

// componentInstanceFieldPath builds the mapping-style YAML locator for a
// violation whose authoring site is the component instance itself — the
// component key under .spec.components.
//
// The synthesized-provenance flag is what decides the shape, not the path
// string. Normalization folds an Aggregate's top-level components into a
// synthesized `default` subsystem, so a root component reaches here carrying a
// non-empty subsystem path it was never authored under; rendering the
// qualified path for it would name a location the source file does not
// contain, and the locator would fall back to the head of the file. A
// component in a subsystem the author actually declared yields "" instead: its
// authored site is under .spec.subsystems.*, which this shape cannot express,
// and an empty locator routes the consumer to file-level positioning rather
// than to a wrong key.
func componentInstanceFieldPath(nodeID, subsystemPath string, synthesizedSubsystem bool) string {
	if subsystemPath != "" && !synthesizedSubsystem {
		return ""
	}
	return fmt.Sprintf(".spec.components.%s", lastSegment(nodeID))
}
