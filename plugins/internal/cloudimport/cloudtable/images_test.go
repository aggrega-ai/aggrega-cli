// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/imageref"
)

// productRecipe writes an arm Recipe named name implementing the compute
// Component, holding locals as written and a fragment of no resource.
func productRecipe(name, locals string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name + "\n  version: 1.0.0\n" +
		"spec:\n  capability: arm\n  implements: core/compute/acme-app:1.0.0\n  locals:\n" + locals +
		"  artifacts:\n    product.yaml: |\n      resources: {}\n"
}

// TestImageRowsOfTheMiniatureHub generates the image rows of the miniature
// hub, two of whose Recipes name a literal default image in their locals, one
// reading the Release's whole image from infra.image and one not, and requires
// a row for each, ordered by repository, with the repository as the Compose
// image table reads it.
func TestImageRowsOfTheMiniatureHub(t *testing.T) {
	t.Parallel()
	images, err := generateImagesFromHub(miniatureHub, armFormatOrFail(t))
	if err != nil {
		t.Fatalf("generate the image rows: %v", err)
	}
	want := []cloudimport.ImageRow{
		{Repository: "ghcr.io/acme/product", Component: "core/compute/acme-app", Recipe: "acme-product",
			ImageKey: "image"},
		{Repository: "ghcr.io/acme/tagless", Component: "core/compute/acme-app", Recipe: "acme-product-tagless"},
	}
	if !reflect.DeepEqual(images, want) {
		t.Errorf("the miniature hub generated the image rows\n%#v\nwant\n%#v", images, want)
	}
}

// TestAnImageRowRecordsTheKeyTheRecipeReadsTheWholeImageFrom generates the
// image row of a Recipe whose locals hold default_image, reading the
// Release's whole image from infra.image — in its own text, as a field or
// through optional, or through the mapping library it imports — or reading
// none, and requires a row recording the key image where it reads it and no
// key where it does not.
func TestAnImageRowRecordsTheKeyTheRecipeReadsTheWholeImageFrom(t *testing.T) {
	t.Parallel()
	locals := "    default_image: ghcr.io/acme/extra:1.0.0\n"
	output := func(value string) string {
		return strings.Replace(productRecipe("extra", locals), "      resources: {}\n",
			"      resources: {}\n      outputs: {image: {type: string, value: '"+value+"'}}\n", 1)
	}
	for _, row := range []struct {
		scenario, recipe, want string
	}{
		{"a field", output("{{ .infra.image }}"), "image"},
		{"optional", output(`{{ toString (optional .infra "image" .locals.default_image) }}`), "image"},
		{"an imported library", "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: extra\n" +
			"  version: 1.0.0\nspec:\n  capability: arm\n  implements: core/compute/acme-app:1.0.0\n  locals:\n" +
			locals + "  source:\n    kind: bicep-module-oci\n    url: oci://mcr.microsoft.com/bicep/avm/res/acme/extra\n" +
			"    version: 0.1.0\n  valuesMapping:\n    $import: AcmeAppParameters#/mappings/App\n", "image"},
		{"no read", output("{{ .locals.default_image }}"), ""},
		{"another key", output("{{ .infra.image_tag }}"), ""},
	} {
		root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/extra/1.0.0/recipe.yaml": row.recipe})
		images, err := generateImagesFromHub(root, armFormatOrFail(t))
		if err != nil {
			t.Fatalf("%s: generate the image rows: %v", row.scenario, err)
		}
		i := slices.IndexFunc(images, func(r cloudimport.ImageRow) bool { return r.Recipe == "extra" })
		if i < 0 {
			t.Errorf("%s: no row is generated for the Recipe", row.scenario)
			continue
		}
		if images[i].Repository != "ghcr.io/acme/extra" || images[i].ImageKey != row.want {
			t.Errorf("%s: the row is %#v, want the repository ghcr.io/acme/extra and the key %q", row.scenario,
				images[i], row.want)
		}
	}
}

// TestAnImageThatIsNotLiteralGivesNoImageRow requires a default_image holding
// a template action, one that is no image reference, and an image named in an
// entry other than default_image — image among them — to give no row.
func TestAnImageThatIsNotLiteralGivesNoImageRow(t *testing.T) {
	t.Parallel()
	for name, locals := range map[string]string{
		"templated":       "    default_image: '{{ .infra.registry }}/acme/templated:1'\n",
		"no image":        "    port: \"8080\"\n",
		"named image":     "    image: ghcr.io/acme/other:1\n",
		"not a reference": "    default_image: two words\n",
	} {
		root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/extra/1.0.0/recipe.yaml": productRecipe("extra", locals)})
		images, err := generateImagesFromHub(root, armFormatOrFail(t))
		if err != nil {
			t.Fatalf("%s: generate the image rows: %v", name, err)
		}
		for _, r := range images {
			if r.Recipe == "extra" {
				t.Errorf("%s: a row %#v is generated for it", name, r)
			}
		}
	}
}

// TestTwoProductsClaimingOneRepositoryAreRefused adds a Recipe naming, with
// another tag, the repository another names, and requires a refusal
// naming the repository and both Recipes, with no rows.
func TestTwoProductsClaimingOneRepositoryAreRefused(t *testing.T) {
	t.Parallel()
	root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/rival/1.0.0/recipe.yaml": productRecipe("rival",
		"    default_image: ghcr.io/acme/product:2.0.0\n")})
	images, err := generateImagesFromHub(root, armFormatOrFail(t))
	if repo, ok := imageref.Repository("ghcr.io/acme/product:2.0.0"); !ok || repo != "ghcr.io/acme/product" {
		t.Fatalf("the fixture's repository reads as %q, %v", repo, ok)
	}
	var conflict *imageConflict
	if !errors.As(err, &conflict) || images != nil {
		t.Fatalf("two products of one repository generated %v, %v; want a refusal", images, err)
	}
	for _, want := range []string{"ghcr.io/acme/product", "acme-product", "rival"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestAProductImplementingAnAbsentComponentIsRefused requires an image row's
// Recipe to implement a Component the hub holds.
func TestAProductImplementingAnAbsentComponentIsRefused(t *testing.T) {
	t.Parallel()
	body := strings.Replace(productRecipe("ghost", "    default_image: ghcr.io/acme/ghost\n"), "core/compute/acme-app",
		"core/compute/acme-absent", 1)
	root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/ghost/1.0.0/recipe.yaml": body})
	_, err := generateImagesFromHub(root, armFormatOrFail(t))
	var missing *missingComponent
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("a product implementing an absent Component gave %v; want a refusal naming it", err)
	}
}

// TestTheArmImageRowsAreWhatTheHubGenerates requires the arm importer's image
// rows to be what the hub's arm Recipes generate.
func TestTheArmImageRowsAreWhatTheHubGenerates(t *testing.T) {
	t.Parallel()
	images, err := generateImagesFromHub(hub(t), armFormatOrFail(t))
	if err != nil {
		t.Fatalf("generate the arm image rows from %s: %v", hub(t), err)
	}
	if got := armimport.Table().Images; !slices.Equal(got, images) {
		t.Errorf("the committed arm image rows are\n%#v\nand the hub generates\n%#v", got, images)
	}
}
