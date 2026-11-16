// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestTheIgnoreAdvisoryReadsEachEntityAsTheKindItWasCollectedAs drives the pull's
// ignore-rule advisory over a Recipe and a FeatureRecipe that share one reference,
// each in a directory of its own with a local source and a lock recording another
// plugin's rules, so each would be advised on. Only the FeatureRecipe is used by an
// instance of this Release, so the one advisory names the FeatureRecipe. Reading
// each entity by name alone — the Recipe first — would advise on the Recipe in the
// FeatureRecipe's place.
func TestTheIgnoreAdvisoryReadsEachEntityAsTheKindItWasCollectedAs(t *testing.T) {
	const uri = "helm/demo:1.0.0"
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	documents := map[string]string{
		"recipe/helm/demo/1.0.0/recipe.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: demo\n  version: 1.0.0\n" +
			"spec:\n  capability: helm\n  implements: core/demo:1.0.0\n  source: ./chart\n",
		"featurerecipe/helm/demo/1.0.0/recipe.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: demo\n  version: 1.0.0\n" +
			"spec:\n  capability: helm\n  implements: core/demo-feature:1.0.0\n  source: ./chart\n",
	}
	other := types.IgnorePlugin{Name: "provider/other-helm", Patterns: []string{"only-this/"}}
	for rel, body := range documents {
		doc := filepath.Join(cat, filepath.FromSlash(rel))
		chart := filepath.Join(filepath.Dir(doc), "chart", "Chart.yaml")
		for path, content := range map[string]string{doc: body, chart: "apiVersion: v2\nname: demo\nversion: 0.1.0\n"} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pins := recipepin.Pins{LocalSourceDigests: []types.LocalSourceDigest{{Path: "chart/Chart.yaml",
			Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}},
			Ignore: &types.IgnoreRules{Plugin: &other}}
		if _, err := recipepin.WriteLock(doc, pins); err != nil {
			t.Fatalf("WriteLock %s: %v", rel, err)
		}
	}
	// The view is built over the catalog as written: it enumerates its directories
	// once, on its first read.
	wasmtest.RequireEmbeddedEntities(t)
	view := catalog.NewLazyLookup(ws, "", []string{cat}, nil)
	if _, ok, err := view.ServingProvider("helm", nil); err != nil || !ok {
		if err != nil {
			t.Fatalf("ServingProvider: %v", err)
		}
		t.Fatal("this binary carries no provider serving helm, though its entities were generated")
	}

	entities := []closure.ResolvedRef{
		{URI: uri, KindHint: closure.KindRecipe},
		{URI: uri, KindHint: closure.KindFeatureRecipe},
	}
	used := map[closure.EntityKey][]closure.ComponentSourceInfra{
		{Kind: closure.KindFeatureRecipe, URI: uri}: {{Component: "web.features.demo", Infra: map[string]any{}}},
	}
	var out bytes.Buffer
	adviseIgnoreRuleDifferences(&out, view, entities, used, nil)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `warning: FeatureRecipe "demo"`) {
		t.Fatalf("want exactly one advisory, on the FeatureRecipe an instance uses; got:\n%s", out.String())
	}
}
