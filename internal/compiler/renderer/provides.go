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

// providesScope is the reference set a provides template is checked against. The
// universe is ProvidesContext — the same type the provides pass evaluates against
// — so the roots the check admits are exactly the surface that pass exposes, and
// .context is gated one level down by ProvidesContextBlock's own fields rather
// than by a second list beside it. A field added to that block therefore becomes
// reachable from a provides template with no edit here.
//
// Two roots the type defines are excluded, and the reason is ordering rather than
// taste:
//
//   - locals. A base Recipe's locals are evaluated against resolved provides — a
//     local may read .provides, so provides resolves first — which makes
//     evaluated locals unreachable from a base provides template by construction.
//     Permitting the root delivered the raw authored template text into the
//     consumer's rendered output instead of a value. The ProvidesContext.Locals
//     field is populated on the attachment FeatureRecipe path, which resolves
//     that entity's own locals BEFORE its provides and does not run this check;
//     the exclusion is therefore specific to the base Recipe pass this check
//     guards, not a claim that the field is always empty.
//   - attachment_name. No attachment is being resolved during a base Recipe's
//     provides pass, so the name would render empty.
//
// Both exclusions are deliberately kept narrowings: the type carries the fields
// because another pass populates them, and this surface refuses them because its
// own pass cannot. Removing either from the exclude set would reopen a
// silent-empty or raw-text render rather than simplify anything.
var providesScope = templateScope{
	data:    ProvidesContext{},
	exclude: []string{"locals", "attachment_name"},
}

// CheckProvidesSelfContainment verifies a provides template references only the
// self-contained Pass-1 roots: .context.instance_id, .values, .infra, and
// static text. Any other root halts with ErrProvidesSelfContainment naming the
// offending expression — a non-wired .context.* key (.context.inject,
// .context.uses, .context.annotations, .context.labels, .context.metadata, …),
// a non-wired top-level root (.metadata, .provides, …), or .locals, which is
// resolved after provides and so cannot be read from one. The check is a
// positive allowlist over the template's parsed field accesses, so a reference
// to a root the provides context does not expose is a Loud Guardrail rather
// than a silent empty render — or, for .locals, rather than the raw authored
// template text reaching a consumer's rendered output. The halt is structural
// ("root not in the provides context"), never a judgment about what the
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
	tree, err := parseTemplateForRootWalk(templateText, "{{", "}}")
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
	return cerrors.Wrap(ErrProvidesSelfContainment, cerrors.CatalogError, cerrors.SourceLocation{File: recipeID},
		fmt.Sprintf("Recipe %q provides references %q — the provides block is self-contained and may reference only "+
			"%s, and static text", recipeID, expr, providesAllowedRootsPhrase()))
}

// EvaluateAllProvides executes Pass 1: evaluate all provides templates in any order.
// Each provides template receives only self-contained data (instance_id, outputs, values, infra).
// Returns resolved provides per node.
func EvaluateAllProvides(
	graph *types.IRGraph,
	recipes map[string]*types.Recipe, // nodeID → resolved Recipe
) (NodeProvides, error) {
	result := make(NodeProvides)

	// Sorted node iteration for determinism — graph.Spec.Nodes preserves
	// whatever ordering the IR builder produced (currently alphabetical).
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		recipe, ok := recipes[nodeID]
		if !ok || recipe == nil || recipe.Spec.Provides.Len() == 0 {
			result[nodeID] = types.OrderedMap[string]{}
			continue
		}

		// Check self-containment for each provides template
		for key, tmplText := range recipe.Spec.Provides.All() {
			if err := CheckProvidesSelfContainment(tmplText, recipe.Metadata.Name); err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q provides[%q]", nodeID, key))
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

		// Locals stays zero here. A base Recipe's locals are resolved after
		// provides, so the only value this pass could supply is the authored
		// template text — which the self-containment check above has already
		// refused, since .locals is not an admitted root.
		pCtx := ProvidesContext{
			Context: ProvidesContextBlock{InstanceID: nodeID},
			Values:  values,
			Infra:   infra,
		}

		// Evaluate each provides template in author order
		resolved := types.OrderedMap[string]{}
		for key, tmplText := range recipe.Spec.Provides.All() {
			val, err := evalTemplateString(tmplText, pCtx, fmt.Sprintf("provides[%q]", key))
			if err != nil {
				return nil, cerrors.Wrap(ErrProvidesEvaluation, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
					fmt.Sprintf("node %q provides[%q]: %v", nodeID, key, err))
			}
			resolved.Set(key, strings.TrimSpace(val))
		}

		// Also resolve component type version for the provides
		_ = node // node available if needed for version

		result[nodeID] = resolved
	}

	return result, nil
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
