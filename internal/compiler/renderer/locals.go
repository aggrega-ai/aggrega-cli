// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LocalsContext is the restricted context available during Recipe .locals
// evaluation: node-local data only — the node's own identity, its values and infra
// pillars, and its own already-resolved provides.
//
// Self-containment is a property of this type rather than of a check over template
// text. Context is a ProvidesContextBlock, which defines instance_id and nothing
// else, so .context.inject and .context.uses name keys this surface's context does
// not define and the reference halts before the template executes. Cross-node data
// is therefore unreachable from a local by construction, which is what keeps locals
// resolvable without ordering the graph first.
type LocalsContext struct {
	Context  ProvidesContextBlock     `json:"context"`
	Values   types.OrderedMap[any]    `json:"values"`
	Infra    types.OrderedMap[any]    `json:"infra"`
	Provides types.OrderedMap[string] `json:"provides"`
}

// EvaluateRecipeLocals evaluates each entry in recipe.Spec.Locals as a Go template
// against the restricted LocalsContext. Returns a deterministic resolved OrderedMap.
// Runs as a Phase B preamble: after Pass 1 (provides available), before Phase A/B.
// Iteration is in author order (recipe.Spec.Locals is an OrderedMap).
func EvaluateRecipeLocals(
	recipe *types.Recipe,
	nodeID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
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
