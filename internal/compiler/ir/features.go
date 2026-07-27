// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// featureResult holds the normalized attachments for one node.
type featureResult struct {
	Features     types.OrderedMap[types.IRFeatureEntry]
	FeatureOrder []string // deterministic order following Onion Model
	Markers      []string // canonical feature type strings

	// leafScope records which authoring scope currently owns each merged
	// leaf, so a contribution from a broader scope cannot overwrite a
	// narrower scope's leaf just by arriving later in the cascade. It is
	// merge-time bookkeeping: the emitted IR carries the winning value and
	// its provenance, never the contest that produced them. Addressed by
	// key only and never iterated, so it has no bearing on output order.
	leafScope map[scopeKey]attachmentScope
}

// newFeatureResult is the one construction site for a per-node result, so
// no caller can produce one whose merge bookkeeping is unallocated.
func newFeatureResult() *featureResult {
	return &featureResult{
		Features:  types.OrderedMap[types.IRFeatureEntry]{},
		leafScope: map[scopeKey]attachmentScope{},
	}
}

// scopeKey addresses one contested leaf: the attachment's instance name, the
// pillar, and the flat-dotted leaf path. Instance names are unique per node,
// so the name plus pillar plus leaf addresses a leaf outright.
type scopeKey struct {
	instance string
	pillar   string
	leaf     string
}

const (
	pillarValues = "values"
	pillarInfra  = "infra"
)

// attachmentScope is the authoring scope one attachment contribution came
// from. The per-node merge compares scopes so a same-instance-name contest
// at one leaf resolves by where the contribution was authored rather than
// by the order in which the cascade happens to visit the tiers.
//
// The scope has two ranked axes, and the order in which they are compared
// is the contract:
//
//   - authority — a declaration on the composing document outranks one made
//     inside a document that composing document pulls in.
//   - reach — a declaration naming one component instance is narrower than
//     one made subsystem-wide or boundary-wide.
//
// authority is compared first. A composing Aggregate's subsystem-wide
// declaration therefore beats a composed sub-Aggregate's component-level
// one: a composed document must not be able to defeat its composer merely
// by declaring at a narrower scope, or composition would stop being
// authoritative.
type attachmentScope struct {
	authority uint8
	reach     uint8
}

const (
	authorityComposed  uint8 = 0
	authorityComposing uint8 = 1

	reachShared    uint8 = 0
	reachComponent uint8 = 1
)

var (
	// scopeComposedComponent is a component-level attachment declared inside
	// a sub-Aggregate the build composes.
	scopeComposedComponent = attachmentScope{authority: authorityComposed, reach: reachComponent}

	// scopeShared is a subsystem-wide (Aggregate) or boundary-wide (Release)
	// attachment on the composing document.
	scopeShared = attachmentScope{authority: authorityComposing, reach: reachShared}

	// scopeComponent is a component-scoped attachment on the composing
	// document — an Aggregate component features: block, or a Release
	// boundary's per-component features entry.
	scopeComponent = attachmentScope{authority: authorityComposing, reach: reachComponent}
)

// outranks reports whether s beats other for ownership of one leaf. The
// comparison order — authority, then reach — IS the precedence rule order
// collectNodeFeatures states as its contract.
//
// Equal scopes do not outrank each other, so two contributions from the
// same scope fall through to author order and the later one wins; that is
// what keeps an inner subsystem beating an outer one and a deeper boundary
// beating a shallower one.
//
// The developer scopes and the operator scopes are never compared against
// each other, because each authoring side supplies only its own pillar: a
// values leaf is contested among Aggregate scopes alone and an infra leaf
// among Release scopes alone.
func (s attachmentScope) outranks(other attachmentScope) bool {
	if s.authority != other.authority {
		return s.authority > other.authority
	}
	return s.reach > other.reach
}

