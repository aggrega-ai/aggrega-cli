// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TopologyResult is the output of Build — the upstream input to the IR
// builder (Step 3). Five fields, all carrying author-declared ordering
// disciplines that flow from the Aggregate's authoring sequence:
//
//   - Nodes carries the Aggregate's component order (and recursively the
//     order of components in any sub-aggregate it imports) through
//     subsystem flattening. The OrderedMap[*TopologyNode] shape is load-
//     bearing for IR JSON emission, where downstream consumers (policy
//     engines, GitOps repos, --trace output) rely on author-order
//     preservation.
//   - Edges flow in compositional author order: the cross-product of
//     (component sequence × within-component `uses:` socket order ×
//     within-socket alias order), augmented per node by feature and
//     secret attachment order. Each layer is author-declared via an
//     OrderedMap surface; the resulting slice carries the cross-product
//     into the IR builder without re-sorting. Build's step 4 walks
//     flat.Nodes via OrderedMap.All() to drive the outer iteration; the
//     IR builder's BuildIR step 2 (topology→IR edge conversion loop)
//     reads this slice in slice order. The full producer-and-consumer
//     contract is documented in this package's doc.go (Cross-package
//     determinism contract).
//
// IRSpec.FeatureMarkers (in the downstream IR) is the canonical example
// of a synthesized-derived collection that lacks a single authoring
// surface — markers are accumulated into a Go set across the per-node
// feature-cascade walk and sort-extracted alphabetically for stability.
// TopologyResult's own fields are all author-declared; the synthesised-
// derived discipline applies elsewhere.
//
// FeatureAttachments and SecretAttachments are per-node slices; the
// per-node iteration order is the YAML attachment order from
// collectAttachments. ExportedComponents maps the alias an Aggregate
// publishes via spec.exportedComponents to the dot-notation ID that
// alias resolves to after sub-aggregate flattening.
type TopologyResult struct {
	Nodes              types.OrderedMap[*TopologyNode]
	Edges              []NormalizedEdge
	FeatureAttachments map[string][]FeatureAttachment
	SecretAttachments  map[string][]SecretAttachment
	ExportedComponents map[string]string // alias → resolved dot-notation ID
}

// TopologyNode is one flattened node in the topology graph. Fields are
// populated across two phases of Build, plus one upstream contribution:
//
//   - Phase 1 (flattenAggregate): InstanceID, ComponentType, Values,
//     SelfInject, SubsystemPath, SourceAggregate. InstanceID is the dot-
//     notation ID synthesised by qualifiedID (the dot is reserved as
//     the compiler-internal path separator); SubsystemPath is the
//     dotted path of the subsystem the component sits in (empty at
//     top level); SourceAggregate is the dotted prefix of the
//     sub-aggregate that contributed the node (empty for root-aggregate
//     components).
//   - Phase 2 (Build steps 2 + 3): Labels populated from the catalog via
//     CatalogLookup; BoundaryPath populated from the resolver's
//     BoundaryInfra map. Both are set after flattening because they
//     require resources flattening doesn't reach (the catalog and the
//     resolver result respectively).
//   - SelfInject carries the component-level self-inject block from the
//     Aggregate's `inject:` declaration on the component instance. It
//     does NOT generate a topology edge (self-inject is a per-node
//     pipe, not a graph relationship); the renderer's self-inject pass
//     consumes it for component-self template evaluation.
type TopologyNode struct {
	InstanceID      string
	ComponentType   string
	Labels          types.OrderedMap[string]
	Values          types.OrderedMap[any]
	SelfInject      *types.InjectBlock // Component-level self-inject (carried to IR for Pass 2a)
	SubsystemPath   string
	BoundaryPath    string
	SourceAggregate string // prefix of the sub-aggregate that contributed this node ("" = root)
	// SourceFile is the Aggregate file where this node was authored,
	// stamped verbatim from BuildConfig.RootAggregateFile (root-tier
	// nodes) or from the path the SubAggregateLoader returned
	// (sub-aggregate nodes). Production callers feed in workspace-
	// relative paths so the IR wire shape stays byte-identical across
	// developer machines; the catalog-backed loader applies that
	// normalization using the workspace root it was constructed with
	// (the first argument to Load/LoadAll).
	// Empty when the loader returns empty (synthetic test fixtures) —
	// the IR builder propagates the empty case to IRNode.SourceFile,
	// which downstream consumers (the analyzer's OriginFile
	// population, the daemon's per-file diagnostic filter) interpret
	// as "no provenance known" and handle via backfill.
	SourceFile string
}

