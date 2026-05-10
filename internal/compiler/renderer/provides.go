// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// forbiddenProvidesRefs matches template actions referencing cross-node data.
var forbiddenProvidesRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// CheckProvidesSelfContainment scans a provides template for forbidden references
// to .context.inject or .context.uses. Returns an error if found.
func CheckProvidesSelfContainment(templateText, recipeID string) error {
	if forbiddenProvidesRefs.MatchString(templateText) {
		return cerrors.Wrap(ErrProvidesSelfContainment, cerrors.CatalogError, cerrors.SourceLocation{File: recipeID},
			fmt.Sprintf("Recipe %q provides block references .context.inject or .context.uses — the provides contract must be self-contained", recipeID))
	}
	return nil
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

		pCtx := ProvidesContext{
			Context: ProvidesContextBlock{InstanceID: nodeID},
			Values:  values,
			Infra:   infra,
			Locals:  recipe.Spec.Locals,
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
