// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// providesAllowedTopLevelRoots are the only top-level template roots a provides
// template may reference, in the order the diagnostic lists them. The provides
// block is evaluated in Pass 1 from self-contained node data alone (see
// ProvidesContext), so its reachable roots are exactly the context surface that
// pass exposes.
//
// Recipe locals are deliberately absent. Locals are evaluated against resolved
// provides — a local may read .provides, so provides is resolved first — which
// makes evaluated locals unreachable from a provides template by construction.
// A provides template that needs a local's value inlines the expression the
// local itself computes.
//
// This slice and providesAllowedContextRoots are the single home of the rule:
// CheckProvidesSelfContainment enforces them and providesAllowedRootsPhrase
// renders them into the diagnostic, so what the build refuses and what the
// author is told cannot disagree.
var providesAllowedTopLevelRoots = []string{"context", "values", "infra"}

// providesAllowedContextRoots are the only .context.* sub-roots a provides
// template may reference. Everything else under .context — inject and uses
// (cross-node), annotations, labels, and metadata (placement- or
// deployment-coupled) — is outside the self-contained Pass-1 surface.
var providesAllowedContextRoots = []string{"instance_id"}

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
func CheckProvidesSelfContainment(templateText, recipeID string) error {
	paths, err := CollectTemplateFieldAccesses(templateText)
	if err != nil {
		// Malformed template: let provides evaluation report the parse error.
		return nil
	}
	for _, p := range paths {
		segs := strings.Split(strings.TrimPrefix(p, "."), ".")
		root := segs[0]
		if root == "" || !slices.Contains(providesAllowedTopLevelRoots, root) {
			return providesContainmentErr(recipeID, p)
		}
		if root == "context" && (len(segs) < 2 || !slices.Contains(providesAllowedContextRoots, segs[1])) {
			return providesContainmentErr(recipeID, p)
		}
	}
	return nil
}

// providesAllowedRootsPhrase renders the allowlists as the prose list the
// self-containment diagnostic quotes. Derived rather than restated so the
// message names exactly the roots the check admits: .context is gated at two
// levels, so it contributes one entry per permitted sub-root, and every other
// root contributes itself.
func providesAllowedRootsPhrase() string {
	roots := make([]string, 0, len(providesAllowedTopLevelRoots)+len(providesAllowedContextRoots))
	for _, root := range providesAllowedTopLevelRoots {
		if root == "context" {
			for _, sub := range providesAllowedContextRoots {
				roots = append(roots, ".context."+sub)
			}
			continue
		}
		roots = append(roots, "."+root)
	}
	return strings.Join(roots, ", ")
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
