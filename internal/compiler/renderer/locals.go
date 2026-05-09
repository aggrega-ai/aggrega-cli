// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"regexp"
	"strings"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// LocalsContext is the restricted context available during Recipe .locals evaluation.
// Self-contained data only: instance_id, values, infra, provides (Pass 1 output).
// Locals templates MUST NOT reference .context.inject or .context.uses.
type LocalsContext struct {
	Context  ProvidesContextBlock          `json:"context"`
	Values   types.OrderedMap[interface{}] `json:"values"`
	Infra    types.OrderedMap[interface{}] `json:"infra"`
	Provides types.OrderedMap[string]      `json:"provides"`
}

// forbiddenLocalsRefs matches template actions referencing cross-node or inject data.
var forbiddenLocalsRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// CheckLocalsSelfContainment scans a locals template for forbidden references
// to .context.inject or .context.uses. Returns an error if found.
func CheckLocalsSelfContainment(templateText, recipeID, key string) error {
	if forbiddenLocalsRefs.MatchString(templateText) {
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: recipeID},
			fmt.Sprintf("Recipe %q locals[%q] references .context.inject or .context.uses — locals must be self-contained", recipeID, key),
			nil, nil)
	}
	return nil
}

// EvaluateRecipeLocals evaluates each entry in recipe.Spec.Locals as a Go template
// against the restricted LocalsContext. Returns a deterministic resolved OrderedMap.
// Runs as a Phase B preamble: after Pass 1 (provides available), before Phase A/B.
// Iteration is in author order (recipe.Spec.Locals is an OrderedMap).
func EvaluateRecipeLocals(
	recipe *types.Recipe,
	nodeID string,
	values types.OrderedMap[interface{}],
	infra types.OrderedMap[interface{}],
	provides types.OrderedMap[string],
) (types.OrderedMap[string], error) {
	resolved := types.OrderedMap[string]{}
	if recipe == nil || recipe.Spec.Locals.Len() == 0 {
		return resolved, nil
	}

	lCtx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: nodeID},
		Values:   values,
		Infra:    infra,
		Provides: provides,
	}

	for key, tmplText := range recipe.Spec.Locals.All() {
		if err := CheckLocalsSelfContainment(tmplText, recipe.Metadata.Name, key); err != nil {
			return types.OrderedMap[string]{}, err
		}
		val, err := EvalTemplate(fmt.Sprintf("locals[%q]", key), tmplText, lCtx)
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: recipe.Metadata.Name}, fmt.Sprintf("Recipe %q locals[%q]", recipe.Metadata.Name, key))
		}
		resolved.Set(key, strings.TrimSpace(val))
	}
	return resolved, nil
}

// mergeFeatureLocals returns the merge of base Recipe's evaluated locals and a
// FeatureRecipe's own locals. FR entries override base entries on key collision.
// Author order preserved — base first, then FR-only entries.
func mergeFeatureLocals(base, frOwn types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, v := range frOwn.All() {
		out.Set(k, v)
	}
	return out
}