// NormalizedEdge is one directed edge in the topology graph. The shape is
// the canonical form normalizeUses converges both `uses` authoring shapes
// (string-array and verbose-map) onto — downstream code (the IR builder,
// Step 4 analyzer, every policy plugin) sees a single representation per
// edge regardless of which authoring form produced it.
//
// Field semantics:
//
//   - Source / Target: dot-notation node IDs. Source is the per-node
//     position the `uses` block was declared on; Target is the sibling
//     instance name the entry pointed at.
//   - Socket: the `usesSchema` socket name from the Component or Feature
//     catalog entity. The resolver enforces socket-allowlist and
//     required-socket validation upstream, so the socket name is
//     guaranteed valid before this package runs.
//   - Alias: the user-provided edge name. Equal to Target for the string-
//     array form, distinct from Target for the verbose-map form (where
//     the map key is the alias and the inner `target:` field names the
//     actual destination).
//   - Inject: optional inject-block payload from the verbose-map form;
//     nil for string-array entries that carry no inject directives.
//   - OriginFeature: non-empty when the edge originated from a Feature's
//     own `uses` block (vs the Component's own `uses` block). The IR
//     builder copies this field directly into IREdge.OriginFeature so
//     downstream policy and security tooling can distinguish
//     Component-authored edges from Feature-authored edges; this
//     atomicity is what lets attaching a Feature add edges to the graph
//     without polluting the parent Component's apparent edge set.
type NormalizedEdge struct {
	Source        string
	Target        string
	Socket        string
	Alias         string
	Inject        *types.InjectBlock
	OriginFeature string // non-empty if edge came from a Feature's uses
}

// FeatureAttachment pairs a feature instance name with its FeatureInstance
// payload for one node. Topology surfaces the attachment as-authored in
// the Aggregate; IR's collectNodeFeatures (Step 3) walks the slice in
// order and feeds each entry into mergeFeature for the per-node
// feature-cascade fold across the five tier sources (sub-aggregate
// component, root-aggregate component, root-aggregate subsystem,
// release boundary, release component-specific).
//
// SourceTier is currently always 0 — collectAttachments emits the field
// but never with a non-zero value, and the IR builder does not overwrite
// it during the cascade walk. The field exists for future use; the
// existing inline comment ("set by IR builder in Step 3") describes a
// behaviour the IR builder has not yet implemented.
type FeatureAttachment struct {
	InstanceName string
	Feature      types.FeatureInstance
	SourceTier   int // Onion Model tier (set by IR builder in Step 3)
}

// SecretAttachment pairs a secret instance name with its SecretInstance
// payload for one node. Same shape and same tier-tagging caveat as
// FeatureAttachment: SourceTier is currently always 0. Secret attachments
// flow into IR's Secrets bucket without going through the Onion Model
// merge (secrets are typed instances rather than label-discriminated
// credential Features; the credential-routing branch in IR's mergeFeature
// is the parallel mechanism for kind:secret-labelled Features).
type SecretAttachment struct {
	InstanceName string
	Secret       types.SecretInstance
	SourceTier   int
}

// SubAggregateLoader resolves a sub-aggregate type URI to its loaded
// Aggregate definition. flattenAggregate calls back through this
// function whenever it encounters a `spec.aggregates` entry that names a
// sub-aggregate type to import.
//
// Returns the loaded Aggregate together with the filesystem path it was
// loaded from. The path threads through flatten into TopologyNode.SourceFile
// so per-node IR records carry source-file provenance for per-file
// diagnostic filtering. The path may be empty when the loader returns a
// synthetic Aggregate (test fixtures) — consumers must tolerate empty.
//
// The callback indirection — rather than a direct *catalog.Catalog
// reference — keeps the topology package free of any catalog-package
// import dependency and lets tests construct in-memory sub-aggregate
// fixtures without spinning up a filesystem catalog. The production
// caller wires `cat.SubAggregateLoader()` from pkg/catalog (see
// pkg/catalog/catalog.go for the adapter pattern documentation).
type SubAggregateLoader func(typeName string) (*types.Aggregate, string, error)

