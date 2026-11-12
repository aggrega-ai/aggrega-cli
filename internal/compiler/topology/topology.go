// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"context"
	"fmt"

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
//     IR builder's BuildIRContext step 2 (topology→IR edge conversion loop)
//     reads this slice in slice order. The full producer-and-consumer
//     contract is documented in this package's doc.go (Cross-package
//     determinism contract).
//
// TopologyResult's own fields are all author-declared; the
// synthesized-derived discipline applies elsewhere.
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
	// DeclaringSubsystems is the subsystem every component in the composition was
	// declared under, keyed by the identity that component carries here, in the
	// order flattening met each declaration.
	//
	// It is a list per identity, and that is the whole of what it carries: Nodes
	// holds one component per identity, so a second declaration of one identity
	// replaces the first there and the two become indistinguishable from one. Both
	// are kept here, and they stay readable for as long as this composition does —
	// a rule over the flattened set reads it to ask whether one identity was
	// declared more than once.
	//
	// A component a false `active` toggle removed is absent, exactly as it is
	// absent from Nodes.
	DeclaringSubsystems types.OrderedMap[[]string]
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

	// moduleDocuments is the document of every sub-module the composition brought
	// in, keyed by the dotted prefix its components carry. It is read by lookup
	// only, through [TopologyResult.ModuleDocument], so no output depends on an
	// iteration order over it.
	moduleDocuments map[string]types.NormalizedAggregate
}

