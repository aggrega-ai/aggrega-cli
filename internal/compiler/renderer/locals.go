// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// LocalsContext is the restricted context available during Recipe .locals evaluation.
// Self-contained data only: instance_id, values, infra, provides (Pass 1 output).
// Locals templates MUST NOT reference .context.inject or .context.uses.
type LocalsContext struct {
	Context  ProvidesContextBlock   `json:"context"`
	Values   map[string]interface{} `json:"values"`
	Infra    map[string]interface{} `json:"infra"`
	Provides map[string]string      `json:"provides"`
}

// forbiddenLocalsRefs matches template actions referencing cross-node or inject data.
var forbiddenLocalsRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// CheckLocalsSelfContainment scans a locals template for forbidden references
// to .context.inject or .context.uses. Returns an error if found.
func CheckLocalsSelfContainment(templateText, recipeID, key string) error {
	if forbiddenLocalsRefs.MatchString(templateText) {
		return fmt.Errorf(
			"Recipe %q locals[%q] references .context.inject or .context.uses — locals must be self-contained",
			recipeID, key,
		)
	}
	return nil
}

// EvaluateRecipeLocals evaluates each entry in recipe.Spec.Locals as a Go template
// against the restricted LocalsContext. Returns a deterministic resolved map.
// Runs as a Phase B preamble: after Pass 1 (provides available), before Phase A/B.
func EvaluateRecipeLocals(
	recipe *types.Recipe,
	nodeID string,
	values map[string]interface{},
	infra map[string]interface{},
	provides map[string]string,
) (map[string]string, error) {
	if recipe == nil || len(recipe.Spec.Locals) == 0 {
		return map[string]string{}, nil
	}

	lCtx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: nodeID},
		Values:   ensureMap(values),
		Infra:    ensureMap(infra),
		Provides: ensureStringMap(provides),
	}

	resolved := make(map[string]string, len(recipe.Spec.Locals))
	keys := sortedStringMapKeys(recipe.Spec.Locals)
	for _, key := range keys {
		tmplText := recipe.Spec.Locals[key]
		if err := CheckLocalsSelfContainment(tmplText, recipe.Metadata.Name, key); err != nil {
			return nil, err
		}
		val, err := EvalTemplate(fmt.Sprintf("locals[%q]", key), tmplText, lCtx)
		if err != nil {
			return nil, fmt.Errorf("Recipe %q locals[%q]: %w", recipe.Metadata.Name, key, err)
		}
		resolved[key] = strings.TrimSpace(val)
	}
	return resolved, nil
}

// mergeFeatureLocals returns the merge of base Recipe's evaluated locals and a
// FeatureRecipe's own locals. FR entries override base entries on key collision.
// WI-1 treats FR-own locals as raw strings; WI-2 will upgrade to
// template-evaluated overlay using feature-scoped context.
func mergeFeatureLocals(base, frOwn map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(frOwn))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range frOwn {
		out[k] = v
	}
	return out
}
