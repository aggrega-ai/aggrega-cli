// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// BuildIRConfig is the input bundle for BuildIRContext. These four fields are
// all required for a successful build:
//
//   - Topology supplies the flattened node graph plus edges and per-node
//     attachments. Step 2 produces it; the IR builder reads Nodes (in author
//     order, OrderedMap-backed), Edges (in compositional author order — the
//     cross-product of component sequence × within-component `uses:` socket
//     order × within-socket alias order × per-node attachment order
//     — consumed in slice order without re-sorting), and
//     FeatureAttachments.
//   - ResolveResult supplies the original Aggregate / Manifest / Release
//     plus per-node BoundaryInfra. The IR builder reads Aggregate for
//     subsystem traversal (Tier 3), Release for boundary traversal (Tiers
//     4 + 5), and BoundaryInfra for the IRSpec InfraMapping, the
//     release-annotations bucket, and per-node sync-wave assignment
//     (BoundaryInfra.SyncWave is populated by the resolver from each
//     boundary's syncWave field; default 0).
//   - CatalogLookup is the same callback shape produced by pkg/catalog. The
//     IR builder uses it to resolve component `metadata.version` (for
//     IRNode.ResolvedVersion), to extract catalog annotations and the
//     CatalogMetadata enterprise suite, and to resolve each attachment's own
//     catalog version.
//   - CompilerVersion is plumbed through to provenance generation so the
//     emitted IRGraph records the binary that produced it. The orchestrator
//     at cmd/aggrega/build.go is the production caller and supplies the
//     CLI's version string here.
//
// Realization is the one optional field: the binding each attachment's
// realization is recorded from, on a node's attachments and a boundary's
// alike. A nil binding records none, and the graph is otherwise identical.
type BuildIRConfig struct {
	Topology        *topology.TopologyResult
	ResolveResult   *resolver.ResolveResult
	CatalogLookup   resolver.CatalogLookup
	CompilerVersion string
	Realization     RealizationBinding
}

// FeatureOrder is BuildIRContext's secondary return value: per-node, the ordered
// list of Feature instance names in cascade-tier sequence (sub-aggregate
// component → root-aggregate component → root-aggregate subsystem →
// release boundary → release component-specific). The map keys by
// nodeID; the slice value is the deterministic execution order Phase A
// and Phase B of the renderer must follow.
//
// FeatureOrder exists as a separate slice rather than relying on the Go
// map iteration order of IRSpec.FeatureValuesMapping because Go map
// iteration is deliberately randomised and would defeat the byte-for-byte
// determinism contract the compiler commits to. The renderer's Phase A
// reads FeatureOrder[nodeID] to dispatch FeatureRecipes in tier order —
// see internal/compiler/renderer/phase_a.go's ExecutePhaseA.
type FeatureOrder map[string][]string

// AttachmentLeafKey addresses one leaf of one pillar of one attachment on one
// node — the coordinate a rule over such a leaf is invoked at.
type AttachmentLeafKey struct {
	Node       string
	Attachment string
	Pillar     string
	Leaf       string
}

// attachmentKey addresses one attachment on one node.
type attachmentKey struct {
	node       string
	attachment string
}

