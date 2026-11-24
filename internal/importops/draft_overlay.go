// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// overlayDrafts returns the view the composition and the owed-value reading
// consult: base first, then each Component and Recipe files declare, decoded by
// the catalog's own parse of one entity ([catalog.ParseComponentEntity],
// [catalog.ParseRecipeEntity]). So an instance of a Component the importer
// drafted maps to the Recipe it drafted realizing it, and the values that
// Component's schema declares are asked, exactly as they are for a catalog
// Component.
//
// Of a drafted file only its kind is read to choose it, and a file that does not
// read as its kind is not consulted. Of a drafted entity the view reads what the
// catalog reads of its own: its identity, a Recipe's capability and
// spec.implements, a Component's extends and schema. What an entity renders,
// templates or means is never read.
//
// A drafted entity of an identity base holds is not consulted: the catalog's
// entity wins, as a catalog folder's entity shadows a published one of its
// identity. Of two drafted entities of one identity, the first drafted is the one
// consulted. A drafted Recipe whose spec.implements names a Component neither the
// drafts nor base hold is not consulted, and a warning names the Recipe, its file
// and the type; where base resolves no Component — a harness standing in for the
// catalog, which answers no catalog lookup — only the drafts' Components are
// known, and such a Recipe is consulted.
//
// The view answers every [RecipeIndex] method and, where base answers a catalog
// lookup ([owedCatalog]), that lookup too: base's, its Component, Recipe and
// extends-ancestry readings reaching the drafts after base. Where files declare
// no Component or Recipe the view would consult, it is base itself; a nil base, a
// harness holding no catalog, which no target is composed against, stays nil.
//
// It is a function of base and files alone, so whichever catalog view an import
// composes over, the drafts are read after it the same way.
func overlayDrafts(base RecipeIndex, files []abi.ScaffoldFile) (RecipeIndex, []abi.Diagnostic) {
	if base == nil {
		return nil, nil
	}
	o := &draftOverlay{base: base,
		drafts: &catalog.Catalog{Components: map[string]types.Component{}, Recipes: map[string]types.Recipe{}}}
	o.draftedComponent = o.drafts.CatalogLookup().Component
	withLookup, answersLookup := base.(owedCatalog)
	if answersLookup {
		o.lookup = withLookup.CatalogLookup()
		o.baseComponent = o.lookup.Component
	}

	type draftedRecipe struct {
		path   string
		recipe types.Recipe
	}
	var recipes []draftedRecipe
	for _, f := range files {
		switch draftKind(f.Content) {
		case entitykind.Component:
			c, err := catalog.ParseComponentEntity([]byte(f.Content), f.Path)
			if err != nil {
				continue
			}
			key := catalog.RecipeMappingURI(c.Metadata.Name, c.Metadata.Version)
			if _, held := o.drafts.Components[key]; held || o.baseHoldsComponent(c) {
				continue
			}
			o.drafts.Components[key] = c
		case entitykind.Recipe:
			r, err := catalog.ParseRecipeEntity([]byte(f.Content), f.Path)
			if err != nil {
				continue
			}
			recipes = append(recipes, draftedRecipe{path: f.Path, recipe: r})
		}
	}

	// The Recipes are judged once every drafted Component is in view, so a Recipe
	// drafted before the Component it implements finds it.
	var diags []abi.Diagnostic
	for _, d := range recipes {
		r := d.recipe
		key := catalog.RecipeIdentityURI(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
		if _, held := o.drafts.Recipes[key]; held || o.baseHoldsRecipe(key) {
			continue
		}
		if _, ok := o.component(r.Spec.Implements); !ok && o.baseComponent != nil {
			diags = append(diags, warnDiagnostic(fmt.Sprintf("the drafted Recipe %s in %s realizes no Component the "+
				"import can map an instance to: it implements the component type %s, which neither the drafts nor "+
				"the workspace's catalog hold", strconv.Quote(key), strconv.Quote(d.path),
				strconv.Quote(r.Spec.Implements))))
			continue
		}
		o.drafts.Recipes[key] = r
	}

	switch {
	case len(o.drafts.Components) == 0 && len(o.drafts.Recipes) == 0:
		return base, diags
	case answersLookup:
		return draftOverlayLookup{o}, diags
	}
	return o, diags
}

// draftOverlay is the view [overlayDrafts] returns over a base that answers no
// catalog lookup: base's readings first, then the drafted Components and Recipes
// it consults.
//
// The drafts are held in a catalog of their own, indexed as the catalog indexes
// its entities, so every reading of them — a type's version, the ladder a
// mapping value is resolved by, the Recipes implementing a type, the capabilities
// they bind to — is the catalog's own.
type draftOverlay struct {
	base RecipeIndex
	// lookup is base's catalog lookup, where base answers one.
	lookup resolver.CatalogLookup
	// baseComponent is base's resolution of a component type, nil where base
	// resolves none.
	baseComponent func(typeURI string) (types.Component, bool)
	// drafts holds the drafted Components and Recipes consulted, and
	// draftedComponent is its resolution of a component type.
	drafts           *catalog.Catalog
	draftedComponent func(typeURI string) (types.Component, bool)
}

// draftOverlayLookup is the view [overlayDrafts] returns over a base answering a
// catalog lookup, which it answers too.
type draftOverlayLookup struct {
	*draftOverlay
}

// CatalogLookup is base's catalog lookup with its Component, Recipe and
// extends-ancestry readings answered by the view, so the owed-value reading
// compiles a drafted Component's schema as it compiles a catalog Component's.
// Every other reading is base's.
func (o draftOverlayLookup) CatalogLookup() resolver.CatalogLookup {
	l := o.lookup
	l.Component = o.component
	l.Recipe = o.RecipeByURI
	l.ComponentAncestry = o.ComponentAncestry
	return l
}

// baseHoldsComponent reports whether base resolves a Component of c's identity:
// its name at its version.
func (o *draftOverlay) baseHoldsComponent(c types.Component) bool {
	if o.baseComponent == nil {
		return false
	}
	held, ok := o.baseComponent(catalog.RecipeMappingURI(c.Metadata.Name, c.Metadata.Version))
	return ok && held.Metadata.Name == c.Metadata.Name && held.Metadata.Version == c.Metadata.Version
}

// baseHoldsRecipe reports whether base resolves the Recipe of the catalog
// identity key, as that identity.
func (o *draftOverlay) baseHoldsRecipe(key string) bool {
	held, ok := o.base.RecipeByURI(key)
	return ok && catalog.RecipeIdentityURI(held.Spec.Capability, held.Metadata.Name, held.Metadata.Version) == key
}

// resolve resolves a component type through the view — to base's Component where
// base resolves it, else to the drafted one — and reports whether it is drafted.
func (o *draftOverlay) resolve(typeURI string) (comp types.Component, drafted, ok bool) {
	if o.baseComponent != nil {
		if c, found := o.baseComponent(typeURI); found {
			return c, false, true
		}
	}
	if c, found := o.draftedComponent(typeURI); found {
		return c, true, true
	}
	return types.Component{}, false, false
}

// component is the Component the view resolves a type to.
func (o *draftOverlay) component(typeURI string) (types.Component, bool) {
	c, _, ok := o.resolve(typeURI)
	return c, ok
}

// RecipeCapabilities answers the capabilities base's Recipes and the drafted
// ones bind to, each once and sorted.
func (o *draftOverlay) RecipeCapabilities() []string {
	out := append(slices.Clone(o.base.RecipeCapabilities()), o.drafts.RecipeCapabilities()...)
	slices.Sort(out)
	return slices.Compact(out)
}

// RecipesImplementing answers base's Recipes implementing componentType, then
// the drafted ones.
//
// A type naming no version is asked of both at the version the view resolves it
// to, so the two answer for one version — base's where base holds the type, the
// drafted Component's where only the drafts do.
//
// 🔴 DELIBERATE NEAR-DUPLICATE of the catalog's own request rule
// (implementsRequestAt in pkg/catalog): a type pinning a version is asked as
// written, and one pinning none at the version resolution selects. Each catalog
// applies that rule over its own Components, and here the type is resolved
// across two of them, base before the drafts, so the version is settled here and
// handed to each pinned, which each then asks as written.
func (o *draftOverlay) RecipesImplementing(componentType string) []types.Recipe {
	request := componentType
	if _, pinned := types.ParseVersionPin(componentType).Pin(); !pinned {
		if comp, _, ok := o.resolve(componentType); ok {
			request = catalog.RecipeMappingURI(comp.Metadata.Name, comp.Metadata.Version)
		}
	}
	return append(slices.Clone(o.base.RecipesImplementing(request)), o.drafts.RecipesImplementing(request)...)
}

// FeatureRecipesImplementing is base's: an import's drafts consulted beside the
// catalog are Components and Recipes.
func (o *draftOverlay) FeatureRecipesImplementing(featureType string) []types.FeatureRecipe {
	return o.base.FeatureRecipesImplementing(featureType)
}

// RecipeByURI resolves a mapping value through base, and through the drafted
// Recipes by the catalog's own ladder where base resolves nothing.
func (o *draftOverlay) RecipeByURI(uri string) (types.Recipe, bool) {
	if r, ok := o.base.RecipeByURI(uri); ok {
		return r, true
	}
	return o.drafts.RecipeByURI(uri)
}

// FeatureRecipeByURI is base's.
func (o *draftOverlay) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	return o.base.FeatureRecipeByURI(uri)
}

// ComponentAncestry answers base's ancestry of a type the view resolves through
// base, or resolves through neither. For a drafted type it is the drafted
// Component's own extends chain, each link resolved through the view — into the
// catalog where a link names a catalog Component — walked by the walk the build's
// inheritance merge takes ([validator.ComponentAncestry]), nearest base first and
// each written as a mapping key names that type; a chain that cannot be walked
// answers its fault.
func (o *draftOverlay) ComponentAncestry(componentType string) ([]string, error) {
	comp, drafted, ok := o.resolve(componentType)
	if !ok || !drafted {
		return o.base.ComponentAncestry(componentType)
	}
	chain, errs := validator.ComponentAncestry(comp, o.component)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	keys := make([]string, 0, len(chain))
	for _, anc := range chain {
		keys = append(keys, catalog.RecipeMappingURI(anc.Metadata.Name, anc.Metadata.Version))
	}
	return keys, nil
}

// AggregateLookup is base's.
func (o *draftOverlay) AggregateLookup() resolver.AggregateLookup {
	return o.base.AggregateLookup()
}
