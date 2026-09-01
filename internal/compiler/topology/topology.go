// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"context"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TopologyResult is the output of Build — the upstream input to the IR
// builder (Step 3). Every field carries an author-declared ordering discipline
// that flows from the Aggregate's authoring sequence:
//
//   - Nodes carries the Aggregate's component order (and recursively the
//     order of components in any sub-aggregate it imports) through
//     subsystem flattening. The OrderedMap[*TopologyNode] shape is load-
//     bearing for IR JSON emission, where downstream consumers (policy
//     engines, GitOps repos, --trace output) rely on author-order
//     preservation.
//   - Edges flow in compositional author order: the cross-product of
//     (component sequence × within-component `uses:` socket order ×
//     within-socket alias order), augmented per node by attachment
//     order. Each layer is author-declared via an
//     OrderedMap surface; the resulting slice carries the cross-product
//     into the IR builder without re-sorting. Build's step 4 walks
//     flat.Nodes via OrderedMap.All() to drive the outer iteration; the
//     IR builder's BuildIR step 2 (topology→IR edge conversion loop)
//     reads this slice in slice order. The full producer-and-consumer
//     contract is documented in this package's doc.go (Cross-package
//     determinism contract).
//
// TopologyResult's own fields are all author-declared; the
// synthesised-derived discipline applies elsewhere.
//
// FeatureAttachments is a per-node slice; the per-node iteration order is
// the YAML attachment order from collectAttachments. ExportedComponents maps the alias an Aggregate
// publishes via spec.exportedComponents to the dot-notation ID that
// alias resolves to after sub-aggregate flattening.
type TopologyResult struct {
	Nodes              types.OrderedMap[*TopologyNode]
	Edges              []NormalizedEdge
	FeatureAttachments map[string][]FeatureAttachment
	// ExportedComponents is the live export-alias table: alias → resolved
	// dot-notation ID, carrying only aliases whose target survived pruning.
	ExportedComponents types.OrderedMap[string]
	// ExportedComponentsDeclared is every export alias the Aggregate tree
	// published, including aliases dropped because their target was pruned.
	// It is a snapshot taken before the drop, so a consumer distinguishing "this
	// alias was never declared" from "this alias was declared and its target
	// pruned" reads the two tables against each other rather than depending on
	// the order in which it observes one being mutated.
	ExportedComponentsDeclared types.OrderedMap[string]
	// Pruned is every flattened node ID a false `active` toggle removed from the
	// topology, in the order flattening encountered them. Only ever stores true,
	// so presence is membership. A node here is absent from Nodes, which is what
	// lets a consumer tell a reference to a deliberately-removed component apart
	// from a reference to one that never existed.
	Pruned types.OrderedMap[bool]
	// StageView presents this composition to a check as the named facts complete
	// once it is flattened, with every fact complete earlier answered by the
	// presentation the resolve produced.
	//
	// It is carried rather than rebuilt because it cannot be rebuilt from the
	// fields above: the module set flattening recorded is not among them, and two
	// producers of one fact are free to disagree about it with nothing comparing
	// them. A later stage extends this presentation by embedding it, so carrying it
	// is what lets the presentation of a later stage answer an earlier stage's
	// facts by delegation instead of by reading these fields again.
	//
	// Nil when the build holds no resolve result, because the earlier stage's
	// presentation is built from the documents that result carries.
	StageView constraint.TopologyView

	// StageCoordinates enumerates what a rule over this stage is invoked once
	// per: the component instances the composition flattened, and the dependency
	// sockets each of their types declares.
	//
	// It is a second field over the same value rather than a second value. The
	// presentation and the enumeration are one type here because both are read
	// from the flattened set this stage produced, and one reading of one set is
	// what keeps the coordinate a rule is invoked for and the facts it then
	// reads from disagreeing. The field is typed as the axes so a caller reaches
	// them without asserting: a presentation that stopped supplying one would
	// stop compiling here rather than leaving a rule silently uninvoked.
	//
	// Nil under exactly the condition StageView is nil, and for the same reason.
	StageCoordinates StageAxes
}

