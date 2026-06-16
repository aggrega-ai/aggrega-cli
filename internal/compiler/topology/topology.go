// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"context"
	"fmt"

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
	InstanceID    string
	ComponentType string
	Labels        types.OrderedMap[string]
	Values        types.OrderedMap[any]
	SelfInject    *types.InjectBlock // Component-level self-inject (carried to IR for Pass 2a)
	// Uses is the component's authored `uses` block, carried onto the node so
	// the edge-normalisation pass can read it directly. Sub-aggregate components
	// are not present in the root Aggregate, so resolving their `uses` by name
	// against the root would drop their edges; carrying the block on the node
	// (alongside SourceAggregate, the prefix used to namespace edge targets)
	// makes edge normalisation uniform across root, subsystem, and sub-aggregate
	// origins.
	Uses types.OrderedMap[types.UsesSocketValue]
	// ScopeFeatureUses is the cascade of subsystem-wide feature attachments that
	// apply to this node — accumulated during flattening from every subsystem on
	// the node's path (outer to inner, author order), carried on the node so the
	// edge pass projects them without re-walking a subsystem tree. Carrying it
	// here (rather than walking the root Aggregate at edge time) is what makes
	// subsystem-wide features declared inside a sub-aggregate project correctly:
	// the cascade is captured against the sub-aggregate's own tree at flatten
	// time, and the node's SourceAggregate prefix namespaces the edge targets.
	ScopeFeatureUses []scopeFeatureUses
	// ResolvedParameters is the per-sub-aggregate parameter map (the parent's
	// SubAggregate.Parameters fulfilling the sub-aggregate's own contract,
	// defaults applied) resolved when the sub-aggregate was loaded. Nil for
	// root/subsystem nodes (the root parameter map is threaded separately). It
	// is the context the post-flatten conformance pass evaluates a sub-aggregate
	// node's parameter-dependent predicates against, instead of the root context.
	ResolvedParameters map[string]any
	SubsystemPath      string
	// SynthesizedSubsystem is true when SubsystemPath names the compiler-
	// synthesized `default` subsystem (an Aggregate authored with top-level
	// components rather than named subsystems). Derived provenance carried onto
	// the IR node; independent of SynthesizedBoundary (a node may be synthesized
	// on either axis or both).
	SynthesizedSubsystem bool
	BoundaryPath         string
	// SynthesizedBoundary is true when BoundaryPath names the compiler-
	// synthesized `default` boundary (a Release authored with top-level
	// components rather than named boundaries). Derived provenance carried onto
	// the IR node; routes output to dist/ flat.
	SynthesizedBoundary bool
	SourceAggregate     string // prefix of the sub-aggregate that contributed this node ("" = root)
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
	// OriginFeatureInstance is the per-attachment instance name of the
	// Feature whose uses block produced this edge (empty for Component-own
	// edges). It disambiguates two attachments of the same Feature type on
	// one boundary, letting a boundary-scope FeatureRecipe's resolved
	// provides be located by instance rather than by the ambiguous type.
	OriginFeatureInstance string
	// OriginRelease is true when this edge was created by a Release wiring a
	// declared-but-unbound socket to an existing component. It marks AUTHORSHIP,
	// not configuration: an edge a Release merely supplied infra for keeps its
	// origin and carries the Release infra in Infra. The IR builder copies this
	// into IREdge.OriginRelease.
	OriginRelease bool
	// Values / Infra carry the resolved per-connection config: Values from the
	// verbose Aggregate uses entry; Infra from the Aggregate entry and/or merged
	// from a Release per-edge entry. Opaque OrderedMaps the core does not
	// interpret — the conformance gate (Step 1) validates them and the renderer
	// projects them at .uses.<socket>.<alias>.{values,infra}.
	Values types.OrderedMap[any]
	Infra  types.OrderedMap[any]
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
// parentParams is the parent-supplied SubAggregate.Parameters for this
// import (the fulfillment of the sub-aggregate's own `spec.parameters`
// contract). The loader resolves the sub-aggregate's parameters from it
// (defaults + contract validation), interpolates the sub-aggregate's
// `{{ .parameters.X }}` templates against the resolved map, and returns the
// interpolated Aggregate plus that resolved map. The map is the context the
// post-flatten conformance pass evaluates the sub-aggregate's
// parameter-dependent predicates against; it is carried onto each of the
// sub-aggregate's nodes. Returns an empty map when the sub-aggregate declares
// no parameters.
//
// Returns the (interpolated) Aggregate together with the filesystem path it
// was loaded from. The path threads through flatten into
// TopologyNode.SourceFile so per-node IR records carry source-file provenance
// for per-file diagnostic filtering. The path may be empty when the loader
// returns a synthetic Aggregate (test fixtures) — consumers must tolerate empty.
//
// The callback indirection — rather than a direct *catalog.Catalog
// reference — keeps the topology package free of any catalog-package
// import dependency and lets tests construct in-memory sub-aggregate
// fixtures without spinning up a filesystem catalog. The production
// caller wires `cat.SubAggregateLoader()` from pkg/catalog (see
// pkg/catalog/catalog.go for the adapter pattern documentation).
type SubAggregateLoader func(typeName string, parentParams map[string]any) (*types.Aggregate, string, map[string]any, error)

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
	return BuildContext(context.Background(), cfg)
}

