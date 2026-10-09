// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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
		return nil, fmt.Errorf("catalog: Recipe %q not found (check the Manifest recipe mapping, or run `aggrega pull --release <release>` to fetch it)", uri)
	}
	return &r, nil
}

func (l *catalogRecipeLoader) LoadFeatureRecipe(uri string) (*types.FeatureRecipe, error) {
	fr, ok := l.catalog.FeatureRecipeByURI(uri)
	if !ok {
		return nil, fmt.Errorf("catalog: FeatureRecipe %q not found (check the Manifest recipe mapping, or run `aggrega pull --release <release>` to fetch it)", uri)
	}
	return &fr, nil
}

// LoadPlugin reads a Plugin entity through PluginByURI, the accessor the build's
// own plugin lookup reads, so a reference it cannot load is one the build cannot
// load either. Its refusal is therefore the build's for the same reference, and
// is composed where the build's is: a pull that asks this loader about a Plugin
// no other source answered refuses with the condition and sentence the build
// would raise at load.
func (l *catalogRecipeLoader) LoadPlugin(uri string) (*types.Plugin, error) {
	wp, ok := l.catalog.PluginByURI(uri)
	if !ok {
		return nil, pluginpath.UnresolvedEntity(uri, pluginpath.SurfaceEntityOnly)
	}
	return &wp, nil
}