// ModuleDocument returns the document of the sub-module whose components carry
// the given prefix — a node's [TopologyNode.SourceAggregate] — as flattening
// loaded it, with the parameters that import resolved it under already applied.
//
// A module's own declarations exist nowhere else once its components have been
// merged into the composition, so a later stage asking what a module declared
// about a scope of its own — the attachments one of its subsystems carries —
// reads that document here rather than loading the module a second time. A
// module imported twice is two entries, one per prefix, because each import may
// resolve the document under different parameters.
//
// Returns false for the empty prefix, which is the Aggregate's own and not a
// module's, and for a result no flattening produced.
func (r *TopologyResult) ModuleDocument(prefix string) (types.NormalizedAggregate, bool) {
	if r == nil || prefix == "" {
		return types.NormalizedAggregate{}, false
	}
	document, ok := r.moduleDocuments[prefix]
	return document, ok
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
//     SelfInject, SubsystemPath, SourceAggregate, AggregateAnnotations.
//     InstanceID is the dot-notation ID synthesized by qualifiedID (the
//     dot is reserved as the compiler-internal path separator);
//     SubsystemPath is the dotted path of the subsystem the component
//     sits in (empty at top level); SourceAggregate is the dotted prefix of the
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
	// AggregateAnnotations is the node's effective Aggregate-side annotation set:
	// the annotations of every subsystem on its path, outer to inner, with the
	// component's own written over them, as [resolver.WireAggregateAnnotations]
	// computes it for the Aggregate document that declares the node — the root
	// Aggregate, or the sub-aggregate a module node comes from. Only scalar values
	// are present; a structured value takes part in the cascade and is left out of
	// this projection, as it is of every annotation bucket a template reads. Empty
	// when nothing on the node's path carries an annotation.
	AggregateAnnotations types.OrderedMap[string]
	SubsystemPath        string
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
	Source string
	Target string
	// AuthoredTarget is the target as the document that drew this edge wrote
	// it, kept unchanged where Target is later rewritten.
	//
	// The two differ for exactly one class of edge: one whose author named a
	// module's published alias rather than a component id. Resolving that alias
	// replaces Target with the id the module publishes it for, and the string
	// the author typed is gone from the edge afterwards. A rule comparing what
	// one document wrote against what another wrote needs the typed string on
	// both sides — a comparison against the resolved id would report a
	// disagreement between two documents that named the very same alias.
	//
	// It is carried rather than recovered because the resolution is not
	// invertible: several aliases may publish one component, so the id names no
	// single alias to recover.
	AuthoredTarget string
	// AddressedTarget is the name the edge's target was looked up by in the
	// flattened composition: the authored target qualified with the prefix of the
	// module whose document drew the edge, before an export alias is resolved.
	//
	// It is the authored target placed in the composition. A document names
	// components relative to itself — a module's own components by their local
	// names, and a component inside a module it imports as that module's name and
	// an alias the module exports — so the same authored target names different
	// things in different documents, and this is the one spelling that says which.
	// The Release names every component by the id it carries in the composition,
	// so an edge it draws addresses exactly what it authored. It differs from
	// Target where an export alias was resolved, which is the difference a rule
	// about how a connection reached its target reads.
	AddressedTarget string
	Socket          string
	Alias           string
	Inject          *types.InjectBlock
	OriginFeature   string // non-empty if edge came from a Feature's uses
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
//
// Connection is set on an attachment a connection of the component carries,
// lowered onto the component under the name the alias and the slot compose:
// it names that connection, and every later stage reads the connection from
// here rather than out of the name. Nil for an attachment of the component's own
// `features` block.
type FeatureAttachment struct {
	InstanceName string
	Feature      types.FeatureInstance
	SourceTier   int // Onion Model tier (set by IR builder in Step 3)
	Connection   *types.AttachmentConnection
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
	// A build that loads its sub-modules names a routed module's components under
	// the routing entry before the resolve wires the per-component map (see
	// [ModuleLoads.PlaceRoutedComponents]), so each already has an entry of its
	// own. A component that still has none — routed by a resolve that loaded no
	// module, so the routing entry named no components — is matched here, the
	// first point at which its node exists, to the placement that covers it, and
	// the match is written back into the per-component map so every later reader —
	// output directory, sync wave, annotations, per-node infra — sees a routed
	// component exactly as it sees a directly-named one, with no routed-axis
	// knowledge of its own.
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
					featEdges, err := normalizeUses(types.FeatureUsesConnections(fa.Feature.Uses), nodeID, fa.Feature.Type, fa.InstanceName)
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
			subEdges, err := normalizeUses(types.FeatureUsesConnections(sf.uses), nodeID, sf.featureType, sf.instanceName)
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
				bndEdges, err := normalizeUses(types.FeatureUsesConnections(bf.uses), nodeID, bf.featureType, bf.instanceName)
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
	// edge as the developer declared it and skips the infra merge, which keeps a
	// rejected retarget from half-applying. The conflict is REPORTED by the rule
	// declared over the finalised graph, not here: the target each document
	// named survives onto the edge as a fact keyed by that edge, so the rule is
	// asked once per connection over every consumer at once and reports every
	// offending entry in one build. Conformance (Step 1), the target-existence
	// rules, and the undeclared-socket halt are not this pass's concern either.
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
						// developer owns the architecture they declared — and the
						// rule over the finalised graph is what raises it. The
						// entry is skipped here so a rejected redirection cannot
						// half-apply by leaving its infra behind.
						if entry.Target != "" && entry.Target != edges[idx].AuthoredTarget {
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
					// Target existence is enforced by the rules declared over
					// the finalised graph, not here. Whether the entry
					// creates an edge at all is the resolver's rule, asked
					// through the one predicate the required-socket rule also
					// asks, so neither stage can answer it differently.
					if resolver.ReleaseEntryCreatesEdge(entry) {
						edges = append(edges, NormalizedEdge{
							Source:         nodeID,
							Target:         entry.Target,
							AuthoredTarget: entry.Target,
							Socket:         socket,
							Alias:          alias,
							Infra:          entry.Infra,
							OriginRelease:  true,
						})
					}
				}
			}
		}
	}

	// Release per-edge configuration naming a component the composition does not
	// hold. The edge set is final for Release purposes once the pass above has
	// run — every entry that named a target drew its edge, and every entry that
	// omitted one had its chance to land on an existing edge. An entry still
	// carrying `infra` at this point applies to nothing: the infrastructure the
	// operator authored would be absent from the output while the build reported
	// success.
	//
	// The same fault on a component the composition DOES hold is asked once the
	// composition is built, as a rule declared over the Release's authored
	// per-edge surface — it is invoked once per authored entry, so it reports
	// every offending entry with the authored locator of each rather than the
	// first one this walk reaches.
	if cfg.ResolveResult != nil && cfg.ResolveResult.Release != nil {
		if err := guardUnboundReleaseEdgeConfig(flat, edges, cfg.ResolveResult.Release); err != nil {
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
	// dropping the alias before the targets are resolved keeps a stale export
	// from standing in for a component that no longer exists. The aliases are collected
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
	// target that is not itself a node but is a known alias. Whether a direct
	// reference into a module was allowed to name that node is not this pass's
	// question: the rule over the finalised graph refuses one that passes into a
	// module by a name the module does not export. Applies uniformly to
	// every edge origin (the edges slice already aggregates developer, Feature, and
	// Release-wired edges) and walks in author order so the rewrite is deterministic.
	// Runs after pruned aliases are dropped, so a target that resolves to neither
	// a node nor a live alias is left standing as the author wrote it rather than
	// being silently swallowed here — which is what lets the rules over the
	// finalised graph refuse it loudly and say which of the two it is.
	//
	// The rewrite replaces the resolved target alone and leaves the authored one
	// standing beside it, because the resolution is not invertible — several
	// aliases may publish one component — and a rule comparing what one document
	// wrote against what another wrote needs the typed string on both sides. The
	// name the target was looked up by is recorded beside both before it is
	// replaced, for a rule asking how the connection reached its target.
	for i := range edges {
		edges[i].AddressedTarget = edges[i].Target
		if _, isNode := flat.Nodes.Get(edges[i].Target); isNode {
			continue
		}
		if resolved, isAlias := flat.ExportedComponents.Get(edges[i].Target); isAlias {
			edges[i].Target = resolved
		}
	}

	// An edge whose target answers to no surviving component is NOT refused here.
	// The rules that refuse it are declared over the finalised graph, where the
	// target a connection resolved to is a named fact and the connection is
	// presented once whichever surface drew it — so the two are asked over every
	// origin at once and report every offending connection in one build instead
	// of the first. Both classifications survive that move: a target the
	// composition removed by a false `active` toggle is reported apart from one
	// that never existed, and the removed names are carried on the result below
	// for exactly that reason.

	// Guard sub-aggregate components post-flatten. The resolve-stage guards see
	// only the root Aggregate's Components + Subsystems, never the loaded
	// sub-aggregate components — which exist only here, after flattening. Run
	// the exported resolver validators over the sub-aggregate nodes (and their
	// edges) so their values and per-edge config are checked like any other.
	// Each sub-aggregate node carries the parameter context its own module
	// resolved, so the conformance run here is parameter-aware rather than
	// structural-only.
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
		DeclaringSubsystems:        flat.DeclaringSubsystems,
		moduleDocuments:            make(map[string]types.NormalizedAggregate, len(flat.ModuleDocuments)),
	}
	for _, module := range flat.ModuleDocuments {
		result.moduleDocuments[module.path] = module.document
	}
	// Assigned only when one was built. Storing an absent presentation directly
	// would leave the field non-nil while the value behind it is not there, so a
	// consumer's nil check would pass and its first read would panic.
	if stageView != nil {
		result.StageView = stageView
		result.StageCoordinates = stageView
	}

	if cfg.ResolveResult != nil {
		subErrs := guardSubAggregateNodes(flat, edges, cfg)
		// The rules over what an authoring document declares, asked of each
		// sub-module's own document — the half of those rules the reading of the
		// Aggregate before flattening could not ask, because a module's document is
		// loaded only here. Their findings join the sub-aggregate group: both are the
		// module-side counterpart of a check the Aggregate's own components meet.
		moduleErrs, err := guardModuleDocuments(ctx.Err, flat, cfg.CatalogLookup)
		if err != nil {
			return nil, err
		}
		subErrs = append(subErrs, moduleErrs...)
		if len(subErrs) > 0 {
			// Return the fully-built topology alongside the halt: the graph is
			// complete at this final guard, and a resilient-inspection caller
			// that tolerates the catalog-view-inconsistency class needs the
			// graph it just validated. Halting callers check err first and
			// discard the result, so the non-nil graph never leaks into a
			// halt path.
			return result, &resolver.MultiResolverError{
				Entity:     "sub-aggregate conformance",
				Summary:    fmt.Sprintf("%d sub-aggregate refusal(s)", len(subErrs)),
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
		// entry the Release wrote: those two ask whether anything placed a
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

		// The reach rule, asked of every entry the Release's per-edge `uses`
		// surface wrote. It reads the connection set, which is final only once
		// every surface that draws one has drawn it, and the consuming
		// component's type, which a sub-module contributes only when its own
		// document is loaded — so this is the earliest point holding its
		// evidence.
		//
		// It is asked after the rules above rather than before, and the order is
		// a decision rather than an accident. An entry on a component nothing
		// answers to is a fault those rules name at the component's own key,
		// which is where an author fixes it; asking this one first would report
		// the same document at the entry instead and leave the component's own
		// fault unstated.
		configErrs, err := guardReleaseEdgeConfiguration(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(configErrs) > 0 {
			// A condition for its neighbours' reason: one rule, one sentence
			// true of every finding, one fix to name.
			return result, &resolver.MultiResolverError{
				Entity: "release per-edge configuration",
				Summary: fmt.Sprintf(
					"%d unbound release per-edge configuration(s)", len(configErrs)),
				Sentinel:   ErrUnboundReleaseEdgeConfig(),
				Violations: configErrs,
			}
		}

		// The declared-slots rule for a connection's attachments, asked of the
		// components a sub-module contributes — the half the reading of the
		// authoring documents could not ask, because such a component's type is
		// known only once its module has been read — and of every entry the
		// Release writes, which this stage enumerates with the site each was
		// written at.
		//
		// The group names no condition of its own: every finding it carries
		// names the rule's condition already, and naming the condition here too
		// would raise it from this function, outside the rule's own declaration.
		slotErrs, err := guardConnectionAttachmentSlots(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(slotErrs) > 0 {
			return result, &resolver.MultiResolverError{
				Entity:     "connection attachment slots",
				Summary:    fmt.Sprintf("%d connection attachment(s) under a name no slot declares", len(slotErrs)),
				Violations: slotErrs,
			}
		}

		// The rules over what the Release writes about a component's connection
		// attachments: a component-level or boundary-wide key spelling one, and an
		// attachment only the Release makes naming no type. All read the
		// developer's connection attachments, which a sub-module's component
		// carries in its own document, so all are asked here. They follow the
		// declared-slots rule because a name no slot declares is the earlier
		// fault: an author fixes the name before the spelling or the type it is
		// written with means anything.
		//
		// The group names no condition, for the declared-slots group's reason:
		// each finding names its rule's condition already.
		attachmentErrs, err := guardReleaseConnectionAttachments(ctx.Err, cfg, stageView)
		if err != nil {
			return nil, err
		}
		if len(attachmentErrs) > 0 {
			return result, &resolver.MultiResolverError{
				Entity:     "release connection attachments",
				Summary:    fmt.Sprintf("%d release connection attachment violation(s)", len(attachmentErrs)),
				Violations: attachmentErrs,
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
// of a check depend on how deep the author nested the component. The attachment
// slot shape is the one asked through a different instrument, and the call site
// below states why its verdict is nonetheless the resolve's.
//
// Both component pillars are asked, and each names the document whose author
// wrote it: the Aggregate for the configuration a component declares, the
// Release for the physical sizing placed against it. Asking one and not the
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
	releasePath := ""
	if cfg.ResolveResult != nil {
		release = cfg.ResolveResult.Release
		releasePath = cfg.ResolveResult.ReleasePath
	}
	releaseEntries := releaseComponentEntriesByNode(release)
	moduleEntities := moduleDocumentEntities(flat)

	var errs []*resolver.ResolverError
	for nodeID, node := range flat.Nodes.All() {
		if node.SourceAggregate == "" {
			continue // the root Aggregate's components are guarded during resolution
		}
		// What the module's component declares — its attachments, its values, the
		// values on its own connections — is written in the module's own document,
		// so a finding about it names that document, carries its file, and is
		// positioned at the key it holds through the locator assembly, under the
		// local name and the module-local subsystem path the node carries. Naming
		// the importing Aggregate would send the reader to a document that holds
		// none of these keys.
		moduleEntity, known := moduleEntities[node.SourceAggregate]
		if !known {
			moduleEntity = entity
		}
		moduleFile := node.SourceFile
		moduleNode := nodeInstanceOf(nodeID, node)

		// A sub-aggregate node whose type resolved as a catalog reference but is
		// absent from the guard's view is the same loud halt the Step-1 guards
		// raise — classify ONCE here, before any conformance pass. This subsumes
		// the value, per-edge-value, and per-edge-infra conformance below (all gated
		// on the same component lookup, which alone would pass a miss silently),
		// so a no-uses node's miss is loud too, not silently skipped. A type with
		// a ./ or ../ prefix is no exception: it names no catalog entity, and the
		// dependency resolution every surface runs before compiling refuses it, so
		// reaching here with one is the same divergence. A resilient-inspection
		// caller (serve) tolerates the finding via the Step-1 sentinel filter; the
		// build halts.
		catComp, found := catalog.Component(node.ComponentType)
		if !found {
			// The halt takes the type alone: the finding it builds names no document
			// and no node, because a divergence between the build's resolved set and
			// this view is not something a node's author wrote.
			errs = append(errs, resolver.GuardEntityViewInconsistency(node.ComponentType))
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
		//
		// This is the one rule here asked through a different instrument from the
		// resolve's. A root component's authored block answers to the attachment
		// envelope and to the delivery-mechanism allowlist rule; this block
		// answers to the envelope and to the slot projection. The projection is
		// built to reach the verdict those reach — an empty inject block on a slot
		// permitting no delivery is admitted by both, a block naming a mechanism
		// the slot does not permit refused by both — which is what keeps this
		// surface from being stricter than the one it was flattened from.
		if shape, ok := resolver.CompileAttachmentSlotShape(catComp); ok {
			errs = append(errs, shape.ValidateAt(
				authoredAttachments(flat.FeatureAttachments[nodeID]),
				moduleEntity, moduleFile,
				func(path string) string { return constraint.ComponentLocator(moduleNode, path) })...)
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
				// An attachment lowered from one of the component's connections was
				// written on that connection's entry, under the slot it fills, so it
				// is positioned there and named by the slot rather than by the
				// composed name it carries once lowered.
				site := constraint.AttachmentSiteInBlock(constraint.AuthoringDocumentAggregate,
					constraint.ComponentSite(moduleNode.AuthoredName, moduleNode.SubsystemPath, moduleNode.SynthesizedSubsystem),
					att.InstanceName, moduleFile)
				if conn := att.Connection; conn != nil {
					site = constraint.ConnectionAttachmentSiteInAggregate(moduleNode, conn.Socket, conn.Alias, conn.Attachment)
				}
				errs = append(errs, resolver.ValidateFeatureUsesAgainstSchema(
					moduleEntity, resolver.FeatureUsesSite{
						File:       moduleFile,
						Locate:     site.SocketLocator,
						Component:  moduleNode.AuthoredName,
						Attachment: site.Name,
					},
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
			errs = append(errs, resolver.ValidatePillarConformanceAt(
				compiled, "values", node.Values, node.ResolvedParameters, moduleEntity, moduleFile, "",
				func(path string) string { return constraint.ComponentLocator(moduleNode, path) },
				"the Component schema")...)

			// Component infra — the operator-side pillar, over the other document.
			// Its scope is the components the Release PLACED, which is the same
			// standing the rule over root components has and is reached here by the
			// same reading: the placement map holds an entry for a node the
			// Release named and for a node the placement of a routed module
			// covers, and that entry also carries the sizing. So the question of
			// whether this Release answers for the node, and the payload it
			// answered with, come from one lookup and cannot disagree.
			//
			// An entry carrying no sizing is measured rather than skipped: every
			// field of such an entry is optional, so an entry that writes nothing
			// reads the same empty document as a component nothing placed, and the
			// two need opposite treatment. An entry that writes nothing is the
			// maximal violation of a key the contract declares required — that being
			// the one case a required key exists to catch — while a component this
			// Release placed nowhere is not its to answer for and has a different
			// fault, reported by the placement rule that runs after this pass.
			//
			// The finding names the Release rather than the Aggregate, because
			// that is the document whose author wrote the sizing and the one they
			// edit to fix it.
			if release != nil {
				if placement, placed := cfg.ResolveResult.BoundaryInfra[nodeID]; placed && placement != nil {
					errs = append(errs, resolver.ValidatePillarConformanceAt(
						compiled, "infra", placement.Infra, node.ResolvedParameters,
						releaseEntityName(release), releasePath, nodeID,
						releasePlacementLocator(releaseEntries, nodeID, placement),
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
				errs = append(errs, resolver.ValidatePillarConformanceAt(
					compiled, "values", ev.Values, node.ResolvedParameters, moduleEntity, moduleFile, "",
					func(path string) string {
						return constraint.ConnectionLeafLocator(moduleNode, socket, ev.Alias, path)
					},
					"the socket valueSchema")...)
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

	errs = append(errs, guardSubAggregateReleaseEdges(flat, release, releasePath, catalog)...)
	errs = append(errs, guardSubAggregateReleaseAttachmentSockets(flat, release, releasePath, catalog)...)
	return errs
}

// moduleDocumentEntities names, by the prefix each module's components carry, the
// module's own document as a finding about a key inside it names that document.
// It is read by key only, so nothing any output carries depends on an order over
// it.
func moduleDocumentEntities(flat *flattenResult) map[string]string {
	out := map[string]string{}
	for _, module := range flat.ModuleDocuments {
		if agg := module.document.Aggregate(); agg != nil {
			out[module.path] = resolver.ModuleDocumentEntity(agg)
		}
	}
	return out
}

// releaseComponentEntriesByNode indexes the component entries a Release writes by
// the node each names, for a lookup by node. It is read by key only, so nothing
// any output carries depends on an order over it.
func releaseComponentEntriesByNode(release *types.Release) map[string]resolver.ReleaseComponentEntry {
	out := map[string]resolver.ReleaseComponentEntry{}
	for _, entry := range resolver.ReleaseComponentEntries(release) {
		out[entry.NodeID] = entry
	}
	return out
}

// releasePlacementLocator returns how a finding about the sizing a Release placed
// against a node is positioned: inside the component entry the Release wrote for
// the node, through the locator assembly, at the path the finding landed on.
//
// A node the Release placed without writing an entry for it — a component a
// routed module placement covers — has no entry holding the sizing, so its
// findings name the boundary that placed it and stop there: a key inside that
// boundary would be one the document does not hold. The boundary normalization
// synthesized names no level, so such a finding is positioned at the file.
func releasePlacementLocator(
	entries map[string]resolver.ReleaseComponentEntry,
	nodeID string,
	placement *resolver.BoundaryInfra,
) func(path string) string {
	if entry, written := entries[nodeID]; written {
		site, keys := entry.Site()
		return func(path string) string {
			return constraint.SiteLocator(constraint.AuthoringDocumentRelease, site, keys, path)
		}
	}
	scope := placement.BoundaryPath
	if placement.Synthesized {
		scope = ""
	}
	return func(string) string {
		return constraint.SiteLocator(constraint.AuthoringDocumentRelease, constraint.AuthoredSite{ScopePath: scope}, nil, "")
	}
}

// guardSubAggregateReleaseAttachmentSockets asks the attachment socket rule of
// every attachment the Release writes against a component a sub-module
// contributed: an attachment may only name a socket the Feature it attaches
// declares.
//
// The rule over the Release's per-component attachments is asked before
// flattening, of the components the Aggregate declares. A sub-module's
// component is not among them — the Release reaches it through a routing entry
// or by the identifier the composition gives it, neither of which names a
// component the Aggregate declares — so without this the Release could wire an
// invented socket on a module's component and the edge would be drawn. The
// comparison is reached through the resolver's exported validator, the one the
// module's own attachments are measured with above, so an attachment is judged
// by one standard whichever document wrote it.
//
// Only the per-component entries are read. A boundary-wide attachment is the
// Release's own scope, measured once per boundary by the rule over that scope
// whatever the boundary holds, so reading it here would report it again for
// every module component the boundary places.
//
// Nodes are walked in flattened author order, and each node's boundary chain
// outer to inner, so the findings are deterministic.
//
// A finding names the Release's file and is positioned at the socket's entry
// inside the attachment the Release wrote, at the site the Release's own walk
// records for the component entry, through the locator assembly. An entry keyed
// by the identifier the composition gave the component holds the dialect's
// separator, so its locator stops at the components block, and the finding names
// the component, the attachment and the socket in its detail; an entry written
// through a routing entry is keyed by the local name and is named to the socket.
func guardSubAggregateReleaseAttachmentSockets(
	flat *flattenResult,
	release *types.Release,
	releasePath string,
	catalog resolver.CatalogLookup,
) []*resolver.ResolverError {
	if release == nil || catalog.Feature == nil {
		return nil
	}
	entity := releaseEntityName(release)
	entries := releaseComponentEntriesByNode(release)

	var errs []*resolver.ResolverError
	for nodeID, node := range flat.Nodes.All() {
		if node.SourceAggregate == "" {
			continue // a component the Aggregate declares meets the rule before flattening
		}
		current := release.Spec.Boundaries
		for _, part := range splitScopePath(node.BoundaryPath) {
			boundary, ok := current.Get(part)
			if !ok {
				break
			}
			components := types.BoundaryComponents(boundary)
			if entry, ok := components.Get(nodeID); ok {
				for name, feat := range entry.Features.All() {
					if feat.Uses.Len() == 0 {
						continue
					}
					site, keys := entries[nodeID].Site()
					attachment := constraint.AttachmentSiteInBlock(
						constraint.AuthoringDocumentRelease, site, name, releasePath)
					attachment.ComponentKeys = keys
					errs = append(errs, resolver.ValidateFeatureUsesAgainstSchema(
						entity, resolver.FeatureUsesSite{
							File:       releasePath,
							Locate:     attachment.SocketLocator,
							Component:  nodeID,
							Attachment: name,
						},
						feat.Uses, feat.Type, catalog)...)
				}
			}
			current = boundary.Boundaries
		}
	}
	return errs
}

// guardModuleDocuments asks the resolver's rules over an authoring document's own
// declarations of every sub-module's document the composition brought in, in the
// order the composition recorded them.
//
// It is the module half of the pass the resolver asks of the Aggregate before
// flattening: that pass reads the documents a build is handed, and a module's
// document is loaded only by flattening. Which rules it asks, and why those, is
// [resolver.RunModuleGuards]'s to state; this decides only that every module the
// composition holds is asked, and with the parameter context it resolved under.
//
// A module the composition imports more than once is asked once per import,
// because each import may resolve its document under different parameters. A
// declaration both imports share is then one fault reported twice in identical
// words, so a finding identical to one already reported is dropped: the document
// and the key it names are the same, and so is the edit that fixes it.
func guardModuleDocuments(
	cancelled func() error,
	flat *flattenResult,
	catalog resolver.CatalogLookup,
) ([]*resolver.ResolverError, error) {
	var errs []*resolver.ResolverError
	reported := map[string]bool{}
	for _, module := range flat.ModuleDocuments {
		found, err := resolver.RunModuleGuards(cancelled, module.document, module.sourceFile, catalog, module.parameters)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			if reported[f.Error()] {
				continue
			}
			reported[f.Error()] = true
			errs = append(errs, f)
		}
	}
	return errs, nil
}

// releaseEntityName renders the Release document a finding in this package
// names. It is one home for the rendering because every rule here reporting
// against the Release needs the same string, and hand-written copies of it
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
//
// An attachment lowered from one of the component's connections is left out: it
// was written on the connection, not in the component's `features` block, so it
// is not part of the block this reassembles and a slot the component declares
// for its own block says nothing about it.
func authoredAttachments(attachments []FeatureAttachment) types.OrderedMap[types.FeatureInstance] {
	out := types.OrderedMap[types.FeatureInstance]{}
	for _, a := range attachments {
		if a.Connection != nil {
			continue
		}
		out.Set(a.InstanceName, a.Feature)
	}
	return out
}

// guardUnboundReleaseEdgeConfig halts when a Release per-edge `uses` entry
// carries configuration — `infra`, attachments under `features`, or both — and
// names a consuming component the composition does not hold. It runs inside the
// flattening, after the Release edge pass, where the edge set becomes final: an
// entry naming a target has drawn its edge by then, and an entry omitting one has
// had its chance to land on an edge the Aggregate or a Feature declared.
// Configuration still holding no edge at that point is authoring that does
// nothing, and accepting it silently would put the operator's infrastructure, or
// the attachment the operator meant the connection to carry, out of the output
// while the build succeeds.
//
// Three conditions are deliberately NOT this guard's, and each is left to the
// rule that owns it, so a halt added here cannot disable one of them:
//
//   - An entry on a socket the consuming component's usesSchema DOES declare,
//     where nothing drew the edge, belongs to the declared rule over the
//     Release's authored per-edge surface, which is invoked once per authored
//     entry and therefore reports every offending entry with the authored locator
//     of each. That rule is asked after the composition is built.
//   - A socket the consuming component's usesSchema does not declare belongs to
//     the undeclared-socket rule, which produces the better diagnostic — it can
//     list the sockets that do exist.
//   - A target naming no component belongs to the rule over the finalised graph
//     that reports exactly that. An entry naming a target draws its edge wherever
//     the Release edge pass reached it, so it carries configuration that did land
//     somewhere and the fault is the target rather than the configuration.
//   - A consuming component pruned by a false `active` toggle takes its whole
//     edge set with it by design. The entry is inert because the operator turned
//     the component off, not because the entry is wrong.
//
// A component absent from the composition is reported by more than one rule, and
// this one reaches it first: the rule over what a boundary names reports every
// entry addressing a component nothing answers to, and its subject set covers
// this one. This guard is asked inside the flattening while that rule is asked
// after it, so which of the two a document meets is settled by position rather
// than by either rule's own reading. That is a property of where the two sit and
// not a claim that nothing else reports the class.
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
) error {
	if release == nil {
		return nil
	}
	for _, ref := range resolver.ReleaseEdgeEntries(release) {
		configures := releaseEdgeEntryConfigures(ref.Entry)
		if configures == "" {
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
		if _, isNode := flat.Nodes.Get(ref.NodeID); !isNode {
			return fmt.Errorf(
				"%w: release edge %s/%s/%s sets %s on %q, which is not a component in the topology",
				ErrUnboundReleaseEdgeConfig(), ref.NodeID, ref.Socket, ref.Alias, configures, ref.NodeID)
		}
	}
	return nil
}

// releaseEdgeEntryConfigures names what a Release per-edge entry configures on
// the connection it addresses — its per-edge `infra`, the attachments it puts
// under `features`, or both — and is empty for an entry carrying neither.
func releaseEdgeEntryConfigures(entry types.ReleaseUsesEntry) string {
	return configurationNamed(entry.Infra.Len() > 0, entry.Features.Len() > 0)
}

// configurationNamed spells which of a Release connection entry's two
// configuration surfaces carry something, in the order the Release schema
// declares the two keys, and is empty when neither does.
//
// It is the one reading of "this entry carries configuration" for the two places
// that ask whether such an entry reached a connection — the guard inside the
// flattening, which holds the entry, and the rule asked over the finished
// composition, which reads the same two surfaces as facts — so the two cannot
// come to disagree about which entries they are judging. The answer is spelled
// the way a diagnostic names it, because an entry that sets only attachments and a
// message saying it sets infra would send the operator looking for a key they
// never wrote.
func configurationNamed(infra, attachments bool) string {
	switch {
	case infra && attachments:
		return "infra and features"
	case infra:
		return "infra"
	case attachments:
		return "features"
	}
	return ""
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
//
// A finding names the Release's file and is positioned at the entry, through the
// same locator a rule over the Release's per-edge entries is positioned at. An
// entry under a component keyed by the identifier the composition gave it stops
// at the components block, so the finding names the component in its detail.
func guardSubAggregateReleaseEdges(
	flat *flattenResult,
	release *types.Release,
	releasePath string,
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
				Field:  ref.Instance(releasePath).Locator(),
				File:   releasePath,
				Message: fmt.Sprintf("component %q alias %q: socket %q not defined in component type %q usesSchema (valid: %v)",
					ref.NodeID, ref.Alias, ref.Socket, node.ComponentType, catComp.Spec.UsesSchema.Keys()),
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
	uses         types.OrderedMap[types.FeatureUsesSocketValue]
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
// is refused by the rule over the finalised graph like any other connection.
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