// BuildContext is Build with cooperative cancellation: it checks
// ctx.Err() at the per-node label-population boundary so a long
// topology pass over a large workspace can be interrupted (e.g. by the
// daemon's shutdown drain) rather than running to completion. Build is
// the background-context convenience wrapper for callers that do not
// participate in cancellation.
func BuildContext(ctx context.Context, cfg BuildConfig) (*TopologyResult, error) {
	agg := cfg.ResolveResult.Aggregate

	// Loud guardrail (Invariant 4): an Aggregate born through the normalization
	// core never carries top-level components alongside a `default` subsystem —
	// Normalize folds the root into the default and clears it, and the
	// authored-both shape is rejected at the envelope XOR. Reaching flatten in
	// that state is a normalization bypass (a hand-built literal, or a load path
	// that skipped the parse core). Fail loud rather than silently flatten a
	// half-normalized graph. An internal-invariant breach, not an authoring error.
	if agg != nil {
		if _, hasDefault := agg.Spec.Subsystems.Get("default"); hasDefault && agg.Spec.Components.Len() > 0 {
			return nil, fmt.Errorf("%w: construct aggregates through the normalization core (types.Normalize via the parse path), not a hand-built literal", ErrUnnormalizedAggregate)
		}
	}

	// 1. Flatten
	flat, err := flattenAggregate(agg, "", "", cfg.RootAggregateFile, cfg.LoadSubAggregate, map[string]bool{}, 0, nil)
	if err != nil {
		return nil, err
	}

	// 2. Populate labels from catalog
	if cfg.CatalogLookup.Component != nil {
		for _, node := range flat.Nodes.All() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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
			node.SynthesizedBoundary = bi.Synthesized
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
	for nodeID, node := range flat.Nodes.All() {
		// Component uses. Read the authored block from the node (carried during
		// flattening) rather than resolving by name against the root Aggregate —
		// sub-aggregate components are not in the root, so a root lookup would
		// drop their edges. Targets are namespaced with the node's sub-aggregate
		// prefix so a locally-authored target resolves to its flattened node ID
		// (qualifiedID with an empty prefix is the identity, so root and
		// subsystem targets are unchanged).
		if node.Uses.Len() > 0 {
			compEdges, err := normalizeUses(node.Uses, nodeID, "", "")
			if err != nil {
				return nil, err
			}
			for i := range compEdges {
				compEdges[i].Target = qualifiedID(node.SourceAggregate, compEdges[i].Target)
			}
			edges = append(edges, compEdges...)
		}

		// 5. Feature uses (create edges with OriginFeature). A feature attached
		// to a sub-aggregate component is authored inside that sub-aggregate, so
		// its uses targets are local — namespaced with the node's prefix the same
		// way component-edge targets are (identity for root/subsystem nodes).
		if feats, ok := flat.FeatureAttachments[nodeID]; ok {
			for _, fa := range feats {
				if fa.Feature.Uses.Len() > 0 {
					featEdges, err := normalizeUses(fa.Feature.Uses, nodeID, fa.Feature.Type, fa.InstanceName)
					if err != nil {
						return nil, err
					}
					for i := range featEdges {
						featEdges[i].Target = qualifiedID(node.SourceAggregate, featEdges[i].Target)
					}
					edges = append(edges, featEdges...)
				}
			}
		}

		// Secret uses (same local-target namespacing as feature attachments).
		if secs, ok := flat.SecretAttachments[nodeID]; ok {
			for _, sa := range secs {
				if sa.Secret.Uses.Len() > 0 {
					secEdges, err := normalizeUses(sa.Secret.Uses, nodeID, sa.Secret.Type, sa.InstanceName)
					if err != nil {
						return nil, err
					}
					for i := range secEdges {
						secEdges[i].Target = qualifiedID(node.SourceAggregate, secEdges[i].Target)
					}
					edges = append(edges, secEdges...)
				}
			}
		}

		// Scope-wide feature uses. A feature attached subsystem-wide
		// contributes its uses-edge from every component in the scope — the
		// "every component here depends on shared target D" pattern declared
		// once at the scope rather than repeated on each component. The edge
		// stamps the scope feature as its origin, so the consumer is injected
		// the target's provides exactly as for a per-component feature
		// uses-edge. The scope feature's own values/infra cascade is handled
		// separately in the IR builder and is unaffected by this edge
		// projection. The applicable cascade is carried on the node
		// (ScopeFeatureUses), accumulated at flatten time against the subsystem
		// tree actually flattened — so a subsystem-wide feature inside a
		// sub-aggregate projects correctly — in outer-to-inner cascade order,
		// author order within each level. Targets are namespaced with the
		// node's prefix (identity for root/subsystem).
		for _, sf := range node.ScopeFeatureUses {
			if sf.uses.Len() == 0 {
				continue
			}
			subEdges, err := normalizeUses(sf.uses, nodeID, sf.featureType, sf.instanceName)
			if err != nil {
				return nil, err
			}
			for i := range subEdges {
				subEdges[i].Target = qualifiedID(node.SourceAggregate, subEdges[i].Target)
			}
			edges = append(edges, subEdges...)
		}
		if cfg.ResolveResult.Release != nil {
			for _, bf := range boundaryFeatureUses(cfg.ResolveResult.Release.Spec.Boundaries, node.BoundaryPath) {
				if bf.uses.Len() == 0 {
					continue
				}
				bndEdges, err := normalizeUses(bf.uses, nodeID, bf.featureType, bf.instanceName)
				if err != nil {
					return nil, err
				}
				edges = append(edges, bndEdges...)
			}
		}
	}

	// 6. Release per-edge uses surface. Within Architect-declared sockets the
	// Release may configure an existing edge (merge per-edge infra) and/or
	// create an edge into a declared-but-unbound socket pointing at an existing
	// component. This pass does data plumbing only: merge infra and append
	// created edges. It NEVER retargets — a Release entry whose (node, socket,
	// alias) matches an existing edge but names a conflicting target is left
	// untouched here so the Step-4 guardrail can detect the retarget against the
	// intact BoundaryComponent.Uses and halt (skipping the infra merge in that
	// case keeps a rejected retarget from half-applying). Conformance (Step 1),
	// dangling-target, and undeclared-socket halts are not this pass's concern.
	// Determinism: nodes in flat.Nodes author order; sockets and aliases in
	// OrderedMap author order; created edges append after the Aggregate/Feature
	// edges.
	if cfg.ResolveResult != nil && cfg.ResolveResult.Release != nil {
		for nodeID, node := range flat.Nodes.All() {
			ruses, ok := releaseComponentUses(cfg.ResolveResult.Release.Spec.Boundaries, node.BoundaryPath, nodeID)
			if !ok {
				continue
			}
			for socket, aliases := range ruses.All() {
				for alias, entry := range aliases.All() {
					idx := -1
					for i := range edges {
						if edges[i].Source == nodeID && edges[i].Socket == socket && edges[i].Alias == alias {
							idx = i
							break
						}
					}
					if idx >= 0 {
						// Configure an existing edge: merge infra only, never
						// retarget. A conflicting target is a Loud Guardrail — the
						// developer owns the architecture they declared.
						if entry.Target != "" && entry.Target != edges[idx].Target {
							return nil, fmt.Errorf(
								"release edge %s/%s/%s retargets developer-declared target %q to %q: %w",
								nodeID, socket, alias, edges[idx].Target, entry.Target, ErrReleaseEdgeRetarget)
						}
						if entry.Infra.Len() > 0 {
							merged := types.OrderedMap[any]{}
							for k, v := range edges[idx].Infra.All() {
								merged.Set(k, v)
							}
							for k, v := range entry.Infra.All() {
								merged.Set(k, v)
							}
							edges[idx].Infra = merged
						}
						continue
					}
					// No matching edge: create one when a target is given,
					// stamped Release-origin. A targetless unmatched entry is an
					// undeclared-socket case the resolver guard surfaces at Step 1.
					// Target existence is enforced by the origin-agnostic
					// dangling-target sweep below, not here.
					if entry.Target != "" {
						edges = append(edges, NormalizedEdge{
							Source:        nodeID,
							Target:        entry.Target,
							Socket:        socket,
							Alias:         alias,
							Infra:         entry.Infra,
							OriginRelease: true,
						})
					}
				}
			}
		}
	}

	// Origin-agnostic dangling-target guardrail. Every edge — developer-authored
	// (Aggregate), Feature-projected, or Release-wired — must point at a node
	// that exists in the resolved topology. A dangling target is a Loud Guardrail
	// (Invariant 4), not a silent drop: an unresolved target would otherwise
	// leave the consumer's inject silently empty. Walks in author order and halts
	// on the first dangling edge for deterministic output.
	for _, e := range edges {
		if _, exists := flat.Nodes.Get(e.Target); !exists {
			return nil, fmt.Errorf(
				"uses edge %s/%s/%s targets %q, which is not a component in the topology: %w",
				e.Source, e.Socket, e.Alias, e.Target, ErrDanglingUsesTarget)
		}
	}

	// Guard sub-aggregate components post-flatten. The Step-1 resolver guards
	// see only the root Aggregate's Components + Subsystems, never the loaded
	// sub-aggregate components — which exist only here, after flattening. Run
	// the exported resolver validators over the sub-aggregate nodes (and their
	// edges) so their sockets, values, and per-edge config are checked like any
	// other. Predicate-inert: the sub-aggregate's own parameter context is not
	// resolved, so only context-independent structural constraints are checked
	// (conditional x-aggrega predicates are validated where the context exists).
	result := &TopologyResult{
		Nodes:              flat.Nodes,
		Edges:              edges,
		FeatureAttachments: flat.FeatureAttachments,
		SecretAttachments:  flat.SecretAttachments,
		ExportedComponents: flat.ExportedComponents,
	}

	if cfg.ResolveResult != nil {
		if subErrs := guardSubAggregateNodes(flat, edges, cfg); len(subErrs) > 0 {
			// Return the fully-built topology alongside the halt: the graph is
			// complete at this final guard, and a resilient-inspection caller
			// that tolerates the catalog-view-inconsistency class needs the
			// graph it just validated. Halting callers check err first and
			// discard the result, so the non-nil graph never leaks into a
			// halt path.
			return result, &resolver.MultiResolverError{
				Entity:     "Step 2 (sub-aggregate conformance)",
				Summary:    fmt.Sprintf("%d sub-aggregate guard violation(s)", len(subErrs)),
				Violations: subErrs,
			}
		}
	}

	return result, nil
}

// guardSubAggregateNodes runs the resolver's conformance validators over the
// flattened sub-aggregate components (nodes with a non-empty SourceAggregate)
// and their edges. Root and subsystem components are guarded at Step 1; this
// closes the coverage gap for components that exist only after sub-aggregate
// flattening. Each sub-aggregate node carries its own resolved parameter map
// (ResolvedParameters), so conformance is fully parameter-aware: conditional
// x-aggrega-when / x-aggrega-validate predicates evaluate against the
// sub-aggregate's parameter context, the same as root components evaluate
// against theirs. Returns violations in flat.Nodes author order then edge
// order — deterministic.
func guardSubAggregateNodes(flat *flattenResult, edges []NormalizedEdge, cfg BuildConfig) []*resolver.ResolverError {
	catalog := cfg.CatalogLookup
	if catalog.Component == nil {
		return nil
	}
	entity := "Aggregate"
	if cfg.ResolveResult != nil && cfg.ResolveResult.Aggregate != nil {
		entity = fmt.Sprintf("Aggregate %s", cfg.ResolveResult.Aggregate.Metadata.Name)
	}

	var errs []*resolver.ResolverError
	for nodeID, node := range flat.Nodes.All() {
		if node.SourceAggregate == "" {
			continue // root / subsystem components are guarded at Step 1
		}

		// Socket allowlist: every uses socket must exist in the component's usesSchema.
		if node.Uses.Len() > 0 {
			errs = append(errs, resolver.ValidateUsesAgainstSchema(
				entity, fmt.Sprintf(".%s.uses", nodeID), node.Uses, node.ComponentType, catalog)...)
		}

		// Component values — validated against the sub-aggregate's own resolved
		// parameter context (carried on the node), so parameter-dependent
		// predicates evaluate correctly rather than predicate-inert.
		if compiled, ok := resolver.CompileComponentSchema(catalog, node.ComponentType); ok {
			errs = append(errs, resolver.ValidatePillarConformance(
				compiled, "values", node.Values, node.ResolvedParameters, entity,
				fmt.Sprintf(".%s", nodeID), "the Component schema")...)
		}

		// Per-edge developer values authored on the node's uses.
		for socket, sv := range node.Uses.All() {
			for alias, e := range sv.Verbose.All() {
				if e.Values.Len() == 0 {
					continue
				}
				if compiled, ok := resolver.CompileSocketPillarSchema(catalog, node.ComponentType, socket, "values"); ok {
					errs = append(errs, resolver.ValidatePillarConformance(
						compiled, "values", e.Values, node.ResolvedParameters, entity,
						fmt.Sprintf(".%s.uses[%q][%q]", nodeID, socket, alias), "the socket valueSchema")...)
				}
			}
		}
	}

	// Per-edge operator infra: the Release-merged infra lives on the resolved
	// edge. Validate edges whose source is a sub-aggregate node, against the
	// source node's resolved parameter context.
	for _, e := range edges {
		if e.Infra.Len() == 0 {
			continue
		}
		src, ok := flat.Nodes.Get(e.Source)
		if !ok || src.SourceAggregate == "" {
			continue
		}
		if compiled, ok := resolver.CompileSocketPillarSchema(catalog, src.ComponentType, e.Socket, "infra"); ok {
			errs = append(errs, resolver.ValidatePillarConformance(
				compiled, "infra", e.Infra, src.ResolvedParameters, entity,
				fmt.Sprintf(".%s.uses[%q][%q]", e.Source, e.Socket, e.Alias), "the socket infraSchema")...)
		}
	}
	return errs
}

// scopeFeatureUses carries the three fields the edge projection needs
// from a scope-wide (subsystem or boundary) feature attachment: the
// attachment's instance name (the origin-feature instance the IR
// consume-side resolves against), its feature type (the origin feature),
// and its uses block. It unifies the subsystem-side [types.FeatureInstance]
// and boundary-side [types.BoundaryFeature] shapes for the projection loop.
type scopeFeatureUses struct {
	instanceName string
	featureType  string
	uses         types.OrderedMap[types.UsesSocketValue]
}

// boundaryFeatureUses walks the Release boundary chain named by
// boundaryPath (the "/"-joined boundary-name chain stamped on each node)
// from outer to inner, returning every boundary-wide feature attachment in
// author order. Mirrors the IR feature cascade's boundary level.
func boundaryFeatureUses(boundaries types.OrderedMap[types.Boundary], boundaryPath string) []scopeFeatureUses {
	parts := splitScopePath(boundaryPath)
	if len(parts) == 0 {
		return nil
	}
	var result []scopeFeatureUses
	current := boundaries
	for _, part := range parts {
		boundary, ok := current.Get(part)
		if !ok {
			break
		}
		for fname, feat := range boundary.Features.All() {
			result = append(result, scopeFeatureUses{instanceName: fname, featureType: feat.Type, uses: feat.Uses})
		}
		current = boundary.Boundaries
	}
	return result
}

// releaseComponentUses walks the Release boundary chain named by boundaryPath
// (the same walk boundaryFeatureUses uses) and returns the per-edge uses
// surface the Release authored on the BoundaryComponent for nodeID, if any. A
// component is declared in exactly one boundary along its chain, so the first
// level whose Components carry a non-empty Uses for nodeID wins. Returns false
// when no Release component-edge surface applies to the node.
func releaseComponentUses(boundaries types.OrderedMap[types.Boundary], boundaryPath, nodeID string) (types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]], bool) {
	parts := splitScopePath(boundaryPath)
	current := boundaries
	for _, part := range parts {
		boundary, ok := current.Get(part)
		if !ok {
			break
		}
		if bc, ok := boundary.Components.Get(nodeID); ok && bc.Uses.Len() > 0 {
			return bc.Uses, true
		}
		current = boundary.Boundaries
	}
	return types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}, false
}

// splitScopePath splits a subsystem (dot-joined) or boundary (slash-joined)
// scope path into its segments, dropping empty segments. It accepts both
// separators so a single helper serves both cascades, matching the IR
// builder's path-splitting discipline.
func splitScopePath(path string) []string {
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
	if len(path) > start {
		parts = append(parts, path[start:])
	}
	return parts
}