// BuildConfig is the input bundle for Build. Three fields, all required:
//
//   - ResolveResult is the Step 1 output. Build reads its Aggregate (the
//     authoring root passed to flattenAggregate) and BoundaryInfra
//     (consulted per node for BoundaryPath population in step 3).
//   - CatalogLookup is the catalog-side adapter (pkg/catalog returns one
//     via Catalog.CatalogLookup()). Build uses it in step 2 to resolve
//     each node's Component metadata.labels into TopologyNode.Labels.
//   - LoadSubAggregate is the SubAggregateLoader callback used by
//     flattenAggregate when walking the Aggregate's spec.aggregates
//     block. nil disables sub-aggregate loading entirely; the production
//     caller always supplies one.
type BuildConfig struct {
	ResolveResult    *resolver.ResolveResult
	CatalogLookup    resolver.CatalogLookup
	LoadSubAggregate SubAggregateLoader

	// RootAggregateFile is the path of the root Aggregate being
	// flattened, in whatever form the caller wants to surface — the
	// production callers pass workspace-relative so the IR's per-node
	// SourceFile stays byte-identical across developer machines whose
	// workspaces live at different absolute paths. flattenAggregate
	// stamps this onto every node declared at the root tier
	// (TopologyNode.SourceFile) verbatim; sub-aggregate nodes inherit
	// the path returned by SubAggregateLoader, also verbatim. The
	// catalog-backed loader returns workspace-relative paths when it
	// was constructed with a non-empty workspace root (the first
	// argument to Load/LoadAll). Empty is tolerated (synthetic
	// tests); consumers downstream backfill or treat the empty case
	// as "no provenance known".
	RootAggregateFile string
}

