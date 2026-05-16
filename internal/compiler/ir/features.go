// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// featureResult holds the normalized features and secrets for one node.
type featureResult struct {
	Features     types.OrderedMap[types.IRFeatureEntry]
	Secrets      types.OrderedMap[types.IRSecretEntry]
	FeatureOrder []string // deterministic order following Onion Model
	Markers      []string // canonical feature type strings
}

// featureProvenance is the trio mergeFeature stamps on the IR entry at
// first-sight insertion. Origin is "explicit" for author-declared
// features (Tiers 1-4 plus Tier 5 entries with no matching side-table
// record) and "annotation" for synthetic attachments produced by the
// annotation-driven expansion pass (Tier 5 entries matched in
// ResolveResult.AnnotationProvenance). AnnotationKey + AnnotationSource
// populate only on the annotation path; both are empty when Origin is
// "explicit".
type featureProvenance struct {
	Origin           string
	AnnotationKey    string
	AnnotationSource string
}

// explicitProvenance is the sentinel value passed to mergeFeature from
// the Aggregate-side, Subsystem, and Boundary tiers — all of which
// produce explicit attachments.
var explicitProvenance = featureProvenance{Origin: "explicit"}

// collectNodeFeatures assembles the merged Feature list for one node by
// processing five ordered cascade tiers in sequence. mergeFeature handles
// per-instance "last wins" on values, the type-mismatch fatal halt that
// guards same-instance-name divergence between Aggregate and Release,
// and the credential-routing branch that diverts kind:secret features
// into the per-node Secrets map rather than Features.
//
// Tier order, with the data sources each tier reads from:
//
//   - Tier 1 — Sub-aggregate component features. Already collected into
//     topoResult.FeatureAttachments by the topology builder's
//     flattenAggregate; iterated here in attachment order.
//   - Tier 2 — Root Aggregate component features. Same source; the
//     topology builder appends Tier 2 attachments after Tier 1 so a
//     single iteration over FeatureAttachments processes both in the
//     correct outer-to-inner order.
//   - Tier 3 — Root Aggregate subsystem features (outer-to-inner).
//     collectSubsystemFeatures walks the subsystem path on the resolved
//     Aggregate, emitting features at each level in author order.
//   - Tier 4 — Release boundary-wide features (outer-to-inner).
//     collectBoundaryWideFeatures walks the boundary path on the Release
//     and reads boundary-level Features. Release contributions to a
//     same-named instance are infra-only — values come from the
//     Aggregate side, infra from the Release side; see the post-merge
//     infra overlay block here.
//   - Tier 5 — Release component-specific features.
//     collectReleaseComponentFeatures recursively scans every boundary's
//     per-component Features map for entries keyed by nodeID.
//
// After the five tiers, secret attachments from topology are added
// directly into result.Secrets — these are typed Secret instances rather
// than label-discriminated credential Features, so they bypass the
// mergeFeature credential-routing branch and land straight in the
// secrets bucket.
//
// Markers (the canonical feature-type strings the IR exposes via
// FeatureMarkers) are accumulated into a set across all five tiers plus
// the Secret pass, then sort-extracted at the end so byte-identical
// input produces byte-identical output across runs.
func collectNodeFeatures(
	nodeID string,
	topoNode *topology.TopologyNode,
	topoResult *topology.TopologyResult,
	resolveResult *resolver.ResolveResult,
	catalogLookup resolver.CatalogLookup,
) (*featureResult, error) {
	result := &featureResult{
		Features: types.OrderedMap[types.IRFeatureEntry]{},
		Secrets:  types.OrderedMap[types.IRSecretEntry]{},
	}

	markerSet := map[string]bool{}

	// Collect features in Onion Model order.
	// Each tier appends FeatureAttachments; we process them in order.

	// Tier 1: Sub-aggregate component features (from topology attachments of sub-agg origin)
	// Tier 2: Root Aggregate component features (from topology attachments of root origin)
	// These are already collected in topology.FeatureAttachments by flattenAggregate.
	if attachments, ok := topoResult.FeatureAttachments[nodeID]; ok {
		for _, fa := range attachments {
			if err := mergeFeature(result, fa.InstanceName, fa.Feature, markerSet, catalogLookup, nodeID, explicitProvenance); err != nil {
				return nil, err
			}
		}
	}

	// Tier 3: Root Aggregate subsystem features (outer-to-inner)
	if topoNode.SubsystemPath != "" {
		subsystemFeatures := collectSubsystemFeatures(resolveResult.Aggregate, topoNode.SubsystemPath)
		for _, sf := range subsystemFeatures {
			if err := mergeFeature(result, sf.name, sf.feat, markerSet, catalogLookup, nodeID, explicitProvenance); err != nil {
				return nil, err
			}
		}
	}

	// Tier 4: Release boundary-wide features (outer-to-inner)
	if topoNode.BoundaryPath != "" && resolveResult.Release != nil {
		boundaryFeatures := collectBoundaryWideFeatures(resolveResult.Release.Spec.Boundaries, topoNode.BoundaryPath)
		for _, bf := range boundaryFeatures {
			feat := types.FeatureInstance{Type: bf.feat.Type} // Release provides infra only, not values
			if err := mergeFeature(result, bf.name, feat, markerSet, catalogLookup, nodeID, explicitProvenance); err != nil {
				return nil, err
			}
			// Merge infra separately for Release features
			if existing, ok := result.Features.Get(bf.name); ok && bf.feat.Infra.Len() > 0 {
				if existing.Infra.Len() == 0 {
					existing.Infra = types.OrderedMap[any]{}
				}
				for k, v := range bf.feat.Infra.All() {
					existing.Infra.Set(k, v)
				}
				result.Features.Set(bf.name, existing)
			}
		}
	}

	// Tier 5: Release component-specific features. Annotation-driven
	// synthetic attachments (produced by resolver.ExpandAnnotations during
	// Step 1) live in BoundaryComponent.Features alongside author-declared
	// explicit attachments and are indistinguishable here. The IR layer
	// recovers the distinction by consulting
	// resolveResult.AnnotationProvenance: a side-table entry signals an
	// annotation-driven attachment; absence signals explicit.
	if resolveResult.Release != nil {
		compFeatures := collectReleaseComponentFeatures(resolveResult.Release.Spec.Boundaries, nodeID)
		for _, cf := range compFeatures {
			feat := types.FeatureInstance{Type: cf.feat.Type}
			prov := explicitProvenance
			if record, ok := resolveResult.AnnotationProvenance[resolver.AnnotationProvenanceKey(topoNode.BoundaryPath, nodeID, cf.name)]; ok {
				prov = featureProvenance{
					Origin:           "annotation",
					AnnotationKey:    record.AnnotationKey,
					AnnotationSource: record.AnnotationSource,
				}
			}
			if err := mergeFeature(result, cf.name, feat, markerSet, catalogLookup, nodeID, prov); err != nil {
				return nil, err
			}
			if existing, ok := result.Features.Get(cf.name); ok && cf.feat.Infra.Len() > 0 {
				if existing.Infra.Len() == 0 {
					existing.Infra = types.OrderedMap[any]{}
				}
				for k, v := range cf.feat.Infra.All() {
					existing.Infra.Set(k, v)
				}
				result.Features.Set(cf.name, existing)
			}
		}
	}

	// Collect secrets from topology attachments
	if attachments, ok := topoResult.SecretAttachments[nodeID]; ok {
		for _, sa := range attachments {
			result.Secrets.Set(sa.InstanceName, types.IRSecretEntry{
				Type:   sa.Secret.Type,
				Values: sa.Secret.Values,
			})
			markerSet[sa.Secret.Type] = true
		}
	}

	// Build sorted markers
	for m := range markerSet {
		result.Markers = append(result.Markers, m)
	}
	sort.Strings(result.Markers)

	return result, nil
}

