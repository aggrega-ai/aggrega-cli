// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/imageref"
)

// imageLocal is the locals entry a container product's Recipe holds its
// default image in, and imageKey the infra key under which a Release writes the
// whole image the Recipe runs instead, as the published container libraries
// name them.
const (
	imageLocal = "default_image"
	imageKey   = "image"
)

// imageRowOf returns the image row of the Recipe r, a container product's: its
// locals name a literal default image, whose repository, read as the Compose
// image table reads it, is the row's. The image key is imageKey where the
// Recipe's valuesMapping or fragments, every library they import resolved,
// read that infra key, and empty where they read none. It reports false for a
// Recipe whose locals name no default image, or one that is no image
// reference, holding a template action.
func imageRowOf(r recipeSource) (cloudimport.ImageRow, bool) {
	image, ok := r.locals[imageLocal]
	if !ok {
		return cloudimport.ImageRow{}, false
	}
	repository, ok := imageref.Repository(image)
	if !ok {
		return cloudimport.ImageRow{}, false
	}
	row := cloudimport.ImageRow{Repository: repository, Component: r.component, Recipe: r.name}
	texts := []string{r.valuesMapping}
	for _, f := range r.fragments {
		texts = append(texts, f.text)
	}
	for _, text := range texts {
		if readsInfraKey(text, imageKey) {
			row.ImageKey = imageKey
			break
		}
	}
	return row, true
}

// readsInfraKey reports whether text reads the infra key key: as a field,
// .infra.<key> or a path below it, or through optional, index or hasKey
// handed .infra and the key as a string literal. Text that does not parse
// reads nothing.
func readsInfraKey(text, key string) bool {
	if text == "" {
		return false
	}
	root, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return false
	}
	field := ".infra." + key
	for _, path := range gotmpl.TreeFieldAccesses(root) {
		if path == field || strings.HasPrefix(path, field+".") {
			return true
		}
	}
	found := false
	eachCommand(root, func(args []argument) {
		found = found || len(args) >= 3 && args[0].kind == argIdentifier &&
			slices.Contains([]string{"optional", "index", "hasKey"}, args[0].text) &&
			args[1].kind == argField && args[1].text == ".infra" && args[2].kind == argString && args[2].text == key
	})
	return found
}

// imageConflict is two or more Recipes naming one image repository.
type imageConflict struct {
	repositories []string
	recipes      [][]string
}

// Error names each repository with the Recipes naming it, and what to do.
func (c *imageConflict) Error() string {
	var lines []string
	for i, repository := range c.repositories {
		lines = append(lines, "the image repository "+repository+" is named by the Recipes "+
			strings.Join(c.recipes[i], " and "))
	}
	return strings.Join(lines, "; ") + "; an image maps to one container product's Recipe, and the hub's text does " +
		"not say which, so no table is written: name a repository in one of them alone"
}

// generateImages generates the image rows of h's capability: one for each
// Recipe, read at the version a reference naming it without one resolves to,
// whose locals name a literal default image, ordered by repository. A Recipe
// implementing a Component the hub holds no version of is refused, and so are
// two Recipes naming one repository.
func generateImages(h *hubCatalog) ([]cloudimport.ImageRow, error) {
	var (
		rows    []cloudimport.ImageRow
		missing missingComponent
	)
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok {
			continue
		}
		src, err := h.read(r)
		if err != nil {
			return nil, err
		}
		row, ok := imageRowOf(src)
		if !ok {
			continue
		}
		if _, ok := h.labels[row.Component]; !ok {
			missing.recipes = append(missing.recipes, h.capability+"/"+name+":"+r.Metadata.Version)
			missing.components = append(missing.components, row.Component)
			continue
		}
		rows = append(rows, row)
	}
	if len(missing.recipes) > 0 {
		return nil, &missing
	}
	slices.SortFunc(rows, func(a, b cloudimport.ImageRow) int {
		return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Recipe, b.Recipe))
	})
	var conflict imageConflict
	for i := 0; i < len(rows); {
		j := i + 1
		for j < len(rows) && rows[j].Repository == rows[i].Repository {
			j++
		}
		if j-i > 1 {
			var recipes []string
			for _, r := range rows[i:j] {
				recipes = append(recipes, h.capability+"/"+r.Recipe+" implementing "+r.Component)
			}
			conflict.repositories = append(conflict.repositories, rows[i].Repository)
			conflict.recipes = append(conflict.recipes, recipes)
		}
		i = j
	}
	if len(conflict.repositories) > 0 {
		return nil, &conflict
	}
	return rows, nil
}

// generateImagesFromHub generates f's image rows from the checkout at hub.
func generateImagesFromHub(hub string, f format) ([]cloudimport.ImageRow, error) {
	h, err := loadHub(hub, f.capability)
	if err != nil {
		return nil, err
	}
	return generateImages(h)
}