// AttachmentSites is BuildIRContext's third return value: where the
// contributions the attachment cascade folded were written. It answers two
// questions about them — per attachment leaf, the site the contribution that won
// that leaf was written at; and per attachment, every place that contributed the
// attachment at all.
//
// It exists because the graph cannot answer either question and nothing else
// can. An attachment's configuration is assembled from every scope reaching
// its node, leaf by leaf, so a scope-wide contribution and a component-scoped
// one on the same attachment each keep the leaves the other did not mention.
// Which of them supplied any one leaf, and which scopes contributed the
// attachment in the first place, is settled while the cascade folds and is
// gone the moment it finishes: the merged entry carries the winning value and
// its origin, never the contest and never the tiers. A diagnostic about that
// leaf has to point at an edit somebody can make, and the only edit that exists
// is the one that won; a diagnostic about the attachment has to name every place
// it was written, because removing it means changing each of them.
//
// Recovering either afterwards is refused rather than merely unimplemented. It
// would mean re-running the precedence, and walking the scope trees, over the
// authoring documents at each reader — a second derivation of values already
// derived once, free to disagree with the fold that produced the bytes.
//
// # What it holds, and what bounds it
//
// One small site per leaf — a scope path and a component name — and one short
// list of places per attachment, and never the contributions themselves. The
// retention therefore grows with the coordinates the rules are invoked at, a
// leaf or an attachment apiece, and with the tiers that actually wrote each
// attachment, rather than with whole authored payloads.
//
// # Where each attachment as a whole is positioned
//
// It answers a third question: where an attachment as a whole was written — the
// block a finding about the attachment names, in the document whose author wrote
// it. The node an attachment lands on cannot say: an attachment reaches it from
// the component's own block, from a scope of either document, from the
// Release's entry for the component, or from a connection entry, and only the
// first is a key in the component's own block. The answer is chosen by the rule
// that positions the conformance of the same attachment's configuration, from
// the blocks the cascade recorded each contribution was written in, so a finding
// about the attachment and a finding about its values name one block.
//
// All three are addressed by key only and never iterated, so none has any
// bearing on the order anything is emitted in. The zero value holds none and
// reads as absent, which is what lets a presentation tell a projection nobody
// supplied from one that simply holds no entry; [newAttachmentSites] is the one
// construction site of a present one.
type AttachmentSites struct {
	leaves        map[AttachmentLeafKey]constraint.AuthoredSite
	contributions map[attachmentKey][]constraint.AttachmentContribution
	attachments   map[attachmentKey]constraint.AttachmentSite
}

// newAttachmentSites is the one construction site of a present projection, so
// no writer can produce one whose halves are unallocated.
func newAttachmentSites() AttachmentSites {
	return AttachmentSites{
		leaves:        map[AttachmentLeafKey]constraint.AuthoredSite{},
		contributions: map[attachmentKey][]constraint.AttachmentContribution{},
		attachments:   map[attachmentKey]constraint.AttachmentSite{},
	}
}

// recordAttachmentSites writes, for each attachment one node carries, the block
// the attachment as a whole is positioned at — through the same site rule the
// conformance of its configuration is positioned by, asked for the attachment
// rather than for one pillar. An attachment the rule records no block for — one
// annotation-driven expansion produced — gets no entry.
//
// The walk ranges an ordered map and writes into a map addressed by key, so the
// order it takes reaches nothing emitted.
func (s AttachmentSites) recordAttachmentSites(
	nodeID string,
	attachments types.OrderedMap[types.IRFeatureEntry],
	siteOf attachmentSiteRule,
) {
	for name := range attachments.All() {
		if _, site, recorded := siteOf(name, ""); recorded {
			s.attachments[attachmentKey{node: nodeID, attachment: name}] = site
		}
	}
}

// attachmentSiteOf is where one attachment as a whole was written, and whether
// the projection records a block for it. It is the one reader of that half.
func (s AttachmentSites) attachmentSiteOf(node, attachment string) (constraint.AttachmentSite, bool) {
	site, recorded := s.attachments[attachmentKey{node: node, attachment: attachment}]
	return site, recorded
}

// present reports whether this projection was built at all, as against being the
// zero value a caller that supplied none holds.
func (s AttachmentSites) present() bool {
	return s.leaves != nil && s.contributions != nil
}

// siteOf is the site recorded for one attachment leaf, and the zero site when
// the projection holds none for it.
//
// Every reader addresses the projection through here rather than composing the
// key where it reads. The key is a composite, and a field added to it is
// silently defaulted in whatever keyed literal does not set it — so a second
// reader spelling its own key would go on compiling while addressing a
// different entry than the projection wrote. The projection's own writer still
// composes a key, because it defines the mapping rather than consulting it.
//
// The zero site is a real answer and not a lookup failure: it names no scope
// and no component, which is what a leaf outside the projection has to read as.
func (s AttachmentSites) siteOf(node, attachment, pillar, leaf string) constraint.AuthoredSite {
	return s.leaves[AttachmentLeafKey{
		Node:       node,
		Attachment: attachment,
		Pillar:     pillar,
		Leaf:       leaf,
	}]
}

// contributionsOf is the list of places one attachment on one node was
// contributed from, in cascade order, and nil for an attachment the projection
// holds no entry for. It is the one reader of that half, for the reason siteOf
// is the one reader of the other.
func (s AttachmentSites) contributionsOf(node, attachment string) []constraint.AttachmentContribution {
	return s.contributions[attachmentKey{node: node, attachment: attachment}]
}

