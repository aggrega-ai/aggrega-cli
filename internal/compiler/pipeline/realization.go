// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// realizationBinding turns the render's FeatureRecipe lookup into the binding
// the graph build records each attachment's realization from.
//
// It adds nothing to the lookup's answer and decides nothing of its own: the
// FeatureRecipe is the one the lookup returns for the attachment's name and
// Feature type — the name tier first, then the type tier, exactly as the render
// asks — and its identity is spelled by [catalog.RecipeMappingURI], the one
// composition of the "<name>:<version>" form, so the graph names it the way the
// documents it renders and every other surface name it. An attachment the
// lookup binds to nothing records nothing.
//
// The composition sits here rather than in the graph build because the graph
// build cannot reach the package that owns the form, and the lookup sits outside
// it for the same reason; this package can reach both.
//
// A nil lookup yields a nil binding, so a caller with no Manifest to bind by
// finalises a graph that records no realization.
func realizationBinding(featureRecipe func(instanceName, featureType string) (*types.FeatureRecipe, bool)) ir.RealizationBinding {
	if featureRecipe == nil {
		return nil
	}
	return func(instanceName, featureType string) string {
		fr, found := featureRecipe(instanceName, featureType)
		if !found || fr == nil {
			return ""
		}
		return catalog.RecipeMappingURI(fr.Metadata.Name, fr.Metadata.Version)
	}
}
