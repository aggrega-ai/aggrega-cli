// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// featureResult holds the normalized features and secrets for one node.
type featureResult struct {
	Features     types.OrderedMap[types.IRFeatureEntry]
	Secrets      types.OrderedMap[types.IRSecretEntry]
	FeatureOrder []string // deterministic order following Onion Model
	Markers      []string // canonical feature type strings
}

// featureProvenance carries the per-contribution provenance facts that
// the per-leaf merge stamps onto every leaf this contribution emits.
// Origin is "explicit" for author-declared features and "annotation"
// for synthetic attachments produced by the annotation-driven expansion
// pass. Side is "aggregate" or "release" when Origin is "annotation"
// (naming the authoring surface the annotation came from) and empty
// when Origin is "explicit". AnnotationKey + AnnotationSource populate
// only on the annotation path; all three of those fields are empty for
// "explicit".
type featureProvenance struct {
	Origin           string
	Side             string
	AnnotationKey    string
	AnnotationSource string
}

// toFieldProvenance converts the per-contribution provenance into the
// per-leaf wire-shape struct stored on IRFeatureEntry. The conversion
// is direct — fields map 1:1; OverridesAnnotation stays nil at this
// point and the merge step populates it when an explicit contribution
// suppresses an existing annotation leaf.
func (p featureProvenance) toFieldProvenance() types.FieldProvenance {
	return types.FieldProvenance{
		Origin:           p.Origin,
		Side:             p.Side,
		AnnotationKey:    p.AnnotationKey,
		AnnotationSource: p.AnnotationSource,
	}
}

// explicitProvenance is the sentinel value passed to mergeContribution
// from tiers that produce author-declared attachments — root-Aggregate
// + sub-aggregate components without a side-table match (Tiers 1/2),
// Aggregate subsystem features (Tier 3), and Release boundary-wide
// features (Tier 4). Tier 5 callers compute provenance per-attachment
// to distinguish explicit from annotation per the side-table lookup.
var explicitProvenance = featureProvenance{Origin: "explicit"}

