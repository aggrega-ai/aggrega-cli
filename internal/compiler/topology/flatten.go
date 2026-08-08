// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"
	"strconv"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const maxSubAggregateDepth = 10

// flattenResult is the intermediate output of aggregate flattening.
//
// Nodes uses OrderedMap so the Aggregate's author-declared component order
// (and the order of components in any sub-aggregate it imports) survives
// subsystem flattening. Downstream consumers (IR construction → IR JSON
// emission) rely on this author-order propagation per the deterministic-
// ordering contract.
type flattenResult struct {
	Nodes              types.OrderedMap[*TopologyNode]
	FeatureAttachments map[string][]FeatureAttachment
	// ExportedComponents maps the alias an Aggregate publishes to the
	// dot-notation ID that alias resolves to after flattening. Ordered because
	// the table is read downstream to rewrite edge targets, which is in the
	// byte-identical-output path; the order is the order the authoring surface
	// declares the aliases in.
	ExportedComponents types.OrderedMap[string]
	// Pruned is the set of flattened node IDs a false `active` toggle removed
	// from the topology. Pruned nodes are absent from Nodes (so their own edges
	// and attachments never materialize); the set is retained so the
	// dangling-target sweep can tell a survivor's edge into a deliberately
	// pruned target apart from an edge into a target that never existed.
	//
	// Only ever stores true, so presence is membership. Ordered rather than a
	// plain map because the set is carried onward to a consumer downstream of
	// flattening: an unordered set placed in the pipeline's own products has no
	// iteration order, which the byte-identical-output contract forbids. The
	// order is the order in which flattening encountered each pruned node, which
	// is author order at every level.
	Pruned types.OrderedMap[bool]
}