// componentAttachmentScope reports the scope of a node's own
// component-level features: block. The attachments a node receives at that
// scope come from exactly ONE aggregate — the one that declares the
// component — because a node ID colliding between a composed document and
// its composer halts topology construction. So the node's source aggregate
// decides the scope outright rather than approximating it.
func componentAttachmentScope(topoNode *topology.TopologyNode) attachmentScope {
	if topoNode != nil && topoNode.SourceAggregate != "" {
		return scopeComposedComponent
	}
	return scopeComponent
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
	Origin           types.AttachmentOrigin
	Side             string
	AnnotationKey    string
	AnnotationSource string
}

// toFieldProvenance converts the per-contribution provenance into the
// per-leaf wire-shape struct stored on IRFeatureEntry. The conversion
// is direct — fields map 1:1; OverriddenAnnotation stays nil at this
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
var explicitProvenance = featureProvenance{Origin: types.OriginExplicit}

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

// collectNodeFeatures assembles the merged attachment set for one node by
// processing five ordered cascade tiers in sequence, folding every
// contribution through the single mergeContribution body.
//
// Tier order, with the data sources each tier reads from:
//
//   - Tier 1 — Sub-aggregate component features. Already collected into
//     topoResult.FeatureAttachments by the topology builder's
//     flattenAggregate; iterated here in attachment order.
//   - Tier 2 — Root Aggregate component features. Same source and the same
//     loop. A node's component-level attachments come from exactly one
//     aggregate — the one that declares the component — so a given node is
//     a Tier 1 node or a Tier 2 node, never both.
//   - Tier 3 — Root Aggregate subsystem features (outer-to-inner).
//     collectSubsystemFeatures walks the subsystem path on the resolved
//     Aggregate, emitting features at each level in author order.
//   - Tier 4 — Release boundary-wide features (outer-to-inner).
//     collectBoundaryWideFeatures walks the boundary path on the Release
//     and reads boundary-level Features. Release contributions to a
//     same-named instance are infra-only — values come from the Aggregate
//     side, infra from the Release side.
//   - Tier 5 — Release component-specific features.
//     collectReleaseComponentFeatures recursively scans every boundary's
//     per-component Features map for entries keyed by nodeID.
//
// # Precedence contract
//
// The tier sequence above is the MECHANISM, not the precedence rule. That
// same sequence fixes the order in which the rendering pass executes each
// attachment's injectMapping, so it cannot double as the answer to "which
// contribution owns a contested leaf". Ownership is decided by four rules,
// and the ORDER OF THE RULES is the contract:
//
//		R1 (origin)  >  R3 (composition authority)  >  R4 (specificity)
//
//	  - R1 — ORIGIN. Explicit authoring beats annotation-driven expansion,
//	    independent of the scope either came from. R1 is resolved in
//	    mergeLeaf, which documents its three cases; it outranks every scope
//	    rule below.
//	  - R2 — PILLAR OWNERSHIP. Each authoring domain fills its own pillar —
//	    the Aggregate tiers contribute values, the Release tiers contribute
//	    infra — so the two never contest the same leaf. That is why there is
//	    no developer-versus-operator precedence question to answer.
//	  - R3 — COMPOSITION AUTHORITY. A declaration on the composing Aggregate
//	    outranks one made inside an Aggregate it composes, even when the
//	    composed document declared at the narrower scope. Otherwise a
//	    composed document could defeat its composer simply by choosing a
//	    narrower scope, and composition would stop being authoritative.
//	  - R4 — SPECIFICITY. Within one authoring level and one pillar, the
//	    narrower scope refines the broader one, per leaf: a component-scoped
//	    attachment overrides a subsystem-wide or boundary-wide attachment of
//	    the same instance name leaf by leaf, and the leaves the narrower
//	    scope does not mention keep the broader scope's values.
//
// R3 is compared before R4 deliberately; attachmentScope.outranks is where
// that rule order lives, as the order in which it compares the two axes.
//
// Precedence governs value and fill resolution only. An attachment's
// authored inject block merges per-mechanism-per-key with the incoming
// contribution winning at the delivery key, because the tier sequence also
// orders the pre-render injection mapping — ranking inject by scope would
// put two different orderings on one attachment.
//
// Every attachment a node holds routes through these five tiers and the one
// mergeContribution body, whatever the attachment produces. There is no
// second construction path for a credential to drift down, and no second
// precedence semantic for one to be governed by.
//
// Markers (the canonical feature-type strings the IR exposes via
// FeatureMarkers) are accumulated into a set across all five tiers, then
// sort-extracted at the end so byte-identical input produces
// byte-identical output across runs.
func collectNodeFeatures(
	nodeID string,
	topoNode *topology.TopologyNode,
	topoResult *topology.TopologyResult,
	resolveResult *resolver.ResolveResult,
	catalogLookup resolver.CatalogLookup,
) (*featureResult, error) {
	result := newFeatureResult()

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
		componentScope := componentAttachmentScope(topoNode)
		for _, fa := range attachments {
			prov := explicitProvenance
			if canonicalPath != "" {
				if record, ok := resolveResult.AnnotationProvenance[resolver.AnnotationProvenanceKey(canonicalPath, "", fa.InstanceName)]; ok {
					prov = featureProvenance{
						Origin:           types.OriginAnnotation,
						Side:             "aggregate",
						AnnotationKey:    record.AnnotationKey,
						AnnotationSource: record.AnnotationSource,
					}
				}
			}
			if err := mergeContribution(result, fa.InstanceName, fa.Feature.Type, fa.Feature.Values, types.OrderedMap[any]{}, fa.Feature.Inject, markerSet, catalogLookup, nodeID, prov, componentScope); err != nil {
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
		subsystemFeatures := collectSubsystemFeatures(types.Normalize(resolveResult.Aggregate), topoNode.SubsystemPath)
		for _, sf := range subsystemFeatures {
			if err := mergeContribution(result, sf.name, sf.feat.Type, sf.feat.Values, types.OrderedMap[any]{}, sf.feat.Inject, markerSet, catalogLookup, nodeID, explicitProvenance, scopeShared); err != nil {
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
			if err := mergeContribution(result, bf.name, bf.feat.Type, types.OrderedMap[any]{}, bf.feat.Infra, nil, markerSet, catalogLookup, nodeID, explicitProvenance, scopeShared); err != nil {
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
					Origin:           types.OriginAnnotation,
					Side:             "release",
					AnnotationKey:    record.AnnotationKey,
					AnnotationSource: record.AnnotationSource,
				}
			}
			if err := mergeContribution(result, cf.name, cf.feat.Type, types.OrderedMap[any]{}, cf.feat.Infra, nil, markerSet, catalogLookup, nodeID, prov, scopeComponent); err != nil {
				return nil, err
			}
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

// validateNodeAttachments validates a node's merged attachment pillars against
// each attachment's declared catalog schema, closed and default-aware, via the
// shared resolver.ValidatePillarConformance entry point — the same validator the
// Step-1 component gate uses, so an authored schema is interpreted identically
// across surfaces.
//
// What is validated is the EFFECTIVE merged result of the 5-Tier Onion Model
// (the values/infra after collectNodeFeatures folds every tier), not a single
// pre-merge fragment — so a value made conformant in one tier and illegal by a
// later tier's override is caught.
//
// Every attachment resolves its schema through the Feature catalog lookup — a
// credential is a Feature carrying the credential-kind label, so one lookup
// serves them all. An attachment whose type has no catalog schema, or whose
// schema does not declare the pillar, contributes nothing.
//
// Attachment infra is closed against the attachment's own
// schema.infra — there is NO provider union (the component-gate construction):
// an attachment is an overlay and does not own a rendering provider, so its
// schema.infra is its complete infra contract. Errors are returned unsorted;
// joinAttachmentConformance canonicalises the combined set.
func validateNodeAttachments(
	nodeID string,
	feats types.OrderedMap[types.IRFeatureEntry],
	parameters map[string]any,
	entity string,
	compiledFor func(featureType string) (compactform.Schema, bool),
) []*resolver.ResolverError {
	var errs []*resolver.ResolverError
	for name, entry := range feats.All() {
		compiled, ok := compiledFor(entry.Type)
		if !ok {
			continue
		}
		prefix := fmt.Sprintf(".spec.components.%s.features.%s", nodeID, name)
		errs = append(errs, resolver.ValidatePillarConformance(compiled, "values", entry.Values, parameters, entity, prefix, "the Feature schema")...)
		errs = append(errs, resolver.ValidatePillarConformance(compiled, "infra", entry.Infra, parameters, entity, prefix, "the Feature schema")...)
	}
	return errs
}

// joinAttachmentConformance canonicalises the collected attachment-conformance
// violations by (Entity, Field) — byte-identical across runs of the same input,
// matching the Step-1 gate's ordering — and joins them into one error. The
// joined error wraps every violation, so errors.Is(err, ErrSchemaConformance)
// holds for the build halt.
func joinAttachmentConformance(errs []*resolver.ResolverError) error {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Entity != errs[j].Entity {
			return errs[i].Entity < errs[j].Entity
		}
		return errs[i].Field < errs[j].Field
	})
	wrapped := make([]error, len(errs))
	for i, e := range errs {
		wrapped[i] = e
	}
	return errors.Join(wrapped...)
}

// newAttachmentEntry builds a fresh IR attachment entry from one contribution.
// It is the ONLY site that constructs one, so there is no lossier construction
// path for any attachment family to drift down.
//
// The first-sight stamp on top-level Origin/AnnotationKey/AnnotationSource
// preserves per-attachment provenance for contributions carrying no Values/Infra
// leaves; the leaf-derived top-level Origin computed by finalizeTopLevelOrigin
// overwrites these when per-leaf detail exists. The contribution's nested
// OrderedMap structure copies into entry.Values / entry.Infra verbatim; the
// provenance maps record one FieldProvenance per primitive leaf, keyed by the
// dotted path flattened from the nested tree.
func newAttachmentEntry(featType, resolvedVersion string, values, infra types.OrderedMap[any], inject types.OrderedMap[types.OrderedMap[string]], provenance featureProvenance) types.IRFeatureEntry {
	entry := types.IRFeatureEntry{
		Type:             featType,
		ResolvedVersion:  resolvedVersion,
		Values:           types.OrderedMap[any]{},
		Infra:            types.OrderedMap[any]{},
		Inject:           inject,
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
	return entry
}

// featureVersion resolves the catalog version of a Feature/credential type — the
// attachment's OWN resolved version (NOT the host component's version). The entry's
// resolved_version reflects the feature, so a node carrying components at one version
// and features at another reports each accurately. Returns "" when the type is
// absent (the IR keeps the empty string rather than borrowing a wrong version).
func featureVersion(catalogLookup resolver.CatalogLookup, featType string) string {
	if catalogLookup.Feature == nil || featType == "" {
		return ""
	}
	feat, found := catalogLookup.Feature(featType)
	if !found {
		return ""
	}
	return feat.Metadata.Version
}

// mergeContribution folds one attachment contribution into the per-node
// featureResult through a per-pillar per-leaf merge that records
// FieldProvenance for every leaf in both pillars.
//
// One body serves every attachment family. A credential — a Feature whose
// catalog entity carries the feature.aggrega.ai/kind: secret label — lands in
// the same bucket as any other attachment and is merged by the same rules, so
// there is no second precedence semantic for it to drift behind and its
// FeatureRecipe reads .infra and per-leaf provenance exactly as another's
// does. Nothing here reads that label.
//
// Rules:
//
//   - Type-match fatal halt. When an existing entry for instanceName
//     carries a non-empty Type that disagrees with the incoming Type, the
//     merge halts with ErrFeatureTypeMismatch. One instance name across
//     tiers must describe one attachment.
//   - Per-leaf merge per pillar. Every leaf of the incoming values and
//     infra contribution is resolved by mergeLeaf, which applies the
//     origin rule first and the authoring-scope rank second — see the
//     precedence contract on collectNodeFeatures for the rule order and
//     mergeLeaf for each case.
//   - Authored inject merges per-mechanism-per-key with the incoming
//     contribution winning at the delivery key. Scope does not rank
//     inject: the tier sequence also orders the pre-render injection
//     mapping, so a scope-ranked inject would put two orderings on one
//     attachment.
//
// The markerSet parameter aggregates attachment-type strings across all
// tiers; the caller sort-extracts at the end of collectNodeFeatures.
// FeatureOrder is appended only on first sight of an attachment instance
// name, so the order vector stays stable across re-merges of the same
// instance. It is the one execution order Phase A and Phase B.6 both follow.
func mergeContribution(
	result *featureResult,
	instanceName string,
	featType string,
	values, infra types.OrderedMap[any],
	inject *types.InjectBlock,
	markerSet map[string]bool,
	catalogLookup resolver.CatalogLookup,
	nodeID string,
	provenance featureProvenance,
	scope attachmentScope,
) error {
	// resolved_version is the attachment's OWN feature/credential version, not the
	// host component's — resolved once here from the catalog.
	version := featureVersion(catalogLookup, featType)
	// Authored attachment-level inject (the developer's delivery declaration,
	// a provides-key reference resolved against the attachment's own provides)
	// projects into the IR entry; the topology layer carries it only on the
	// Aggregate-side tiers (Release boundary tiers pass nil).
	injectMap := convertInjectBlock(inject)

	existing, exists := result.Features.Get(instanceName)
	if !exists {
		entry := newAttachmentEntry(featType, version, values, infra, injectMap, provenance)
		result.Features.Set(instanceName, entry)
		result.FeatureOrder = append(result.FeatureOrder, instanceName)
		recordLeafScopes(result.leafScope, instanceName, values, infra, scope)
		markerSet[featType] = true
		return nil
	}

	if existing.Type != "" && featType != "" && existing.Type != featType {
		return cerrors.Wrap(
			ErrFeatureTypeMismatch,
			cerrors.OpsError,
			cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q attachment %q: existing type %q conflicts with %q", nodeID, instanceName, existing.Type, featType),
		)
	}

	for _, leaf := range types.FlattenLeaves(values) {
		key := scopeKey{instance: instanceName, pillar: pillarValues, leaf: leaf.Path}
		mergeLeaf(&existing.Values, &existing.ValuesProvenance, result.leafScope, key, leaf.Value, provenance, scope)
	}
	for _, leaf := range types.FlattenLeaves(infra) {
		key := scopeKey{instance: instanceName, pillar: pillarInfra, leaf: leaf.Path}
		mergeLeaf(&existing.Infra, &existing.InfraProvenance, result.leafScope, key, leaf.Value, provenance, scope)
	}

	// Merge the authored attachment inject per-mechanism-per-key (incoming wins
	// at the delivery key). Only Aggregate-side tiers carry inject; Release tiers
	// pass nil, so this preserves the Aggregate-declared delivery on those merges.
	for mech, keys := range injectMap.All() {
		dst, ok := existing.Inject.Get(mech)
		if !ok {
			dst = types.OrderedMap[string]{}
		}
		for k, v := range keys.All() {
			dst.Set(k, v)
		}
		existing.Inject.Set(mech, dst)
	}

	if featType != "" {
		existing.Type = featType
		if existing.ResolvedVersion == "" {
			existing.ResolvedVersion = version
		}
	}
	result.Features.Set(instanceName, existing)
	markerSet[featType] = true
	return nil
}

// recordLeafScopes stamps the owning scope onto every leaf of a
// first-sight contribution, so a later broader-scope contribution to the
// same instance can be ranked against it. The traversal delegates to
// types.FlattenLeaves — the one home for "which leaves does this pillar
// have" — so it cannot disagree with the provenance newAttachmentEntry
// records over the same leaves.
func recordLeafScopes(
	scopes map[scopeKey]attachmentScope,
	instance string,
	values, infra types.OrderedMap[any],
	scope attachmentScope,
) {
	for _, leaf := range types.FlattenLeaves(values) {
		scopes[scopeKey{instance: instance, pillar: pillarValues, leaf: leaf.Path}] = scope
	}
	for _, leaf := range types.FlattenLeaves(infra) {
		scopes[scopeKey{instance: instance, pillar: pillarInfra, leaf: leaf.Path}] = scope
	}
}

// mergeLeaf resolves ownership of one leaf in a pillar tree. key.leaf is a
// flat-dotted-leaf path (e.g. "pool" for a top-level leaf or
// "ingress.host" for a nested leaf); the pillar values map carries the
// nested OrderedMap tree, and types.SetLeafAtPath writes the leaf value at
// the path's terminal position, creating intermediate OrderedMap nodes as
// needed. The pillar provenance map is keyed by the same flat-dotted-leaf
// path, so every leaf carries its own FieldProvenance regardless of
// nesting depth.
//
// # R1 — ORIGIN, and why it outranks every scope rule
//
// Explicit authoring beats annotation-driven expansion at a contested
// leaf, whatever scope either contribution came from. An author who wrote
// the leaf down means it; an annotation is a convenience expansion over
// the same surface, and letting an expansion at a narrower scope silently
// beat an explicit declaration would make the authored file untrustworthy.
// The three cases:
//
//   - Existing explicit, incoming annotation → the explicit leaf stays and
//     the annotation contribution is suppressed silently.
//   - Existing annotation, incoming explicit → the explicit contribution
//     wins, and the suppressed annotation provenance is retained on the
//     winner as OverriddenAnnotation so the advisory lint and the inspect
//     rendering can name what was replaced. Only ONE suppressed
//     contribution is ever retained: a same-origin replacement replaces
//     the provenance wholesale rather than chaining.
//   - Same origin → falls through to the authoring-scope rank below.
//
// # Scope rank — R3 then R4
//
// For a same-origin contest the owning scope decides: the incoming
// contribution takes the leaf unless the scope that already owns it
// outranks the incoming scope, per attachmentScope.outranks. Equal scopes
// do not outrank each other, so same-scope contributions still resolve by
// author order with the later one winning.
//
// A leaf with provenance but no recorded scope falls back to the zero
// attachmentScope, which outranks nothing — so the incoming contribution
// wins rather than a missing record silently pinning a leaf.
func mergeLeaf(
	pillarTree *types.OrderedMap[any],
	pillarProv *types.OrderedMap[types.FieldProvenance],
	scopes map[scopeKey]attachmentScope,
	key scopeKey,
	incomingValue any,
	incomingProv featureProvenance,
	incomingScope attachmentScope,
) {
	if pillarTree.Len() == 0 {
		*pillarTree = types.OrderedMap[any]{}
	}
	if pillarProv.Len() == 0 {
		*pillarProv = types.OrderedMap[types.FieldProvenance]{}
	}
	take := func(prov types.FieldProvenance) {
		types.SetLeafAtPath(pillarTree, key.leaf, incomingValue)
		pillarProv.Set(key.leaf, prov)
		scopes[key] = incomingScope
	}
	existingProv, exists := pillarProv.Get(key.leaf)
	if !exists {
		take(incomingProv.toFieldProvenance())
		return
	}
	if existingProv.Origin == types.OriginExplicit && incomingProv.Origin == types.OriginAnnotation {
		return
	}
	if existingProv.Origin == types.OriginAnnotation && incomingProv.Origin == types.OriginExplicit {
		suppressed := existingProv
		newProv := incomingProv.toFieldProvenance()
		newProv.OverriddenAnnotation = &suppressed
		take(newProv)
		return
	}
	if scopes[key].outranks(incomingScope) {
		return
	}
	take(incomingProv.toFieldProvenance())
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
// An attachment with no populated leaves keeps the first-sight stamp
// mergeContribution wrote.
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
func deriveTopLevelOrigin(values, infra types.OrderedMap[types.FieldProvenance]) (origin types.AttachmentOrigin, key, source string) {
	hasLeaf := false
	var firstAnnotation types.FieldProvenance
	allExplicit := true
	allAnnotation := true
	sameAnnotation := true
	walk := func(m types.OrderedMap[types.FieldProvenance]) {
		for _, fp := range m.All() {
			hasLeaf = true
			if fp.Origin != types.OriginExplicit {
				allExplicit = false
			}
			if fp.Origin != types.OriginAnnotation {
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
		return types.OriginExplicit, "", ""
	}
	if allAnnotation && sameAnnotation {
		return types.OriginAnnotation, firstAnnotation.AnnotationKey, firstAnnotation.AnnotationSource
	}
	return types.OriginMixed, "", ""
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
// author order. Outer subsystems are emitted before inner subsystems, and
// every level of the chain carries the same subsystem-wide scope, so the
// same Feature declared at a deeper level refines one declared at a
// shallower level through the merge's same-scope author-order fall-through.
//
// The path traversal stops short on the first missing path part so that
// a subsystem-path the topology asserts but the resolved Aggregate no
// longer contains (a defensive condition; should not happen on a clean
// pipeline) returns the partial outer-tier list rather than panicking.
func collectSubsystemFeatures(na types.NormalizedAggregate, subsystemPath string) []namedFeature {
	agg := na.Aggregate()
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
// Boundary features are typed [types.BoundaryFeature] values decoded
// directly from YAML (Boundary.Features is OrderedMap[BoundaryFeature]).
// Mergeable values are NOT collected here — Release boundaries contribute
// infra-only under the pillar-split rule (values from the Aggregate, infra
// from the Release), which is what keeps the developer and operator scopes
// from ever contesting one leaf. The infra these entries carry is folded in
// by the same per-leaf merge every other tier goes through.
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
		for fname, bf := range boundary.Features.All() {
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
// and emit the entries keyed by nodeID. This is the narrowest operator
// scope, so it refines the broader boundary-wide cascade per leaf.
//
// The recursion is unconstrained by boundary path: any boundary anywhere
// in the tree may carry a per-component Features map for nodeID, and all
// of them contribute. As with the boundary-wide level, Features-instance
// payloads here arrive as the same opaque BoundaryFeature shape and
// contribute infra only.
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

// buildBoundaryFeaturesProjection projects every Release boundary's Feature
// attachments into the IR, keyed boundary-path -> instance-name (the path is the
// "/"-joined boundary-name chain, the same coordinate `findBoundaryAtPath` and the
// renderer's boundary-FR cascade use). This un-folded projection is the single
// source the lock-time source-descent (`irEntityInfra`) and the render-time
// boundary-FR dispatch both read, so neither re-walks the Release. The per-node
// Tier-4 fold into FeatureValuesMapping (collectBoundaryWideFeatures) is unchanged;
// this projection is additive, for boundary-scope FR source identity + bf.Infra.
func buildBoundaryFeaturesProjection(boundaries types.OrderedMap[types.Boundary]) types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]] {
	out := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	var walk func(bs types.OrderedMap[types.Boundary], prefix string)
	walk = func(bs types.OrderedMap[types.Boundary], prefix string) {
		for name, boundary := range bs.All() {
			path := name
			if prefix != "" {
				path = prefix + "/" + name
			}
			if boundary.Features.Len() > 0 {
				inner := types.OrderedMap[types.IRBoundaryFeature]{}
				for fname, bf := range boundary.Features.All() {
					inner.Set(fname, types.IRBoundaryFeature{Type: bf.Type, Infra: bf.Infra})
				}
				if inner.Len() > 0 {
					out.Set(path, inner)
				}
			}
			walk(boundary.Boundaries, path)
		}
	}
	walk(boundaries, "")
	return out
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
