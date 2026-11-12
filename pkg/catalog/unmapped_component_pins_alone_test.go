// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolutionView presents one component instance's Recipe resolution to the
// rule over it, one fact per field of the resolution, as the composition's view
// presents it.
//
// The resolution itself is the lazy lookup's own answer, taken from the lookup a
// build reads, so what the rule is handed is what production computes: this
// view only projects it. Every other fact of the stage is the embedded
// interface's, left nil, so a rule reaching for a fact this test does not
// supply panics rather than reading a value nobody set — and the test reports
// the panic.
type resolutionView struct {
	constraint.TopologyView
	node          string
	componentType string
	resolution    resolver.NodeRecipeResolution
}

func (v resolutionView) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: v.node, AuthoredName: v.node}}
}

func (v resolutionView) FlattenedNodeComponentType(string) (string, bool) {
	return v.componentType, true
}

func (v resolutionView) NodeRecipeMappingAmbiguousKeys(string) []string {
	return v.resolution.AmbiguousKeys
}

func (v resolutionView) NodeMappedRecipeReference(string) (string, bool) {
	return v.resolution.Reference, v.resolution.Mapped
}

func (v resolutionView) NodeMappedRecipePresent(string) bool { return v.resolution.Present }

func (v resolutionView) NodeRecipePinCandidates(string) []string {
	return v.resolution.PinCandidates
}

func (v resolutionView) NodeRecipePinCandidatesFromPinsAlone(string) bool {
	return v.resolution.PinCandidatesFromPinsAlone
}

// TestAnUnmappedComponentNamesWhatItSearchedWithNoCatalogDirectory drives the
// rule that every component instance resolves to a Recipe, over an instance the
// Manifest maps to nothing, with the resolution the lazy lookup a build reads
// computes for it: once in a workspace that names no catalog directory — every
// Recipe the lookup holds is one the lockfile pins, read from the content cache
// — and once with one empty catalog directory added, the control. Both lookups
// hold exactly the same pinned Recipe, so the one thing the rows compare is
// whether a catalog directory was read.
//
// With no catalog directory, a Recipe a registry publishes is in the lookup only
// once the Manifest names it, so a search for implementers of a type the
// Manifest maps to nothing finds none, however many a registry publishes. Where
// none is found, the finding must claim an absence of the pins and never of
// "the catalog", say a registry's were not searched, and offer pinning one a
// registry publishes; with a catalog directory it reads as it always has. Where
// a pinned Recipe implements the type, it is offered in both forms alike.
//
// 🔴 PLANTED DEFECT: in Catalog.nodeRecipeResolution
// (pkg/catalog/node_recipe_resolution.go), leave PinCandidatesFromPinsAlone
// unset: the first row reddens, reading "no recipe in the catalog implements".
// Set it true unconditionally: the control rows redden on the scope they report.
func TestAnUnmappedComponentNamesWhatItSearchedWithNoCatalogDirectory(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const recipeURI = "compose/mariadb:1.0.0"
	pins := Pins{
		{Kind: "Recipe", URI: recipeURI}: writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe,
			recipeBundle(t, bridgeRecipeYAML)),
	}
	view := func(dirs []string) *LazyLookup {
		v := NewLazyLookup(ws, cacheDir, dirs, nil)
		v.SetPins(pins)
		return v
	}
	// A Manifest whose mapping reaches another type, so the instance under test
	// is one no entry maps rather than one in a Manifest that maps nothing at all.
	otherMapped := func() *types.Manifest {
		comp := &types.MappingTier{}
		comp.Type.Set("core/datastore/memcached", "compose/memcached:1.0.0")
		return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}
	}

	const (
		pinsAloneAdvice = "no recipe the lockfile pins implements this type; with no catalog directory, the " +
			"recipes searched are only those the lockfile pins, not a registry's; pin one a registry publishes " +
			"in the Manifest, or write one, then map the type to it"
		catalogAdvice    = "no recipe in the catalog implements this type; write one, then map the type to it"
		candidatesAdvice = "pin one of them in the Manifest"
	)
	cases := []struct {
		name          string
		catalogDirs   []string
		componentType string
		wantPinsAlone bool
		wantAdvice    string
		wantDetail    string
	}{
		{
			name:          "no catalog directory, nothing pinned implements the type",
			componentType: "core/datastore/redis:1.0.0",
			wantPinsAlone: true,
			wantAdvice:    pinsAloneAdvice,
		},
		{
			name:          "control: an empty catalog directory, nothing implements the type",
			catalogDirs:   []string{t.TempDir()},
			componentType: "core/datastore/redis:1.0.0",
			wantPinsAlone: false,
			wantAdvice:    catalogAdvice,
		},
		{
			name:          "no catalog directory, a pinned Recipe implements the type",
			componentType: "core/datastore/mariadb",
			wantPinsAlone: true,
			wantAdvice:    candidatesAdvice,
			wantDetail:    recipeURI,
		},
		{
			name:          "control: an empty catalog directory, a pinned Recipe implements the type",
			catalogDirs:   []string{t.TempDir()},
			componentType: "core/datastore/mariadb",
			wantPinsAlone: false,
			wantAdvice:    candidatesAdvice,
			wantDetail:    recipeURI,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolution := view(tc.catalogDirs).NodeRecipeResolution(otherMapped(), "store", tc.componentType)
			if resolution.Mapped {
				t.Fatalf("the instance must be one no mapping entry reaches; the lookup mapped it to %q", resolution.Reference)
			}
			if resolution.PinCandidatesFromPinsAlone != tc.wantPinsAlone {
				t.Errorf("the candidates' scope reads pins-alone=%v; want %v", resolution.PinCandidatesFromPinsAlone, tc.wantPinsAlone)
			}

			v := resolutionView{node: "store", componentType: tc.componentType, resolution: resolution}
			var objections []constraint.Objection
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the rule panicked: %v", r)
					}
				}()
				objections, err = constraint.RunOnNodes(context.Background().Err, v, constraint.TopologyView(v), NodeResolvesToARecipe)
			}()
			if err != nil {
				t.Fatalf("the walk over an uncancelled signal returned %v", err)
			}
			if len(objections) != 1 {
				t.Fatalf("an unmapped instance must be reported once; got %d objection(s)", len(objections))
			}
			o := objections[0]
			if !errors.Is(o.Sentinel(), ErrNoRecipeResolved()) {
				t.Errorf("expected ErrNoRecipeResolved; got %v", o.Sentinel())
			}
			if o.Advice() != tc.wantAdvice {
				t.Errorf("the finding must advise %q; got %q", tc.wantAdvice, o.Advice())
			}
			if tc.wantDetail != "" && !strings.Contains(strings.Join(o.Detail(), " "), tc.wantDetail) {
				t.Errorf("the finding must offer %q; got detail %v", tc.wantDetail, o.Detail())
			}
		})
	}
}
