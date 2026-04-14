// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// FeatureRecipeRef pairs a feature instance name with its resolved FeatureRecipe.
type FeatureRecipeRef struct {
	InstanceName  string
	FeatureRecipe *types.FeatureRecipe
}

// ExecutePhaseA runs Phase A (Pre-Render Interception) for one node.
// It iterates FeatureRecipes in the provided order (5-Tier Onion Model from ir.FeatureOrder),
// evaluates each injectMapping template, and deep-merges the output over ctx.Context.Inject.
func ExecutePhaseA(ctx *types.TemplateContext, featureRecipes []FeatureRecipeRef) error {
	for _, ref := range featureRecipes {
		if ref.FeatureRecipe == nil {
			continue
		}
		tmplText := ref.FeatureRecipe.Spec.InjectMapping
		if tmplText == "" {
			continue
		}

		// Evaluate injectMapping with the current template context
		output, err := EvalTemplate(
			fmt.Sprintf("injectMapping[%s/%s]", ref.InstanceName, ref.FeatureRecipe.Metadata.Name),
			tmplText,
			ctx,
		)
		if err != nil {
			return fmt.Errorf("Phase A: feature %q (recipe %q) injectMapping: %w",
				ref.InstanceName, ref.FeatureRecipe.Metadata.Name, err)
		}

		// Parse the output and deep-merge over the inject pipe
		overlay, err := ParseInjectMappingOutput(output)
		if err != nil {
			return fmt.Errorf("Phase A: feature %q (recipe %q) injectMapping output parse: %w",
				ref.InstanceName, ref.FeatureRecipe.Metadata.Name, err)
		}

		if overlay != nil {
			DeepMergeInjectStructured(&ctx.Context.Inject, overlay)
		}
	}

	return nil
}
