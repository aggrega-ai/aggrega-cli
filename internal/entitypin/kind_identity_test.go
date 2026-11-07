// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestARecipeAndAFeatureRecipeOfOneNameEachPinTheirOwnDocument drives the step over a
// Recipe and a FeatureRecipe that share their identity, each in a directory of its
// own, each authoring a source and importing a file of its own.
//
// Named by kind, they are two entities: each is planned, each is handed the source
// it authored and no other, and the digest the step returns for each identity is the
// row digest of that entity's own document. A step that read both by name alone
// would plan the one its first loader answered for and hand the other's identity
// nothing — or the same document's digest.
func TestARecipeAndAFeatureRecipeOfOneNameEachPinTheirOwnDocument(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	recipePath := filepath.Join(cat, "shared-recipe", "recipe.yaml")
	writeFile(t, recipePath, recipeYAML("shared",
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/recipe.tgz\n    version: \"1.0.0\"\n", true))
	writeFile(t, filepath.Join(cat, "shared-recipe", "extra.yaml"), "kind: ConfigMap\n")
	featurePath := filepath.Join(cat, "shared-feature", "featurerecipe.yaml")
	writeFile(t, featurePath, "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: shared\n  version: \"1.0.0\"\n"+
		"spec:\n  capability: helm\n  implements: shared\n"+
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/feature.tgz\n    version: \"2.0.0\"\n"+
		"  artifacts:\n    patch.yaml:\n      $import: ./patch.yaml\n")
	writeFile(t, filepath.Join(cat, "shared-feature", "patch.yaml"), "kind: Secret\n")

	const uri = "helm/shared:1.0.0"
	recipeKey := closure.EntityKey{Kind: closure.KindRecipe, URI: uri}
	featureKey := closure.EntityKey{Kind: closure.KindFeatureRecipe, URI: uri}
	view := catalog.NewLazyLookup(ws, "", []string{cat}, nil)
	plan, err := Compute(PlanInputs{
		View:          view,
		Entities:      []closure.EntityKey{recipeKey, featureKey},
		Resolver:      &recordingResolver{},
		WorkspaceRoot: ws,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	cases := []struct {
		kind, path, source string
		key                closure.EntityKey
	}{
		{"Recipe", recipePath, "https://example.invalid/recipe.tgz", recipeKey},
		{"FeatureRecipe", featurePath, "https://example.invalid/feature.tgz", featureKey},
	}
	if len(plan.entities) != len(cases) {
		t.Fatalf("a Recipe and a FeatureRecipe of one name are two entities to plan; got %d: %+v", len(plan.entities), plan.entities)
	}
	digests, err := plan.EntityDigests()
	if err != nil {
		t.Fatalf("EntityDigests: %v", err)
	}
	if len(digests) != len(cases) {
		t.Fatalf("each identity gets a digest of its own; got %v", digests)
	}
	for _, tc := range cases {
		var planned *entityPlan
		for i := range plan.entities {
			if plan.entities[i].path == tc.path {
				planned = &plan.entities[i]
			}
		}
		if planned == nil {
			t.Fatalf("the %s at %s was not planned: %+v", tc.kind, tc.path, plan.entities)
		}
		if planned.kind != tc.kind {
			t.Errorf("the document at %s was planned as a %s, want %s", tc.path, planned.kind, tc.kind)
		}
		if got := planned.want.SourceDigests; len(got) != 1 || got[0].URL != tc.source {
			t.Errorf("the %s must pin the one source it authored, %s; wants %+v", tc.kind, tc.source, got)
		}
		want, err := view.EntityRowDigestAt(tc.path)
		if err != nil {
			t.Fatalf("EntityRowDigestAt(%s): %v", tc.path, err)
		}
		if digests[tc.key] != want {
			t.Errorf("the %s's identity carries digest %q, want its own document's %q", tc.kind, digests[tc.key], want)
		}
	}
	if digests[recipeKey] == digests[featureKey] {
		t.Errorf("the two documents differ, so their digests must: both are %q", digests[recipeKey])
	}
}