// BuildIRContext executes Step 3 of the compiler pipeline: graph finalization.
//
// It is the ONLY graph-construction entry this package exports, and the only
// site that may call it is the compiler pipeline's own graph-construction
// chokepoint. A surface that assembles a graph for itself gets one built
// outside the sequence the pipeline runs around this step, and the two then
// differ in whatever that sequence contributes — which is invisible at the call
// site, because both produce a graph. The constraint is not expressible in the
// language across a package boundary, so a source-walking guard asserts it; the
// guard names its own coverage bound.
//
// ctx carries cooperative cancellation: the pass checks ctx.Err() at the
// per-node build boundary so a long IR-assembly over a large graph can be
// interrupted (for example by the daemon's shutdown drain). Cancellation only
// aborts — it never alters a successful build's output.
//
// Steps performed (numbered to match the body's section comments):
//
//  1. Build IR nodes from topology nodes — including labels, paths,
//     annotation buckets, catalog metadata, ResolvedVersion, SyncWave
//     (per-boundary from the resolver), and the SelfInject pipe.
//  2. Convert topology edges to IR edges — preserving Source / Target /
//     Socket / Alias / OriginFeature (the per-edge origin marker that
//     keeps Feature-authored edges separable from Component-authored
//     edges) and converting the typed inject block via convertInjectBlock.
//  3. Collect and normalize features through the per-node feature
//     cascade via collectNodeFeatures.
//  4. Populate values / infra / feature mappings on the IRSpec.
//  5. Build the three annotation buckets via buildAnnotationBuckets
//     (catalog / release / aggregate; the aggregate bucket is empty
//     today because the schema bans annotations on Aggregate
//     ComponentInstance blocks).
//  6. Generate provenance via buildProvenance.
//
// Determinism. Iteration over the topology's flattened nodes preserves the
// Aggregate's author-declared component order — topology.Nodes is an
// OrderedMap populated during subsystem flattening in author order, and
// author order flows through to IR JSON output via OrderedMap.MarshalJSON.
// A node's attachments iterate in the topology attachment order; the
// edges slice is inherited from
// topology in compositional author order (component sequence ×
// within-component `uses:` socket order × within-socket alias order ×
// per-node attachments — topology.Build step 4 drives
// the cross-product via OrderedMap.All()). Both inheritance commitments
// are the consumer side of the cross-package determinism contract — see
// this package's doc.go for the full statement.
//
// Error model. The function only returns an error if collectNodeFeatures
// fails — which today happens exclusively on ErrFeatureTypeMismatch.
// All other operations either succeed or fall through (missing
// CatalogLookup, missing Release, JSON-marshal failures on values/infra
// mapping leave the slot empty rather than aborting). Each caller adds its
// own context prefix when wrapping the returned error.
func BuildIRContext(ctx context.Context, cfg BuildIRConfig) (*types.IRGraph, FeatureOrder, AttachmentSites, error) {
	topo := cfg.Topology
	res := cfg.ResolveResult

	irNodes := types.OrderedMap[types.IRNode]{}
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping := types.OrderedMap[json.RawMessage]{}
	featureValuesMapping := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	featureOrder := make(FeatureOrder)
	// Per attachment leaf, where the contribution that won it was written, and per
	// attachment, every place that contributed it. It is filled for every node
	// rather than for the nodes some rule might reach, per AttachmentSites' own
	// contract.
	attachmentSites := newAttachmentSites()

	// The per-node attachment gates. Both subjects only exist after the 5-Tier
	// merge below — the merged per-attachment values/infra, and which declared
	// slot each merged attachment fills — so these run here rather than with the
	// guards over the authored document. compiledFeature memoises the compiled
	// Feature schema per type (Secrets resolve through the same Feature lookup);
	// attachmentErrs collects across all nodes for one joined halt.
	featureSchemaCache := map[string]compactform.Schema{}
	featureSchemaMiss := map[string]bool{}
	compiledFeature := func(featureType string) (compactform.Schema, bool) {
		if c, ok := featureSchemaCache[featureType]; ok {
			return c, true
		}
		if featureSchemaMiss[featureType] {
			return nil, false
		}
		compiled, ok := CompileFeatureSchema(cfg.CatalogLookup, featureType)
		if !ok {
			featureSchemaMiss[featureType] = true
			return nil, false
		}
		featureSchemaCache[featureType] = compiled
		return compiled, true
	}

	// Declared-sensitive leaves per Component type, memoised across the node walk
	// for the reason the Feature compile above is. The reader is made here, once
	// per build, so the memo's lifetime is this build and a catalog edit between
	// builds is never carried over. It is the same reader the label-transform
	// input withholds a node's values against, so the two cannot disagree about
	// what one Component declared; a catalog-less build declares nothing.
	declaredComponentLeaves := topology.DeclaredComponentLeaves(cfg.CatalogLookup)
	var attachmentErrs []*resolver.ResolverError
	var attachmentParams map[string]any
	attachmentEntity := "Aggregate"
	if res != nil {
		attachmentParams = res.Parameters
		if res.Aggregate != nil {
			attachmentEntity = fmt.Sprintf("Aggregate %s", res.Aggregate.Metadata.Name)
		}
	}

	// 1. Build IR nodes in author order (topo.Nodes is OrderedMap-backed).
	for nodeID, topoNode := range topo.Nodes.All() {
		if err := ctx.Err(); err != nil {
			return nil, nil, AttachmentSites{}, err
		}

		catalogAnnotations, releaseAnnotations, aggregateAnnotations := buildAnnotationBuckets(
			nodeID, topoNode.ComponentType, cfg.CatalogLookup, res.BoundaryInfra, topoNode.AggregateAnnotations,
		)

		irNode := types.IRNode{
			Labels:               topoNode.Labels,
			Component:            topoNode.ComponentType,
			SubsystemPath:        topoNode.SubsystemPath,
			SynthesizedSubsystem: topoNode.SynthesizedSubsystem,
			BoundaryPath:         topoNode.BoundaryPath,
			SynthesizedBoundary:  topoNode.SynthesizedBoundary,
			CatalogAnnotations:   catalogAnnotations,
			ReleaseAnnotations:   releaseAnnotations,
			AggregateAnnotations: aggregateAnnotations,
			CatalogMetadata:      buildCatalogMetadata(topoNode.ComponentType, cfg.CatalogLookup),
			SelfInject:           topoNode.SelfInject,
			SourceFile:           topoNode.SourceFile,
			// The authored block, carried as written rather than reconstructed
			// from the edges below: normalisation drops a socket that produced no
			// edge, so reconstruction could not recover which sockets the author
			// filled. The one exception is a connection attachment's values, which
			// authoredUsesCopy leaves out; its godoc carries why.
			Uses: authoredUsesCopy(topoNode.Uses),
		}

		// Resolved version + schema-declared pillar defaults from catalog.
		// Prefer the composed effective schema for an extends-ing Component so
		// inherited defaults materialise into the rendered output (consistent
		// with the conformance gate validating against the same effective
		// schema); fall back to the authored schema for a non-extends Component.
		var valueDefaults, infraDefaults types.OrderedMap[any]
		if cfg.CatalogLookup.Component != nil {
			catComp, found := cfg.CatalogLookup.Component(topoNode.ComponentType)
			if found {
				irNode.ResolvedVersion = catComp.Metadata.Version
				if eff, ok := effectiveComponentSchema(cfg.CatalogLookup, topoNode.ComponentType); ok {
					valueDefaults = compactform.PillarDefaults(eff, "values")
					infraDefaults = compactform.PillarDefaults(eff, "infra")
				} else {
					valueDefaults, infraDefaults = componentPillarDefaults(catComp, cfg.CatalogLookup.SchemaLibraries)
				}
			}
		}

		// Sync wave from the node's containing boundary's per-boundary SyncWave field.
		// Resolver pre-populates BoundaryInfra.SyncWave during the boundary-infra
		// cascade; default 0 for boundaries that omit syncWave or for nodes without
		// BoundaryInfra context.
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil {
			irNode.SyncWave = bi.SyncWave
		}

		// 4. Values mapping — author values plus declared schema defaults for
		// keys the Aggregate omitted (author values win).
		filledValues := mergeSchemaDefaults(topoNode.Values, valueDefaults)
		if filledValues.Len() > 0 {
			raw, err := json.Marshal(filledValues)
			if err == nil {
				valuesMapping.Set(nodeID, raw)
			}
		}

		// Infra mapping from boundary infra plus declared schema defaults for
		// keys the Release omitted (Release values win).
		var baseInfra types.OrderedMap[any]
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil {
			baseInfra = bi.Infra
		}
		filledInfra := mergeSchemaDefaults(baseInfra, infraDefaults)
		if filledInfra.Len() > 0 {
			raw, err := json.Marshal(filledInfra)
			if err == nil {
				infraMapping.Set(nodeID, raw)
			}
		}

		// The node's own per-leaf sensitivity marking, over the SAME two
		// payloads the two mappings above carry rather than over the authored
		// blocks they were built from. A default the schema materialised at a
		// declared-sensitive leaf is a datum the graph carries, so a marking read
		// off the pre-merge blocks would leave it unnamed. The declarations come
		// from the node's Component through the same compile-and-read pair every
		// other consumer of a Component's declarations uses, so no surface can
		// come to a different answer about what this Component declared.
		irNode.SensitivePayloadLeaves = derivePayloadSensitiveLeaves(
			filledValues, filledInfra, declaredComponentLeaves(topoNode.ComponentType),
		)

		irNodes.Set(nodeID, irNode)

		// 3. Feature normalization (5-Tier Onion Model)
		featResult, err := collectNodeFeatures(
			nodeID, topoNode, topo, res, cfg.CatalogLookup,
		)
		if err != nil {
			return nil, nil, AttachmentSites{}, err
		}

		// Which FeatureRecipe the Manifest binds to each merged attachment, read
		// from the caller's binding — the lookup the render binds the same
		// attachment by — so every surface handing this graph out names the
		// FeatureRecipe a build of the same documents renders it with.
		bindRealizations(&featResult.Features, cfg.Realization)

		if featResult.Features.Len() > 0 {
			featureValuesMapping.Set(nodeID, featResult.Features)
			featureOrder[nodeID] = featResult.FeatureOrder
		}

		featResult.projectAttachmentSites(nodeID, attachmentSites)

		// Where each attachment was written. One site rule serves both readers
		// below it — the conformance gate here, and the rules over the finalised
		// graph through the recorded sites — so the two cannot come to name
		// different blocks for one attachment.
		siteOf := attachmentSiteRuleFor(topoNode, featResult.places, res, attachmentEntity)
		attachmentSites.recordAttachmentSites(nodeID, featResult.Features, siteOf)

		// Validate the merged attachment pillars against each attachment's
		// declared Feature schema (collected across all nodes for one joined
		// halt below).
		attachmentErrs = append(attachmentErrs,
			validateNodeAttachments(nodeID, featResult.Features,
				attachmentParams, attachmentEntity, compiledFeature, siteOf)...)

		// Ask the slot-shape rule of every fill this node received from a broader
		// scope. It belongs here and not with the authored-document guards for the
		// reason the line above belongs here: which slot closes a contributed fill
		// is a property of the component it reached, so the pair of the two is the
		// earliest subject that can see the defect.
		attachmentErrs = append(attachmentErrs,
			validateContributedFillSlots(nodeID, attachmentEntity, featResult,
				topoNode, cfg.CatalogLookup)...)
	}

	if len(attachmentErrs) > 0 {
		return nil, nil, AttachmentSites{}, joinAttachmentViolations(attachmentErrs)
	}

	// 2. Convert topology edges to IR edges. A socket's declared per-edge
	// defaults materialise here — for both the `values` and the `infra` pillar,
	// for keys the author omitted — under the same default-fill contract the
	// component pillars get above, so a declared default reaches rendered output
	// instead of being validation-only. Applying it at IR-build keeps the
	// render-side projection a verbatim, catalog-free JOIN: the renderer never
	// needs socket schema knowledge to see a default.
	irEdges := make([]types.IREdge, 0, len(topo.Edges))
	socketDefaults := map[string]types.OrderedMap[any]{}
	// Keyed by pillar as well as (componentType, socket): the two pillars are
	// separate declarations on the same socket.
	defaultsFor := func(componentType, socket, pillar string) types.OrderedMap[any] {
		key := componentType + "\x00" + socket + "\x00" + pillar
		defs, cached := socketDefaults[key]
		if !cached {
			defs = socketPillarDefaults(cfg.CatalogLookup, componentType, socket, pillar)
			socketDefaults[key] = defs
		}
		return defs
	}
	// Keyed by the DECLARING entity's identity as well as the socket: a
	// Feature-authored edge's socket is declared on the Feature, not on the
	// consuming Component, so a key carrying only the component type would serve
	// one surface's answer for the other.
	declarations := map[string]string{}
	declarationFor := func(componentType, originFeature, socket string) string {
		key := componentType + "\x00" + originFeature + "\x00" + socket
		d, cached := declarations[key]
		if !cached {
			d = socketDeclaration(cfg.CatalogLookup, componentType, originFeature, socket)
			declarations[key] = d
		}
		return d
	}
	// What each socket's per-edge contract declares sensitive, keyed like the
	// defaults above: by the consuming component's type and the socket, both
	// pillars in one answer because the marking names leaves of both.
	socketSensitive := map[string]resolver.SensitiveLeaves{}
	sensitiveFor := func(componentType, socket string) resolver.SensitiveLeaves {
		key := componentType + "\x00" + socket
		declared, cached := socketSensitive[key]
		if !cached {
			declared = resolver.DeclaredSocketSensitiveLeaves(cfg.CatalogLookup, componentType, socket)
			socketSensitive[key] = declared
		}
		return declared
	}
	for _, e := range topo.Edges {
		edgeValues, edgeInfra := e.Values, e.Infra
		var sourceComponentType string
		if src, ok := topo.Nodes.Get(e.Source); ok && src != nil {
			sourceComponentType = src.ComponentType
		}
		var edgeSensitive []string
		if cfg.CatalogLookup.Component != nil && sourceComponentType != "" {
			if defs := defaultsFor(sourceComponentType, e.Socket, "values"); defs.Len() > 0 {
				edgeValues = mergeSchemaDefaults(e.Values, defs)
			}
			if defs := defaultsFor(sourceComponentType, e.Socket, "infra"); defs.Len() > 0 {
				edgeInfra = mergeSchemaDefaults(e.Infra, defs)
			}
			// The connection's per-leaf marking, over the same two payloads the
			// edge goes on to carry — defaults included, for the reason the node's
			// marking reads its filled payloads — against the contract the
			// CONSUMING component declares on the socket, which is the contract both
			// payloads were validated against. The target's declarations govern
			// what the target publishes, never what its consumer writes for the
			// connection, so they are not read here. A Feature-drawn edge reads the
			// same contract: a Feature's socket cannot declare a per-edge one, so
			// the consuming component's is the only declaration there is.
			edgeSensitive = derivePayloadSensitiveLeaves(edgeValues, edgeInfra, sensitiveFor(sourceComponentType, e.Socket))
		}
		irEdge := types.IREdge{
			Source:                 e.Source,
			Target:                 e.Target,
			Socket:                 e.Socket,
			Alias:                  e.Alias,
			SocketDeclaration:      declarationFor(sourceComponentType, e.OriginFeature, e.Socket),
			OriginFeature:          e.OriginFeature,
			OriginFeatureInstance:  e.OriginFeatureInstance,
			OriginRelease:          e.OriginRelease,
			Values:                 edgeValues,
			Infra:                  edgeInfra,
			Inject:                 convertInjectBlock(e.Inject),
			SensitivePayloadLeaves: edgeSensitive,
		}
		irEdges = append(irEdges, irEdge)
	}

	// 6. Provenance
	prov := buildProvenance(res, cfg.CompilerVersion)

	// Boundary-feature projection: the un-folded Release boundary attachments,
	// the single source both lock-time source-descent and the render-time
	// boundary-FR dispatch read (no Release re-walk on either side).
	var boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]
	if res.Release != nil {
		boundaryFeatures = buildBoundaryFeaturesProjection(res.Release.Spec.Boundaries, cfg.CatalogLookup, cfg.Realization)
	}

	graph := &types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata: types.IRMetadata{
			AggregateName: res.Aggregate.Metadata.Name,
		},
		Spec: types.IRSpec{
			Nodes:                irNodes,
			Edges:                irEdges,
			ValuesMapping:        valuesMapping,
			InfraMapping:         infraMapping,
			FeatureValuesMapping: featureValuesMapping,
			BoundaryFeatures:     boundaryFeatures,
			Provenance:           prov,
		},
	}

	return graph, featureOrder, attachmentSites, nil
}

