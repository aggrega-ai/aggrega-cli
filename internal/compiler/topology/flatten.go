// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"fmt"
	"strconv"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
	// and attachments never materialize); the set is retained so the rules over
	// the finalised graph can tell a survivor's edge into a deliberately pruned
	// target apart from an edge into a target that never existed.
	//
	// Only ever stores true, so presence is membership. Ordered rather than a
	// plain map because the set is carried onward to a consumer downstream of
	// flattening: an unordered set placed in the pipeline's own products has no
	// iteration order, which the byte-identical-output contract forbids. The
	// order is the order in which flattening encountered each pruned node, which
	// is author order at every level.
	Pruned types.OrderedMap[bool]
	// Modules is every sub-module the composition brought in, in author order
	// with an importing module before the modules nested inside it.
	//
	// It records the module itself rather than only the nodes it contributed,
	// because a rule about what was done with a module has to be askable of a
	// module that contributed nothing — a module absent from the record is a
	// module such a rule is never asked about, which is silence a caller cannot
	// tell from approval.
	//
	// A module a false `active` toggle removed is absent: nothing it declares was
	// loaded, so no rule over the composed build has anything to say about it.
	Modules []ModuleDeclaration
	// DeclaringSubsystems is the subsystem every surviving component was declared
	// under, keyed by the identity that component takes in the flattened set, in
	// the order the walk met each declaration.
	//
	// It is a list per identity rather than one entry, because that is the whole
	// of what it exists to carry. Nodes holds one component per identity by
	// construction, so a second declaration of one identity replaces the first
	// there and the pair is unobservable afterwards; here both are kept, which is
	// what lets a rule over the flattened composition be asked whether one
	// identity was declared twice.
	//
	// Ordered rather than a plain map because it is carried onward on the
	// composition this stage produces, where an unordered set has no iteration
	// order and the byte-identical-output contract forbids one. The order is the
	// order flattening met each component, which is author order at every level.
	//
	// A component a false `active` toggle removed is absent, for the reason it is
	// absent from Nodes: it was never composed, so nothing else can collide with
	// it.
	DeclaringSubsystems types.OrderedMap[[]string]
	// ModuleDocuments is the document of every sub-module the composition brought
	// in, in the order Modules lists the modules, with the parameter context each
	// was resolved under.
	//
	// It is carried because a module's document exists nowhere else once flattening
	// has merged its components into the composition: the rules over what an
	// authoring document declares are asked of the Aggregate before flattening, and
	// a module's document is loaded only here. A rule asked of the module's own
	// declarations reads it from this record rather than loading the module again.
	ModuleDocuments []loadedModule
}

// loadedModule is one sub-module's own document, as flattening loaded it.
type loadedModule struct {
	// path is the module's dotted prefix, as [ModuleDeclaration.Path] carries it:
	// the prefix every component the module contributes carries.
	path string
	// document is the module's Aggregate, normalized as flattening walked it.
	document types.NormalizedAggregate
	// parameters is the parameter context the module resolved under.
	parameters map[string]any
	// sourceFile is the file the loader read the module's document from, as
	// [TopologyNode.SourceFile] carries it for the module's components, and empty
	// where the loader named none.
	sourceFile string
}

// ModuleDeclaration is one sub-module the composition brought in: the dotted
// prefix its components carry in the flattened set, and the document that
// declared it.
//
// The declaring document is the IMPORTING one — the root Aggregate for a module
// the Aggregate imports directly, the importing module's own document for one
// nested inside it — because that is where the `aggregates` entry a diagnostic
// would point an author at actually sits. The module's own document declares its
// contents and never its import.
type ModuleDeclaration struct {
	// Path is the module's dotted prefix, which is also the prefix every node it
	// contributes carries.
	Path string
	// DeclaredIn is the Aggregate file holding the `aggregates` entry that
	// imported the module. Empty when the caller supplied no path for that
	// document, exactly as [TopologyNode.SourceFile] is.
	DeclaredIn string
	// Exports are the aliases the module's own document lists under
	// `exportedComponents`, in the order it lists them: the names a document
	// importing the module may reach its components by. It is what the module
	// declared, whether or not the component an alias names survived its
	// activation toggle.
	Exports []string
}

