// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// providesScope is the reference set a base Recipe's provides template is checked
// against, and both surfaces over that authored text read it: the rule declared
// over the entry as written, and the evaluation that renders it. One statement of
// what the surface admits, consulted by both, is what keeps them from agreeing
// only for as long as one of them keeps running first.
//
// The universe is ProvidesContext — the same type the provides pass evaluates
// against — so the roots the scope admits are exactly the surface that pass
// exposes, and .context is gated one level down by ProvidesContextBlock's own
// fields rather than by a second list beside it. A field added to that block
// therefore becomes reachable from a provides template with no edit here.
//
// One root the type defines is excluded, and the reason is stage ordering rather
// than taste: attachment_name. No attachment is being resolved during a
// base Recipe's provides pass, so the reference has no name to resolve to and
// would reach the emitted artifact as a value nobody authored. The exclusion is
// deliberately kept a narrowing — the type carries the field because the
// attachment FeatureRecipe path populates it, and this surface refuses it
// because its own pass cannot. Removing it from the exclude set would reopen
// that render rather than simplify anything.
//
// .locals is admitted, and admitting it is also an ordering property: a node's
// locals are resolved in the pass preamble, ahead of that node's provides, so a
// provides template reading .locals receives resolved values. That holds on both
// paths that populate the pillar — the base Recipe pass this scope governs, and the
// attachment FeatureRecipe path, which resolves its own locals before its provides
// and is not read against this scope at all.
//
// Admitting the root admits an OPEN keyed map, so the root check alone cannot refuse
// a key the block never declared. The key-level refusal is a separate check run once
// the block is resolved (checkProvidesLocalsReferences) — the root is admitted here,
// and the key is checked there.
var providesScope = templateScope{
	data:    ProvidesContext{},
	exclude: []string{"attachment_name"},
}

// EvaluateAllProvides executes Pass 1: per node, resolve that node's locals and
// then its provides. Both are node-local computations — neither reads another
// node's data — so the walk is order-independent across nodes. Within a node the
// order is fixed, locals first, and that is what lets a provides template read
// .locals and receive resolved values rather than authored template text.
//
// A locals template receives self-contained node-local data
// (.context.instance_id, .values, .infra) plus the entries of its own block
// resolved so far, so a local may read an earlier sibling; a provides template
// receives the same node-local data plus the node's whole resolved .locals. A
// provides template naming a local the block does not declare halts rather than
// rendering empty, and one naming a root this surface does not admit halts here
// as well as at the rule declared over the same authored text — so the refusal
// holds whichever of the two a build reaches first. Returns
// the resolved provides and the resolved locals per node — the locals travel out of
// this pass rather than being recomputed downstream, so the render passes and the
// assertion pass read one evaluation.
//
// declared answers, per node, what its Component declares sensitive among values
// and infra and which of its locals are computed from it; a failing locals or
// provides template reports without such a value. Its zero value declares nothing.
func EvaluateAllProvides(
	graph *types.IRGraph,
	recipes map[string]*types.Recipe, // nodeID → resolved Recipe
	renderedNames NodeRenderedNames, // nodeID → the name its provider reports; absent where none
	declared refusalDeclarations,
) (NodeProvides, NodeLocals, error) {
	allProvides := make(NodeProvides)
	allLocals := make(NodeLocals)

	// Sorted node iteration for determinism — graph.Spec.Nodes preserves
	// whatever ordering the IR builder produced (currently alphabetical).
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		recipe := recipes[nodeID]

		// Build restricted context
		values := types.OrderedMap[any]{}
		if raw, ok := graph.Spec.ValuesMapping.Get(nodeID); ok {
			_ = parse.IRPayload(raw, &values)
		}
		infra := types.OrderedMap[any]{}
		if raw, ok := graph.Spec.InfraMapping.Get(nodeID); ok {
			_ = parse.IRPayload(raw, &infra)
		}

		withheld := declared.node(nodeID).pillarKeys()
		nodeLocals, err := EvaluateRecipeLocals(recipe, nodeID, values, infra, withheld)
		if err != nil {
			return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q locals", nodeID))
		}
		allLocals[nodeID] = nodeLocals

		if recipe == nil || recipe.Spec.Provides.Len() == 0 {
			allProvides[nodeID] = types.OrderedMap[string]{}
			continue
		}

		// The self-containment reading, taken before this pass, admits .locals as a
		// whole root; the block's key set is only known once the preamble has resolved
		// it, so the key-level refusal runs here rather than with the root reading.
		if err := checkProvidesLocalsReferences(recipe.Spec.Provides, nodeLocals, func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("Recipe %q provides[%q]", recipe.Metadata.Name, key),
				File:  recipe.Metadata.Name,
			}
		}); err != nil {
			// A node locator over an already-composed diagnostic, the same shape as
			// the locals frame above: the wrapped error states the condition, so
			// restating it here would put one sentence on two adjacent lines.
			return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q provides", nodeID))
		}

		pCtx := ProvidesContext{
			Context: ProvidesContextBlock{
				InstanceID: encodeNodeIdentity(nodeID),
				// Absent where the node's provider reports no such name. The rule
				// declared over the same authored text refuses a template that reads
				// the key in that case, so a value reaching evaluation empty is a
				// template the rule already passed — one that never mentions it.
				RenderedName: renderedNames[nodeID],
			},
			Values: values,
			Infra:  infra,
			Locals: nodeLocals,
		}

		// Evaluate each provides template in author order, against the scope this
		// surface declares rather than against the context type alone. It is the
		// same scope the rule declared over the same authored text reads, so what
		// the surface admits is one statement both of them consult rather than two
		// that agree while one of them happens to run first.
		resolved := types.OrderedMap[string]{}
		for key, tmplText := range recipe.Spec.Provides.All() {
			val, err := evalValueTemplate(
				fmt.Sprintf("provides[%q]", key), tmplText, providesScope.with(pCtx, withheld), "{{", "}}")
			if err != nil {
				// The evaluation's report is carried as its whole cause chain. The
				// error is a diagnostic envelope whose own text is a single frame —
				// the surface and the stage — while what the evaluation said, the
				// failing expression and the key a read did not find, sits in the
				// frames beneath it; formatting the envelope alone would print the
				// frame and drop the reason.
				return nil, nil, cerrors.Wrap(ErrProvidesEvaluation(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
					cerrors.ComposeMessage(ErrProvidesEvaluation(), []string{
						fmt.Sprintf("node %q", nodeID),
						fmt.Sprintf("provides[%q]", key),
						cerrors.CauseChain(err),
					}, ""))
			}
			resolved.Set(key, strings.TrimSpace(val))
		}

		allProvides[nodeID] = resolved
	}

	return allProvides, allLocals, nil
}

// flattenOrderedStringMap copies an OrderedMap[string] into a plain
// map[string]string. Used at the boundary with engine and provider types that
// require map-shaped inputs.
func flattenOrderedStringMap(m types.OrderedMap[string]) map[string]string {
	if m.Len() == 0 {
		return nil
	}
	out := make(map[string]string, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}

// flattenOrderedMap copies an OrderedMap[any] into a plain
// map[string]any. Used at the boundary with engines/provider types.
func flattenOrderedMap(m types.OrderedMap[any]) map[string]any {
	if m.Len() == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}
