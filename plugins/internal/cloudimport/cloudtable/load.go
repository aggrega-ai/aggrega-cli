// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/libraryimport"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// Where a hub checkout keeps, beneath its root, the Components, the schema
// libraries they read, and the mapping and artifact libraries a Recipe imports.
var (
	componentDir       = filepath.Join("aggrega-catalog", "component")
	schemaLibraryDir   = filepath.Join("aggrega-catalog", "schemalibrary")
	mappingLibraryDir  = filepath.Join("aggrega-catalog", "mappinglibrary")
	artifactLibraryDir = filepath.Join("aggrega-catalog", "artifactlibrary")
)

// recipeDir and featureRecipeDir are where a hub checkout keeps the Recipes and
// the FeatureRecipes of a capability.
func recipeDir(capability string) string {
	return filepath.Join("aggrega-catalog", "recipe", capability)
}

func featureRecipeDir(capability string) string {
	return filepath.Join("aggrega-catalog", "feature-recipe", capability)
}

// readDirs returns every directory beneath a hub checkout's root the generator
// reads for a capability.
func readDirs(capability string) []string {
	return []string{recipeDir(capability), featureRecipeDir(capability), componentDir, schemaLibraryDir,
		mappingLibraryDir, artifactLibraryDir}
}

// hubCatalog is a hub checkout's catalog as the generator reads it for one
// capability: loaded by the compiler's own loader from the capability's Recipes
// and FeatureRecipes, the Components and the libraries, and the labels of each
// Component as an import is handed them.
type hubCatalog struct {
	root, capability string
	cat              *catalog.Catalog
	libs             catalog.LibrarySet
	// labels holds each Component's labels by name, at the version a reference
	// naming it without one resolves to, its extends chain resolved.
	labels map[string]map[string]string
	// requires holds, by the same name and version, the values keys each
	// Component requires and declares no default for, with their types.
	requires map[string][]cloudimport.Requirement
}

// loadHub loads the catalog of the hub checkout at root for capability.
func loadHub(root, capability string) (*hubCatalog, error) {
	if info, err := os.Stat(filepath.Join(root, recipeDir(capability))); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s holds no %s Recipe under %s", root, capability, recipeDir(capability))
	}
	var dirs []string
	for _, d := range readDirs(capability) {
		if info, err := os.Stat(filepath.Join(root, d)); err == nil && info.IsDir() {
			dirs = append(dirs, filepath.Join(root, d))
		}
	}
	cat, err := catalog.LoadAll("", dirs...)
	if err != nil {
		return nil, fmt.Errorf("load the %s Recipes of %s and what they read: %w", capability, root, err)
	}
	h := &hubCatalog{root: root, capability: capability, cat: cat, libs: cat.DefaultLibraries(),
		labels: map[string]map[string]string{}, requires: map[string][]cloudimport.Requirement{}}
	if view := importersocket.ProjectCatalog(cat); view != nil {
		for _, c := range view.Components {
			h.labels[c.Name] = c.Labels
			reqs, err := h.requiredValues(c.Name)
			if err != nil {
				return nil, err
			}
			h.requires[c.Name] = reqs
		}
	}
	return h, nil
}

// requiredValues returns the values keys the Component named name requires and
// declares no default for, at the version a reference naming it without one
// resolves to, its contract compiled as a build compiles it
// ([resolver.CompileComponentContract]): each a requirement of the key under
// values, with the type its schema declares, in the order its schema lists
// them. A declared contract that does not compile is refused, naming the
// Component and why.
func (h *hubCatalog) requiredValues(name string) ([]cloudimport.Requirement, error) {
	compiled, declared, err := resolver.CompileComponentContract(h.cat.CatalogLookup(), name)
	if err != nil {
		return nil, fmt.Errorf("compile the schema of the Component %s: %w", name, err)
	}
	if !declared {
		return nil, nil
	}
	var out []cloudimport.Requirement
	for _, f := range compactform.PillarRequired(compiled, "values") {
		typ, _ := f.Schema["type"].(string)
		out = append(out, cloudimport.Requirement{Key: "values." + f.Name, Type: typ})
	}
	return out, nil
}