// evalActive interprets a component's or module's `active` toggle.
//
// A nil toggle is one the author never wrote, and the entity is present. A
// toggle the author did write must resolve to a boolean: it reaches flatten
// already resolved to a literal — a boolean written directly, or a parameter
// template the resolver rendered before unmarshal — and a literal that does not
// parse as a boolean is a Loud Guardrail naming the node and the offending
// value.
//
// An empty literal is one such value, and separating it from an omitted toggle
// is why the toggle arrives as a pointer. A toggle reading a parameter resolves
// to nothing while that parameter is unfulfilled, so treating empty as "present"
// would deploy the very entity the author gated behind that parameter.
func evalActive(active *types.ActiveFlag, nodeID string) (bool, error) {
	if active == nil {
		return true, nil
	}
	s := strings.TrimSpace(string(*active))
	b, err := strconv.ParseBool(s)
	if err != nil {
		hint := ""
		if s == "" {
			// Stated as what a parameter-driven toggle does rather than as a
			// claim about this one, so the sentence holds for a toggle authored
			// empty outright as well as for one whose parameter is unfulfilled.
			// The two are the same literal here and cannot be told apart.
			hint = "a toggle reading a parameter resolves to nothing until that parameter is fulfilled"
		}
		values := []string{
			fmt.Sprintf("node %q", nodeID),
			fmt.Sprintf("active resolved to %q", string(*active)),
		}
		return false, cerrors.Wrap(ErrInvalidActive(), cerrors.DeveloperError, cerrors.SourceLocation{Instance: nodeID},
			cerrors.ComposeMessage(ErrInvalidActive(), values, hint))
	}
	return b, nil
}

