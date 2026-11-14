// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAnUnmappedFeatureNamesWhatItSearchedWithNoCatalogDirectory drives the
// resolution-completeness halt a build installs before it renders, over an
// unmapped feature instance, on the lazy lookup a build reads in a workspace
// that names no catalog directory — every Recipe and FeatureRecipe it holds is
// one the lockfile pins, read from the content cache — and on the same view with
// one empty catalog directory added, the control: both views hold exactly the
// same pinned entities, so the one thing the rows compare is whether a catalog
// directory was read.
//
// With no catalog directory, a FeatureRecipe a registry publishes is in the view
// only once the Manifest names it, so a search over the view finds none for an
// attachment the Manifest maps to nothing, however many a registry publishes.
// The halt must therefore claim an absence of the pins and never of "the
// catalog", say a registry's were not searched, and offer pinning one a registry
// publishes; with a catalog directory it reads as it always has.
//
//   - nothing pinned implements the type, and the component's capability is
//     known: the halt names the capability, as the case with implementers
//     elsewhere does, and offers both remedies;
//   - one pinned FeatureRecipe implements it for another capability: the list is
//     named, and the claim beside it is made of the pins;
//   - nothing pinned implements it, and the capability is not known: the claim
//     is made of the pins, with no capability named;
//   - the attachment names no type, with and without a catalog directory: the
//     halt says so, keeps the condition, and never prints the empty type or a
//     search for it.
//
// 🔴 PLANTED DEFECT: drop the empty-type branch at the head of
// unmappedFeatureFault (pkg/catalog/manifest_lookup.go): both untyped rows
// redden, reading `(type "")` and "implements ;".
//
// 🔴 PLANTED DEFECT: in LazyLookup.recipeCatalog (pkg/catalog/lazy_lookup.go),
// leave recipeSetsFromPinsAlone false: every row with no catalog directory
// reddens, reading "in the catalog". Set it true unconditionally: the control
// rows redden, reading "the lockfile pins".
func TestAnUnmappedFeatureNamesWhatItSearchedWithNoCatalogDirectory(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const (
		recipeURI    = "compose/mariadb:1.0.0"
		elsewhereURI = "helm/backup-elsewhere:1.0.0"
	)
	pins := Pins{
		{Kind: "Recipe", URI: recipeURI}: writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe,
			recipeBundle(t, bridgeRecipeYAML)),
		{Kind: "FeatureRecipe", URI: elsewhereURI}: writePinnedSlot(t, ws, cacheDir, elsewhereURI, oci.MediaTypeFeatureRecipe,
			recipeBundle(t, capabilityFeatureRecipeYAML("backup-elsewhere", "helm", "sec/backup:1.0.0"))),
	}
	view := func(dirs []string) *LazyLookup {
		v := NewLazyLookup(ws, cacheDir, dirs, nil)
		v.SetPins(pins)
		return v
	}
	hostMapped := func() *types.Manifest {
		comp := &types.MappingTier{}
		comp.Type.Set("core/datastore/mariadb", recipeURI)
		return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}
	}

	const scope = "; with no catalog directory, the feature recipes searched are only those the lockfile pins, not a registry's"
	// An attachment naming no type is refused for that, in either view, and never
	// as a search for an empty type.
	const untypedHalt = `feature "backup" is attached with no type, so no feature recipe can be chosen for it: an ` +
		`attachment the Release writes names its Feature under type, unless the Aggregate declares an attachment of ` +
		`the same name for it to configure; name the Feature under the attachment's type in the Release, or declare ` +
		`the attachment in the Aggregate and configure it from the Release`
	cases := []struct {
		name        string
		catalogDirs []string
		manifest    *types.Manifest
		featureType string
		want        string
		notWant     string
	}{
		{
			name:        "no catalog directory, nothing pinned implements the type",
			manifest:    hostMapped(),
			featureType: "sec/lonely:1.0.0",
			want: `feature "backup" (type "sec/lonely:1.0.0"); no feature recipe the lockfile pins implements sec/lonely:1.0.0 ` +
				`for capability "compose", the capability its component's Recipe binds to` + scope + `; pin one a registry ` +
				`publishes in the Manifest, where one for another capability renders as its own output beside the ` +
				`component's, or write one for "compose", then map the type to it`,
			notWant: "in the catalog",
		},
		{
			name:        "control: an empty catalog directory, nothing implements the type",
			catalogDirs: []string{t.TempDir()},
			manifest:    hostMapped(),
			featureType: "sec/lonely:1.0.0",
			want:        `feature "backup" (type "sec/lonely:1.0.0"); no feature recipe in the catalog implements sec/lonely:1.0.0; write one, then map the type to it`,
			notWant:     "lockfile pins",
		},
		{
			name:        "no catalog directory, a pinned one implements it for another capability",
			manifest:    hostMapped(),
			featureType: "sec/backup:1.0.0",
			want: `feature "backup" (type "sec/backup:1.0.0"); no feature recipe the lockfile pins implements sec/backup:1.0.0 ` +
				`for capability "compose", the capability its component's Recipe binds to` + scope + `; feature recipes ` +
				`implementing it for other capabilities: [helm/backup-elsewhere:1.0.0]; pin one of those in the Manifest, and ` +
				`it renders as its own output beside the component's, or write one for "compose", then map the type to it`,
			notWant: "in the catalog",
		},
		{
			name:        "control: an empty catalog directory, one implements it for another capability",
			catalogDirs: []string{t.TempDir()},
			manifest:    hostMapped(),
			featureType: "sec/backup:1.0.0",
			want: `feature "backup" (type "sec/backup:1.0.0"); no feature recipe in the catalog implements sec/backup:1.0.0 ` +
				`for capability "compose", the capability its component's Recipe binds to; feature recipes implementing it ` +
				`for other capabilities: [helm/backup-elsewhere:1.0.0]; pin one of those in the Manifest`,
			notWant: "lockfile pins",
		},
		{
			name:        "no catalog directory, the attachment names no type",
			manifest:    hostMapped(),
			featureType: "",
			want:        untypedHalt,
			notWant:     `(type "")`,
		},
		{
			name:        "control: an empty catalog directory, the attachment names no type",
			catalogDirs: []string{t.TempDir()},
			manifest:    hostMapped(),
			featureType: "",
			want:        untypedHalt,
			notWant:     "implements ;",
		},
		{
			name:        "no catalog directory, the component's capability is not known",
			manifest:    &types.Manifest{},
			featureType: "sec/lonely:1.0.0",
			want: `feature "backup" (type "sec/lonely:1.0.0"); no feature recipe the lockfile pins implements sec/lonely:1.0.0` +
				scope + `; pin one a registry publishes in the Manifest, or write one, then map the type to it`,
			notWant: "for capability",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph := &types.IRGraph{}
			graph.Spec.Nodes.Set("db", types.IRNode{Component: "core/datastore/mariadb"})
			feats := types.OrderedMap[types.IRFeatureEntry]{}
			feats.Set("backup", types.IRFeatureEntry{Type: tc.featureType})
			graph.Spec.FeatureValuesMapping.Set("db", feats)

			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the halt panicked: %v", r)
					}
				}()
				err = view(tc.catalogDirs).VerifyRecipeResolution(tc.manifest, graph)
			}()
			if err == nil {
				t.Fatalf("an unmapped feature instance must halt")
			}
			if !errors.Is(err, ErrNoRecipeResolved()) {
				t.Errorf("expected ErrNoRecipeResolved in the chain; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the halt must read %q; got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("the halt must not read %q in this case; got %v", tc.notWant, err)
			}
		})
	}
}
