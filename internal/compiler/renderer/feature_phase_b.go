// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// EvaluateFeatureRecipeLocals evaluates fr.Spec.Locals templates against the
// feature-scoped context and returns the resolved map. Same self-containment
// rules as Recipe locals: no .context.inject / .context.uses references.
// This is the WI-2 upgrade over the raw-string overlay used in
// Phase A by WI-1 — FR locals that are templates get rendered here with
// the feature's own values/infra and the node's provides.
func EvaluateFeatureRecipeLocals(
	fr *types.FeatureRecipe,
	instanceName string,
	values map[string]interface{},
	infra map[string]interface{},
	provides map[string]string,
) (map[string]string, error) {
	if fr == nil || len(fr.Spec.Locals) == 0 {
		return map[string]string{}, nil
	}

	lCtx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: instanceName},
		Values:   ensureMap(values),
		Infra:    ensureMap(infra),
		Provides: ensureStringMap(provides),
	}

	resolved := make(map[string]string, len(fr.Spec.Locals))
	keys := sortedStringMapKeys(fr.Spec.Locals)
	for _, key := range keys {
		tmplText := fr.Spec.Locals[key]
		if err := CheckLocalsSelfContainment(tmplText, fr.Metadata.Name, key); err != nil {
			return nil, err
		}
		val, err := EvalTemplate(fmt.Sprintf("fr-locals[%q]", key), tmplText, lCtx)
		if err != nil {
			return nil, fmt.Errorf("FeatureRecipe %q locals[%q]: %w", fr.Metadata.Name, key, err)
		}
		resolved[key] = strings.TrimSpace(val)
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
		return ctx.Values, nil
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
	if err := json.Unmarshal([]byte(output), &values); err != nil {
		return nil, fmt.Errorf("Phase B (feature): FeatureRecipe %q valuesMapping output is not valid JSON: %w",
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
