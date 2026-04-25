// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// EvaluateFeatureRecipeLocals evaluates fr.Spec.Locals templates against the
// feature-scoped context and returns the resolved map. Same self-containment
// rules as Recipe locals: no .context.inject / .context.uses references.
// Iteration is in author order (fr.Spec.Locals is an OrderedMap).
func EvaluateFeatureRecipeLocals(
	fr *types.FeatureRecipe,
	instanceName string,
	values types.OrderedMap[interface{}],
	infra types.OrderedMap[interface{}],
	provides types.OrderedMap[string],
) (types.OrderedMap[string], error) {
	resolved := types.OrderedMap[string]{}
	if fr == nil || fr.Spec.Locals.Len() == 0 {
		return resolved, nil
	}

	lCtx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: instanceName},
		Values:   values,
		Infra:    infra,
		Provides: provides,
	}

	for key, tmplText := range fr.Spec.Locals.All() {
		if err := CheckLocalsSelfContainment(tmplText, fr.Metadata.Name, key); err != nil {
			return types.OrderedMap[string]{}, err
		}
		val, err := EvalTemplate(fmt.Sprintf("fr-locals[%q]", key), tmplText, lCtx)
		if err != nil {
			return types.OrderedMap[string]{}, fmt.Errorf("FeatureRecipe %q locals[%q]: %w", fr.Metadata.Name, key, err)
		}
		resolved.Set(key, strings.TrimSpace(val))
	}
	return resolved, nil
}

// ExecutePhaseBForFeature evaluates a FeatureRecipe's valuesMapping (or
// valuesMappingFile) and returns the values dictionary for the provider engine.
// Mirrors ExecutePhaseB for base Recipes but takes a *types.FeatureRecipe.
func ExecutePhaseBForFeature(ctx *types.TemplateContext, fr *types.FeatureRecipe) (map[string]interface{}, error) {
	if fr == nil {
		return map[string]interface{}{}, nil
	}

	if fr.Spec.ValuesMappingJsonnet != "" {
		return nil, fmt.Errorf("%w: FeatureRecipe %q uses valuesMappingJsonnet", ErrJsonnetNotSupported, fr.Metadata.Name)
	}

	tmplText := fr.Spec.ValuesMapping
	if tmplText == "" && fr.Spec.ValuesMappingFile != "" {
		data, err := os.ReadFile(fr.Spec.ValuesMappingFile)
		if err != nil {
			return nil, fmt.Errorf("Phase B (feature): FeatureRecipe %q valuesMappingFile %q: %w",
				fr.Metadata.Name, fr.Spec.ValuesMappingFile, err)
		}
		tmplText = string(data)
	}

	if tmplText == "" {
		// No valuesMapping — return feature-scoped .values as-is.
		out := make(map[string]interface{}, ctx.Values.Len())
		for k, v := range ctx.Values.All() {
			out[k] = v
		}
		return out, nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("fr-valuesMapping[%s]", fr.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return nil, fmt.Errorf("Phase B (feature): FeatureRecipe %q valuesMapping: %w", fr.Metadata.Name, err)
	}

	var values map[string]interface{}
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return nil, fmt.Errorf("Phase B (feature): FeatureRecipe %q valuesMapping output is not valid YAML: %w",
			fr.Metadata.Name, err)
	}

	return values, nil
}

// isResourceGeneratingFR returns true if the FeatureRecipe carries source or
// valuesMapping (i.e., it produces its own resources in Phase B). Inject-only
// FRs return false — they're already handled by Phase A.
func isResourceGeneratingFR(fr *types.FeatureRecipe) bool {
	if fr == nil {
		return false
	}
	if fr.Spec.Source != nil {
		if s, ok := fr.Spec.Source.(string); ok && s != "" {
			return true
		}
		if _, ok := fr.Spec.Source.(string); !ok {
			// Structured (non-string) source — treat as present.
			return true
		}
	}
	if fr.Spec.ValuesMapping != "" {
		return true
	}
	if fr.Spec.ValuesMappingFile != "" {
		return true
	}
	return false
}
