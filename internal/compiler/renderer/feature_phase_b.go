// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// EvaluateFeatureRecipeLocals evaluates fr.Spec.Locals templates against the
// feature-scoped context and returns the resolved map. Same self-containment
// rules as Recipe locals: no .context.inject / .context.uses references.
// Iteration is in author order (fr.Spec.Locals is an OrderedMap).
func EvaluateFeatureRecipeLocals(
	fr *types.FeatureRecipe,
	instanceName string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
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
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q locals[%q]", fr.Metadata.Name, key))
		}
		resolved.Set(key, strings.TrimSpace(val))
	}
	return resolved, nil
}

// ExecutePhaseBForFeature evaluates a FeatureRecipe's valuesMapping (or
// valuesMappingFile) and returns the values dictionary for the provider engine.
// Mirrors ExecutePhaseB for base Recipes but takes a *types.FeatureRecipe.
func ExecutePhaseBForFeature(ctx *types.TemplateContext, fr *types.FeatureRecipe) (map[string]any, error) {
	if fr == nil {
		return map[string]any{}, nil
	}

	if fr.Spec.ValuesMappingJsonnet != "" {
		return nil, cerrors.Wrap(ErrJsonnetNotSupported, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("FeatureRecipe %q uses valuesMappingJsonnet", fr.Metadata.Name))
	}

	tmplText := fr.Spec.ValuesMapping
	if tmplText == "" && fr.Spec.ValuesMappingFile != "" {
		data, err := os.ReadFile(fr.Spec.ValuesMappingFile)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Spec.ValuesMappingFile}, fmt.Sprintf("Phase B (feature): FeatureRecipe %q valuesMappingFile", fr.Metadata.Name))
		}
		tmplText = string(data)
	}

	if tmplText == "" {
		// No valuesMapping — return feature-scoped .values as-is.
		out := make(map[string]any, ctx.Values.Len())
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
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("Phase B (feature): FeatureRecipe %q valuesMapping", fr.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("Phase B (feature): FeatureRecipe %q valuesMapping output is not valid YAML", fr.Metadata.Name))
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