// evalActive interprets a component's or module's resolved `active` toggle.
// The toggle reaches flatten already resolved to a literal — a boolean the
// author wrote directly, or a parameter template the resolver rendered before
// unmarshal. An empty literal means the toggle was omitted and the entity is
// present; otherwise the literal must parse as a boolean. A non-empty,
// non-boolean literal (including the "<no value>" a template over a missing key
// produces) is a Loud Guardrail naming the node and the offending value.
func evalActive(active types.ActiveFlag, nodeID string) (bool, error) {
	s := strings.TrimSpace(string(active))
	if s == "" {
		return true, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, cerrors.Wrap(ErrInvalidActive(), cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q active resolved to %q, which is not a boolean", nodeID, string(active)))
	}
	return b, nil
}

// flattenAggregate recursively walks an Aggregate tree, flattening all
// components (including sub-aggregate components) into a single flat
// node map with dot-notation IDs. The dot is reserved as the
// compiler-internal path separator; user-authored instance names cannot
// contain dots, so synthesised dotted IDs are unambiguous. Author order
// is preserved at every level by iterating OrderedMap-backed
// collections — root-topology blocks (`components`, `subsystems`,
// `aggregates`) are YAML maps keyed by user-authored instance names so
// the parser preserves declaration order.
//
// Walk order — two passes over the current Aggregate level, each in
// author order:
//
//  1. Subsystems (agg.Spec.Subsystems). flattenSubsystems walks each
//     subsystem's components and nested sub-subsystems, accumulating
//     dotted subsystemPath segments. A component authored at the top-level
//     `.spec.components` surface is reached here, inside the synthesized
//     default subsystem: the parameter is a [types.NormalizedAggregate],
//     obtainable only from [types.Normalize], which folds that surface into
//     the default and empties the root map — and the authoring shape
//     declaring both a root components map and subsystems is rejected at the
//     document-envelope gate before any Aggregate is parsed. The type and
//     that gate together are what make a subsystems-only pass complete, so
//     there is no root-components pass; reintroducing one would tell a reader
//     the shape is reachable when it is not.
//  2. Sub-aggregate imports (agg.Spec.Aggregates). For each, the loader
//     resolves the type URI to a child Aggregate; the recursive call
//     uses the imported Aggregate's name as the new prefix and merges
//     the child's flattened nodes / attachments / exported components
//     back into the parent's result.
//
// Two cycle / depth disciplines protect the recursion:
//
//   - The `visited` set is keyed by sub-aggregate type URI, copied per
//     branch (copyVisited) so siblings don't poison each other; a
//     repeat type URI on the same branch produces ErrSubAggregateLoad
//     wrapping a "circular" message.
//   - The `depth` counter is bounded by maxSubAggregateDepth (10);
//     exceeding it produces ErrMaxDepth. The depth bound subsumes cycle
//     detection when the visited-set check would have otherwise allowed
//     infinite recursion via type-renaming tricks.
//
// Note on the runtime-graph cycle distinction: flattenAggregate's
// cycle / depth checks operate on the sub-aggregate INCLUSION graph
// (which authoring file embeds which). The runtime `uses` graph
// supports cycles without restriction; it is built later in
// normalizeUses. The two graphs are unrelated and the cycle rules
// differ deliberately — runtime back-edges are a legitimate authoring
// shape (multi-agent loops); inclusion cycles are authoring bugs.
// plainParams converts a sub-aggregate's authored parameter fulfillment
// (OrderedMap[any]) into the plain map[string]any the resolver parameter
// machinery consumes. Nil/empty in → nil out.
func plainParams(m types.OrderedMap[any]) map[string]any {
	if m.Len() == 0 {
		return nil
	}
	out := make(map[string]any, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}

func flattenAggregate(
	na types.NormalizedAggregate,
	prefix string,
	subsystemPath string,
	sourceFile string,
	loader SubAggregateLoader,
	visited map[string]bool,
	depth int,
	params map[string]any,
) (*flattenResult, error) {
	if depth > maxSubAggregateDepth {
		return nil, cerrors.Wrap(ErrMaxDepth(), cerrors.DeveloperError, cerrors.SourceLocation{File: prefix}, fmt.Sprintf("depth %d at prefix %q", depth, prefix))
	}

	result := &flattenResult{
		Nodes:              types.OrderedMap[*TopologyNode]{},
		FeatureAttachments: make(map[string][]FeatureAttachment),
		ExportedComponents: types.OrderedMap[string]{},
		Pruned:             types.OrderedMap[bool]{},
	}

	agg := na.Aggregate()

	// Walk subsystems in author order (recursive)
	if err := flattenSubsystems(result, prefix, subsystemPath, sourceFile, na, params); err != nil {
		return nil, err
	}

	// Walk sub-aggregates in author order
	if loader != nil {
		for aggName, subAgg := range agg.Spec.Aggregates.All() {
			childPrefix := qualifiedID(prefix, aggName)

			keep, err := evalActive(subAgg.Active, childPrefix)
			if err != nil {
				return nil, err
			}
			if !keep {
				// Pruned module: skip loading and recursing the whole module, so
				// none of its components, edges, attachments, or exports
				// materialize.
				continue
			}

			if visited[subAgg.Type] {
				return nil, cerrors.Wrap(ErrSubAggregateLoad(), cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("circular sub-aggregate reference %q at %q", subAgg.Type, childPrefix))
			}

			childAgg, childSourceFile, childParams, err := loader(subAgg.Type, plainParams(subAgg.Parameters))
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("%v", ErrSubAggregateLoad()))
			}

			childVisited := copyVisited(visited)
			childVisited[subAgg.Type] = true

			// The loader's contract is a parsed *types.Aggregate; flatten's is a
			// normalized one. Normalizing here is where the two meet, so the
			// recursion cannot be entered without the guarantee. A loader that
			// already normalized — every production one loads through the parse
			// core, which does — hands back the identical value.
			childResult, err := flattenAggregate(types.Normalize(childAgg), childPrefix, "", childSourceFile, loader, childVisited, depth+1, childParams)
			if err != nil {
				return nil, err
			}

			// Merge child results — preserve child's author-order during merge.
			for id, node := range childResult.Nodes.All() {
				if existing, exists := result.Nodes.Get(id); exists {
					return nil, cerrors.Wrap(ErrSubAggregateNameCollision(), cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("id %q (path %q) collides with existing entry from %q", id, childPrefix, existing.SourceAggregate))
				}
				result.Nodes.Set(id, node)
			}
			for id, feats := range childResult.FeatureAttachments {
				result.FeatureAttachments[id] = append(result.FeatureAttachments[id], feats...)
			}
			// Walk the child's own encounter order rather than a plain-map range,
			// so the merged set's order stays derived from authoring rather than
			// from Go's map iteration.
			for id := range childResult.Pruned.All() {
				result.Pruned.Set(id, true)
			}

			// Rewire exported_components: alias scoped to sub-aggregate prefix.
			// A re-exported alias (the child re-publishing a grandchild's export
			// under a new name) names the child's alias table rather than a real
			// node; resolveExportTarget follows that indirection to the concrete
			// internal node ID so every stored export value is already-resolved —
			// the contract the edge-target rewrite relies on.
			if childAgg != nil {
				for exportAlias, localRef := range childAgg.Spec.ExportedComponents.All() {
					candidate := qualifiedID(childPrefix, localRef)
					resolved, err := resolveExportTarget(candidate, result.Nodes, childResult.ExportedComponents)
					if err != nil {
						return nil, err
					}
					result.ExportedComponents.Set(qualifiedID(childPrefix, exportAlias), resolved)
				}
			}
		}
	}

	return result, nil
}

