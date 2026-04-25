// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ExecutePhaseB runs Phase B (Base Rendering) for one node.
// It evaluates the Recipe's valuesMapping (or valuesMappingFile) Go template
// and returns the values dictionary as map[string]interface{}.
// The provider engine consumes this dictionary to render K8s manifests.
func ExecutePhaseB(ctx *types.TemplateContext, recipe *types.Recipe) (map[string]interface{}, error) {
	if recipe == nil {
		return map[string]interface{}{}, nil
	}

	// Check for unsupported Jsonnet
	if recipe.Spec.ValuesMappingJsonnet != "" {
		return nil, fmt.Errorf("%w: recipe %q uses valuesMappingJsonnet", ErrJsonnetNotSupported, recipe.Metadata.Name)
	}

	// Determine template text
	tmplText := recipe.Spec.ValuesMapping
	if tmplText == "" && recipe.Spec.ValuesMappingFile != "" {
		data, err := os.ReadFile(recipe.Spec.ValuesMappingFile)
		if err != nil {
			return nil, fmt.Errorf("Phase B: recipe %q valuesMappingFile %q: %w",
				recipe.Metadata.Name, recipe.Spec.ValuesMappingFile, err)
		}
		tmplText = string(data)
	}

	if tmplText == "" {
		// No valuesMapping — return values from context as-is (flattened for engine)
		return flattenOrderedMap(ctx.Values), nil
	}

	// Evaluate template
	output, err := EvalTemplate(
		fmt.Sprintf("valuesMapping[%s]", recipe.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return nil, fmt.Errorf("Phase B: recipe %q valuesMapping: %w", recipe.Metadata.Name, err)
	}

	// Template output is YAML per the contract (yaml.v3 reads valid JSON
	// natively; new Recipes can use block scalars, comments, etc.).
	var values map[string]interface{}
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return nil, fmt.Errorf("Phase B: recipe %q valuesMapping output is not valid YAML: %w",
			recipe.Metadata.Name, err)
	}

	return values, nil
}
