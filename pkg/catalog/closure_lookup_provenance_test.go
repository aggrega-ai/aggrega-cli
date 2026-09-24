// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The subjects of the provenance table. Each is an entity a closure walk resolves
// by URI, and each has exactly one true answer to "where did this come from",
// whichever lookup construction is asked.
const (
	provenanceAuthoredPluginYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: authored-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenanceColocatedPluginYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: colocated-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenancePulledPluginYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: pulled-step
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`
	provenanceAuthoredFeatureRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: authored-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/secret-value"
`
)

// TestClosureLookupProvenanceAgreesAcrossConstructions is the table the pin
// decision rests on: every construction of the closure lookup reports the same
// resolution path for the same subject.
//
// The pull resolves through the eager catalog, which never holds a cached entity
// and so reaches one through its cache tier; the build resolves through the lazy
// view, which folds a lockfile-pinned cache slot into the same index its authored
// entities live in. A pulled entity therefore arrives at the two sides by
// different routes, and the pull-time rule that a catalog-authored plugin is not
// pinned keys on the path each route reports. Where the two disagree, a build's
// reconstruction drops what the pull pinned and refuses the lockfile the pull
// just wrote — so the agreement is asserted here, per subject and per
// construction, rather than left to the two routes happening to match.
//
// The subjects cover each answer a plugin entity can have: authored in a catalog
// directory, authored there with its artifact beside it, folded from a pinned
// cache slot, and carried inside the binary. A Recipe and a FeatureRecipe folded
// from pinned slots stand beside them, because the same fold places those kinds.
func TestClosureLookupProvenanceAgreesAcrossConstructions(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	dir := filepath.Join(ws, "catalog")

	writeProvenanceFile(t, filepath.Join(dir, "plugin", "authored-step", "plugin.yaml"), []byte(provenanceAuthoredPluginYAML))
	writeProvenanceFile(t, filepath.Join(dir, "plugin", "colocated-step", "plugin.yaml"), []byte(provenanceColocatedPluginYAML))
	writeProvenanceFile(t, filepath.Join(dir, "plugin", "colocated-step", "colocated-step.wasm"), []byte("\x00asm\x01\x00\x00\x00"))
	writeProvenanceFile(t, filepath.Join(dir, "feature-recipe", "compose", "authored-secret", "recipe.yaml"), []byte(provenanceAuthoredFeatureRecipeYAML))
	// The Feature the authored FeatureRecipe implements, so the directory is a
	// catalog an eager load accepts whole.
	writeProvenanceFile(t, filepath.Join(dir, "feature", "core", "secret-value", "feature.yaml"), []byte(bridgeFeatureYAML))

	const (
		pulledPluginURI = "pipeline-step/pulled-step:1.0.0"
		pulledRecipeURI = "compose/mariadb:1.0.0"
		pulledFRURI     = "compose/dotenv-secret:1.0.0"
	)
	pins := map[string]Pin{
		pulledPluginURI: writePinnedSlot(t, ws, cacheDir, pulledPluginURI, oci.MediaTypePlugin, []byte(provenancePulledPluginYAML)),
		pulledRecipeURI: writePinnedSlot(t, ws, cacheDir, pulledRecipeURI, oci.MediaTypeRecipe, recipeBundle(t, bridgeRecipeYAML)),
		pulledFRURI:     writePinnedSlot(t, ws, cacheDir, pulledFRURI, oci.MediaTypeFeatureRecipe, recipeBundle(t, bridgeFeatureRecipeYAML)),
	}

	embeddedID, embeddedVersion := provenanceEmbeddedProvider(t)

	subjects := []struct {
		name, uri, kindHint, want string
	}{
		{"catalog-authored plugin", "pipeline-step/authored-step:1.0.0", pluginEntityKindHint, closure.ResolutionPathCatalog},
		{"co-located plugin", "pipeline-step/colocated-step:1.0.0", pluginEntityKindHint, closure.ResolutionPathCatalog},
		{"cache-folded plugin", pulledPluginURI, pluginEntityKindHint, closure.ResolutionPathCache},
		{"embedded provider", embeddedID + ":" + embeddedVersion, pluginEntityKindHint, closure.ResolutionPathEmbedded},
		{"catalog-authored feature recipe", "compose/authored-secret:1.0.0", "feature_recipe", closure.ResolutionPathCatalog},
		{"cache-folded feature recipe", pulledFRURI, "feature_recipe", closure.ResolutionPathCache},
		{"cache-folded recipe", pulledRecipeURI, "recipe", closure.ResolutionPathCache},
	}

	eager := func(t *testing.T) *Catalog {
		t.Helper()
		cat, err := LoadAllWithCacheDir(ws, cacheDir, dir)
		if err != nil {
			t.Fatalf("LoadAllWithCacheDir: %v", err)
		}
		return cat
	}
	pinnedView := func() *LazyLookup {
		view := NewLazyLookup(ws, cacheDir, []string{dir}, nil)
		view.SetPins(pins)
		return view
	}
	constructions := []struct {
		name  string
		build func(t *testing.T) closure.Lookup
	}{
		{"eager ClosureLookup", func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookup(nil, ws, cacheDir)
		}},
		{"eager ClosureLookupWithExternals", func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookupWithExternals(nil, ws, cacheDir, nil)
		}},
		{"eager ClosureLookupWithReuse", func(t *testing.T) closure.Lookup {
			return eager(t).ClosureLookupWithReuse(nil, ws, cacheDir, nil, nil, &ReuseVerification{PriorPins: pins})
		}},
		{"lazy ClosureLookup", func(t *testing.T) closure.Lookup {
			return pinnedView().ClosureLookup(nil, ws, cacheDir, nil)
		}},
		{"lazy ClosureLookupWithPins", func(t *testing.T) closure.Lookup {
			return pinnedView().ClosureLookupWithPins(nil, ws, cacheDir, nil, pins)
		}},
		{"lazy request view", func(t *testing.T) closure.Lookup {
			return NewLazyLookup(ws, cacheDir, []string{dir}, nil).ForRequest(pins).ClosureLookup(nil, ws, cacheDir, nil)
		}},
	}

	for _, c := range constructions {
		for _, s := range subjects {
			t.Run(c.name+"/"+s.name, func(t *testing.T) {
				lookup := c.build(t)
				res, err := lookup.ResolveCatalogEntity(context.Background(), s.uri, s.kindHint)
				if err != nil {
					t.Fatalf("ResolveCatalogEntity(%q): %v", s.uri, err)
				}
				if res.NotFound {
					t.Fatalf("ResolveCatalogEntity(%q) found nothing; the subject is not resolvable through this "+
						"construction, so its provenance cannot be compared", s.uri)
				}
				if res.ResolutionPath != s.want {
					t.Errorf("ResolveCatalogEntity(%q).ResolutionPath = %q; want %q", s.uri, res.ResolutionPath, s.want)
				}
				if s.want == closure.ResolutionPathCache && res.Digest != pins[s.uri].Digest {
					t.Errorf("ResolveCatalogEntity(%q).Digest = %q; want the pin's %q — a cache-folded entity is "+
						"hashed over the slot's verified bytes", s.uri, res.Digest, pins[s.uri].Digest)
				}
			})
		}
	}
}

// provenanceEmbeddedProvider returns the bare identity and version of one core
// provider this binary carries, read out of the binary so the version is never
// written down. A binary built without its core plugins carries none, and the
// test skips there rather than asserting over an empty set.
func provenanceEmbeddedProvider(t *testing.T) (identity, version string) {
	t.Helper()
	for _, uri := range embedded.EntityURIs() {
		raw, ok := embedded.LookupEntity(uri)
		if !ok {
			continue
		}
		var p types.Plugin
		if err := yaml.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Metadata.PluginType != string(plugintype.Provider) || p.Metadata.Version == "" {
			continue
		}
		return p.Coordinate(), p.Metadata.Version
	}
	t.Skip("this binary carries no core provider entities")
	return "", ""
}

func writeProvenanceFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
