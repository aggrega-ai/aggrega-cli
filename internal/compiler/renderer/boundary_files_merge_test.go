// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// boundaryArtifactsTestRecipe is a terraform Recipe whose boundary block carries
// one inline entry and one library import.
func boundaryArtifactsTestRecipe() *types.Recipe {
	shared := types.OrderedMap[types.ArtifactEntry]{}
	shared.Set("providers.tf", types.ArtifactEntry{Inline: "provider \"azurerm\" {\n  features {}\n}\n"})
	shared.Set("shared", types.ArtifactEntry{Import: "Shared#/artifacts/bundle", AuthoredImport: "Shared#/artifacts/bundle"})
	return &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "apim", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "terraform",
			Implements: "core/api-gateway",
			Boundary:   types.RecipeSpecBoundary{Artifacts: shared},
		},
	}
}

// TestMergeBoundaryFiles is the merge's table: what a dispatch group's shared-file
// declarations reduce to before they are placed. Identical bytes under one name are
// one placement naming every contributor; different bytes under one name are one
// placement per contributor, so the rule over the group's output sees each of them;
// and the output follows the order names were first declared, whatever order the
// names sort in.
func TestMergeBoundaryFiles(t *testing.T) {
	t.Parallel()

	file := func(name, content string) boundaryArtifact {
		return boundaryArtifact{name: name, content: []byte(content)}
	}
	cases := []struct {
		name     string
		declared []boundaryFileContribution
		want     []boundaryFileContribution
	}{
		{
			name: "one contributor's file is placed as declared",
			declared: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api"},
			},
			want: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api"},
			},
		},
		{
			name: "identical bytes under one name are one file belonging to every contributor",
			declared: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api"},
				{file: file("providers.tf", "a"), producer: "component web"},
				{file: file("providers.tf", "a"), producer: "component worker"},
			},
			want: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api, component web and component worker"},
			},
		},
		{
			name: "different bytes under one name are placed once per contributor",
			declared: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api"},
				{file: file("providers.tf", "a"), producer: "component web"},
				{file: file("providers.tf", "b"), producer: "component worker"},
			},
			want: []boundaryFileContribution{
				{file: file("providers.tf", "a"), producer: "component api"},
				{file: file("providers.tf", "a"), producer: "component web"},
				{file: file("providers.tf", "b"), producer: "component worker"},
			},
		},
		{
			name: "names come out in the order first declared, and each name is merged on its own",
			declared: []boundaryFileContribution{
				{file: file("versions.tf", "v"), producer: "component web"},
				{file: file("providers.tf", "a"), producer: "component web"},
				{file: file("providers.tf", "a"), producer: "component api"},
				{file: file("versions.tf", "w"), producer: "component api"},
			},
			want: []boundaryFileContribution{
				{file: file("versions.tf", "v"), producer: "component web"},
				{file: file("versions.tf", "w"), producer: "component api"},
				{file: file("providers.tf", "a"), producer: "component web and component api"},
			},
		},
		{
			name:     "no declarations place nothing",
			declared: nil,
			want:     nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := mergeBoundaryFiles(c.declared); !reflect.DeepEqual(got, c.want) {
				t.Errorf("mergeBoundaryFiles:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// TestBoundaryArtifactEntriesAreEnumeratedWithTheRecipesTemplateFields holds the
// boundary block's inline entries to the one enumeration the rules over a Recipe's
// template fields are invoked along: each is presented under its own label family,
// positioned at its key in the block, read with the delimiter pair an artifacts
// entry is read with, and an imported entry carries no text and is absent.
func TestBoundaryArtifactEntriesAreEnumeratedWithTheRecipesTemplateFields(t *testing.T) {
	t.Parallel()

	recipe := boundaryArtifactsTestRecipe()
	var got []RecipeTemplateField
	for _, f := range RecipeTemplateFields(recipe) {
		if f.Label == "boundary.artifacts[providers.tf]" || f.Label == "boundary.artifacts[shared]" {
			got = append(got, f)
		}
	}
	want := []RecipeTemplateField{{
		Label:        "boundary.artifacts[providers.tf]",
		Text:         "provider \"azurerm\" {\n  features {}\n}\n",
		AuthoredKeys: []string{"boundary", "artifacts", "providers.tf"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enumerated boundary fields:\n got %+v\nwant %+v", got, want)
	}
	if !RecipeTemplateFieldReadsWithArtifactDelimiters("boundary.artifacts[providers.tf]") {
		t.Error("a boundary block entry is not read with the artifact delimiter pair it is evaluated with")
	}
	if RecipeTemplateFieldReadsWithArtifactDelimiters("valuesMapping") {
		t.Error("the values mapping is read with the artifact delimiter pair")
	}
}
