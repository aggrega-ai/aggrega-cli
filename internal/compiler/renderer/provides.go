// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// providesScope is the reference set a provides template is checked against. The
// universe is ProvidesContext — the same type the provides pass evaluates against
// — so the roots the check admits are exactly the surface that pass exposes, and
// .context is gated one level down by ProvidesContextBlock's own fields rather
// than by a second list beside it. A field added to that block therefore becomes
// reachable from a provides template with no edit here.
//
// One root the type defines is excluded, and the reason is ordering rather than
// taste: attachment_name. No attachment is being resolved during a base Recipe's
// provides pass, so the name would render empty. The exclusion is deliberately
// kept a narrowing — the type carries the field because the attachment
// FeatureRecipe path populates it, and this surface refuses it because its own
// pass cannot. Removing it from the exclude set would reopen a silent-empty
// render rather than simplify anything.
//
// .locals is admitted, and admitting it is also an ordering property: a node's
// locals are resolved in the pass preamble, ahead of that node's provides, so a
// provides template reading .locals receives resolved values. That holds on both
// paths that populate the pillar — the base Recipe pass this check guards, and the
// attachment FeatureRecipe path, which resolves its own locals before its provides
// and does not run this check.
//
// Admitting the root admits an OPEN keyed map, so the root check alone cannot refuse
// a key the block never declared. The key-level refusal is a separate check run once
// the block is resolved (checkProvidesLocalsReferences) — the root is admitted here,
// and the key is checked there.
var providesScope = templateScope{
	data:    ProvidesContext{},
	exclude: []string{"attachment_name"},
}

// CheckProvidesSelfContainment verifies a provides template references only the
// self-contained Pass-1 roots: .context.instance_id, .values, .infra, .locals —
// resolved in the pass preamble, ahead of provides — and static text. Any other
// root halts with ErrProvidesSelfContainment naming the offending expression — a
// non-wired .context.* key (.context.inject, .context.uses, .context.annotations,
// .context.labels, .context.metadata, …), a non-wired top-level root (.metadata,
// .provides, …), or .attachment_name, which names no attachment during a base
// Recipe's provides pass. The check is a positive allowlist over the template's
// parsed field accesses, so a reference to a root the provides context does not
// expose is a Loud Guardrail rather than a silent empty render. The halt is
// structural ("root not in the provides context"), never a judgment about what the
// referenced value means.
//
// The check is providesScope, evaluated by the same walk and the same
// type-derived derivation the evaluator applies to every other template surface —
// this surface differs only in the narrowing it declares, not in how a reference
// is identified. Sharing the walk is what keeps a defensive wrapper from reading
// as a reference to a root named after the key it guards: only the wrapped
// expression reaches the context root, so the trailing key of a chained fallback
// is not mistaken for a root.
func CheckProvidesSelfContainment(templateText, recipeID string) error {
	if templateText == "" {
		return nil
	}
	tree, err := gotmpl.ParseTree(templateText, "{{", "}}")
	if err != nil {
		// Malformed template: let provides evaluation report the parse error.
		return nil
	}
	if v := providesScope.check(tree); v != nil {
		return providesContainmentErr(recipeID, v.Ref.Path)
	}
	return nil
}

// providesAllowedRootsPhrase renders the scope's admitted set as the prose list
// the self-containment diagnostic quotes. Derived from the scope rather than
// restated so the message names exactly the references the check admits: a root
// gated one level down contributes one entry per admitted key, and an open root
// contributes itself.
func providesAllowedRootsPhrase() string {
	roots, ok := providesScope.admittedRoots()
	if !ok {
		return ""
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if keys, gated := providesScope.admittedKeys(root); gated {
			out = append(out, templateRootsPhrase("."+root, keys))
			continue
		}
		out = append(out, "."+root)
	}
	return strings.Join(out, ", ")
}

func providesContainmentErr(recipeID, expr string) error {
	return cerrors.Wrap(ErrProvidesSelfContainment(), cerrors.CatalogError, cerrors.SourceLocation{File: recipeID},
		fmt.Sprintf("Recipe %q provides references %q — the provides block is self-contained and may reference only "+
			"%s, and static text", recipeID, expr, providesAllowedRootsPhrase()))
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
// rendering empty. Returns
// the resolved provides and the resolved locals per node — the locals travel out of
// this pass rather than being recomputed downstream, so the render passes and the
// assertion pass read one evaluation.
func EvaluateAllProvides(
	graph *types.IRGraph,
	recipes map[string]*types.Recipe, // nodeID → resolved Recipe
) (NodeProvides, NodeLocals, error) {
	allProvides := make(NodeProvides)
	allLocals := make(NodeLocals)

	// Sorted node iteration for determinism — graph.Spec.Nodes preserves
	// whatever ordering the IR builder produced (currently alphabetical).
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		recipe := recipes[nodeID]

		// Check self-containment for each provides template. The check reads
		// template text and evaluates nothing, so it runs ahead of the locals
		// preamble: a provides template naming a root this surface refuses reports
		// that refusal whatever else the Recipe declares.
		if recipe != nil {
			for key, tmplText := range recipe.Spec.Provides.All() {
				if err := CheckProvidesSelfContainment(tmplText, recipe.Metadata.Name); err != nil {
					return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q provides[%q]", nodeID, key))
				}
			}
		}

		// Build restricted context
		values := types.OrderedMap[any]{}
		if raw, ok := graph.Spec.ValuesMapping.Get(nodeID); ok {
			_ = parse.IRPayload(raw, &values)
		}
		infra := types.OrderedMap[any]{}
		if raw, ok := graph.Spec.InfraMapping.Get(nodeID); ok {
			_ = parse.IRPayload(raw, &infra)
		}

		nodeLocals, err := EvaluateRecipeLocals(recipe, nodeID, values, infra)
		if err != nil {
			return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q locals", nodeID))
		}
		allLocals[nodeID] = nodeLocals

		if recipe == nil || recipe.Spec.Provides.Len() == 0 {
			allProvides[nodeID] = types.OrderedMap[string]{}
			continue
		}

		// The self-containment check above admits .locals as a whole root; the block's
		// key set is only known once the preamble has resolved it, so the key-level
		// refusal runs here rather than beside the root check.
		if err := checkProvidesLocalsReferences(recipe.Spec.Provides, nodeLocals, func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("Recipe %q provides[%q]", recipe.Metadata.Name, key),
				File:  recipe.Metadata.Name,
			}
		}); err != nil {
			return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q provides", nodeID))
		}

		pCtx := ProvidesContext{
			Context: ProvidesContextBlock{InstanceID: nodeID},
			Values:  values,
			Infra:   infra,
			Locals:  nodeLocals,
		}

		// Evaluate each provides template in author order
		resolved := types.OrderedMap[string]{}
		for key, tmplText := range recipe.Spec.Provides.All() {
			val, err := evalTemplateString(tmplText, pCtx, fmt.Sprintf("provides[%q]", key))
			if err != nil {
				return nil, nil, cerrors.Wrap(ErrProvidesEvaluation(), cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
					fmt.Sprintf("node %q provides[%q]: %v", nodeID, key, err))
			}
			resolved.Set(key, strings.TrimSpace(val))
		}

		allProvides[nodeID] = resolved
	}

	return allProvides, allLocals, nil
}

// evalTemplateString evaluates a Go template string with Sprig functions.
// Uses toLowercaseMap for consistent lowercase field names in templates.
func evalTemplateString(tmplText string, data any, name string) (string, error) {
	return EvalTemplate(name, tmplText, data)
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
