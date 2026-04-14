// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// featureResult holds the normalized features and secrets for one node.
type featureResult struct {
	Features     map[string]types.IRFeatureEntry
	Secrets      map[string]types.IRSecretEntry
	FeatureOrder []string // deterministic order following Onion Model
	Markers      []string // canonical feature type strings
}

// collectNodeFeatures implements the 5-Tier Onion Model for one node.
// Tiers are collected in order; "last wins" deep-merge on values.
func collectNodeFeatures(
	nodeID string,
	topoNode *topology.TopologyNode,
	topoResult *topology.TopologyResult,
	resolveResult *resolver.ResolveResult,
	catalogLookup resolver.CatalogLookup,
) (*featureResult, error) {
	result := &featureResult{
		Features: make(map[string]types.IRFeatureEntry),
		Secrets:  make(map[string]types.IRSecretEntry),
	}

	markerSet := map[string]bool{}

	// Collect features in Onion Model order.
	// Each tier appends FeatureAttachments; we process them in order.

	// Tier 1: Sub-aggregate component features (from topology attachments of sub-agg origin)
	// Tier 2: Root Aggregate component features (from topology attachments of root origin)
	// These are already collected in topology.FeatureAttachments by flattenAggregate.
	if attachments, ok := topoResult.FeatureAttachments[nodeID]; ok {
		for _, fa := range attachments {
			if err := mergeFeature(result, fa.InstanceName, fa.Feature, markerSet, catalogLookup, nodeID); err != nil {
				return nil, err
			}
		}
	}

	// Tier 3: Root Aggregate subsystem features (outer-to-inner)
	if topoNode.SubsystemPath != "" {
		subsystemFeatures := collectSubsystemFeatures(resolveResult.Aggregate, topoNode.SubsystemPath)
		for _, sf := range subsystemFeatures {
			if err := mergeFeature(result, sf.name, sf.feat, markerSet, catalogLookup, nodeID); err != nil {
				return nil, err
			}
		}
	}

	// Tier 4: Release boundary-wide features (outer-to-inner)
	if topoNode.BoundaryPath != "" && resolveResult.Release != nil {
		boundaryFeatures := collectBoundaryWideFeatures(resolveResult.Release.Spec.Boundaries, topoNode.BoundaryPath)
		for _, bf := range boundaryFeatures {
			feat := types.FeatureInstance{Type: bf.feat.Type, Values: nil} // Release provides infra only, not values
			if err := mergeFeature(result, bf.name, feat, markerSet, catalogLookup, nodeID); err != nil {
				return nil, err
			}
			// Merge infra separately for Release features
			if existing, ok := result.Features[bf.name]; ok && bf.feat.Infra != nil {
				if existing.Infra == nil {
					existing.Infra = make(map[string]interface{})
				}
				for k, v := range bf.feat.Infra {
					existing.Infra[k] = v
				}
				result.Features[bf.name] = existing
			}
		}
	}

	// Tier 5: Release component-specific features
	if resolveResult.Release != nil {
		compFeatures := collectReleaseComponentFeatures(resolveResult.Release.Spec.Boundaries, nodeID)
		for _, cf := range compFeatures {
			feat := types.FeatureInstance{Type: cf.feat.Type}
			if err := mergeFeature(result, cf.name, feat, markerSet, catalogLookup, nodeID); err != nil {
				return nil, err
			}
			if existing, ok := result.Features[cf.name]; ok && cf.feat.Infra != nil {
				if existing.Infra == nil {
					existing.Infra = make(map[string]interface{})
				}
				for k, v := range cf.feat.Infra {
					existing.Infra[k] = v
				}
				result.Features[cf.name] = existing
			}
		}
	}

	// Collect secrets from topology attachments
	if attachments, ok := topoResult.SecretAttachments[nodeID]; ok {
		for _, sa := range attachments {
			result.Secrets[sa.InstanceName] = types.IRSecretEntry{
				Type:   sa.Secret.Type,
				Values: sa.Secret.Values,
			}
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

// mergeFeature merges a feature into the result following "last wins" semantics.
// Type mismatch between existing and new → fatal error.
// Credential features (kind=secret label) go to Secrets, not Features.
func mergeFeature(
	result *featureResult,
	instanceName string,
	feat types.FeatureInstance,
	markerSet map[string]bool,
	catalogLookup resolver.CatalogLookup,
	nodeID string,
) error {
	// Check if credential feature
	if isCredential(feat.Type, catalogLookup) {
		result.Secrets[instanceName] = types.IRSecretEntry{
			Type:   feat.Type,
			Values: feat.Values,
		}
		markerSet[feat.Type] = true
		return nil
	}

	existing, exists := result.Features[instanceName]
	if !exists {
		// New feature
		result.Features[instanceName] = types.IRFeatureEntry{
			Type:   feat.Type,
			Values: feat.Values,
		}
		result.FeatureOrder = append(result.FeatureOrder, instanceName)
		markerSet[feat.Type] = true
		return nil
	}

	// Merge: type must match
	if existing.Type != "" && feat.Type != "" && existing.Type != feat.Type {
		return fmt.Errorf(
			"%w: node %q feature %q: existing type %q conflicts with %q",
			ErrFeatureTypeMismatch, nodeID, instanceName, existing.Type, feat.Type,
		)
	}

	// "Last wins" on values
	if feat.Values != nil {
		if existing.Values == nil {
			existing.Values = make(map[string]interface{})
		}
		for k, v := range feat.Values {
			existing.Values[k] = v
		}
	}
	if feat.Type != "" {
		existing.Type = feat.Type
	}
	result.Features[instanceName] = existing
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
	return feat.Metadata.Labels[types.FeatureKindLabel] == types.FeatureKindSecret
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

// collectSubsystemFeatures walks the subsystem tree from root to the target path,
// collecting features at each level (outer-to-inner, sorted keys).
func collectSubsystemFeatures(agg *types.Aggregate, subsystemPath string) []namedFeature {
	parts := splitPath(subsystemPath)
	if len(parts) == 0 {
		return nil
	}

	var result []namedFeature
	current := agg.Spec.Subsystems

	for _, part := range parts {
		sub, ok := current[part]
		if !ok {
			break
		}
		// Collect this subsystem's features (sorted)
		featNames := sortedStringKeys(sub.Features)
		for _, fname := range featNames {
			result = append(result, namedFeature{name: fname, feat: sub.Features[fname]})
		}
		current = sub.Subsystems
	}

	return result
}

// collectBoundaryWideFeatures walks the Release boundary tree from root to the
// target boundary path, collecting boundary-wide features (outer-to-inner).
func collectBoundaryWideFeatures(boundaries map[string]types.Boundary, boundaryPath string) []namedBoundaryFeature {
	parts := splitPath(boundaryPath)
	if len(parts) == 0 {
		return nil
	}

	var result []namedBoundaryFeature
	current := boundaries

	for _, part := range parts {
		boundary, ok := current[part]
		if !ok {
			break
		}
		// Collect boundary-wide features (sorted)
		featNames := sortedInterfaceKeys(boundary.Features)
		for _, fname := range featNames {
			raw := boundary.Features[fname]
			bf := parseBoundaryFeature(raw)
			result = append(result, namedBoundaryFeature{name: fname, feat: bf})
		}
		current = boundary.Boundaries
	}

	return result
}

// collectReleaseComponentFeatures finds component-specific features for a node
// across all Release boundaries.
func collectReleaseComponentFeatures(boundaries map[string]types.Boundary, nodeID string) []namedBoundaryFeature {
	var result []namedBoundaryFeature
	collectCompFeaturesRecursive(boundaries, nodeID, &result)
	return result
}

func collectCompFeaturesRecursive(boundaries map[string]types.Boundary, nodeID string, result *[]namedBoundaryFeature) {
	bNames := make([]string, 0, len(boundaries))
	for k := range boundaries {
		bNames = append(bNames, k)
	}
	sort.Strings(bNames)

	for _, bName := range bNames {
		boundary := boundaries[bName]
		if bc, ok := boundary.Components[nodeID]; ok {
			featNames := sortedBoundaryFeatureKeys(bc.Features)
			for _, fname := range featNames {
				*result = append(*result, namedBoundaryFeature{name: fname, feat: bc.Features[fname]})
			}
		}
		collectCompFeaturesRecursive(boundary.Boundaries, nodeID, result)
	}
}

func parseBoundaryFeature(raw interface{}) types.BoundaryFeature {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return types.BoundaryFeature{}
	}
	bf := types.BoundaryFeature{}
	if t, ok := m["type"].(string); ok {
		bf.Type = t
	}
	if infra, ok := m["infra"].(map[string]interface{}); ok {
		bf.Infra = infra
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

// Sorted key helpers.

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedInterfaceKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoundaryFeatureKeys(m map[string]types.BoundaryFeature) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