// flattenSubsystems walks the Aggregate's subsystem tree, appending each
// component to the flattenResult's node map and accumulating dotted
// subsystemPath segments as the descent proceeds. The subsystemPath ends up on
// each TopologyNode and feeds the IR's scope-feature cascade
// (collectSubsystemFeatures in internal/compiler/ir/features.go) — the path
// string is what tells the IR which subsystem-level features cascade to the node
// and in what outer-to-inner order.
//
// Subsystems are not gated by the sub-aggregate cycle / depth bounds: they are
// part of the authoring root of the same Aggregate, so the only bound is the YAML
// schema's structural validity. Pathologically deep subsystem nesting in a single
// Aggregate would still reach the recursion stack limit, but no architectural
// anchor enshrines a depth bound for subsystem nesting today.
//
// rootPath is the dotted path the tree's top level hangs beneath — empty for an
// Aggregate's own subsystems.
//
// Returns ErrSubAggregateNameCollision (wrapped) if a Set on the flat node map
// would overwrite an existing entry. The collision is impossible by current schema
// (root-level instance names cannot contain dots) but the guard defends against
// future schema loosening or validation bypass.
func flattenSubsystems(result *flattenResult, prefix, rootPath, sourceFile string, na types.NormalizedAggregate, params map[string]any) error {
	return types.WalkInstanceTree(na.Aggregate().Spec.Subsystems, types.SubsystemTree(), rootPath, []scopeFeatureUses(nil),
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, []scopeFeatureUses]{
			Path: types.JoinSubsystemPath,
			// Extend the inherited subsystem-feature cascade with this subsystem's own
			// subsystem-wide features (author order), so a component at this level — and
			// every nested level — carries the features of all subsystems on its path.
			// Capturing it against the subsystem tree actually being flattened is what
			// makes it correct for a sub-aggregate's own subsystems.
			Enter: func(sub types.Subsystem, _, _ string, ancestorScopeFeatures []scopeFeatureUses) []scopeFeatureUses {
				// Build a fresh slice (do not append into the ancestor's backing array, or
				// sibling subsystems would see each other's features through a shared array).
				scopeFeatures := make([]scopeFeatureUses, len(ancestorScopeFeatures), len(ancestorScopeFeatures)+sub.Features.Len())
				copy(scopeFeatures, ancestorScopeFeatures)
				for fname, feat := range sub.Features.All() {
					scopeFeatures = append(scopeFeatures, scopeFeatureUses{
						instanceName: fname,
						featureType:  feat.Type,
						uses:         feat.Uses,
					})
				}
				return scopeFeatures
			},
			Visit: func(comp types.ComponentInstance, name, subsystemPath string, synthesized bool, scopeFeatures []scopeFeatureUses) error {
				nodeID := qualifiedID(prefix, name)
				keep, err := evalActive(comp.Active, nodeID)
				if err != nil {
					return err
				}
				if !keep {
					result.Pruned.Set(nodeID, true)
					return nil
				}
				if existing, exists := result.Nodes.Get(nodeID); exists {
					return cerrors.Wrap(ErrSubAggregateNameCollision(), cerrors.DeveloperError, cerrors.SourceLocation{File: prefix}, fmt.Sprintf("id %q (path %q) collides with existing entry from %q", nodeID, prefix, existing.SourceAggregate))
				}
				result.Nodes.Set(nodeID, &TopologyNode{
					InstanceID:           nodeID,
					ComponentType:        comp.Type,
					Values:               comp.Values,
					SelfInject:           comp.Inject,
					Uses:                 comp.Uses,
					ScopeFeatureUses:     scopeFeatures,
					ResolvedParameters:   params,
					SubsystemPath:        subsystemPath,
					SynthesizedSubsystem: synthesized,
					SourceAggregate:      prefix,
					SourceFile:           sourceFile,
				})
				collectAttachments(result, nodeID, comp, 0)
				return nil
			},
		})
}

