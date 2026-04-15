// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// forbiddenProvidesRefs matches template actions referencing cross-node data.
var forbiddenProvidesRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// CheckProvidesSelfContainment scans a provides template for forbidden references
// to .context.inject or .context.uses. Returns an error if found.
func CheckProvidesSelfContainment(templateText, recipeID string) error {
	if forbiddenProvidesRefs.MatchString(templateText) {
		return fmt.Errorf(
			"%w: Recipe %q provides block references .context.inject or .context.uses — the provides contract must be self-contained",
			ErrProvidesSelfContainment, recipeID,
		)
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

	// Sorted node iteration for determinism
	nodeIDs := sortedStringKeys(graph.Spec.Nodes)
	for _, nodeID := range nodeIDs {
		node := graph.Spec.Nodes[nodeID]
		recipe, ok := recipes[nodeID]
		if !ok || recipe == nil || len(recipe.Spec.Provides) == 0 {
			result[nodeID] = map[string]string{}
			continue
		}

		// Check self-containment for each provides template
		for key, tmplText := range recipe.Spec.Provides {
			if err := CheckProvidesSelfContainment(tmplText, recipe.Metadata.Name); err != nil {
				return nil, fmt.Errorf("node %q provides[%q]: %w", nodeID, key, err)
			}
		}

		// Build restricted context
		var values map[string]interface{}
		if raw, ok := graph.Spec.ValuesMapping[nodeID]; ok {
			_ = json.Unmarshal(raw, &values)
		}
		var infra map[string]interface{}
		if raw, ok := graph.Spec.InfraMapping[nodeID]; ok {
			_ = json.Unmarshal(raw, &infra)
		}

		pCtx := ProvidesContext{
			Context: ProvidesContextBlock{InstanceID: nodeID},
			Values:  ensureMap(values),
			Infra:   ensureMap(infra),
			Outputs: ensureStringMap(recipe.Spec.Outputs),
		}

		// Evaluate each provides template
		resolved := make(map[string]string, len(recipe.Spec.Provides))
		provKeys := sortedStringMapKeys(recipe.Spec.Provides)
		for _, key := range provKeys {
			tmplText := recipe.Spec.Provides[key]
			val, err := evalTemplateString(tmplText, pCtx, fmt.Sprintf("provides[%q]", key))
			if err != nil {
				return nil, fmt.Errorf("%w: node %q provides[%q]: %v", ErrProvidesEvaluation, nodeID, key, err)
			}
			resolved[key] = strings.TrimSpace(val)
		}

		// Also resolve component type version for the provides
		_ = node // node available if needed for version

		result[nodeID] = resolved
	}

	return result, nil
}

// evalTemplateString evaluates a Go template string with Sprig functions.
// Uses toLowercaseMap for consistent lowercase field names in templates.
func evalTemplateString(tmplText string, data interface{}, name string) (string, error) {
	return EvalTemplate(name, tmplText, data)
}

func ensureMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	return m
}

func ensureStringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