// authoredUsesCopy returns the node's authored `uses` block for the graph's own
// copy of it, with every attachment a connection carries reduced to its type and
// description.
//
// The values are left out because the graph already carries them in the one
// place that marks them. A connection's attachment is lowered onto the node as
// one of its attachments, and there its values sit beside the per-leaf
// sensitivity marking every surface displaying the graph withholds by — the
// command-line graph output and an editor's panel alike, each reading the
// marking at the positions it knows. A second copy in the authored block would
// sit at a position no marking covers, so a value an author declared sensitive
// would print in the clear on every one of those surfaces. Keeping the type and
// description keeps what the block is for — which connections the author
// filled, and with what — without that second copy.
//
// A block carrying no connection attachment is returned as it is, so a node that
// writes none carries the bytes it carried before; the input is never mutated,
// since it is the topology's own record of what was authored.
func authoredUsesCopy(uses types.OrderedMap[types.UsesSocketValue]) types.OrderedMap[types.UsesSocketValue] {
	carries := false
	for _, sv := range uses.All() {
		for _, entry := range sv.Verbose.All() {
			if entry.Features.Len() > 0 {
				carries = true
			}
		}
	}
	if !carries {
		return uses
	}
	out := types.OrderedMap[types.UsesSocketValue]{}
	for socket, sv := range uses.All() {
		copied := types.UsesSocketValue{Compact: sv.Compact}
		for alias, entry := range sv.Verbose.All() {
			if entry.Features.Len() > 0 {
				withheld := types.OrderedMap[types.ConnectionAttachment]{}
				for slot, att := range entry.Features.All() {
					withheld.Set(slot, types.ConnectionAttachment{Type: att.Type, Description: att.Description})
				}
				entry.Features = withheld
			}
			copied.Verbose.Set(alias, entry)
		}
		out.Set(socket, copied)
	}
	return out
}

