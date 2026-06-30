// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ClosureRecipeLoader returns a closure.RecipeLoader backed by this
// in-memory Catalog. LoadRecipe and LoadFeatureRecipe wrap the
// existing RecipeByURI / FeatureRecipeByURI lookups with the
// closure-package interface shape. Not-found cases surface as wrapped
// errors so the closure walker's source-descent step can distinguish
// a missing-Recipe failure from other parse / fetch errors.
func (c *Catalog) ClosureRecipeLoader() closure.RecipeLoader {
	return &catalogRecipeLoader{catalog: c}
}

type catalogRecipeLoader struct {
	catalog *Catalog
}

func (l *catalogRecipeLoader) LoadRecipe(uri string) (*types.Recipe, error) {
	r, ok := l.catalog.RecipeByURI(uri)
	if !ok {
		return nil, fmt.Errorf("catalog: Recipe %q not found", uri)
	}
	return &r, nil
}

func (l *catalogRecipeLoader) LoadFeatureRecipe(uri string) (*types.FeatureRecipe, error) {
	fr, ok := l.catalog.FeatureRecipeByURI(uri)
	if !ok {
		return nil, fmt.Errorf("catalog: FeatureRecipe %q not found", uri)
	}
	return &fr, nil
}

func (l *catalogRecipeLoader) LoadPlugin(uri string) (*types.Plugin, error) {
	wp, ok := l.catalog.PluginByURI(uri)
	if !ok {
		return nil, fmt.Errorf("catalog: Plugin %q not found", uri)
	}
	return &wp, nil
}