// flattenAggregate recursively walks an Aggregate tree, flattening all
// components (including sub-aggregate components) into a single flat
// node map with dot-notation IDs. The dot is reserved as the
// compiler-internal path separator; user-authored instance names cannot
// contain dots, so synthesized dotted IDs are unambiguous. Author order
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
//   - The `depth` counter is bounded by maxSubAggregateDepth;
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
		return nil, cerrors.Wrap(ErrMaxDepth(), cerrors.DeveloperError, cerrors.SourceLocation{Instance: prefix},
			cerrors.ComposeMessage(ErrMaxDepth(), []string{
				fmt.Sprintf("depth %d", depth),
				fmt.Sprintf("prefix %q", prefix),
			}, ""))
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
				return nil, cerrors.Wrap(ErrSubAggregateLoad(), cerrors.DeveloperError, cerrors.SourceLocation{File: sourceFile, Instance: childPrefix},
					cerrors.ComposeMessage(ErrSubAggregateLoad(), []string{
						fmt.Sprintf("circular sub-aggregate reference %q", subAgg.Type),
						fmt.Sprintf("at %q", childPrefix),
					}, ""))
			}

			childAgg, childSourceFile, childParams, err := loader(subAgg.Type, plainParams(subAgg.Parameters))
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: sourceFile, Instance: childPrefix}, fmt.Sprintf("%v", ErrSubAggregateLoad()))
			}

			// Recorded once the module has loaded and before its own tree is
			// walked, so the order is the importing module before the modules
			// nested inside it — the same outer-before-inner order the Release's
			// routed-module report uses, so the two read against each other
			// without either being re-sorted.
			declaration := ModuleDeclaration{
				Path:       childPrefix,
				DeclaredIn: sourceFile,
			}
			if childAgg != nil {
				declaration.Exports = childAgg.Spec.ExportedComponents.Keys()
			}
			result.Modules = append(result.Modules, declaration)

			childVisited := copyVisited(visited)
			childVisited[subAgg.Type] = true

			// The loader's contract is a parsed *types.Aggregate; flatten's is a
			// normalized one. Normalizing here is where the two meet, so the
			// recursion cannot be entered without the guarantee. A loader that
			// already normalized — every production one loads through the parse
			// core, which does — hands back the identical value.
			childDocument := types.Normalize(childAgg)
			result.ModuleDocuments = append(result.ModuleDocuments, loadedModule{
				path:       childPrefix,
				document:   childDocument,
				parameters: childParams,
				sourceFile: childSourceFile,
			})
			childResult, err := flattenAggregate(childDocument, childPrefix, "", childSourceFile, loader, childVisited, depth+1, childParams)
			if err != nil {
				return nil, err
			}

			// Merge child results — preserve child's author-order during merge.
			for id, node := range childResult.Nodes.All() {
				if existing, exists := result.Nodes.Get(id); exists {
					return nil, cerrors.Wrap(ErrSubAggregateNameCollision(), cerrors.DeveloperError, cerrors.SourceLocation{File: sourceFile, Instance: childPrefix},
						cerrors.ComposeMessage(ErrSubAggregateNameCollision(), []string{
							fmt.Sprintf("id %q", id),
							fmt.Sprintf("path %q", childPrefix),
							fmt.Sprintf("collides with existing entry from %q", existing.SourceAggregate),
						}, ""))
				}
				result.Nodes.Set(id, node)
			}
			// The module's own record travels with its nodes, so a pair of
			// subsystems inside a module is reported by the same rule that reports
			// a pair inside the importing document. The identities are already
			// distinct by the merge above, so this copies rather than folds.
			for id, declaring := range childResult.DeclaringSubsystems.All() {
				result.DeclaringSubsystems.Set(id, declaring)
			}
			for id, feats := range childResult.FeatureAttachments {
				result.FeatureAttachments[id] = append(result.FeatureAttachments[id], feats...)
			}
			result.Modules = append(result.Modules, childResult.Modules...)
			result.ModuleDocuments = append(result.ModuleDocuments, childResult.ModuleDocuments...)
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
			// The aliases the modules nested inside the child export travel up with
			// the child's nodes. A module's own document reaches a module it imports
			// through that module's aliases, and the connection it draws is resolved
			// against the table the whole composition ends with — so a table holding
			// only the aliases of the modules one level down would leave such a
			// connection naming nothing. Every key is already qualified with the
			// prefix of the module that exports it, so the tables of different levels
			// cannot collide. Which document may reach which alias is not decided by
			// the table: a rule over the finished composition reads each connection
			// against the export list of the module it passes into.
			for alias, resolved := range childResult.ExportedComponents.All() {
				result.ExportedComponents.Set(alias, resolved)
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
// The walk raises nothing when two subsystems declare a component under one
// name. It records both declarations on the result's DeclaringSubsystems and
// lets the node map keep the last of them; the rule declared over the flattened
// composition reads that record and reports the pair.
func flattenSubsystems(result *flattenResult, prefix, rootPath, sourceFile string, na types.NormalizedAggregate, params map[string]any) error {
	// The Aggregate-side annotation cascade of this one document, read per node
	// below rather than re-derived alongside the feature cascade: the resolver is
	// its one home. It is keyed by the path inside the document, so the lookup
	// drops the rootPath the walk's own paths hang beneath.
	aggregateAnnotations := resolver.WireAggregateAnnotations(na)
	annotationsOf := func(subsystemPath, name string) types.OrderedMap[string] {
		if rootPath != "" {
			subsystemPath = strings.TrimPrefix(subsystemPath, rootPath+".")
		}
		return aggregateAnnotations[resolver.AggregateAnnotationComponentPath(subsystemPath, name)]
	}
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
				// Every declaration is recorded and the node map keeps the last of
				// them. A second declaration of one identity is a fault, and the rule
				// over the flattened composition is what reports it — halting here
				// instead would report it from inside the production of the very
				// facts that rule reads.
				declaring, _ := result.DeclaringSubsystems.Get(nodeID)
				result.DeclaringSubsystems.Set(nodeID, append(declaring, subsystemPath))
				result.Nodes.Set(nodeID, &TopologyNode{
					InstanceID:           nodeID,
					ComponentType:        comp.Type,
					Values:               comp.Values,
					SelfInject:           comp.Inject,
					Uses:                 comp.Uses,
					ScopeFeatureUses:     scopeFeatures,
					ResolvedParameters:   params,
					AggregateAnnotations: annotationsOf(subsystemPath, name),
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
//
// The attachments a component's connections carry are lowered here too, after
// the component's own block, so they are attachments of the CONSUMING component
// from this point on — the Manifest mapping, the FeatureRecipe, the conformance
// gate and the sensitivity marking treat them like any other. Each takes the
// name the alias and the slot compose, and carries its connection as data. The
// set and its order are read from [types.ComponentInstance.ConnectionAttachments]
// — the component's sockets, then each socket's aliases, then each connection's
// slots, in author order, which is what keeps the attachment order byte-stable —
// and the lock-time walk reads the same enumeration, so every attachment lowered
// here names a Feature the lockfile pins. Because this runs for every component
// flattening reaches, a component a sub-module contributes is lowered exactly as
// one the Aggregate declares.
func collectAttachments(result *flattenResult, nodeID string, comp types.ComponentInstance, tier int) {
	for fname, feat := range comp.Features.All() {
		result.FeatureAttachments[nodeID] = append(result.FeatureAttachments[nodeID], FeatureAttachment{
			InstanceName: fname,
			Feature:      feat,
			SourceTier:   tier,
		})
	}
	for conn, att := range comp.ConnectionAttachments() {
		result.FeatureAttachments[nodeID] = append(result.FeatureAttachments[nodeID], FeatureAttachment{
			InstanceName: types.JoinConnectionAttachmentName(conn.Alias, conn.Attachment),
			Feature: types.FeatureInstance{
				Type:        att.Type,
				Description: att.Description,
				Values:      att.Values,
			},
			SourceTier: tier,
			Connection: &conn,
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
//     rule over the finalised graph halts on the unresolved reference;
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
			return "", cerrors.Wrap(ErrReexportCycle(), cerrors.DeveloperError, cerrors.SourceLocation{Instance: candidate},
				cerrors.ComposeMessage(ErrReexportCycle(), []string{
					fmt.Sprintf("alias chain %s", strings.Join(chain, " -> ")),
				}, ""))
		}
		visited[cur] = true
		cur = next
	}
}

// qualifiedID composes a node's flattened id from the prefix of the sub-module
// contributing it and its own name. It delegates rather than re-spelling the
// join, because the Release addresses a routed component by the same id: were the
// two spellings ever to diverge, a Release entry aimed at a sub-module's component
// would silently reach no node.
func qualifiedID(prefix, name string) string {
	return types.JoinQualifiedID(prefix, name)
}

func copyVisited(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