// mergeFeature accepts one (instanceName, FeatureInstance) pair from one
// of the five cascade tiers and folds it into the per-node featureResult.
// Three rules govern the fold:
//
//   - Credential routing. isCredential checks the feature type URI
//     against the catalog and returns true when the catalog entity
//     carries the feature.aggrega.ai/kind: secret label (see
//     types.FeatureKindLabel / FeatureKindSecret). Credential features
//     route into result.Secrets and never appear in result.Features —
//     this preserves the architectural separation between operational
//     features and credentials at the IR layer.
//   - Type-match fatal halt. When an existing feature entry for
//     instanceName carries a non-empty Type that disagrees with the
//     incoming Type, the merge halts with ErrFeatureTypeMismatch
//     wrapping the conflict. The check is what makes the pillar-split
//     merge (values from Aggregate, infra from Release) coherent: same
//     instance name across tiers must describe the same Feature.
//   - "Last wins" deep-merge on values. When the existing entry is
//     compatible (same type or untyped on either side), incoming Values
//     entries are written over existing ones key-by-key — later tiers
//     override earlier tiers per-key without wholesale replacement.
//     FeatureOrder is appended only on first sight of an instance name,
//     so the order vector is stable across re-merges of the same
//     instance.
//
// The markerSet parameter aggregates feature-type strings across all
// tiers; the caller sort-extracts at the end of collectNodeFeatures.
func mergeFeature(
	result *featureResult,
	instanceName string,
	feat types.FeatureInstance,
	markerSet map[string]bool,
	catalogLookup resolver.CatalogLookup,
	nodeID string,
	provenance featureProvenance,
) error {
	// Check if credential feature
	if isCredential(feat.Type, catalogLookup) {
		result.Secrets.Set(instanceName, types.IRSecretEntry{
			Type:   feat.Type,
			Values: feat.Values,
		})
		markerSet[feat.Type] = true
		return nil
	}

	existing, exists := result.Features.Get(instanceName)
	if !exists {
		// New feature — first sight wins for provenance fields.
		result.Features.Set(instanceName, types.IRFeatureEntry{
			Type:             feat.Type,
			Values:           feat.Values,
			Origin:           provenance.Origin,
			AnnotationKey:    provenance.AnnotationKey,
			AnnotationSource: provenance.AnnotationSource,
		})
		result.FeatureOrder = append(result.FeatureOrder, instanceName)
		markerSet[feat.Type] = true
		return nil
	}

	// Merge: type must match
	if existing.Type != "" && feat.Type != "" && existing.Type != feat.Type {
		return cerrors.Wrap(
			ErrFeatureTypeMismatch,
			cerrors.OpsError,
			cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q feature %q: existing type %q conflicts with %q", nodeID, instanceName, existing.Type, feat.Type),
		)
	}

	// "Last wins" on values
	if feat.Values.Len() > 0 {
		if existing.Values.Len() == 0 {
			existing.Values = types.OrderedMap[any]{}
		}
		for k, v := range feat.Values.All() {
			existing.Values.Set(k, v)
		}
	}
	if feat.Type != "" {
		existing.Type = feat.Type
	}
	result.Features.Set(instanceName, existing)
	markerSet[feat.Type] = true
	return nil
}