// StageAxes is every axis a rule over the flattened composition is invoked once
// per.
//
// It names the axes rather than being satisfied by whichever the presentation
// happens to offer: a presentation that stopped supplying one would stop
// compiling at the assignment, instead of leaving a rule that reads that axis
// silently uninvoked. Each axis stays a separate interface in the framework —
// one per enumerated kind — and this is the set of them this stage supplies, so
// adding an axis here is a deliberate widening rather than a side effect of a
// method appearing on the presentation.
type StageAxes interface {
	constraint.NodeEnumerator
	constraint.NodeSocketEnumerator
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
	// Values / Infra carry the resolved per-connection config, one per
	// authoring surface: Values from the verbose Aggregate uses entry, Infra
	// from a Release per-edge entry. Opaque OrderedMaps the core does not
	// interpret — the conformance gate (Step 1) validates them and the renderer
	// projects them at .uses.<alias>.{values,infra}.
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

// BuildConfig is the input bundle for Build. Four fields: ResolveResult and
// CatalogLookup are required; LoadSubAggregate and RootAggregateFile each have
// a defined empty behaviour, documented below and on the field respectively.
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
//  1. Flatten the Aggregate tree. flattenAggregate walks subsystems and
//     sub-aggregate imports recursively in author order.
//     The map-based authoring shape (root-topology blocks are YAML maps
//     keyed by user-authored instance names) gives stable per-block
//     instance IDs; dot-notation IDs flow from sub-aggregate flattening;
//     a depth bound on inclusion recursion (maxSubAggregateDepth = 10)
//     terminates pathological chains. The result carries the per-node
//     FeatureAttachments / ExportedComponents.
//  2. Populate Labels from the catalog. Labels live on the Component
//     catalog entity (the schema bans them on Aggregate component
//     instances), so the resolved label map is fetched here.
//  3. Populate BoundaryPath from the resolver. Boundaries are a Release-
//     side concern; the resolver computed each node's path during Step 1.
//  4. Normalise uses → directed edges. The outer iteration walks
//     flat.Nodes via OrderedMap.All() to drive compositional author-
//     order edge emission; the inline comment at the call site names
//     the cross-product discipline directly.
//  5. Collect attachment uses edges. An attachment's `uses` block declares
//     its own edges; each edge carries OriginFeature so Feature-authored
//     edges remain separable from Component-authored edges in downstream
//     policy and rendering.
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
// ctx.Err() at its phase boundaries — before the flatten pass, in the
// per-node label-population loop, and in the per-node edge-normalization
// loop — so a long topology pass over a large workspace can be
// interrupted (e.g. by the daemon's shutdown drain) rather than running
// to completion. Build is the background-context convenience wrapper for
// callers that do not participate in cancellation.
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
			return nil, fmt.Errorf("%w: construct aggregates through the normalization core (types.Normalize via the parse path), not a hand-built literal", ErrUnnormalizedAggregate())
		}
	}

	// Phase boundary: a cancelled context (e.g. the daemon's shutdown drain)
	// aborts before the flatten pass, which descends the whole sub-aggregate
	// tree and is the heavy front of topology on a large workspace.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1. Flatten. The flattener takes a normalized Aggregate, so the guarantee it
	// relies on — components live under subsystems — is carried by the value
	// rather than assumed of the caller. Normalize is idempotent, so an Aggregate
	// that already came through the parse core is passed through unchanged.
	flat, err := flattenAggregate(types.Normalize(agg), "", "", cfg.RootAggregateFile, cfg.LoadSubAggregate, map[string]bool{}, 0, nil)
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

	// 3. Populate boundary paths from resolver.
	//
	// A component the Release placed by routing its whole sub-module has no entry
	// of its own in the per-component map, and could not have: the routing entry
	// named no components, because a sub-module's component names do not exist
	// until its document is loaded, which is what flattening above just did. This
	// is therefore the first point at which such a node can be matched to the
	// placement that covers it, and the match is written back into the
	// per-component map so every later reader — output directory, sync wave,
	// annotations, per-node infra — sees a routed component exactly as it sees a
	// directly-named one, with no routed-axis knowledge of its own.
	//
	// A node whose id the Release names directly keeps that entry: it is the more
	// specific instruction, and the two cannot disagree about which boundary the
	// node belongs to, because naming a component in one boundary while routing its
	// module into another is rejected when the Release loads.
	for nodeID, node := range flat.Nodes.All() {
		bi, ok := cfg.ResolveResult.BoundaryInfra[nodeID]
		if (!ok || bi == nil) && node.SourceAggregate != "" {
			if routed, isRouted := resolver.RoutedInfraFor(cfg.ResolveResult.RoutedAggregateInfra, node.SourceAggregate); isRouted {
				// Copied per node: the placement is shared by every component of the
				// module, and a later phase that annotates one node's context must not
				// reach the others.
				placed := *routed
				if cfg.ResolveResult.BoundaryInfra == nil {
					cfg.ResolveResult.BoundaryInfra = map[string]*resolver.BoundaryInfra{}
				}
				cfg.ResolveResult.BoundaryInfra[nodeID] = &placed
				bi, ok = &placed, true
			}
		}
		if ok && bi != nil {
			node.BoundaryPath = bi.BoundaryPath
			node.SynthesizedBoundary = bi.Synthesized
		}
	}

	// 4. Normalize uses → edges. Edges flow in compositional author order:
	// the cross-product of (component sequence × within-component `uses:`
	// socket order × within-socket alias order), augmented per node by
	// attachment order. The outer iteration walks
	// flat.Nodes via OrderedMap.All() to preserve Aggregate authoring
	// sequence; per-node socket and alias loops preserve their existing
	// author-order semantics. The IR builder consumes the resulting edge
	// slice in slice order without re-sorting (cross-package contract
	// documented in this package's doc.go and the consumer-side commitment
	// in internal/compiler/ir/doc.go).
	var edges []NormalizedEdge
	for nodeID, node := range flat.Nodes.All() {
		// Phase boundary: the edge cross-product scales with node count, so a
		// cancelled context aborts cooperatively within this loop on a large
		// workspace rather than running every node's uses and attachment edges.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

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

		// 5. Attachment uses (create edges with OriginFeature). An attachment on
		// a sub-aggregate component is authored inside that sub-aggregate, so
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
		// Release-side attachment uses. One reader serves both scopes a Release
		// can attach at — the boundary's own features and a boundary component's —
		// because the two are one feature declared at two breadths and must draw
		// the same edge from the same declaration. Targets are graph ids as
		// authored, not namespaced with the node's sub-module prefix; the reader's
		// godoc carries why.
		if cfg.ResolveResult.Release != nil {
			for _, bf := range boundaryFeatureUses(cfg.ResolveResult.Release.Spec.Boundaries, node.BoundaryPath, nodeID) {
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
	// alias) matches an existing edge but names a conflicting target leaves that
	// edge as the developer declared it, records the conflict, and skips the
	// infra merge, which keeps a rejected retarget from half-applying. The
	// recorded conflicts are reported together once the walk is complete, so an
	// author sees every offending entry in one build rather than clearing them
	// one build at a time. Conformance (Step 1), dangling-target, and
	// undeclared-socket halts are not this pass's concern.
	// Determinism: nodes in flat.Nodes author order; sockets and aliases in
	// OrderedMap author order; created edges append after the Aggregate/Feature
	// edges; recorded conflicts follow that same walk, so the diagnostic is
	// ordered by the authoring surfaces rather than by a set that would have to
	// be sorted back into an order.
	if cfg.ResolveResult != nil && cfg.ResolveResult.Release != nil {
		var retargets []string
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
							retargets = append(retargets, fmt.Sprintf(
								"release edge %s/%s/%s retargets developer-declared target %q to %q",
								nodeID, socket, alias, edges[idx].Target, entry.Target))
							continue
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
					// stamped Release-origin. A targetless entry configures an
					// edge rather than creating one, so it draws nothing here;
					// whether it found an edge to configure is settled by the
					// unbound-configuration sweep below, once every edge exists.
					// Target existence is enforced by the origin-agnostic
					// dangling-target sweep below, not here. Whether the entry
					// creates an edge at all is the resolver's rule, asked
					// through the one predicate the required-socket rule also
					// asks, so neither stage can answer it differently.
					if resolver.ReleaseEntryCreatesEdge(entry) {
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
		// Every conflicting entry the walk met, reported as one halt. The halt
		// lands after the walk rather than at the first conflict so the author is
		// told about all of them at once; the walk's own bookkeeping — a
		// conflicting entry keeps the declared target and takes no infra — is what
		// makes carrying on past one conflict safe, and the edge set is discarded
		// with the error in any case.
		if len(retargets) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrReleaseEdgeRetarget(), strings.Join(retargets, "; "))
		}
	}

	// Release per-edge configuration that reached no edge. The edge set is final
	// for Release purposes once the pass above has run — every entry that named
	// a target drew its edge, and every entry that omitted one had its chance to
	// land on an existing edge. An entry still carrying `infra` at this point
	// applies to nothing: the infrastructure the operator authored would be
	// absent from the output while the build reported success.
	if cfg.ResolveResult != nil && cfg.ResolveResult.Release != nil {
		if err := guardUnboundReleaseEdgeConfig(flat, edges, cfg.ResolveResult.Release, cfg.CatalogLookup); err != nil {
			return nil, err
		}
	}

	// Snapshot the declared alias table before the drop below removes entries
	// from it, so what the Aggregate tree published stays legible afterwards.
	declaredExports := types.OrderedMap[string]{}
	for alias, target := range flat.ExportedComponents.All() {
		declaredExports.Set(alias, target)
	}

	// Drop exported-component aliases whose resolved target was pruned. A pruned
	// component must not be published as part of the module's public interface;
	// dropping the alias before the dangling sweep keeps a stale export from
	// pointing at a component that no longer exists. The aliases are collected
	// first and deleted after the walk: deleting during iteration would mutate
	// the key order the walk is reading.
	var prunedAliases []string
	for alias, target := range flat.ExportedComponents.All() {
		if _, gone := flat.Pruned.Get(target); gone {
			prunedAliases = append(prunedAliases, alias)
		}
	}
	for _, alias := range prunedAliases {
		flat.ExportedComponents.Delete(alias)
	}

	// Resolve export-alias edge targets to their internal node IDs. A sub-aggregate
	// publishes a public interface through exported components; a consuming edge may
	// target that public alias (the dotted path into the module) rather than the
	// module's internal component ID. Each exported alias maps to an already-resolved
	// internal node ID. An edge that names a real node directly keeps that target —
	// a direct reference wins over the alias table — so this pass only rewrites a
	// target that is not itself a node but is a known alias. Applies uniformly to
	// every edge origin (the edges slice already aggregates developer, Feature, and
	// Release-wired edges) and walks in author order so the rewrite is deterministic.
	// Runs after pruned aliases are dropped and before the dangling sweep, so a
	// target that resolves to neither a node nor a live alias still halts loudly
	// below rather than being silently swallowed here.
	for i := range edges {
		if _, isNode := flat.Nodes.Get(edges[i].Target); isNode {
			continue
		}
		if resolved, isAlias := flat.ExportedComponents.Get(edges[i].Target); isAlias {
			edges[i].Target = resolved
		}
	}

	// Origin-agnostic dangling-target guardrail. Every edge — developer-authored
	// (Aggregate), Feature-projected, or Release-wired — must point at a node
	// that exists in the resolved topology. A dangling target is a Loud Guardrail
	// (Invariant 4), not a silent drop: an unresolved target would otherwise
	// leave the consumer's inject silently empty. Walks in author order and halts
	// on the first offending edge for deterministic output. A target that was
	// pruned by a false `active` toggle is classified apart from one that never
	// existed, so the author sees why the target is missing.
	for _, e := range edges {
		if _, exists := flat.Nodes.Get(e.Target); !exists {
			if _, gone := flat.Pruned.Get(e.Target); gone {
				return nil, fmt.Errorf(
					"%w: uses edge %s/%s/%s targets %q, which was pruned from the topology because its active toggle resolved to false",
					ErrUsesPrunedComponent(), e.Source, e.Socket, e.Alias, e.Target)
			}
			return nil, fmt.Errorf(
				"%w: uses edge %s/%s/%s targets %q, which is not a component in the topology",
				ErrDanglingUsesTarget(), e.Source, e.Socket, e.Alias, e.Target)
		}
	}

	// Guard sub-aggregate components post-flatten. The resolve-stage guards see
	// only the root Aggregate's Components + Subsystems, never the loaded
	// sub-aggregate components — which exist only here, after flattening. Run
	// the exported resolver validators over the sub-aggregate nodes (and their
	// edges) so their values and per-edge config are checked like any other.
	// Predicate-inert: the sub-aggregate's own parameter context is not
	// resolved, so only context-independent structural constraints are checked
	// (conditional x-aggrega predicates are validated where the context exists).
	//
	// A rule declared over the composition itself is NOT asked here. Such a rule
	// is invoked once over every node the composition holds, by the pass that
	// reads this stage's presentation, so asking it here for the module-contributed
	// nodes alone would ask it twice for those and report one fault twice.

	// The presentation of what this build flattened, produced once. The placement
	// rule below reads it, and it is carried on the result so that the stage above
	// answers this stage's facts by embedding this value rather than by building a
	// second presentation over the same composition.
	stageView := newStageView(cfg, flat, edges)

	result := &TopologyResult{
		Nodes:                      flat.Nodes,
		Edges:                      edges,
		FeatureAttachments:         flat.FeatureAttachments,
		ExportedComponents:         flat.ExportedComponents,
		ExportedComponentsDeclared: declaredExports,
		Pruned:                     flat.Pruned,
	}
	// Assigned only when one was built. Storing an absent presentation directly
	// would leave the field non-nil while the value behind it is not there, so a
	// consumer's nil check would pass and its first read would panic.
	if stageView != nil {
		result.StageView = stageView
		result.StageCoordinates = stageView
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
				Entity:     "sub-aggregate conformance",
				Summary:    fmt.Sprintf("%d sub-aggregate guard violation(s)", len(subErrs)),
				Violations: subErrs,
			}
		}

		// The placement rule, asked of every sub-module the composition brought
		// in. It is expressed as a declared check rather than as a guard written
		// here, so where it runs follows from the facts it reads instead of from
		// this call site: the facts are complete at this stage and nowhere
		// earlier, because a module's node set exists only once the composition
		// is flattened and a node's placement is written after that.
		placementErrs, err := guardSubModulePlacement(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(placementErrs) > 0 {
			// One rule produced every finding, so one sentence is true of all of
			// them and names the fix. The group is therefore a condition and
			// declares that condition's identity, which is what lets its header
			// open with the registered sentence instead of a count.
			return result, &resolver.MultiResolverError{
				Entity:     "sub-module placement",
				Summary:    fmt.Sprintf("%d sub-module placement violation(s)", len(placementErrs)),
				Sentinel:   ErrUnplacedSubModule(),
				Violations: placementErrs,
			}
		}

		// The same rule one coordinate in, asked of every component the
		// composition holds. It reads the placement written above, so this is the
		// earliest point at which it can be asked of a routed component at all;
		// and it is asked over the whole node set rather than the
		// module-contributed part, which is why it is not folded into the
		// sub-aggregate guard above.
		componentErrs, err := guardComponentPlacement(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(componentErrs) > 0 {
			// A condition for its neighbour's reason: one rule, one sentence
			// true of every finding, one fix to name.
			return result, &resolver.MultiResolverError{
				Entity:     "component placement",
				Summary:    fmt.Sprintf("%d component placement violation(s)", len(componentErrs)),
				Sentinel:   ErrUnplacedComponent(),
				Violations: componentErrs,
			}
		}

		// The same relation read from its other end, asked of every component
		// entry the deployment wrote: those two ask whether anything placed a
		// component, this asks whether anything answers to an entry. It reads the
		// flattened node set, so it could not be asked before the composition was
		// flattened either.
		//
		// It is asked after them rather than before, and the order is a decision
		// rather than an accident. A document carrying both faults is one an
		// author fixes at the placement end, so reporting placement first leaves
		// every such document halting exactly as it did before this rule existed;
		// asking this one first would re-identify those halts without changing
		// which documents halt.
		referenceErrs, err := guardBoundaryComponentResolution(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(referenceErrs) > 0 {
			// A condition for the same reason as the two above: one rule, and
			// one sentence that is true of every entry it reported.
			return result, &resolver.MultiResolverError{
				Entity: "boundary component resolution",
				Summary: fmt.Sprintf(
					"%d unresolved boundary component reference(s)", len(referenceErrs)),
				Sentinel:   ErrUnresolvedBoundaryComponent(),
				Violations: referenceErrs,
			}
		}
	}

	return result, nil
}

// guardSubAggregateNodes runs the resolver's conformance validators over the
// flattened sub-aggregate components (nodes with a non-empty SourceAggregate)
// and their edges. Root and subsystem components are guarded during resolution; this
// closes the coverage gap for components that exist only after sub-aggregate
// flattening. Each sub-aggregate node carries its own resolved parameter map
// (ResolvedParameters), so conformance is fully parameter-aware: conditional
// x-aggrega-when / x-aggrega-validate predicates evaluate against the
// sub-aggregate's parameter context, the same as root components evaluate
// against theirs. Returns violations in flat.Nodes author order then edge
// order — deterministic.
//
// The rules asked here are the SAME rules the resolve asks, reached through the
// resolver's exported validators rather than restated, because the only thing
// that differs for a sub-aggregate component is where its declaration is read
// from. A rule asked of root components and not of these would make the coverage
// of a check depend on how deep the author nested the component.
//
// Both component pillars are asked, and each names the document whose author
// wrote it: the application for the configuration a component declares, the
// deployment for the physical sizing placed against it. Asking one and not the
// other would make which half of a component type's contract binds depend on how
// deep the author nested the component, which is the same coverage-by-nesting
// the paragraph above rules out.
//
// The node filter above is what bounds this pass, and it is why a rule whose
// evidence is complete only once the composition is flattened does not belong
// here: such a rule has to be asked of every node, and a pass reaching only the
// module-contributed ones would answer for half the composition. Those rules are
// declared over this stage's presentation and invoked once over the whole node
// set instead.
func guardSubAggregateNodes(flat *flattenResult, edges []NormalizedEdge, cfg BuildConfig) []*resolver.ResolverError {
	catalog := cfg.CatalogLookup
	if catalog.Component == nil {
		return nil
	}
	entity := "Aggregate"
	if cfg.ResolveResult != nil && cfg.ResolveResult.Aggregate != nil {
		entity = fmt.Sprintf("Aggregate %s", cfg.ResolveResult.Aggregate.Metadata.Name)
	}
	var release *types.Release
	if cfg.ResolveResult != nil {
		release = cfg.ResolveResult.Release
	}

	var errs []*resolver.ResolverError
	for nodeID, node := range flat.Nodes.All() {
		if node.SourceAggregate == "" {
			continue // root / subsystem components are guarded during resolution
		}

		// A sub-aggregate node whose type resolved as a catalog reference but is
		// absent from the guard's view is the same loud halt the Step-1 guards
		// raise — classify ONCE here, before any conformance pass. This subsumes
		// the value, per-edge-value, and per-edge-infra conformance below (all gated
		// on the same component lookup that previously skipped silently on a miss),
		// so a no-uses node's miss is loud too, not silently skipped. A Tier-1
		// relative-path type (a local file with no catalog entity) is a legitimate
		// skip (classifier returns nil). A resilient-inspection caller (serve)
		// tolerates the finding via the Step-1 sentinel filter; the build halts.
		catComp, found := catalog.Component(node.ComponentType)
		if !found {
			// The classifier takes the type alone: the finding it builds names no
			// document and no node, because a divergence between the build's
			// resolved set and this view is not something a node's author wrote.
			if verr := resolver.GuardEntityViewInconsistency(node.ComponentType); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}

		// Component-own uses carries no socket rule to ask here, and its absence is
		// deliberate rather than an omission. A socket the catalog did not declare
		// is accepted on this surface exactly as it is for a root component — the
		// flattened sub-aggregate surface must not be stricter than the surface it
		// was flattened from, or a module would lose a capability merely by being
		// composed — so there is no name to reject. The one rule the surface does
		// carry is the catalog-view classification, and that is asked above for
		// every node rather than only for the ones declaring a uses block.

		// Attachment slot shape over the block the module's component authored.
		// The attachments live on the per-node slice flattening collected, not on
		// the node, so they are re-projected into the authored map shape the
		// validator reads.
		if shape, ok := resolver.CompileAttachmentSlotShape(catComp); ok {
			errs = append(errs, shape.Validate(
				authoredAttachments(flat.FeatureAttachments[nodeID]),
				entity, fmt.Sprintf(".%s", nodeID))...)
		}

		// Attachment socket names on the same authored block. An attachment may
		// only name a socket the Feature it attaches declares, wherever the
		// attachment was written — so the rule is reached through the resolver's
		// own validator rather than restated, and a module's attachment cannot be
		// judged by a different standard than an Aggregate's.
		//
		// It is asked here because it cannot be asked earlier: the pre-flatten walk
		// reads the Aggregate and Release documents, and a module's document is not
		// among them until the loader has brought it in. Left unasked, an invented
		// socket on a module's attachment does not merely go unreported — the
		// feature-projected edge is drawn and reaches the consuming node's render
		// context, so the mistake lands in the emitted artifact.
		if catalog.Feature != nil {
			for _, att := range flat.FeatureAttachments[nodeID] {
				if att.Feature.Uses.Len() == 0 {
					continue
				}
				errs = append(errs, resolver.ValidateFeatureUsesAgainstSchema(
					entity, fmt.Sprintf(".%s.features[%q].uses", nodeID, att.InstanceName),
					att.Feature.Uses, att.Feature.Type, catalog)...)
			}
		}

		// The two component pillars, measured against the one contract that
		// declares both. The contract is compiled once and read twice: the compile
		// is uncached, so asking for it per pillar would compile the same type's
		// schema twice per node for no difference in the answer.
		if compiled, ok := resolver.CompileComponentSchema(catalog, node.ComponentType); ok {
			// Component values — validated against the sub-aggregate's own resolved
			// parameter context (carried on the node), so parameter-dependent
			// predicates evaluate correctly rather than predicate-inert.
			errs = append(errs, resolver.ValidatePillarConformance(
				compiled, "values", node.Values, node.ResolvedParameters, entity,
				fmt.Sprintf(".%s", nodeID), "the Component schema")...)

			// Component infra — the operator-side pillar, over the other document.
			// Its scope is the components the deployment PLACED, which is the same
			// standing the rule over root components has and is reached here by the
			// same reading: the placement map holds an entry for a node the
			// deployment named and for a node the placement of a routed module
			// covers, and that entry also carries the sizing. So the question of
			// whether this deployment answers for the node, and the payload it
			// answered with, come from one lookup and cannot disagree.
			//
			// An entry carrying no sizing is measured rather than skipped: every
			// field of such an entry is optional, so an entry that writes nothing
			// reads the same empty document as a component nothing placed, and the
			// two need opposite treatment. An entry that writes nothing is the
			// maximal violation of a key the contract declares required — that being
			// the one case a required key exists to catch — while a component this
			// deployment placed nowhere is not its to answer for and has a different
			// fault, reported by the placement rule that runs after this pass.
			//
			// The finding names the deployment rather than the application, because
			// that is the document whose author wrote the sizing and the one they
			// edit to fix it.
			if release != nil {
				if placement, placed := cfg.ResolveResult.BoundaryInfra[nodeID]; placed && placement != nil {
					errs = append(errs, resolver.ValidatePillarConformance(
						compiled, "infra", placement.Infra, node.ResolvedParameters,
						releaseEntityName(release), fmt.Sprintf(".%s", nodeID),
						"the Component schema")...)
				}
			}
		}

		// Per-edge developer values authored on the node's uses. Projected through
		// the resolver's own edge-values view so an edge that authors nothing is
		// checked here exactly as it is for a root component — a socket contract
		// must not depend on how deep the author nested the component.
		for socket, sv := range node.Uses.All() {
			compiled, ok := resolver.CompileSocketPillarSchema(catalog, node.ComponentType, socket, "values")
			if !ok {
				continue
			}
			for _, ev := range resolver.EdgeValuesOf(sv) {
				errs = append(errs, resolver.ValidatePillarConformance(
					compiled, "values", ev.Values, node.ResolvedParameters, entity,
					fmt.Sprintf(".%s.uses[%q][%q]", nodeID, socket, ev.Alias), "the socket valueSchema")...)
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

	errs = append(errs, guardSubAggregateReleaseEdges(flat, release, catalog)...)
	return errs
}

// releaseEntityName renders the deployment document a finding in this package
// names. It is one home for the rendering because every rule here reporting
// against the deployment needs the same string, and hand-written copies of it
// are free to spell one document differently — a reader comparing two
// diagnostics about the same file could not account for the difference, and
// nothing compares them.
//
// A document whose name is empty is named by its kind alone, which keeps a
// diagnostic from carrying a trailing space where a name should be.
func releaseEntityName(release *types.Release) string {
	if release == nil || release.Metadata.Name == "" {
		return "Release"
	}
	return "Release " + release.Metadata.Name
}

// authoredAttachments re-projects the per-node attachment slice flattening
// collected back into the author-ordered map shape the slot-shape validator
// reads. Flattening splits a component's `features` block into a slice so the IR
// cascade can walk tiers; the shape rule is asked of the block as authored, so it
// is reassembled here rather than the validator being taught a second input
// shape.
func authoredAttachments(attachments []FeatureAttachment) types.OrderedMap[types.FeatureInstance] {
	out := types.OrderedMap[types.FeatureInstance]{}
	for _, a := range attachments {
		out.Set(a.InstanceName, a.Feature)
	}
	return out
}

// guardUnboundReleaseEdgeConfig halts when a Release per-edge `uses` entry
// carries `infra` that no edge will ever receive. It runs after the Release edge
// pass, where the edge set becomes final: an entry naming a target has drawn its
// edge by then, and an entry omitting one has had its chance to land on an edge
// the Aggregate or a Feature declared. Configuration still holding no edge at
// that point is authoring that does nothing, and accepting it silently would put
// the operator's infrastructure out of the output while the build succeeds.
//
// Three conditions are deliberately NOT this guard's, and each is left to the
// rule that owns it, so a halt added here cannot disable one of them:
//
//   - A socket the consuming component's usesSchema does not declare belongs to
//     the undeclared-socket rule, which produces the better diagnostic — it can
//     list the sockets that do exist. Such an entry is skipped here whenever the
//     consuming component's type is in view, whether the rule already ran before
//     flattening or runs over the flattened sub-aggregate nodes afterwards.
//   - A target that names no component belongs to the dangling-target sweep: an
//     entry with a target does create an edge, so it never reaches this guard.
//   - A consuming component pruned by a false `active` toggle takes its whole
//     edge set with it by design. The entry is inert because the operator turned
//     the component off, not because the entry is wrong.
//
// A node absent from the topology altogether is this guard's, because the entry
// configures nothing for the same reason and nothing else reports it. The
// diagnostic says which case it is so the author knows whether to correct the
// node name or to wire the edge.
//
// The entries come from the shared Release-boundary walk, so the first offender
// is the same one on every run of the same input. That walk visits every
// boundary, while the Release edge pass visits only the boundary chain the
// consuming node belongs to — so an entry the pass never reached is caught here
// too, on the same evidence as any other: no edge carries it.
func guardUnboundReleaseEdgeConfig(
	flat *flattenResult,
	edges []NormalizedEdge,
	release *types.Release,
	catalog resolver.CatalogLookup,
) error {
	if release == nil {
		return nil
	}
	for _, ref := range resolver.ReleaseEdgeEntries(release) {
		if ref.Entry.Infra.Len() == 0 {
			continue
		}
		bound := false
		for i := range edges {
			if edges[i].Source == ref.NodeID && edges[i].Socket == ref.Socket && edges[i].Alias == ref.Alias {
				bound = true
				break
			}
		}
		if bound {
			continue
		}
		if _, pruned := flat.Pruned.Get(ref.NodeID); pruned {
			continue
		}
		node, isNode := flat.Nodes.Get(ref.NodeID)
		if !isNode {
			return fmt.Errorf(
				"%w: release edge %s/%s/%s sets infra on %q, which is not a component in the topology",
				ErrUnboundReleaseEdgeConfig(), ref.NodeID, ref.Socket, ref.Alias, ref.NodeID)
		}
		// Without a catalog view, or with a component type absent from it, whether
		// the socket is declared cannot be answered — and the answer is what
		// separates this rule from the undeclared-socket one. Both cases are
		// classified by their own rules elsewhere; guessing here would take a halt
		// from a rule that reports it better.
		if catalog.Component == nil {
			continue
		}
		catComp, found := catalog.Component(node.ComponentType)
		if !found {
			continue
		}
		if _, declared := catComp.Spec.UsesSchema.Get(ref.Socket); !declared {
			continue
		}
		return fmt.Errorf(
			"%w: release edge %s/%s/%s sets infra but no edge is bound there: an entry without a target configures an edge that must already exist, so give the entry a target to wire the edge or remove the entry",
			ErrUnboundReleaseEdgeConfig(), ref.NodeID, ref.Socket, ref.Alias)
	}
	return nil
}

// guardSubAggregateReleaseEdges validates the Release's per-edge `uses` surface
// for consuming nodes that exist only after sub-aggregate flattening. Step 1
// validates the same surface for every node whose component type the resolver can
// see, and skips these — the type is not knowable before the module is loaded — so
// without this pass a Release could name a socket no usesSchema declares, or set
// out-of-range per-edge infra, on a sub-aggregate node and be accepted in silence.
//
// The rule is the socket-allowlist one: the socket must exist in the consuming
// component's usesSchema. Per-edge `infra` conformance is not asked here,
// because by the time this runs an entry's infra has necessarily reached an
// edge — the entry either named a target, which drew one, or landed on one the
// module declared — and the merged result is validated by the per-edge walk
// above. An entry whose infra reached no edge has already halted at the
// unbound-configuration sweep; validating a value that goes nowhere would
// report the lesser of the two faults.
//
// Walk order is the shared Release-boundary walk's, so violations are
// deterministic across runs.
func guardSubAggregateReleaseEdges(
	flat *flattenResult,
	release *types.Release,
	catalog resolver.CatalogLookup,
) []*resolver.ResolverError {
	if release == nil {
		return nil
	}
	entity := releaseEntityName(release)

	var errs []*resolver.ResolverError
	for _, ref := range resolver.ReleaseEdgeEntries(release) {
		node, isNode := flat.Nodes.Get(ref.NodeID)
		if !isNode || node.SourceAggregate == "" {
			continue // absent, or a root/subsystem node Step 1 already validated
		}
		catComp, found := catalog.Component(node.ComponentType)
		if !found {
			continue // classified once by the per-node view-inconsistency check
		}
		if _, declared := catComp.Spec.UsesSchema.Get(ref.Socket); !declared {
			errs = append(errs, &resolver.ResolverError{
				Entity: entity,
				Field:  fmt.Sprintf(".%s.uses[%q][%q]", ref.NodeID, ref.Socket, ref.Alias),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)",
					ref.Socket, node.ComponentType, catComp.Spec.UsesSchema.Keys()),
				Err: resolver.ErrUnknownUsesSocket(),
			})
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
// from outer to inner, returning every feature attachment that contributes a
// uses-edge to nodeID, in author order. Mirrors the IR feature cascade's
// Release-side levels.
//
// Both Release attachment scopes are collected here, and one walk serves them
// because they are one feature declared at two breadths. A boundary's own
// `features` contributes its edge from every component the boundary holds; a
// boundary component's `features` contributes the same edge from that one
// component. Neither may declare `values`, and neither creates compute: the
// edge connects components that already exist, so a target naming no component
// halts at the dangling-target sweep like any other edge.
//
// Per level the boundary's own attachments come before that level's
// per-component ones, which is the breadth order the IR feature cascade reads
// the same two scopes in. A component belongs to exactly one boundary — the
// Release load rejects a name declared in more than one — so the per-component
// lookup contributes at exactly one level of the chain.
//
// Targets are NOT namespaced with the consuming node's sub-module prefix, which
// is what separates these two scopes from the Aggregate-side ones. A Release
// names a component by the id it carries in the assembled graph
// ([types.BoundaryComponents] keys on it), so a target authored beside that name
// in the same document is already a graph id; prefixing it would compose the
// module prefix twice and leave the edge pointing at nothing.
func boundaryFeatureUses(boundaries types.OrderedMap[types.Boundary], boundaryPath, nodeID string) []scopeFeatureUses {
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
		components := types.BoundaryComponents(boundary)
		if bc, ok := components.Get(nodeID); ok {
			for fname, feat := range bc.Features.All() {
				result = append(result, scopeFeatureUses{instanceName: fname, featureType: feat.Type, uses: feat.Uses})
			}
		}
		current = boundary.Boundaries
	}
	return result
}

// releaseComponentUses walks the Release boundary chain named by boundaryPath
// (the same walk boundaryFeatureUses uses) and returns the per-edge uses
// surface the Release authored on the BoundaryComponent for nodeID, if any. The
// outermost level whose Components carry a non-empty Uses for nodeID wins.
// Returns false when no Release component-edge surface applies to the node.
//
// A component belongs to exactly one boundary, so at most one level of the chain
// can declare nodeID at all and the outermost rule never has to arbitrate. That
// is enforced at Release load, which rejects a name declared in more than one
// boundary — and the enforcement is what this reader relies on. Were the shape
// admitted, this walk and the boundary-context walk that produced boundaryPath
// would resolve one node to different declarations: the boundary context takes
// the innermost declaration (a per-component last-write-wins over a depth-first
// descent) while the per-edge surface here takes the outermost that carries a
// non-empty uses, so a node's output location and its per-edge infra could
// originate at different levels. Do not relax the load-time rejection without
// reconciling the two walks first.
func releaseComponentUses(boundaries types.OrderedMap[types.Boundary], boundaryPath, nodeID string) (types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]], bool) {
	parts := splitScopePath(boundaryPath)
	current := boundaries
	for _, part := range parts {
		boundary, ok := current.Get(part)
		if !ok {
			break
		}
		components := types.BoundaryComponents(boundary)
		if bc, ok := components.Get(nodeID); ok && bc.Uses.Len() > 0 {
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
