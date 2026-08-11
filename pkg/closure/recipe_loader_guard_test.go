// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardBareLookup answers the four resolution surfaces and nothing else. It
// stands for a Lookup whose backing cannot read a Recipe / FeatureRecipe /
// Plugin body — the case RequireRecipeLoader exists to refuse, and the case that
// would otherwise reach a recipe pass as a nil loader and be skipped in silence.
type guardBareLookup struct{}

func (guardBareLookup) ResolveCatalogEntity(string, string) (closure.Resolution, error) {
	return closure.Resolution{}, nil
}

func (guardBareLookup) ResolveAggregate(string) (closure.Resolution, error) {
	return closure.Resolution{}, nil
}

func (guardBareLookup) ResolveLibrary(closure.LibraryAxis, string) (closure.LibraryResolution, error) {
	return closure.LibraryResolution{}, nil
}

func (guardBareLookup) ResolvePlugin(string, string) (closure.Resolution, error) {
	return closure.Resolution{}, nil
}

// guardServingLookup is a bare lookup that also hands out a recipe loader, the
// shape a catalog-backed implementation has. A zero-valued loader field models
// an implementation that carries the surface but yields nothing through it.
type guardServingLookup struct {
	guardBareLookup
	loader closure.RecipeLoader
}

func (l guardServingLookup) ClosureRecipeLoader() closure.RecipeLoader { return l.loader }

// guardRecipeLoader returns a body carrying the name it was built with, so the
// passing control can prove the guard handed back a loader that actually loads
// rather than merely a non-nil value.
type guardRecipeLoader struct{ name string }

func (l *guardRecipeLoader) LoadRecipe(string) (*types.Recipe, error) {
	r := &types.Recipe{}
	r.Metadata.Name = l.name
	return r, nil
}

func (l *guardRecipeLoader) LoadFeatureRecipe(string) (*types.FeatureRecipe, error) {
	return &types.FeatureRecipe{}, nil
}

func (l *guardRecipeLoader) LoadPlugin(string) (*types.Plugin, error) {
	return &types.Plugin{}, nil
}

func TestRequireRecipeLoaderRefusesALookupWithNoRecipeSurface(t *testing.T) {
	loader, err := closure.RequireRecipeLoader(guardBareLookup{})
	if err == nil {
		t.Fatal("RequireRecipeLoader accepted a lookup that cannot serve recipe bodies; a guard that cannot refuse is a call")
	}
	if !errors.Is(err, closure.ErrNoRecipeLoader()) {
		t.Fatalf("refusal does not carry ErrNoRecipeLoader: %v", err)
	}
	if loader != nil {
		t.Fatalf("refusal returned a loader anyway: %#v", loader)
	}
}

// A lookup carrying the surface but yielding nothing through it is refused on
// the same footing as one missing the surface: both leave a recipe pass with no
// way to read bodies, and the pass reads either as "no loader was wired".
func TestRequireRecipeLoaderRefusesALookupThatYieldsNoLoader(t *testing.T) {
	loader, err := closure.RequireRecipeLoader(guardServingLookup{})
	if err == nil {
		t.Fatal("RequireRecipeLoader accepted a lookup whose recipe surface yields nothing")
	}
	if !errors.Is(err, closure.ErrNoRecipeLoader()) {
		t.Fatalf("refusal does not carry ErrNoRecipeLoader: %v", err)
	}
	if loader != nil {
		t.Fatalf("refusal returned a loader anyway: %#v", loader)
	}
}

func TestRequireRecipeLoaderReturnsAUsableLoader(t *testing.T) {
	served := &guardRecipeLoader{name: "guarded-recipe"}

	loader, err := closure.RequireRecipeLoader(guardServingLookup{loader: served})
	if err != nil {
		t.Fatalf("RequireRecipeLoader refused a lookup that can serve recipe bodies: %v", err)
	}
	if loader == nil {
		t.Fatal("RequireRecipeLoader returned no error and no loader")
	}

	got, err := loader.LoadRecipe("hub.aggrega.ai/recipe/example/svc:1.0.0")
	if err != nil {
		t.Fatalf("loading through the returned loader: %v", err)
	}
	if got.Metadata.Name != served.name {
		t.Fatalf("returned loader is not the one the lookup serves: loaded %q, want %q", got.Metadata.Name, served.name)
	}
}