// Build executes Step 2 of the compiler pipeline: topology construction.
//
// Five phases, in order:
//
//  1. Flatten the Aggregate tree. flattenAggregate walks components,
//     subsystems, and sub-aggregate imports recursively in author order.
//     The map-based authoring shape (root-topology blocks are YAML maps
//     keyed by user-authored instance names) gives stable per-block
//     instance IDs; dot-notation IDs flow from sub-aggregate flattening;
//     a depth bound on inclusion recursion (maxSubAggregateDepth = 10)
//     terminates pathological chains. The result carries the per-node
//     FeatureAttachments / SecretAttachments / ExportedComponents.
//  2. Populate Labels from the catalog. Labels live on the Component
//     catalog entity (the schema bans them on Aggregate component
//     instances), so the resolved label map is fetched here.
//  3. Populate BoundaryPath from the resolver. Boundaries are a Release-
//     side concern; the resolver computed each node's path during Step 1.
//  4. Normalise uses → directed edges. The outer iteration walks
//     flat.Nodes via OrderedMap.All() to drive compositional author-
//     order edge emission; the inline comment at the call site names
//     the cross-product discipline directly.
//  5. Collect Feature and Secret uses edges. Feature/Secret `uses`
//     blocks declare their own edges; each edge carries OriginFeature
//     so Feature-authored edges remain separable from Component-authored
//     edges in downstream policy and rendering.
//
// Cyclic edges are accepted without error — Build does not topologically
// sort or reject back-edges; the runtime `uses` graph legitimately
// supports cycles (e.g. multi-agent loops). flattenAggregate's
// visited-set + ErrMaxDepth handle cycles in the orthogonal
// sub-aggregate inclusion graph (a different graph from the runtime
// `uses` topology), where cycles ARE authoring bugs.
//
// Error model. Build returns the first error from flattenAggregate
// (sub-aggregate load failure, max-depth, circular include) or from
// normalizeUses (invalid uses format). Catalog and BoundaryInfra lookups
// are best-effort — a missing catalog entry leaves Labels empty, a
// missing BoundaryInfra entry leaves BoundaryPath empty; neither is
// fatal. Self-inject is read from the Aggregate's `inject:` block during
// flattening and never produces an edge here — self-inject is a
// per-node pipe carried forward to the renderer's self-inject pass,
// not a topology relationship.
func Build(cfg BuildConfig) (*TopologyResult, error) {
	agg := cfg.ResolveResult.Aggregate

	// 1. Flatten
	flat, err := flattenAggregate(agg, "", "", cfg.RootAggregateFile, cfg.LoadSubAggregate, map[string]bool{}, 0)
	if err != nil {
		return nil, err
	}

	// 2. Populate labels from catalog
	if cfg.CatalogLookup.Component != nil {
		for _, node := range flat.Nodes.All() {
			catComp, found := cfg.CatalogLookup.Component(node.ComponentType)
			if found {
				node.Labels = catComp.Metadata.Labels
			}
		}
	}

	// 3. Populate boundary paths from resolver
	for nodeID, node := range flat.Nodes.All() {
		if bi, ok := cfg.ResolveResult.BoundaryInfra[nodeID]; ok && bi != nil {
			node.BoundaryPath = bi.BoundaryPath
		}
	}

	// 4. Normalize uses → edges. Edges flow in compositional author order:
	// the cross-product of (component sequence × within-component `uses:`
	// socket order × within-socket alias order), augmented per node by
	// feature and secret attachment order. The outer iteration walks
	// flat.Nodes via OrderedMap.All() to preserve Aggregate authoring
	// sequence; per-node socket and alias loops preserve their existing
	// author-order semantics. The IR builder consumes the resulting edge
	// slice in slice order without re-sorting (cross-package contract
	// documented in this package's doc.go and the consumer-side commitment
	// in internal/compiler/ir/doc.go).
	var edges []NormalizedEdge
	for nodeID := range flat.Nodes.All() {
		// Component uses
		comp := findComponent(agg, nodeID)
		if comp != nil && comp.Uses.Len() > 0 {
			compEdges, err := normalizeUses(comp.Uses, nodeID, "")
			if err != nil {
				return nil, err
			}
			edges = append(edges, compEdges...)
		}

		// 5. Feature uses (create edges with OriginFeature)
		if feats, ok := flat.FeatureAttachments[nodeID]; ok {
			for _, fa := range feats {
				if fa.Feature.Uses.Len() > 0 {
					featEdges, err := normalizeUses(fa.Feature.Uses, nodeID, fa.Feature.Type)
					if err != nil {
						return nil, err
					}
					edges = append(edges, featEdges...)
				}
			}
		}

		// Secret uses
		if secs, ok := flat.SecretAttachments[nodeID]; ok {
			for _, sa := range secs {
				if sa.Secret.Uses.Len() > 0 {
					secEdges, err := normalizeUses(sa.Secret.Uses, nodeID, sa.Secret.Type)
					if err != nil {
						return nil, err
					}
					edges = append(edges, secEdges...)
				}
			}
		}
	}

	return &TopologyResult{
		Nodes:              flat.Nodes,
		Edges:              edges,
		FeatureAttachments: flat.FeatureAttachments,
		SecretAttachments:  flat.SecretAttachments,
		ExportedComponents: flat.ExportedComponents,
	}, nil
}

// findComponent looks up a ComponentInstance by its (unflattened) name in
// the root Aggregate's top-level components and subsystem hierarchy. Does
// not search sub-aggregates: they have already been flattened into the
// nodes map by Phase 1 and their components are addressed by dot-notation
// IDs that don't appear as keys in the root Aggregate's spec.Components.
// findComponent's nodeID parameter is therefore expected to name either a
// top-level root-aggregate component or a subsystem-nested component;
// nodeIDs containing a dot from sub-aggregate imports return nil here
// (deliberate — those nodes carry no Component metadata visible at the
// root level, so the per-node `uses` lookup in Build's edge phase
// correctly skips them).
func findComponent(agg *types.Aggregate, name string) *types.ComponentInstance {
	if comp, ok := agg.Spec.Components.Get(name); ok {
		return &comp
	}
	// Search subsystems
	for _, sub := range agg.Spec.Subsystems.All() {
		if c := findComponentInSubsystem(sub, name); c != nil {
			return c
		}
	}
	return nil
}

func findComponentInSubsystem(sub types.Subsystem, name string) *types.ComponentInstance {
	if comp, ok := sub.Components.Get(name); ok {
		return &comp
	}
	for _, nested := range sub.Subsystems.All() {
		if c := findComponentInSubsystem(nested, name); c != nil {
			return c
		}
	}
	return nil
}