func isCredential(typeURI string, catalogLookup resolver.CatalogLookup) bool {
	if catalogLookup.Feature == nil {
		return false
	}
	feat, found := catalogLookup.Feature(typeURI)
	if !found {
		return false
	}
	kind, _ := feat.Metadata.Labels.Get(types.FeatureKindLabel)
	return kind == types.FeatureKindSecret
}

// namedFeature pairs an instance name with a FeatureInstance for ordered collection.
type namedFeature struct {
	name string
	feat types.FeatureInstance
}

// namedBoundaryFeature pairs an instance name with a BoundaryFeature.
type namedBoundaryFeature struct {
	name string
	feat types.BoundaryFeature
}

// collectSubsystemFeatures implements the per-subsystem cascade level:
// for a node sitting inside a subsystem path like "subsys-a/subsys-b",
// walk the Aggregate's subsystem tree from root toward the leaf, emitting
// every subsystem-level Feature attached at each level in OrderedMap
// author order. Outer subsystems are emitted before inner subsystems so
// the same Feature declared at a deeper level overrides one declared at
// a shallower level via "last wins" in mergeFeature.
//
// The path traversal stops short on the first missing path part so that
// a subsystem-path the topology asserts but the resolved Aggregate no
// longer contains (a defensive condition; should not happen on a clean
// pipeline) returns the partial outer-tier list rather than panicking.
func collectSubsystemFeatures(agg *types.Aggregate, subsystemPath string) []namedFeature {
	parts := splitPath(subsystemPath)
	if len(parts) == 0 {
		return nil
	}

	var result []namedFeature
	current := agg.Spec.Subsystems

	for _, part := range parts {
		sub, ok := current.Get(part)
		if !ok {
			break
		}
		// Collect this subsystem's features (author order)
		for fname, feat := range sub.Features.All() {
			result = append(result, namedFeature{name: fname, feat: feat})
		}
		current = sub.Subsystems
	}

	return result
}