// recipeNames returns the name of every Recipe of the capability the hub
// holds, each once and sorted.
func (h *hubCatalog) recipeNames() []string {
	names := map[string]bool{}
	for _, r := range h.cat.Recipes {
		if types.ParseVersionPin(r.Spec.Capability).Base() == h.capability {
			names[r.Metadata.Name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// featureRecipeNames returns the name of every FeatureRecipe of the capability
// the hub holds, each once and sorted.
func (h *hubCatalog) featureRecipeNames() []string {
	names := map[string]bool{}
	for _, r := range h.cat.FeatureRecipes {
		if types.ParseVersionPin(r.Spec.Capability).Base() == h.capability {
			names[r.Metadata.Name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// recipe returns the version of the capability's Recipe named name a reference
// naming it without a version resolves to.
func (h *hubCatalog) recipe(name string) (types.Recipe, bool) {
	return h.cat.RecipeByURI(h.capability + "/" + name)
}

// importError is a Recipe's import the hub's catalog does not resolve.
type importError struct {
	identity, path, ref string
	err                 error
}

// Error names the Recipe, its file and the reference, and what to fix.
func (e *importError) Error() string {
	return fmt.Sprintf("the Recipe %s (%s) imports %s, which this checkout does not resolve: %v; publish what it "+
		"names in the hub or correct the reference, and no table is written", e.identity, e.path, e.ref, e.err)
}

// Unwrap returns the resolution's own error, which names its condition.
func (e *importError) Unwrap() error { return e.err }

// read returns what an extractor reads of r, its valuesMapping and every file
// of its artifacts resolved as a build resolves them: a library entry through
// the catalog's libraries, and a file at the path the catalog's load anchored
// to the Recipe's directory. An import that does not resolve is refused,
// naming r and the reference.
func (h *hubCatalog) read(r types.Recipe) (recipeSource, error) {
	identity := h.capability + "/" + r.Metadata.Name + ":" + r.Metadata.Version
	rel := filepath.Join(recipeDir(h.capability), r.Metadata.Name, r.Metadata.Version, "recipe.yaml")
	path := filepath.ToSlash(rel)
	if _, err := os.Stat(filepath.Join(h.root, rel)); err != nil {
		path = identity
	}
	component, _, _ := strings.Cut(r.Spec.Implements, ":")
	out := recipeSource{name: r.Metadata.Name, path: path, component: component, locals: map[string]string{}}
	if source, ok := r.Spec.Source.(map[string]any); ok {
		out.source = source
	}
	locals := r.Spec.Locals
	for _, k := range locals.Keys() {
		out.locals[k], _ = locals.Get(k)
	}
	provides := r.Spec.Provides
	for _, k := range provides.Keys() {
		v, _ := provides.Get(k)
		out.provides = append(out.provides, entry{key: k, value: v})
	}
	assertions := r.Spec.Assertions
	for _, k := range assertions.Keys() {
		v, _ := assertions.Get(k)
		out.assertions = append(out.assertions, v)
	}
	refuse := func(ref string, err error) (recipeSource, error) {
		return recipeSource{}, &importError{identity: identity, path: path, ref: ref, err: err}
	}

	vm := r.Spec.ValuesMapping
	switch {
	case vm.Inline != "":
		out.valuesMapping = vm.Inline
	case vm.IsLibraryImport() || vm.HasOCIPrefix():
		text, err := libraryimport.Mapping(vm.Import, h.libs.Mappings, nil)
		if err != nil {
			return refuse(vm.Import, err)
		}
		out.valuesMapping = text
	case vm.Import != "":
		text, err := os.ReadFile(vm.Import)
		if err != nil {
			return refuse(vm.Import, err)
		}
		out.valuesMapping = string(text)
	}

	artifacts := r.Spec.Artifacts
	for _, k := range artifacts.Keys() {
		a, _ := artifacts.Get(k)
		if a.IsLibraryImport() || a.HasOCIPrefix() {
			bundle, err := libraryimport.Artifact(a.Import, h.libs.Artifacts)
			if err != nil {
				return refuse(a.Import, err)
			}
			for _, name := range bundle.Files.Keys() {
				body, _ := bundle.Files.Get(name)
				out.fragments = append(out.fragments, body)
			}
			continue
		}
		text, err := a.Text()
		if err != nil {
			return refuse(a.Import, err)
		}
		if text != "" {
			out.fragments = append(out.fragments, text)
		}
	}

	if document, ok := h.cat.EntityBytes("Recipe", identity); ok {
		out.document = string(document)
	}
	return out, nil
}
