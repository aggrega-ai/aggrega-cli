// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// FeatureRecipeRef pairs an attachment instance name with its resolved
// FeatureRecipe.
//
// A ref participates in Phase A by having its FeatureRecipe's injectMapping
// evaluated against an attachment-scoped context whose .values/.infra come from
// the matching entry in ctx.Features. One lookup serves every attachment
// family, so a credential FeatureRecipe's injectMapping reads the attachment's
// own infra exactly as any other attachment's does.
type FeatureRecipeRef struct {
	InstanceName  string
	FeatureRecipe *types.FeatureRecipe
}

// ExecutePhaseA runs Phase A (Pre-Render Interception) for one node.
// It iterates FeatureRecipes in the provided order (5-Tier Onion Model from ir.FeatureOrder),
// evaluates each injectMapping template, and deep-merges the output over ctx.Context.Inject.
// For each FeatureRecipe, .values and .infra are swapped to the attachment's own values/infra.
//
// The authored-mechanism set is collected once from ctx.InjectRaw — the
// immutable Developer intent populated by BuildInjectPipe. The set is passed
// into every ParseInjectMappingOutput call so the dangling-reference check
// (every authored mechanism must surface in the parsed FR injectMapping
// output) sees the full Component- or Feature-attachment authoring at every
// FR iteration. ctx.InjectRaw must be either nil or a *types.InjectBlock per
// the field's contract; any other concrete type indicates a contract
// violation upstream and produces a loud halt rather than silently skipping
// the dangling-reference check.
func ExecutePhaseA(ctx *types.TemplateContext, featureRecipes []FeatureRecipeRef) error {
	var authored []string
	if ctx.InjectRaw != nil {
		rawBlock, ok := ctx.InjectRaw.(*types.InjectBlock)
		if !ok {
			return cerrors.Wrap(
				fmt.Errorf("phase A: unexpected InjectRaw type %T (expected *types.InjectBlock)", ctx.InjectRaw),
				cerrors.TranslationError,
				cerrors.SourceLocation{},
				"phase A: authored-mechanism collection",
			)
		}
		if rawBlock != nil {
			if rawBlock.Env != nil {
				authored = append(authored, "env")
			}
			if rawBlock.File != nil {
				authored = append(authored, "file")
			}
			if rawBlock.Arg != nil {
				authored = append(authored, "arg")
			}
			for k := range rawBlock.Additional.All() {
				authored = append(authored, k)
			}
		}
	}

	for _, ref := range featureRecipes {
		if ref.FeatureRecipe == nil {
			continue
		}
		tmplText := ref.FeatureRecipe.Spec.InjectMapping
		if tmplText == "" {
			continue
		}

		// Build feature-scoped context: swap .values/.infra to the feature's own
		// and overlay FR-own locals over the base Recipe's evaluated locals
		// (FR wins on collision). The current implementation uses a raw-string
		// overlay; template-evaluated overlay is a future enhancement.
		featureCtx := *ctx // shallow copy
		if attachment, ok := ctx.Features.Get(ref.InstanceName); ok {
			featureCtx.Values = attachment.Values
			featureCtx.Infra = attachment.Infra
		}
		featureCtx.Locals = mergeFeatureLocals(ctx.Locals, ref.FeatureRecipe.Spec.Locals)

		// Evaluate injectMapping with the feature-scoped context
		output, err := EvalTemplate(
			fmt.Sprintf("injectMapping[%s/%s]", ref.InstanceName, ref.FeatureRecipe.Metadata.Name),
			tmplText,
			&featureCtx,
		)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("Phase A: feature %q (recipe %q) injectMapping", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}

		// Parse the output and deep-merge over the inject pipe
		overlay, err := ParseInjectMappingOutput(output, authored)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("Phase A: feature %q (recipe %q) injectMapping output parse", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}

		if overlay != nil {
			DeepMergeInjectStructured(&ctx.Context.Inject, overlay)
		}
	}

	return nil
}
