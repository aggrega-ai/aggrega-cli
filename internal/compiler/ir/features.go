// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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

	// leafOwners records what is known about the contribution that currently
	// owns each merged leaf. It is merge-time bookkeeping: the emitted IR
	// carries the winning value and its provenance, never the contest that
	// produced them. Addressed by key only and never iterated, so it has no
	// bearing on output order.
	//
	// Its two halves answer two questions and are held together because they
	// have one lifetime: both are written when a leaf is first taken and
	// rewritten whenever it changes hands, so separate maps would be two
	// copies of one bookkeeping with nothing keeping them in step.
	leafOwners map[scopeKey]leafOwner

	// contributedFills records each fill this node received from a scope
	// broader than the node — with the scope it was authored at — so a rule
	// asked of the merged attachment can still name the edit that produced
	// it. Merge-time bookkeeping like leafScope: none of it is emitted.
	//
	// It exists because the merged entry cannot answer the question. An
	// entry's provenance maps are keyed by values and infra leaves and carry
	// an authoring path only for the annotation-expansion route, so an
	// explicitly authored delivery arriving from a broader scope leaves no
	// trace of where it was written. Recorded in cascade order, which is
	// outer scope before inner, so several offending scopes are reported in
	// the order an author reads their own document.
	contributedFills []contributedFill
}

// contributedFill is one fill a node received from a scope broader than
// itself, paired with the locator of the scope that declared it.
type contributedFill struct {
	instance string
	// authoredLocator names the authoring scope, never the node. It is what
	// makes this record worth keeping: a diagnostic about the merged
	// attachment has to point at an edit that exists in the document.
	authoredLocator string
	// contribution is the fill exactly as authored. The merged entry is not a
	// substitute: the merge folds the contribution together with every other
	// tier's, so a rule reading the merged entry could not tell which scope
	// contributed the part it objects to.
	contribution types.FeatureInstance
}

// leafOwner is what the merge knows about the contribution that currently owns
// one leaf.
//
// scope ranks that contribution against a later one, so a broader scope cannot
// take a narrower scope's leaf merely by arriving later in the cascade. site
// names where it was written, which is what a diagnostic about the leaf can
// point at — and the two are different questions about one contribution: a rank
// orders, a site locates, and neither can be recovered from the other.
type leafOwner struct {
	scope attachmentScope
	site  constraint.AuthoredSite
}

// componentOwnSite returns the site of a node's own component block, which is
// where an attachment the developer wrote in that block was written.
//
// It delegates to the framework's component-site rule rather than assembling
// the answer, because the same rule decides where the component itself is
// named: an attachment written inside a component's block has to be located
// under exactly the segments that component is located under, and a second
// derivation of them would be free to drift from the first.
//
// The rule's two scope inputs are the same values the node axis is built from,
// which is what makes the agreement structural rather than coincidental: the
// graph node this stage's enumeration reads them off carries them copied
// verbatim from the topology node read here, so there is one origin and no
// point at which the two could come to differ.
func componentOwnSite(topoNode *topology.TopologyNode, nodeID string) constraint.AuthoredSite {
	if topoNode == nil {
		return constraint.ComponentSite(constraint.AuthoredComponentName(nodeID), "", false)
	}
	return constraint.ComponentSite(
		constraint.AuthoredComponentName(nodeID),
		topoNode.SubsystemPath,
		topoNode.SynthesizedSubsystem,
	)
}

// newFeatureResult is the one construction site for a per-node result, so
// no caller can produce one whose merge bookkeeping is unallocated.
func newFeatureResult() *featureResult {
	return &featureResult{
		Features:   types.OrderedMap[types.IRFeatureEntry]{},
		leafOwners: map[scopeKey]leafOwner{},
	}
}

// recordContributedFill notes a fill that arrived from a scope broader than the
// node, so the post-cascade slot-shape rule can name the scope it was authored
// at. Only fills carrying a delivery block are recorded, because a slot's shape
// closes the delivery and permits every other pillar a fill may carry — so a
// contribution with no delivery has nothing for that rule to object to.
func (r *featureResult) recordContributedFill(instance, authoredLocator string, contribution types.FeatureInstance) {
	if contribution.Inject == nil {
		return
	}
	r.contributedFills = append(r.contributedFills, contributedFill{
		instance:        instance,
		authoredLocator: authoredLocator,
		contribution:    contribution,
	})
}

