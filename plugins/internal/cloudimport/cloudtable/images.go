// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composeimport"
)

// imageLocal is the locals entry a container product's Recipe holds its image
// default in, and imageTagKey the infra key such a Recipe reads the image's
// tag from where it reads one.
const (
	imageLocal  = "image"
	imageTagKey = "image_tag"
)

// imageTagRead matches an action reading the infra key the image's tag is
// written under.
var imageTagRead = regexp.MustCompile(`\{\{[^}]*\.infra\.` + imageTagKey + `\b`)

// imageRowOf returns the image row of the Recipe r, a container product's: its
// locals name a literal image default, whose repository, read as the Compose
// image table reads it, is the row's. The tag key is the infra key the
// Recipe's valuesMapping or fragments read the image's tag from, empty where
// they read none. It reports false for a Recipe whose locals name no image, or
// one that is no image reference, holding a template action.
func imageRowOf(r recipeSource) (cloudimport.ImageRow, bool) {
	image, ok := r.locals[imageLocal]
	if !ok {
		return cloudimport.ImageRow{}, false
	}
	repository, ok := composeimport.Repository(image)
	if !ok {
		return cloudimport.ImageRow{}, false
	}
	row := cloudimport.ImageRow{Repository: repository, Component: r.component, Recipe: r.name}
	for _, text := range append([]string{r.valuesMapping}, r.fragments...) {
		if imageTagRead.MatchString(text) {
			row.TagKey = imageTagKey
			break
		}
	}
	return row, true
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
// whose locals name a literal image default, ordered by repository. A Recipe
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