// collectAttachments records a node's attachments into the per-node slice
// on flattenResult, preserving author order from the Aggregate's component
// instance. The IR builder reads FeatureAttachments[nodeID] in this order
// and feeds it through the per-node attachment-cascade normalisation. Author order is the only ordering applied here; later
// merge passes inside IR may dedupe by instance name with "last wins"
// semantics, but topology surfaces the raw author-attached entries.
//
// The `tier` parameter is currently always 0 from both call sites; the
// SourceTier field on the resulting attachment is therefore always 0
// today. The field exists for future use; SourceTier carries no
// meaningful tier information in the current implementation.
func collectAttachments(result *flattenResult, nodeID string, comp types.ComponentInstance, tier int) {
	for fname, feat := range comp.Features.All() {
		result.FeatureAttachments[nodeID] = append(result.FeatureAttachments[nodeID], FeatureAttachment{
			InstanceName: fname,
			Feature:      feat,
			SourceTier:   tier,
		})
	}
}

// resolveExportTarget walks a re-exported alias to the concrete node it
// ultimately names. When a parent aggregate re-publishes a child module's
// exported alias under a new name, the freshly-qualified export value points
// at the child's alias table rather than a real node; this walk follows that
// indirection until it lands on a node, so every value stored in the export
// table is an already-resolved internal node ID — the contract the edge-target
// rewrite relies on.
//
// candidate is the freshly-qualified export value. nodes is the flattened node
// map built so far (the child's nodes are already merged in at the call site).
// childExports is the child's already-resolved alias table. The walk returns:
//
//   - the resolved node ID when candidate (or a followed alias) names a node;
//   - candidate unchanged when it names neither a node nor a child alias, so the
//     downstream dangling-target guardrail halts on the unresolved reference;
//   - ErrReexportCycle when the alias chain revisits an alias before reaching a
//     node, naming the walked chain.
//
// Export values are strictly deeper-qualified than the frame prefix, so a
// follow-chain increases dotted-ID depth and terminates on well-formed input;
// the visited-set is a backstop against a malformed alias table rather than a
// shape reachable through the acyclic inclusion tree.
func resolveExportTarget(candidate string, nodes types.OrderedMap[*TopologyNode], childExports types.OrderedMap[string]) (string, error) {
	visited := make(map[string]bool)
	var chain []string
	cur := candidate
	for {
		if _, isNode := nodes.Get(cur); isNode {
			return cur, nil
		}
		next, isAlias := childExports.Get(cur)
		if !isAlias {
			return cur, nil
		}
		chain = append(chain, cur)
		if visited[cur] {
			return "", cerrors.Wrap(ErrReexportCycle(), cerrors.DeveloperError, cerrors.SourceLocation{File: candidate}, fmt.Sprintf("re-exported component alias chain loops: %s", strings.Join(chain, " -> ")))
		}
		visited[cur] = true
		cur = next
	}
}

func qualifiedID(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func copyVisited(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