// convertInjectBlock translates the topology layer's typed *types.InjectBlock
// into the IR edge's per-mechanism shape — an outer OrderedMap keyed by
// mechanism name whose values are inner OrderedMaps of delivery-key → resolved
// string. The three standard mechanisms (env, file, arg) populate first in
// that hardcoded struct-field order; custom mechanisms from InjectBlock.Additional
// follow in author order via OrderedMap.All(), so the IR-edge per-mechanism
// map carries env/file/arg first and any custom mechanisms (e.g., ssm_param,
// vault_secret) the Developer authored after.
//
// Mechanism enumeration draws from four sources: the three typed fields and
// the Additional OrderedMap. Each mechanism's payload flows through the
// InjectBlock unmarshal hooks at pkg/types/common.go, so by the time this
// function runs an authored list has been decoded into []any.
//
// Type-coercion contract. A mechanism is the list of {in, from?, value?}
// entries the Aggregate and Release schemas' inject_delivery definition
// describes, and a list carries the order the author wrote, which is what lets
// this function promise author-ordered output. Each entry is decoded with
// types.DecodeVerboseInjectEntry and kept only when it is itself map-shaped and
// carries an in: that is a non-empty string; in: becomes the delivery key, and
// its value is value: when value: is a string, otherwise from: when from: is a
// string. An entry with a usable in: but neither field string-typed
// contributes no delivery key at all, and nothing is reported for it.
//
// A mechanism that is not a list contributes nothing here: the list is the one
// shape a mechanism takes. The document envelope refuses any other before a
// build reads it, and the rules asked while the documents are resolved refuse
// one on every block the Aggregate and the Release carry, as
// [resolver.ErrInjectDeliveryShape], before this runs.
func convertInjectBlock(inject *types.InjectBlock) types.OrderedMap[types.OrderedMap[string]] {
	result := types.OrderedMap[types.OrderedMap[string]]{}
	if inject == nil {
		return result
	}
	convert := func(mech string, raw any) {
		entries, ok := raw.([]any)
		if !ok {
			return
		}
		m := types.OrderedMap[string]{}
		for _, entry := range entries {
			in, value, from, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok {
				continue
			}
			if hasValue {
				m.Set(in, value)
				continue
			}
			if hasFrom {
				m.Set(in, from)
			}
		}
		if m.Len() > 0 {
			result.Set(mech, m)
		}
	}
	convert("env", inject.Env)
	convert("file", inject.File)
	convert("arg", inject.Arg)
	for mech, payload := range inject.Additional.All() {
		convert(mech, payload)
	}
	return result
}