// aggregateCanonicalPath reconstructs the canonical Aggregate-rooted
// dotted path for a topology node so the IR builder can consult the
// resolver's per-component AnnotationProvenance side-table. The path
// shape matches what the resolver's Aggregate-side expansion uses as
// the first slot of AnnotationProvenanceKey.
//
// Shapes returned:
//
//   - Sub-aggregate component (topoNode.SourceAggregate != "") → empty
//     string. The resolver's Aggregate-side expansion does not walk
//     sub-aggregates, so no side-table entry can exist for these
//     components; the caller treats the empty path as "skip lookup".
//   - Top-level root-Aggregate component (SubsystemPath == "") →
//     "components." + InstanceID.
//   - Subsystem-contained root-Aggregate component → the SubsystemPath
//     (stored as bare dotted subsystem chain like "outer.inner") is
//     translated into the canonical "subsystems." prefixed and
//     ".subsystems." joined form, with ".components." + InstanceID
//     appended at the leaf.
func aggregateCanonicalPath(topoNode *topology.TopologyNode) string {
	if topoNode == nil {
		return ""
	}
	if topoNode.SourceAggregate != "" {
		return ""
	}
	if topoNode.SubsystemPath == "" {
		return "components." + topoNode.InstanceID
	}
	parts := strings.Split(topoNode.SubsystemPath, ".")
	return "subsystems." + strings.Join(parts, ".subsystems.") + ".components." + topoNode.InstanceID
}

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
	// Aggregate-side annotation-driven synthetic attachments (produced by
	// resolver.ExpandAnnotations during Step 1) land on
	// ComponentInstance.Features and reach this tier indistinguishable
	// from explicit author-declared attachments. The IR layer recovers
	// the distinction by consulting resolveResult.AnnotationProvenance
	// keyed by the canonical Aggregate-rooted path for the topology node.
	if attachments, ok := topoResult.FeatureAttachments[nodeID]; ok {
		canonicalPath := aggregateCanonicalPath(topoNode)
		for _, fa := range attachments {
			prov := explicitProvenance
			if canonicalPath != "" {
				if record, ok := resolveResult.AnnotationProvenance[resolver.AnnotationProvenanceKey(canonicalPath, "", fa.InstanceName)]; ok {
					prov = featureProvenance{
						Origin:           "annotation",
						Side:             "aggregate",
						AnnotationKey:    record.AnnotationKey,
						AnnotationSource: record.AnnotationSource,
					}
				}
			}
			if err := mergeContribution(result, fa.InstanceName, fa.Feature.Type, fa.Feature.Values, types.OrderedMap[any]{}, markerSet, catalogLookup, nodeID, prov); err != nil {
				return nil, err
			}
		}
	}

	// Tier 3: Root Aggregate subsystem features (outer-to-inner).
	// Subsystem.Features carries explicit author-declared feature
	// attachments only; annotation cascade from a subsystem propagates
	// to the subsystem's components, not to the subsystem's own Features
	// map. Tier 3 stays explicit-only.
	if topoNode.SubsystemPath != "" {
		subsystemFeatures := collectSubsystemFeatures(resolveResult.Aggregate, topoNode.SubsystemPath)
		for _, sf := range subsystemFeatures {
			if err := mergeContribution(result, sf.name, sf.feat.Type, sf.feat.Values, types.OrderedMap[any]{}, markerSet, catalogLookup, nodeID, explicitProvenance); err != nil {
				return nil, err
			}
		}
	}

	// Tier 4: Release boundary-wide features (outer-to-inner). Release
	// boundary-wide attachments carry infra only and are explicit-only
	// at this scope; annotation synthesis on a boundary writes to the
	// boundary's contained components (consumed at Tier 5), not to the
	// boundary's Features map.
	if topoNode.BoundaryPath != "" && resolveResult.Release != nil {
		boundaryFeatures := collectBoundaryWideFeatures(resolveResult.Release.Spec.Boundaries, topoNode.BoundaryPath)
		for _, bf := range boundaryFeatures {
			if err := mergeContribution(result, bf.name, bf.feat.Type, types.OrderedMap[any]{}, bf.feat.Infra, markerSet, catalogLookup, nodeID, explicitProvenance); err != nil {
				return nil, err
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
			prov := explicitProvenance
			if record, ok := resolveResult.AnnotationProvenance[resolver.AnnotationProvenanceKey(topoNode.BoundaryPath, nodeID, cf.name)]; ok {
				prov = featureProvenance{
					Origin:           "annotation",
					Side:             "release",
					AnnotationKey:    record.AnnotationKey,
					AnnotationSource: record.AnnotationSource,
				}
			}
			if err := mergeContribution(result, cf.name, cf.feat.Type, types.OrderedMap[any]{}, cf.feat.Infra, markerSet, catalogLookup, nodeID, prov); err != nil {
				return nil, err
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

	// Derive top-level Origin / AnnotationKey / AnnotationSource per
	// IRFeatureEntry from the per-leaf provenance maps.
	finalizeTopLevelOrigin(result)

	return result, nil
}

// mergeContribution folds one Feature contribution into the per-node
// featureResult, applying per-pillar per-leaf merge with explicit-wins-
// at-leaf override semantics. The function replaces the older "last
// wins on Values; infra overlay outside" pattern with a uniform per-
// leaf merge that records FieldProvenance for every leaf in both
// pillars.
//
// Rules:
//
//   - Credential routing. isCredential checks the feature type URI
//     against the catalog and returns true when the catalog entity
//     carries the feature.aggrega.ai/kind: secret label. Credential
//     features route into result.Secrets and never appear in
//     result.Features.
//   - Type-match fatal halt. When an existing feature entry for
//     instanceName carries a non-empty Type that disagrees with the
//     incoming Type, the merge halts with ErrFeatureTypeMismatch.
//     Same instance name across tiers must describe the same Feature.
//   - Per-leaf merge per pillar. For each leaf in the incoming Values
//     and the incoming Infra contribution, the leaf-level merge
//     applies the explicit-wins-at-leaf rule:
//   - New leaf → incoming wins; per-leaf provenance recorded.
//   - Existing leaf annotation, incoming explicit → incoming wins
//     and the existing annotation provenance moves to
//     OverridesAnnotation on the new explicit provenance.
//   - Existing leaf explicit, incoming annotation → existing stays;
//     incoming is suppressed silently. The author's explicit
//     declaration wins.
//   - Same-origin leaves → last-tier-wins on value; provenance
//     refreshes to the latest contribution. Author-order through
//     the cascade picks the deepest authoring scope.
//
// The markerSet parameter aggregates feature-type strings across all
// tiers; the caller sort-extracts at the end of collectNodeFeatures.
// FeatureOrder is appended only on first sight of an instance name so
// the order vector is stable across re-merges of the same instance.
func mergeContribution(
	result *featureResult,
	instanceName string,
	featType string,
	values, infra types.OrderedMap[any],
	markerSet map[string]bool,
	catalogLookup resolver.CatalogLookup,
	nodeID string,
	provenance featureProvenance,
) error {
	if isCredential(featType, catalogLookup) {
		result.Secrets.Set(instanceName, types.IRSecretEntry{
			Type:   featType,
			Values: values,
		})
		markerSet[featType] = true
		return nil
	}

	existing, exists := result.Features.Get(instanceName)
	if !exists {
		// First-sight stamp on top-level Origin/AnnotationKey/AnnotationSource
		// preserves the per-attachment provenance for cases where the
		// contribution carries no Values or Infra leaves. The leaf-
		// derived top-level Origin computed by finalizeTopLevelOrigin
		// overwrites these fields when per-leaf detail exists.
		//
		// Per-leaf provenance is keyed by flat-dotted-leaf path. The
		// contribution's nested OrderedMap structure copies into
		// entry.Values / entry.Infra verbatim (preserving the nested
		// tree consumed downstream); the provenance maps record one
		// FieldProvenance per primitive leaf, keyed by the dotted path
		// flattened from the nested tree.
		entry := types.IRFeatureEntry{
			Type:             featType,
			Values:           types.OrderedMap[any]{},
			Infra:            types.OrderedMap[any]{},
			ValuesProvenance: types.OrderedMap[types.FieldProvenance]{},
			InfraProvenance:  types.OrderedMap[types.FieldProvenance]{},
			Origin:           provenance.Origin,
			AnnotationKey:    provenance.AnnotationKey,
			AnnotationSource: provenance.AnnotationSource,
		}
		for k, v := range values.All() {
			entry.Values.Set(k, v)
		}
		for k, v := range infra.All() {
			entry.Infra.Set(k, v)
		}
		fp := provenance.toFieldProvenance()
		for _, leaf := range types.FlattenLeaves(values) {
			entry.ValuesProvenance.Set(leaf.Path, fp)
		}
		for _, leaf := range types.FlattenLeaves(infra) {
			entry.InfraProvenance.Set(leaf.Path, fp)
		}
		result.Features.Set(instanceName, entry)
		result.FeatureOrder = append(result.FeatureOrder, instanceName)
		markerSet[featType] = true
		return nil
	}

	if existing.Type != "" && featType != "" && existing.Type != featType {
		return cerrors.Wrap(
			ErrFeatureTypeMismatch,
			cerrors.OpsError,
			cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q feature %q: existing type %q conflicts with %q", nodeID, instanceName, existing.Type, featType),
		)
	}

	for _, leaf := range types.FlattenLeaves(values) {
		mergeLeaf(&existing.Values, &existing.ValuesProvenance, leaf.Path, leaf.Value, provenance)
	}
	for _, leaf := range types.FlattenLeaves(infra) {
		mergeLeaf(&existing.Infra, &existing.InfraProvenance, leaf.Path, leaf.Value, provenance)
	}

	if featType != "" {
		existing.Type = featType
	}
	result.Features.Set(instanceName, existing)
	markerSet[featType] = true
	return nil
}

// mergeLeaf applies one leaf-level merge into a pillar tree per the
// explicit-wins-at-leaf rule. leafPath is a flat-dotted-leaf path
// (e.g. "pool" for a top-level leaf or "ingress.host" for a nested
// leaf); the pillar values map carries the nested OrderedMap tree,
// and types.SetLeafAtPath writes the leaf value at the path's
// terminal position, creating intermediate OrderedMap nodes as
// needed. The pillar provenance map is keyed by the flat-dotted-leaf
// path so every leaf has its own FieldProvenance regardless of
// nesting depth.
func mergeLeaf(
	pillarValues *types.OrderedMap[any],
	pillarProv *types.OrderedMap[types.FieldProvenance],
	leafPath string,
	incomingValue any,
	incomingProv featureProvenance,
) {
	if pillarValues.Len() == 0 {
		*pillarValues = types.OrderedMap[any]{}
	}
	if pillarProv.Len() == 0 {
		*pillarProv = types.OrderedMap[types.FieldProvenance]{}
	}
	existingProv, exists := pillarProv.Get(leafPath)
	if !exists {
		types.SetLeafAtPath(pillarValues, leafPath, incomingValue)
		pillarProv.Set(leafPath, incomingProv.toFieldProvenance())
		return
	}
	if existingProv.Origin == "explicit" && incomingProv.Origin == "annotation" {
		// Explicit already at this leaf; the annotation contribution is
		// suppressed silently. The lint pass (separate run) emits the
		// advisory if the suppressed annotation's effective value would
		// have differed.
		return
	}
	if existingProv.Origin == "annotation" && incomingProv.Origin == "explicit" {
		suppressed := existingProv
		newProv := incomingProv.toFieldProvenance()
		newProv.OverridesAnnotation = &suppressed
		types.SetLeafAtPath(pillarValues, leafPath, incomingValue)
		pillarProv.Set(leafPath, newProv)
		return
	}
	types.SetLeafAtPath(pillarValues, leafPath, incomingValue)
	pillarProv.Set(leafPath, incomingProv.toFieldProvenance())
}

// finalizeTopLevelOrigin walks every IRFeatureEntry in the per-node
// result and derives the top-level Origin / AnnotationKey /
// AnnotationSource fields from per-leaf FieldProvenance across both
// pillars. The derivation rule:
//
//   - Every populated leaf has Origin == "explicit" → top-level
//     Origin = "explicit"; AnnotationKey / AnnotationSource empty.
//   - Every populated leaf has Origin == "annotation", same Side,
//     same AnnotationKey, same AnnotationSource → top-level Origin =
//     "annotation"; AnnotationKey + AnnotationSource = that singular
//     value.
//   - Otherwise → top-level Origin = "mixed"; AnnotationKey +
//     AnnotationSource empty (consumers read per-leaf detail from
//     ValuesProvenance / InfraProvenance).
//
// A Feature attachment with no populated leaves keeps the empty
// Origin (the IR builder reaches this state only on credential
// routing, which excludes the entry from Features; defensive default).
func finalizeTopLevelOrigin(result *featureResult) {
	for name, entry := range result.Features.All() {
		if entry.ValuesProvenance.Len() == 0 && entry.InfraProvenance.Len() == 0 {
			// No per-leaf detail to derive from; the first-sight stamp
			// from mergeContribution stands.
			continue
		}
		origin, key, source := deriveTopLevelOrigin(entry.ValuesProvenance, entry.InfraProvenance)
		entry.Origin = origin
		entry.AnnotationKey = key
		entry.AnnotationSource = source
		result.Features.Set(name, entry)
	}
}

// deriveTopLevelOrigin computes the top-level Origin tuple for one
// IRFeatureEntry from its per-leaf provenance maps.
func deriveTopLevelOrigin(values, infra types.OrderedMap[types.FieldProvenance]) (origin, key, source string) {
	hasLeaf := false
	var firstAnnotation types.FieldProvenance
	allExplicit := true
	allAnnotation := true
	sameAnnotation := true
	walk := func(m types.OrderedMap[types.FieldProvenance]) {
		for _, fp := range m.All() {
			hasLeaf = true
			if fp.Origin != "explicit" {
				allExplicit = false
			}
			if fp.Origin != "annotation" {
				allAnnotation = false
				sameAnnotation = false
				continue
			}
			if firstAnnotation.Origin == "" {
				firstAnnotation = fp
				continue
			}
			if fp.Side != firstAnnotation.Side ||
				fp.AnnotationKey != firstAnnotation.AnnotationKey ||
				fp.AnnotationSource != firstAnnotation.AnnotationSource {
				sameAnnotation = false
			}
		}
	}
	walk(values)
	walk(infra)
	if !hasLeaf {
		return "", "", ""
	}
	if allExplicit {
		return "explicit", "", ""
	}
	if allAnnotation && sameAnnotation {
		return "annotation", firstAnnotation.AnnotationKey, firstAnnotation.AnnotationSource
	}
	return "mixed", "", ""
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