// projectAttachmentSites writes this node's per-leaf sites into the shared
// projection, under the coordinate a rule over such a leaf is invoked at.
//
// It is written for every leaf the node holds rather than for the leaves some
// rule might ask about, because a condition deciding which to retain has to be
// right and a wrong one produces no answer rather than a wrong one — and a rule
// handed no site reports the same silence as a rule that ran and approved.
//
// The walk ranges a Go map, whose order is randomised, and that is sound here
// because it writes into another map addressed by key. Nothing downstream reads
// either in iteration order, so no order this walk takes reaches the output.
func (r *featureResult) projectAttachmentSites(nodeID string, into AttachmentSites) {
	for key, owner := range r.leafOwners {
		into[AttachmentLeafKey{
			Node:       nodeID,
			Attachment: key.instance,
			Pillar:     key.pillar,
			Leaf:       key.leaf,
		}] = owner.site
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

// The coordinate prefix each authoring pillar contributes to a marked leaf's
// path, so one list can carry leaves from both without a bare path being
// ambiguous: a Feature may declare `password` in values and again in infra and
// mean different data.
//
// The prefix is read from the shared leaf-coordinate primitives rather than
// spelled here, because the coordinate it builds is the one a display prints
// and a consumer parses back, and a writer holding its own copy of the prefix
// would agree with the reader on the day it was written. That shared home is
// the same package this one already depends on for the leaf paths themselves,
// so reaching it costs no dependency.
//
// The render side keeps a declared-slot root of its own, and that one is NOT a
// duplicate: it names a schema block rather than a pillar, so it has no
// counterpart here and nothing to share.
const (
	sensitiveLeafRootValues = types.PillarRootValues
	sensitiveLeafRootInfra  = types.PillarRootInfra
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
func collectNodeFeatures(
	nodeID string,
	topoNode *topology.TopologyNode,
	topoResult *topology.TopologyResult,
	resolveResult *resolver.ResolveResult,
	catalogLookup resolver.CatalogLookup,
) (*featureResult, error) {
	result := newFeatureResult()

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
		componentOwner := leafOwner{
			scope: componentAttachmentScope(topoNode),
			site:  componentOwnSite(topoNode, nodeID),
		}
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
			if err := mergeContribution(result, fa.InstanceName, fa.Feature.Type, fa.Feature.Values, types.OrderedMap[any]{}, fa.Feature.Inject, catalogLookup, nodeID, prov, componentOwner); err != nil {
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
			// Recorded before the merge, from the contribution as authored. Each
			// level of the chain is recorded separately even where two levels
			// declare the same instance name: the delivery merge adds and replaces
			// per delivery key and removes no mechanism, so a mechanism any level
			// contributed survives into the merged attachment, and every level that
			// contributed an out-of-shape one is a separate edit to make.
			result.recordContributedFill(sf.name, resolver.ScopeAttachmentFieldPath(sf.scopePath, sf.name), sf.feat)
			owner := leafOwner{scope: scopeShared, site: constraint.AuthoredSite{ScopePath: sf.scopePath}}
			if err := mergeContribution(result, sf.name, sf.feat.Type, sf.feat.Values, types.OrderedMap[any]{}, sf.feat.Inject, catalogLookup, nodeID, explicitProvenance, owner); err != nil {
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
			// The site names the boundary and no component, because that is what
			// the operator wrote: a boundary-wide attachment reaches every
			// component the boundary holds, so there is no component entry to
			// point at and the boundary's own block is the one place it can be
			// changed.
			owner := leafOwner{scope: scopeShared, site: constraint.AuthoredSite{ScopePath: bf.scopePath}}
			if err := mergeContribution(result, bf.name, bf.feat.Type, types.OrderedMap[any]{}, bf.feat.Infra, nil, catalogLookup, nodeID, explicitProvenance, owner); err != nil {
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
			// The component segment is the node's own identifier and not the name
			// its Aggregate wrote, because that is the key the deployment uses:
			// a deployment addresses a component the composition contributed by
			// the qualified identifier the composition gave it. Which spelling a
			// document uses is a property of the document, which is why the site
			// is recorded where the document is read rather than derived later
			// from one naming rule applied to both.
			owner := leafOwner{
				scope: scopeComponent,
				site:  constraint.AuthoredSite{ScopePath: cf.scopePath, Component: nodeID},
			}
			if err := mergeContribution(result, cf.name, cf.feat.Type, types.OrderedMap[any]{}, cf.feat.Infra, nil, catalogLookup, nodeID, prov, owner); err != nil {
				return nil, err
			}
		}
	}

	// Derive top-level Origin / AnnotationKey / AnnotationSource per
	// IRFeatureEntry from the per-leaf provenance maps.
	finalizeTopLevelOrigin(result)

	// Complete the declaration-derived per-attachment facts — the attached
	// Feature's labels, the declared slot the attachment fills, and the two
	// sensitivity facts — over the merged attachments.
	finalizeAttachmentFacts(result, topoNode, catalogLookup)

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
// joinAttachmentViolations canonicalises the combined set.
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

// validateContributedFillSlots asks the slot-shape rule of every fill this node
// received from a broader scope, against the slot set the NODE's Component
// declares.
//
// This is the rule's post-cascade half, and the placement is the point. Which
// slot closes a fill is a property of the component the fill reached, so one
// subsystem-wide fill can be legal on one component in the scope and out of
// shape on another — a question the authored block cannot answer, because the
// block is identical and well-formed in both cases. It therefore runs where a
// node's attachment set is complete, beside the conformance gate and for the
// same reason: the subject is the effective merged attachment, never a
// pre-merge fragment.
//
// The decision comes from the resolver's compiled slot shape — the same
// validator the resolution-time guard over an authored block uses — so the two
// routes cannot diverge on what a slot permits. What differs is only the
// diagnostic: this one names the component and the slot and points at the scope
// the attachment was authored at.
//
// Free attachments are skipped, because Binding carries the answer the merge
// already computed: an instance name the node's Component declares no slot for
// is unconstrained, and an unresolved Component leaves Binding empty rather than
// claiming the slot set is known.
func validateContributedFillSlots(
	nodeID, entity string,
	result *featureResult,
	topoNode *topology.TopologyNode,
	catalogLookup resolver.CatalogLookup,
) []*resolver.ResolverError {
	if len(result.contributedFills) == 0 || topoNode == nil || catalogLookup.Component == nil {
		return nil
	}
	comp, found := catalogLookup.Component(topoNode.ComponentType)
	if !found || comp.Spec.FeaturesSchema.Len() == 0 {
		return nil
	}
	shape, ok := resolver.CompileAttachmentSlotShape(comp)
	if !ok {
		return nil
	}

	var errs []*resolver.ResolverError
	for _, fill := range result.contributedFills {
		// Binding is read rather than the slot map re-consulted: it is the
		// post-cascade answer to "which declared slot does this attachment fill",
		// and asking the map again here would be a second home for that lookup.
		entry, merged := result.Features.Get(fill.instance)
		if !merged || entry.Binding == "" || entry.Binding == types.AttachmentBindingFree {
			continue
		}
		if verr := shape.ValidateContributedFill(
			fill.instance, fill.contribution, true, entity, nodeID, fill.authoredLocator,
		); verr != nil {
			errs = append(errs, verr)
		}
	}
	return errs
}

// joinAttachmentViolations canonicalises the collected per-node attachment
// violations by (Entity, Field) — byte-identical across runs of the same input,
// matching the resolution-time gate's ordering — and joins them into one error.
// The joined error wraps every violation, so errors.Is holds against whichever
// sentinel each one carries.
//
// The set is joined rather than returned per rule so an author fixing a node's
// attachments sees every finding in one build. Two rules feed it — pillar
// conformance against each attachment's own Feature schema, and slot shape for a
// fill contributed from a broader scope — and both are questions about the same
// subject at the same moment, which is what makes one halt the honest shape for
// them.
func joinAttachmentViolations(errs []*resolver.ResolverError) error {
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
// FeatureOrder is appended only on first sight of an attachment instance
// name, so the order vector stays stable across re-merges of the same
// instance. It is the one execution order Phase A and Phase B.6 both follow.
func mergeContribution(
	result *featureResult,
	instanceName string,
	featType string,
	values, infra types.OrderedMap[any],
	inject *types.InjectBlock,
	catalogLookup resolver.CatalogLookup,
	nodeID string,
	provenance featureProvenance,
	owner leafOwner,
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
		recordLeafOwners(result.leafOwners, instanceName, values, infra, owner)
		return nil
	}

	if existing.Type != "" && featType != "" && existing.Type != featType {
		return cerrors.Wrap(
			ErrFeatureTypeMismatch(),
			cerrors.OpsError,
			cerrors.SourceLocation{File: nodeID},
			cerrors.ComposeMessage(ErrFeatureTypeMismatch(), []string{
				fmt.Sprintf("node %q", nodeID),
				fmt.Sprintf("attachment %q", instanceName),
				fmt.Sprintf("existing type %q conflicts with %q", existing.Type, featType),
			}, ""),
		)
	}

	for _, leaf := range types.FlattenLeaves(values) {
		key := scopeKey{instance: instanceName, pillar: pillarValues, leaf: leaf.Path}
		mergeLeaf(&existing.Values, &existing.ValuesProvenance, result.leafOwners, key, leaf.Value, provenance, owner)
	}
	for _, leaf := range types.FlattenLeaves(infra) {
		key := scopeKey{instance: instanceName, pillar: pillarInfra, leaf: leaf.Path}
		mergeLeaf(&existing.Infra, &existing.InfraProvenance, result.leafOwners, key, leaf.Value, provenance, owner)
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
	return nil
}

// recordLeafOwners stamps the owning contribution onto every leaf of a
// first-sight contribution, so a later broader-scope contribution to the same
// instance can be ranked against it and so a rule asked about the leaf can name
// where it was written. The traversal delegates to types.FlattenLeaves — the one
// home for "which leaves does this pillar have" — so it cannot disagree with the
// provenance newAttachmentEntry records over the same leaves.
func recordLeafOwners(
	owners map[scopeKey]leafOwner,
	instance string,
	values, infra types.OrderedMap[any],
	owner leafOwner,
) {
	for _, leaf := range types.FlattenLeaves(values) {
		owners[scopeKey{instance: instance, pillar: pillarValues, leaf: leaf.Path}] = owner
	}
	for _, leaf := range types.FlattenLeaves(infra) {
		owners[scopeKey{instance: instance, pillar: pillarInfra, leaf: leaf.Path}] = owner
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
// A leaf with provenance but no recorded owner falls back to the zero
// leafOwner, whose scope outranks nothing — so the incoming contribution
// wins rather than a missing record silently pinning a leaf.
//
// # The site moves with the value, always
//
// Whenever the leaf changes hands the incoming contribution's site replaces the
// outgoing one, and whenever it does not the recorded site is left alone. That
// is not bookkeeping symmetry: the site names the edit that produced what the
// leaf now holds, so a site left behind by a value that moved would point a
// reader at a document whose text does not appear in the build.
func mergeLeaf(
	pillarTree *types.OrderedMap[any],
	pillarProv *types.OrderedMap[types.FieldProvenance],
	owners map[scopeKey]leafOwner,
	key scopeKey,
	incomingValue any,
	incomingProv featureProvenance,
	incoming leafOwner,
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
		owners[key] = incoming
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
	if owners[key].scope.outranks(incoming.scope) {
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

// finalizeAttachmentFacts walks every IRFeatureEntry in the per-node result and
// completes the facts about the attachment that are properties of declarations
// rather than of the payload: the attached Feature's catalog labels and
// annotations, which declared slot the attachment fills, and the three derived
// sensitivity facts.
//
// It runs after the whole five-tier cascade has folded, for the same reason the
// attachment-conformance gate does: every one of these facts is a property of
// the EFFECTIVE merged attachment, never of a single pre-merge fragment. A
// later tier can replace the attachment's Feature type and can add or replace
// inject delivery keys, so a fact computed on first sight would describe a
// contribution that went on to lose the precedence contest — it would report
// the labels of a Feature the merged attachment no longer names.
//
// The facts:
//
//   - Labels and CatalogAnnotations — the attached Feature's catalog labels and
//     its catalog annotations, each copied verbatim in the entity's own
//     declaration order. Nothing here filters, normalises or rewrites either: a
//     transformed copy would be a second, compiler-shaped version of metadata a
//     catalog author wrote, and a consumer reasoning about that version would be
//     reasoning about something nobody declared. Both come from one resolution
//     of the entity, so the two cannot describe different Features.
//   - Binding — the declared slot the attachment fills, or
//     types.AttachmentBindingFree when it fills none. Left empty when the host
//     Component resolves to no catalog entity, because "fills no declared
//     slot" and "the declared slot set is unknown" are different answers.
//   - Sensitive — true when the merged attachment's Feature declares a sensitive
//     leaf in values, infra or provides, or when the host Component declares its
//     attachment slot of the same name sensitive.
//   - SensitiveInject — the merged inject delivery keys whose referenced
//     provides key the Feature declares sensitive.
//   - SensitiveAuthoredLeaves — the merged values and infra leaves the
//     attachment authored a value at whose path the Feature declares sensitive.
//
// The three sensitivity facts sit at three units — one whole attachment, one
// inject delivery key, one authored leaf — and each is computed from its own
// inputs. None is derived from another, and in particular the per-attachment
// answer is never spread down to the narrower units: it is true whenever the
// Feature declares sensitivity anywhere, which says nothing about a specific
// delivery key or a specific leaf, so spreading it would assert something false
// about a datum an author left readable.
//
// The slot pairs with the attachment by NAME for both Binding and the slot half
// of Sensitive: a deployment fills a slot by attaching a Feature under the
// slot's name, so the attachment's instance name IS the slot name. The slot set
// belongs to the NODE's Component, so it is the same set whichever cascade tier
// contributed the attachment.
//
// The leaf half of Sensitive reads what the Feature's schema DECLARES, not the
// subset of sensitive leaves this attachment happened to author a value for.
// Sensitivity is a property of the catalog entity plus the slot it fills, so a
// linter, a guest or a policy can reason about the entity in isolation;
// intersecting with the carried values would make the same field sensitive in
// one deployment and not another.
//
// SensitiveAuthoredLeaves does intersect, and the two are not in tension because
// they are answers to different questions. Sensitive answers what the entity is,
// which must not vary by deployment. SensitiveAuthoredLeaves answers where this
// deployment put data, which varies by deployment necessarily — a leaf is named
// there only because this attachment authored a value at it. Neither rule
// belongs on the other field: intersecting inside Sensitive would make one
// entity sensitive in one deployment and not another, and declining to intersect
// in SensitiveAuthoredLeaves would name leaves that carry nothing.
//
// Every input is a declaration or a mapping the compiler itself built. No value
// is inspected, and no meaning is read out of a name: the attachment's name is
// used as an exact key against the declared slot map, nothing more.
func finalizeAttachmentFacts(result *featureResult, topoNode *topology.TopologyNode, catalogLookup resolver.CatalogLookup) {
	if result.Features.Len() == 0 {
		return
	}

	componentType := ""
	if topoNode != nil {
		componentType = topoNode.ComponentType
	}
	slots, slotsResolved := ComponentAttachmentSlots(catalogLookup, componentType)
	sensitiveSlots := SensitiveSlots(slots)

	declaredCache := map[string]resolver.SensitiveLeaves{}
	declaredFor := func(featureType string) resolver.SensitiveLeaves {
		if d, cached := declaredCache[featureType]; cached {
			return d
		}
		var declared resolver.SensitiveLeaves
		if compiled, ok := CompileFeatureSchema(catalogLookup, featureType); ok {
			declared = resolver.DeclaredSensitiveLeaves(compiled)
		}
		declaredCache[featureType] = declared
		return declared
	}

	for name, entry := range result.Features.All() {
		declared := declaredFor(entry.Type)
		_, slotDeclared := slots.Get(name)

		entry.Labels, entry.CatalogAnnotations = featureCatalogLabelsAndAnnotations(catalogLookup, entry.Type)
		if slotsResolved {
			entry.Binding = types.AttachmentBindingFree
			if slotDeclared {
				entry.Binding = name
			}
		}

		sensitive := declared.Any() || sensitiveSlots[name]
		marked := deriveInjectSensitivity(entry.Inject, declared.Provides)
		markedLeaves := deriveAuthoredSensitiveLeaves(entry.Values, entry.Infra, declared)
		if sensitive || marked.Len() > 0 || len(markedLeaves) > 0 {
			// A marked delivery key implies a sensitive provides declaration,
			// and a marked leaf implies a sensitive values or infra
			// declaration; either implies a sensitive attachment. So the
			// second and third terms guard cases the derivation above cannot
			// produce rather than live ones.
			entry.Sensitive = sensitive
			entry.SensitiveInject = marked
			entry.SensitiveAuthoredLeaves = markedLeaves
		}
		result.Features.Set(name, entry)
	}
}

// featureCatalogLabelsAndAnnotations copies the catalog labels and the catalog
// annotations of one attached Feature into fresh OrderedMaps, each preserving
// the entity's own declaration order.
//
// The two travel together because they come from ONE resolution of ONE entity.
// Resolving the Feature twice — once per map — would be two lookups of one
// entity in one build, and the second could in principle answer differently
// from the first; returning both from one resolution makes "these two maps
// describe the same Feature" structural rather than incidental.
//
// The copy is what keeps the catalog entity immutable from the IR side: an
// aliased map would let a later write to the IR reach back into the loaded
// entity. Nothing is added, dropped or rewritten in either copy — the verbatim
// contract IRFeatureEntry.Labels and IRFeatureEntry.CatalogAnnotations state is
// these two copies and nothing else. In particular there is no filter, no
// prefix rule and no normalisation: a key in an author's own namespace arrives
// exactly as authored, which is the whole point of carrying the metadata rather
// than a judgement about it.
//
// Returns empty maps when the type resolves to no catalog entity, which is the
// same answer an entity declaring neither gives.
func featureCatalogLabelsAndAnnotations(catalogLookup resolver.CatalogLookup, featType string) (labels, annotations types.OrderedMap[string]) {
	if catalogLookup.Feature == nil || featType == "" {
		return labels, annotations
	}
	feat, found := catalogLookup.Feature(featType)
	if !found {
		return labels, annotations
	}
	return copyOrderedStringMap(feat.Metadata.Labels), copyOrderedStringMap(feat.Metadata.Annotations)
}

// deriveInjectSensitivity selects the delivery keys of one merged attachment's
// inject whose referenced provides key the attachment's Feature declares
// sensitive, preserving inject's own mechanism -> delivery key shape and
// carrying the referenced provides key through as the value.
//
// The provides key a delivery key names is read exactly as the delivery pass
// reads it — the mapping's value, verbatim — so the two cannot disagree about
// which declaration a delivery key resolves against.
//
// A `from:` naming a key the Feature's schema does not declare selects nothing
// here. This derivation answers only whether a referenced key is declared
// sensitive, never whether the reference itself is legal; judging the reference
// belongs to the checks over inject references, and a second judgement here
// would be a second home for it.
//
// An attachment declaring no delivery mechanism — the shape a slot takes when
// its Recipe consumes the material internally rather than delivering it to the
// workload — has no delivery key, so the result is empty. That is the honest
// answer rather than a synthesised key: there is nothing delivered to mark.
func deriveInjectSensitivity(inject types.OrderedMap[types.OrderedMap[string]], sensitiveProvides map[string]bool) types.OrderedMap[types.OrderedMap[string]] {
	out := types.OrderedMap[types.OrderedMap[string]]{}
	if inject.Len() == 0 || len(sensitiveProvides) == 0 {
		return out
	}
	for mechanism, deliveries := range inject.All() {
		marked := types.OrderedMap[string]{}
		for deliveryKey, providesKey := range deliveries.All() {
			if sensitiveProvides[providesKey] {
				marked.Set(deliveryKey, providesKey)
			}
		}
		if marked.Len() > 0 {
			out.Set(mechanism, marked)
		}
	}
	return out
}

// deriveAuthoredSensitiveLeaves selects the leaves one authored values/infra
// pair holds whose path an entity's schema declares sensitive, returning each as
// its pillar-prefixed coordinate.
//
// It is the ONE home for the per-authored-leaf marking, and its three subjects
// are the three places the graph carries authored pillar data: a merged
// attachment's own values and infra, a node's values and infra against its
// Component's declarations, and a boundary attachment's infra against its
// Feature's. Each caller supplies the payloads and the declarations; the rule
// joining them lives only here, so a surface added later cannot come to mark on
// a different reading from the surfaces already marked.
//
// The join asks [resolver.SensitiveLeaves.Covers], which is the one home for
// "does this entity's declarations reach this authored datum" and is the same
// question every withholding surface asks of the same declarations. A
// declaration COVERS a leaf when it names that leaf or names an object
// containing it, so a leaf beneath an object an author declared sensitive is
// named by that declaration and marking it claims nothing the author did not
// write. Matching is segment-wise, so a declaration at "db" reaches "db.password"
// and not a sibling "dbPassword".
//
// The direction the covering rule is NOT allowed to slip into is worth naming
// where the join is made. Marking may not claim more than an author declared,
// because a marking becomes a refusal or a concealment downstream and there is
// no cheap undo for calling public data secret. Covering a subtree an author
// declared is not that: the over-claim would be marking a leaf no declaration
// reaches, which this join cannot do because every input is a declaration.
// Nothing reads a path's own text and no value is inspected — whether the author
// wrote the material itself at a marked leaf or a pointer to it is not knowable
// here and is not what the marking claims.
//
// Each payload is FLATTENED rather than read at its top level, which is
// load-bearing. A pillar is a tree: a nested leaf lives inside a nested map, so
// the pillar's own top-level keys name only the shallowest leaves. Walking those
// keys alone would name a top-level leaf and silently miss every nested one, and
// silence is the wrong direction to fail in for this axis.
//
// The two pillars are collected in declaration order, values before infra, and
// each pillar's leaves arrive in the order the payload holds them. That is
// author order throughout — the payloads are order-preserving containers, so no
// sort is needed and none is applied. Sorting would replace an author order a
// consumer can act on with one it cannot.
//
// Returns nil when the entity declares no sensitive leaf in either pillar, when
// it resolves to no schema at all, or when nothing is authored at the leaves it
// does declare. That is the honest answer rather than an empty list: there is no
// marked leaf to name. A caller with only one pillar to offer passes the zero
// value for the other, which names nothing.
func deriveAuthoredSensitiveLeaves(values, infra types.OrderedMap[any], declared resolver.SensitiveLeaves) []string {
	var out []string
	collect := func(pillar types.OrderedMap[any], root string) {
		if pillar.Len() == 0 {
			return
		}
		for _, leaf := range types.FlattenLeaves(pillar) {
			coordinate := root + leaf.Path
			if declared.Covers(coordinate) {
				out = append(out, coordinate)
			}
		}
	}
	collect(values, sensitiveLeafRootValues)
	collect(infra, sensitiveLeafRootInfra)
	return out
}

// namedFeature pairs an instance name with a FeatureInstance for ordered
// collection, plus the dotted subsystem path of the level that declared it.
//
// scopePath is the declaring level, not the node's own path: a chain walk emits
// an outer subsystem's attachments to every node beneath it, so the two differ
// for any node deeper than the level that declared the attachment. Carried here
// because it is only knowable inside the walk — by the time a contribution has
// been folded, which level supplied it is gone.
type namedFeature struct {
	name      string
	scopePath string
	feat      types.FeatureInstance
}

// namedBoundaryFeature pairs an instance name with a BoundaryFeature, plus the
// path of the boundary that declared it.
//
// scopePath is the declaring boundary, not the node's own: a chain walk emits an
// outer boundary's attachments to every node beneath it, and the per-component
// scan reaches any boundary anywhere in the tree, so the two differ for most
// contributions. Carried here for the reason the subsystem side carries its
// own — it is only knowable inside the walk, and by the time a contribution has
// been folded, which boundary supplied it is gone.
type namedBoundaryFeature struct {
	name      string
	scopePath string
	feat      types.BoundaryFeature
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

	var walked []string
	for _, part := range parts {
		sub, ok := current.Get(part)
		if !ok {
			break
		}
		walked = append(walked, part)
		scopePath := strings.Join(walked, ".")
		// Collect this subsystem's features (author order)
		for fname, feat := range sub.Features.All() {
			result = append(result, namedFeature{name: fname, scopePath: scopePath, feat: feat})
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

	var scopePath string
	for _, part := range parts {
		boundary, ok := current.Get(part)
		if !ok {
			break
		}
		scopePath = boundaryScopePath(scopePath, part, boundary.Synthesized)
		// Collect boundary-wide features (author order)
		for fname, bf := range boundary.Features.All() {
			result = append(result, namedBoundaryFeature{name: fname, scopePath: scopePath, feat: bf})
		}
		current = boundary.Boundaries
	}

	return result
}

// boundaryScopePath extends a boundary path by one level, joined the way the
// boundary axis joins its levels.
//
// A boundary normalization synthesized contributes no level and resets the path
// to empty, because it is not a boundary any file spells: it is created to hold
// the components a Release wrote at its top level, so an entry found inside one
// was written at that top level and naming the generated scope would point at a
// key the document does not contain. It keys on the provenance flag rather than
// on the name, so a boundary an operator declares and happens to name the same
// as the synthesized one keeps its level. This is the boundary axis's form of
// the rule the component locator applies to a synthesized scope on the
// application side.
func boundaryScopePath(prefix, name string, synthesized bool) string {
	if synthesized {
		return ""
	}
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
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
//
// The resolver's Aggregate-versus-Release type-agreement guard runs the same
// descent over the same surface and is deliberately not shared with this one.
// Every occurrence is kept here, in encounter order, because they are merged in
// sequence and the order determines the resulting bytes; that guard folds to a
// name-keyed map because its question is per-name and single-valued. If the two
// are ever unified, the traversal is the part to share — each caller keeps its
// own fold, since collapsing them would change this cascade's output.
func collectReleaseComponentFeatures(boundaries types.OrderedMap[types.Boundary], nodeID string) []namedBoundaryFeature {
	var result []namedBoundaryFeature
	collectCompFeaturesRecursive(boundaries, "", nodeID, &result)
	return result
}

// collectCompFeaturesRecursive walks the boundary tree accumulating the path it
// descended through, because a contribution's own boundary is what a diagnostic
// about one of its leaves points at and the recursion is the only place it is
// known. The path is joined the way the boundary axis joins its levels, so a
// value taken from here splits back into the levels the operator wrote.
func collectCompFeaturesRecursive(
	boundaries types.OrderedMap[types.Boundary],
	prefix string,
	nodeID string,
	result *[]namedBoundaryFeature,
) {
	for name, boundary := range boundaries.All() {
		path := boundaryScopePath(prefix, name, boundary.Synthesized)
		components := types.BoundaryComponents(boundary)
		if bc, ok := components.Get(nodeID); ok {
			for fname, feat := range bc.Features.All() {
				*result = append(*result, namedBoundaryFeature{name: fname, scopePath: path, feat: feat})
			}
		}
		collectCompFeaturesRecursive(boundary.Boundaries, path, nodeID, result)
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
//
// It also derives each attachment's per-authored-leaf sensitivity marking, from
// the attached Feature's own declarations, through the one derivation every
// marked surface shares. The catalog is taken as a parameter rather than reached
// for inside, because that is what makes the marking a property of the
// projection: an attachment reaches the graph through this walk or not at all,
// so a boundary shape added later carries the marking without anyone having to
// remember it. A caller with no catalog gets an unmarked projection, which is
// the same answer a Feature declaring nothing gives.
//
// Only the infra pillar is offered to the derivation, because a boundary-level
// attachment authors no values — the Release pillar-split forbids a values block
// at boundary level, and [types.IRBoundaryFeature] has no field for one.
func buildBoundaryFeaturesProjection(
	boundaries types.OrderedMap[types.Boundary],
	catalogLookup resolver.CatalogLookup,
) types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]] {
	out := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	// Memoised per Feature type across the whole walk: the schema compile is
	// uncached, and two boundaries attaching one Feature type read the same
	// answer. The memo's lifetime is this projection, so it cannot outlive the
	// catalog it was read from.
	declaredCache := map[string]resolver.SensitiveLeaves{}
	declaredFor := func(featureType string) resolver.SensitiveLeaves {
		if d, cached := declaredCache[featureType]; cached {
			return d
		}
		var declared resolver.SensitiveLeaves
		if compiled, ok := CompileFeatureSchema(catalogLookup, featureType); ok {
			declared = resolver.DeclaredSensitiveLeaves(compiled)
		}
		declaredCache[featureType] = declared
		return declared
	}
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
					inner.Set(fname, types.IRBoundaryFeature{
						Type:  bf.Type,
						Infra: bf.Infra,
						SensitiveAuthoredLeaves: deriveAuthoredSensitiveLeaves(
							types.OrderedMap[any]{}, bf.Infra, declaredFor(bf.Type),
						),
					})
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
