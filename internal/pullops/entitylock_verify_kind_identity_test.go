// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAVerifyHoldsARecipeAndAFeatureRecipeOfOneNameEachToItsOwnLock drives the
// verify path's check over the entity locks (verifyEntityLocks, which `pull
// --verify` runs) for a Release reaching a Recipe and a FeatureRecipe that share
// their identity, each in a directory of its own with a lock pinning the one source
// it authors.
//
// Each entity is held to its own lock and its own lockfile source row: a lock that
// disagrees with its own row is reported, whichever of the two it sits beside. A
// check reading both entities by name alone would examine only the one its first
// loader answered for, and would pass over a FeatureRecipe's lock that no longer
// agrees with the lockfile.
func TestAVerifyHoldsARecipeAndAFeatureRecipeOfOneNameEachToItsOwnLock(t *testing.T) {
	t.Parallel()
	const (
		uri        = "helm/shared:1.0.0"
		recipeURL  = "https://example.invalid/recipe.tgz"
		featureURL = "https://example.invalid/feature.tgz"
	)
	recipeDigest := digest.ContentDigest([]byte("the Recipe's source"))
	featureDigest := digest.ContentDigest([]byte("the FeatureRecipe's source"))
	otherDigest := digest.ContentDigest([]byte("bytes neither source is"))

	cases := []struct {
		name string
		// recipePin and featurePin are the digests each entity's lock records for its
		// own source; the lockfile records recipeDigest and featureDigest.
		recipePin, featurePin string
		// want names the source whose disagreement the check must report, and "" where
		// it must report nothing.
		want string
	}{
		{name: "both locks agree with their own rows", recipePin: recipeDigest, featurePin: featureDigest},
		{name: "the FeatureRecipe's lock disagrees with its own row", recipePin: recipeDigest, featurePin: otherDigest,
			want: featureURL + "@2.0.0"},
		{name: "the Recipe's lock disagrees with its own row", recipePin: otherDigest, featurePin: featureDigest,
			want: recipeURL + "@1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			cat := filepath.Join(ws, "catalog")
			recipePath := filepath.Join(cat, "shared-recipe", "recipe.yaml")
			writeEntityDocument(t, recipePath, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: shared\n  version: \"1.0.0\"\n"+
				"spec:\n  capability: helm\n  implements: shared\n"+
				"  source:\n    kind: helm-https-chart\n    url: "+recipeURL+"\n    version: \"1.0.0\"\n")
			featurePath := filepath.Join(cat, "shared-feature", "featurerecipe.yaml")
			writeEntityDocument(t, featurePath, "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: shared\n  version: \"1.0.0\"\n"+
				"spec:\n  capability: helm\n  implements: shared\n"+
				"  source:\n    kind: helm-https-chart\n    url: "+featureURL+"\n    version: \"2.0.0\"\n")
			if _, err := recipepin.WriteLock(recipePath, recipepin.Pins{SourceDigests: []types.SourceDigest{
				{Kind: "helm-https-chart", URL: recipeURL, Version: "1.0.0", Digest: tc.recipePin}}}); err != nil {
				t.Fatalf("write the Recipe's lock: %v", err)
			}
			if _, err := recipepin.WriteLock(featurePath, recipepin.Pins{SourceDigests: []types.SourceDigest{
				{Kind: "helm-https-chart", URL: featureURL, Version: "2.0.0", Digest: tc.featurePin}}}); err != nil {
				t.Fatalf("write the FeatureRecipe's lock: %v", err)
			}

			lf := &lockfile.Lockfile{SourceArtifacts: []lockfile.SourceArtifact{
				{Kind: "helm-https-chart", URL: recipeURL, PinnedVersion: "1.0.0", Digest: recipeDigest,
					ReferencedBy: []lockfile.SourceArtifactReference{{Entity: closure.KindRecipe, URI: uri, Field: "spec.source"}}},
				{Kind: "helm-https-chart", URL: featureURL, PinnedVersion: "2.0.0", Digest: featureDigest,
					ReferencedBy: []lockfile.SourceArtifactReference{{Entity: closure.KindFeatureRecipe, URI: uri, Field: "spec.source"}}},
			}}
			entities := []closure.ResolvedRef{
				{URI: uri, KindHint: closure.KindRecipe},
				{URI: uri, KindHint: closure.KindFeatureRecipe},
			}

			err := verifyEntityLocks(lf, catalog.NewLazyLookup(ws, "", []string{cat}, nil), entities)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("every lock agrees with its own lockfile row; the check reported: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the lock pinning %s disagrees with its lockfile row, and the check reported nothing", tc.want)
			}
			if !errors.Is(err, oci.ErrFetchIntegrity()) {
				t.Errorf("the disagreement must be reported under the integrity condition a build halts with; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the report must name the source %s; got: %v", tc.want, err)
			}
			for _, other := range []string{recipeURL + "@1.0.0", featureURL + "@2.0.0"} {
				if other != tc.want && strings.Contains(err.Error(), other) {
					t.Errorf("the report names %s, whose lock agrees with its own row; got: %v", other, err)
				}
			}
		})
	}
}

// writeEntityDocument writes body at path, creating its directory.
func writeEntityDocument(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
