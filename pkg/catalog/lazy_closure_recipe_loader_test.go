// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// recipeLoaderWithin runs the guard against a real closure lookup and fails if
// it has not returned within the budget.
//
// The budget is the point of the helper. The lazy view guards its state with a
// plain, non-reentrant mutex, and every method of its closure lookup takes that
// mutex; a delegation written through another method that takes it too would
// block on the second acquisition and never return. Left to the package's own
// timeout that shows up as a whole package hanging with no failing test named,
// so the wait is bounded here and the failure says what it means.
func recipeLoaderWithin(t *testing.T, lookup closure.Lookup, budget time.Duration) closure.RecipeLoader {
	t.Helper()

	type outcome struct {
		loader closure.RecipeLoader
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		loader, err := closure.RequireRecipeLoader(lookup)
		done <- outcome{loader: loader, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("RequireRecipeLoader refused a production closure lookup: %v", got.err)
		}
		if got.loader == nil {
			t.Fatal("RequireRecipeLoader returned no error and no loader")
		}
		return got.loader
	case <-time.After(budget):
		t.Fatalf("RequireRecipeLoader did not return within %s: the view's lock is taken twice on one path", budget)
		return nil
	}
}

// emptyCatalogWorkspace is a workspace with a catalog directory and no entities
// in it. The recipe surface is reachable regardless of what the directory holds,
// which is what these tests exercise; body loading is covered where the loader's
// own behaviour is.
func emptyCatalogWorkspace(t *testing.T) (workspaceRoot, cacheDir, catalogDir string) {
	t.Helper()
	workspaceRoot = t.TempDir()
	catalogDir = filepath.Join(workspaceRoot, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("creating the catalog directory: %v", err)
	}
	return workspaceRoot, ".aggrega/cache", catalogDir
}

// The lazy view's closure lookup is the value the build and serve reconstruction
// paths pass, so it is the one the guard has to accept.
func TestRequireRecipeLoaderOverTheLazyClosureLookup(t *testing.T) {
	ws, cacheDir, catalogDir := emptyCatalogWorkspace(t)
	view := catalog.NewLazyLookup(ws, cacheDir, []string{catalogDir}, nil)

	recipeLoaderWithin(t, view.ClosureLookup(nil, ws, cacheDir, nil, nil, nil), 30*time.Second)
}

// A lookup carrying prior pins is built with a reuse verification rather than
// without one — the shape the pull's walk and the workspace-socket reconstruction
// pass — and its base is a differently-configured lookup, so it is covered on its
// own footing rather than by inference from the one carrying none.
func TestRequireRecipeLoaderOverThePinnedLazyClosureLookup(t *testing.T) {
	ws, cacheDir, catalogDir := emptyCatalogWorkspace(t)
	view := catalog.NewLazyLookup(ws, cacheDir, []string{catalogDir}, nil)

	pinned := view.ClosureLookup(nil, ws, cacheDir, nil, nil, &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{}})
	recipeLoaderWithin(t, pinned, 30*time.Second)
}

// The eagerly-loaded catalog's closure lookup is the other implementation the
// guard has to accept.
func TestRequireRecipeLoaderOverTheEagerClosureLookup(t *testing.T) {
	ws, cacheDir, catalogDir := emptyCatalogWorkspace(t)
	cat, _, err := catalog.LoadPassOneTolerant(ws, cacheDir, catalogDir)
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}

	recipeLoaderWithin(t, cat.ClosureLookup(nil, ws, cacheDir), 30*time.Second)
}

// A pin change drops the lazy view's enumerated recipe set, so the loader has to
// be derived when it is asked for. Asking on either side of a pin change must
// still return a loader rather than deadlocking against the re-fold.
func TestRequireRecipeLoaderSurvivesAPinChange(t *testing.T) {
	ws, cacheDir, catalogDir := emptyCatalogWorkspace(t)
	view := catalog.NewLazyLookup(ws, cacheDir, []string{catalogDir}, nil)
	lookup := view.ClosureLookup(nil, ws, cacheDir, nil, nil, nil)

	recipeLoaderWithin(t, lookup, 30*time.Second)
	view.SetPins(map[string]catalog.Pin{})
	recipeLoaderWithin(t, lookup, 30*time.Second)
}