// collectBoundaryWideFeatures implements the per-Release-boundary cascade
// level: for a node sitting inside a Release boundary path, walk the
// boundary tree from root toward the leaf, emitting every boundary-level
// Feature attached at each level in author order. Outer boundaries are
// emitted before inner boundaries — same cascade discipline as the
// per-subsystem level.
//
// Boundary feature payloads arrive as untyped any (Boundary
// .Features is OrderedMap[any]) because Release boundary YAML
// authors features as opaque maps the renderer interprets per-feature;
// parseBoundaryFeature below extracts the type and infra fields.
// Mergeable values are NOT collected here — Release boundaries
// contribute infra-only under the pillar-split rule (values from the
// Aggregate, infra from the Release), and collectNodeFeatures applies
// that infra overlay separately after the merge.
func collectBoundaryWideFeatures(boundaries types.OrderedMap[types.Boundary], boundaryPath string) []namedBoundaryFeature {
	parts := splitPath(boundaryPath)
	if len(parts) == 0 {
		return nil
	}

	var result []namedBoundaryFeature
	current := boundaries

	for _, part := range parts {
		boundary, ok := current.Get(part)
		if !ok {
			break
		}
		// Collect boundary-wide features (author order)
		for fname, raw := range boundary.Features.All() {
			bf := parseBoundaryFeature(raw)
			result = append(result, namedBoundaryFeature{name: fname, feat: bf})
		}
		current = boundary.Boundaries
	}

	return result
}

// collectReleaseComponentFeatures implements the most-specific cascade
// level — boundary entries that name a particular component instance
// directly (rather than cascading boundary-wide). For a given nodeID,
// recursively scan every Release boundary's per-component Features map
// and emit the entries keyed by nodeID. This level merges last so it
// wins on collisions with the broader boundary-wide cascade.
//
// The recursion is unconstrained by boundary path: any boundary anywhere
// in the tree may carry a per-component Features map for nodeID, and all
// of them contribute. As with the boundary-wide level, Features-instance
// payloads here arrive as the same opaque BoundaryFeature shape and
// contribute infra only — collectNodeFeatures applies the infra overlay
// after merge.
func collectReleaseComponentFeatures(boundaries types.OrderedMap[types.Boundary], nodeID string) []namedBoundaryFeature {
	var result []namedBoundaryFeature
	collectCompFeaturesRecursive(boundaries, nodeID, &result)
	return result
}

func collectCompFeaturesRecursive(boundaries types.OrderedMap[types.Boundary], nodeID string, result *[]namedBoundaryFeature) {
	for _, boundary := range boundaries.All() {
		if bc, ok := boundary.Components.Get(nodeID); ok {
			for fname, feat := range bc.Features.All() {
				*result = append(*result, namedBoundaryFeature{name: fname, feat: feat})
			}
		}
		collectCompFeaturesRecursive(boundary.Boundaries, nodeID, result)
	}
}

func parseBoundaryFeature(raw any) types.BoundaryFeature {
	bf := types.BoundaryFeature{}
	switch m := raw.(type) {
	case *types.OrderedMap[any]:
		if m == nil {
			return bf
		}
		if t, ok := m.Get("type"); ok {
			if s, ok := t.(string); ok {
				bf.Type = s
			}
		}
		if infraRaw, ok := m.Get("infra"); ok {
			switch infra := infraRaw.(type) {
			case *types.OrderedMap[any]:
				if infra != nil {
					bf.Infra = *infra
				}
			case map[string]any:
				om := types.OrderedMap[any]{}
				keys := make([]string, 0, len(infra))
				for k := range infra {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					om.Set(k, infra[k])
				}
				bf.Infra = om
			}
		}
	case map[string]any:
		if t, ok := m["type"].(string); ok {
			bf.Type = t
		}
		if infra, ok := m["infra"].(map[string]any); ok {
			om := types.OrderedMap[any]{}
			keys := make([]string, 0, len(infra))
			for k := range infra {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				om.Set(k, infra[k])
			}
			bf.Infra = om
		}
	}
	return bf
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	var parts []string
	start := 0
	for i, ch := range path {
		if ch == '/' || ch == '.' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}
