// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// digestPerURLResolver answers every source with the version its author wrote and a
// digest of its URL, so each source has a pin of its own.
type digestPerURLResolver struct{}

func (digestPerURLResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	return &closure.ResolvedSource{
		Kind:          spec.Kind,
		URL:           spec.URL,
		PinnedVersion: spec.Version,
		Digest:        digest.ContentDigest([]byte(spec.URL)),
	}, nil
}

// TestThePinStepHoldsOnlyTheEntityAnInstanceUsesWhenTwoKindsShareItsName drives the
// pin step a pull runs (entityPins) over a Release reaching a Recipe and a
// FeatureRecipe of one name, each with a writable home and neither with a lock. Only
// the Recipe is used by an instance of this Release, so only the Recipe is held to
// its lock: a run that does not pin halts naming the Recipe's document, and the
// FeatureRecipe — which nothing in this Release attaches — is not named. Reading
// "used" by name alone would take the Recipe's instance for the FeatureRecipe's too
// and halt naming both.
func TestThePinStepHoldsOnlyTheEntityAnInstanceUsesWhenTwoKindsShareItsName(t *testing.T) {
	t.Parallel()
	const uri = "helm/shared:1.0.0"
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	writeEntityDocument(t, filepath.Join(cat, "shared-recipe", "recipe.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: shared\n  version: \"1.0.0\"\n"+
			"spec:\n  capability: helm\n  implements: shared\n"+
			"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/recipe.tgz\n    version: \"1.0.0\"\n")
	writeEntityDocument(t, filepath.Join(cat, "shared-feature", "featurerecipe.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: shared\n  version: \"1.0.0\"\n"+
			"spec:\n  capability: helm\n  implements: shared\n"+
			"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/feature.tgz\n    version: \"2.0.0\"\n")

	entities := []closure.ResolvedRef{
		{URI: uri, KindHint: closure.KindRecipe},
		{URI: uri, KindHint: closure.KindFeatureRecipe},
	}
	used := map[closure.EntityKey][]closure.ComponentSourceInfra{
		{Kind: closure.KindRecipe, URI: uri}: {{Component: "db", Infra: map[string]any{}}},
	}

	_, _, err := entityPins(catalog.NewLazyLookup(ws, "", []string{cat}, nil), entities, used,
		digestPerURLResolver{}, ws, "", false, false, nil, []string{cat})
	if err == nil {
		t.Fatalf("the Recipe an instance uses records no pin for its source, and the pin step did not halt")
	}
	if !strings.Contains(err.Error(), filepath.Join("shared-recipe", "recipe.yaml")) {
		t.Errorf("the halt must name the Recipe an instance uses; got: %v", err)
	}
	if strings.Contains(err.Error(), "shared-feature") {
		t.Errorf("the halt names the FeatureRecipe, which no instance of this Release uses; got: %v", err)
	}
}
