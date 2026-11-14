// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// Proofs of what the drafted Manifest maps for an import given a target: every
// component type and feature type the drafts need — on the drafted Aggregate's
// instances, their connections and its subsystems, on the catalog Aggregates it
// composes at every depth, and on the drafted Release's attachments — each
// mapped to the one realization of the target the catalog offers, or, among
// several, the one named after the type; and a Manifest the importer drafted
// itself, whose mappings the catalog confirms are kept, the others dropped with
// a warning, and the rest completed.

import (
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mappingStub is the catalog the mapping proofs compose against, for the
// compose target:
//
//   - component types: example/x with one compose Recipe, x-server, and a helm
//     one, x-chart; example/y with two, y and y-blue, one named after the type;
//     example/w with two, w-a and w-b, neither so named; example/m and
//     example/deep with one each, the components of the modules below;
//   - feature types: example/secret with one compose FeatureRecipe, secret-env;
//     example/guard with two, guard and guard-strict, one named after the type;
//     example/audit with two, audit-a and audit-b, neither so named; example/backup
//     with only a helm one, backup-helm;
//   - Aggregates: example/mod, composing an instance m1 of example/m attaching
//     example/secret, and the module example/inner as inner, which composes an
//     instance deep1 of example/deep;
//   - ancestry: example/special extends example/x, and no Recipe implements
//     example/special itself.
func mappingStub(t *testing.T) stubRecipes {
	t.Helper()
	recipe := func(capability, name, implements string) types.Recipe {
		return types.Recipe{Metadata: types.RecipeMetadata{Name: name, Version: "1.0.0"},
			Spec: types.RecipeSpec{Capability: capability, Implements: implements}}
	}
	featureRecipe := func(capability, name, implements string) types.FeatureRecipe {
		return types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: name, Version: "1.0.0"},
			Spec: types.FeatureRecipeSpec{Capability: capability, Implements: implements}}
	}
	return stubRecipes{
		recipes: []types.Recipe{
			recipe("compose", "x-server", "example/x"), recipe("helm", "x-chart", "example/x"),
			recipe("compose", "y", "example/y"), recipe("compose", "y-blue", "example/y"),
			recipe("compose", "w-a", "example/w"), recipe("compose", "w-b", "example/w"),
			recipe("compose", "m-server", "example/m"), recipe("compose", "deep-server", "example/deep"),
		},
		featureRecipes: []types.FeatureRecipe{
			featureRecipe("compose", "secret-env", "example/secret"),
			featureRecipe("compose", "guard", "example/guard"), featureRecipe("compose", "guard-strict", "example/guard"),
			featureRecipe("compose", "audit-a", "example/audit"), featureRecipe("compose", "audit-b", "example/audit"),
			featureRecipe("helm", "backup-helm", "example/backup"),
		},
		aggregates: map[string]*types.Aggregate{
			"example/mod": decodedAggregate(t, "spec:\n  components:\n    m1:\n      type: example/m\n"+
				"      features:\n        cred:\n          type: example/secret\n"+
				"  aggregates:\n    inner:\n      type: example/inner\n"),
			"example/inner": decodedAggregate(t, "spec:\n  components:\n    deep1:\n      type: example/deep\n"),
		},
		ancestry: map[string][]string{"example/special": {"example/x"}},
	}
}

// decodedAggregate decodes text as an Aggregate, failing the test where it does
// not read as one.
func decodedAggregate(t *testing.T, text string) *types.Aggregate {
	t.Helper()
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(text), &agg); err != nil {
		t.Fatalf("the stub's Aggregate does not read: %v\n%s", err, text)
	}
	return &agg
}

// The documents the mapping proofs draft: an Aggregate's head and a Release's,
// each completed by the row, and the Release composed — naming the Manifest in
// the key it declared empty.
const (
	mappingAggregateHead = "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: app\n  version: 1.2.3\nspec:\n"
	mappingReleaseHead   = "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: app-dev\n" +
		"  environment: dev\n  version: 0.2.0\nspec:\n  aggregate: app\n"
	mappingManifestHead = "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: app-dev\n" +
		"  version: 0.2.0\nspec:\n  mappings:\n"
)

// TestTheCompositionMapsEveryTypeTheDraftsNeed drives composeDraftSet for the
// compose target over drafted inputs, one row each, and holds the drafted
// Manifest and the diagnostics to what the row names:
//
//   - feature types wherever the drafts attach them — an instance's own
//     attachment, one its connection carries, a subsystem's, and one the Release
//     attaches — are mapped like component types, in the order the drafts first
//     declare each; a Release attachment written with no type adds nothing;
//   - a type of several Recipes or FeatureRecipes maps to the one named after
//     the type, and one with none so named is left unmapped, the warning naming
//     each;
//   - the types of a catalog Aggregate the drafted Aggregate composes, and of the
//     one that composes in turn, are mapped as the Aggregate's own; one the
//     catalog does not hold draws a warning and maps nothing;
//   - a module and an instance whose `active` toggle is written false are left
//     out, as a build prunes them, and one whose toggle is a template a build
//     renders first is kept;
//   - a Manifest the importer drafted keeps each mapping a build of the drafts
//     accepts and drops each it does not with a warning — a name naming no
//     instance, a reference the catalog does not resolve, a FeatureRecipe binding
//     to another target — and is completed with the type mappings it lacks, a type
//     every instance of which it maps by name needing none; it is named and
//     versioned as the drafted Release, in its own place;
//   - a mapping onto a Recipe implementing a base the instance's type extends is
//     kept, as a build accepts it;
//   - a type only some of whose instances the importer maps by name is still
//     mapped by type, or warned of.
//
// 🔴 PLANTED DEFECTS:
//   - in composeDraftSet (internal/importops/compose.go) pass nil in place of
//     needs.features to composeTier: the feature rows redden, the Manifest
//     losing its features mapping and the warnings their feature types;
//   - in mappingFor drop the same-name branch: the rows mapping example/y and
//     example/guard redden, each type then warned of;
//   - in addAggregate (internal/importops/draft_needs.go) drop the loop over the
//     composed Aggregates: the module row reddens, its Manifest mapping nothing;
//   - in writtenInactive return false always: the inactive row reddens, mapping
//     example/x, example/m and example/deep;
//   - in confirms skip the resolution and return true: the importer row reddens
//     on the Manifest and on the two references the catalog does not resolve;
//   - in readCatalog pass a nil ancestry to the build's rule: the ancestry row
//     reddens, the mapping dropped with a warning.
func TestTheCompositionMapsEveryTypeTheDraftsNeed(t *testing.T) {
	t.Parallel()
	stub := mappingStub(t)
	for _, tc := range []struct {
		name         string
		aggregate    string
		release      string
		manifest     string
		wantManifest string
		wantDiags    []string
	}{
		{
			name: "feature types wherever the drafts attach them",
			aggregate: "  subsystems:\n    core:\n      features:\n        audit:\n          type: example/audit\n" +
				"      components:\n        a:\n          type: example/x\n          features:\n            tls:\n" +
				"              type: example/guard\n          uses:\n            store:\n              db:\n" +
				"                target: b\n                features:\n                  credential:\n" +
				"                    type: example/secret\n        b:\n          type: example/x\n",
			release: "  boundaries:\n    main:\n      components:\n        a:\n          features:\n            tls: {}\n" +
				"            backup:\n              type: example/backup\n        b: {}\n",
			wantManifest: mappingManifestHead + "    components:\n      type:\n        example/x: compose/x-server\n" +
				"    features:\n      type:\n        example/guard: compose/guard\n" +
				"        example/secret: compose/secret-env\n",
			wantDiags: []string{
				`warn: the drafted Manifest leaves the feature type "example/audit" unmapped: the workspace's catalog ` +
					`holds more than one feature recipe binding to the target "compose" that implements it ` +
					`("compose/audit-a", "compose/audit-b"), none of them named "audit" after the type, so the import ` +
					`does not choose between them, and a build of the drafts halts naming each attachment of it until ` +
					`the one wanted is mapped`,
				`warn: the drafted Manifest leaves the feature type "example/backup" unmapped: no feature recipe in ` +
					`the workspace's catalog binds to the target "compose" and implements it, so a build of the drafts ` +
					`halts naming each attachment of it until a mapping is added`,
			},
		},
		{
			name:         "several Recipes, one named after the type and none",
			aggregate:    "  components:\n    a:\n      type: example/y\n    c:\n      type: example/w\n",
			release:      "  components:\n    a: {}\n    c: {}\n",
			wantManifest: mappingManifestHead + "    components:\n      type:\n        example/y: compose/y\n",
			wantDiags: []string{
				`warn: the drafted Manifest leaves the component type "example/w" unmapped: the workspace's catalog ` +
					`holds more than one Recipe binding to the target "compose" that implements it ("compose/w-a", ` +
					`"compose/w-b"), none of them named "w" after the type, so the import does not choose between ` +
					`them, and a build of the drafts halts naming each instance of it until the one wanted is mapped`,
			},
		},
		{
			name:      "a composed catalog Aggregate and the one it composes",
			aggregate: "  aggregates:\n    platform:\n      type: example/mod\n",
			release: "  boundaries:\n    main:\n      aggregates:\n        platform:\n          components:\n" +
				"            m1:\n              features:\n                cred: {}\n",
			wantManifest: mappingManifestHead + "    components:\n      type:\n        example/m: compose/m-server\n" +
				"        example/deep: compose/deep-server\n" +
				"    features:\n      type:\n        example/secret: compose/secret-env\n",
		},
		{
			name: "a module and an instance written inactive, and a toggle not yet rendered",
			aggregate: "  components:\n    a:\n      type: example/x\n      active: false\n    b:\n      type: example/y\n" +
				"      active: \"{{ .parameters.on }}\"\n  aggregates:\n    off:\n      type: example/mod\n" +
				"      active: false\n",
			release:      "  components:\n    b: {}\n",
			wantManifest: mappingManifestHead + "    components:\n      type:\n        example/y: compose/y\n",
		},
		{
			name:         "a composed Aggregate the catalog does not hold",
			aggregate:    "  aggregates:\n    platform:\n      type: example/missing\n",
			release:      "  components: {}\n",
			wantManifest: mappingManifestHead + "    components:\n      type: {}\n",
			wantDiags: []string{
				`warn: the drafted Manifest maps none of the types the Aggregate "example/missing" composed as ` +
					`"platform" needs, since the workspace's catalog holds no Aggregate of that name`,
			},
		},
		{
			name: "a Manifest the importer drafted",
			aggregate: "  components:\n    s1:\n      type: example/w\n    s2:\n      type: example/w\n" +
				"    a:\n      type: example/x\n      features:\n        tls:\n          type: example/audit\n",
			release: "  components:\n    s1: {}\n    s2: {}\n    a: {}\n",
			manifest: "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: chosen\n  version: 9.9.9\n" +
				"spec:\n  mappings:\n    components:\n      name:\n        s1: compose/w-a\n        s2: compose/w-b\n" +
				"        ghost: compose/x-server\n        a: compose/nope\n    features:\n      type:\n" +
				"        example/audit: compose/audit-b\n        example/backup: compose/backup-helm\n" +
				"      name:\n        tls: compose/audit-a\n",
			wantManifest: mappingManifestHead + "    components:\n      type:\n        example/x: compose/x-server\n" +
				"      name:\n        s1: compose/w-a\n        s2: compose/w-b\n" +
				"    features:\n      type:\n        example/audit: compose/audit-b\n" +
				"      name:\n        tls: compose/audit-a\n",
			wantDiags: []string{
				`warn: the drafted Manifest drops the importer's mapping of the component instance "ghost" to ` +
					`"compose/x-server": the drafts declare no component instance of that name`,
				`warn: the drafted Manifest drops the importer's mapping of the component instance "a" to ` +
					`"compose/nope": the workspace's catalog holds no Recipe of that name binding to the target ` +
					`"compose" that implements its type "example/x"`,
				`warn: the drafted Manifest drops the importer's mapping of the feature type "example/backup" to ` +
					`"compose/backup-helm": the workspace's catalog holds no feature recipe of that name binding to ` +
					`the target "compose" that implements it`,
			},
		},
		{
			name:      "an importer's mapping the build accepts through the type's ancestry",
			aggregate: "  components:\n    sp:\n      type: example/special\n",
			release:   "  components:\n    sp: {}\n",
			manifest: "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: chosen\n  version: 9.9.9\n" +
				"spec:\n  mappings:\n    components:\n      name:\n        sp: compose/x-server\n",
			wantManifest: mappingManifestHead + "    components:\n      type: {}\n      name:\n" +
				"        sp: compose/x-server\n",
		},
		{
			name:      "a type only some of whose instances the importer maps by name",
			aggregate: "  components:\n    s1:\n      type: example/w\n    s2:\n      type: example/w\n",
			release:   "  components:\n    s1: {}\n    s2: {}\n",
			manifest: "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: chosen\n  version: 9.9.9\n" +
				"spec:\n  mappings:\n    components:\n      name:\n        s1: compose/w-a:1.0.0\n",
			wantManifest: mappingManifestHead + "    components:\n      type: {}\n      name:\n" +
				"        s1: compose/w-a:1.0.0\n",
			wantDiags: []string{
				`warn: the drafted Manifest leaves the component type "example/w" unmapped: the workspace's catalog ` +
					`holds more than one Recipe binding to the target "compose" that implements it ("compose/w-a", ` +
					`"compose/w-b"), none of them named "w" after the type, so the import does not choose between ` +
					`them, and a build of the drafts halts naming each instance of it until the one wanted is mapped`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: mappingAggregateHead + tc.aggregate},
				{Path: "release.yaml", Content: mappingReleaseHead + "  manifest: \"\"\n" + tc.release}}
			if tc.manifest != "" {
				files = append(files, abi.ScaffoldFile{Path: ManifestFileName, Content: tc.manifest})
			}
			got, diags, err := composeDraftSet("compose", stub, files)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			want := []abi.ScaffoldFile{files[0],
				{Path: "release.yaml", Content: mappingReleaseHead + "  manifest: app-dev\n" + tc.release},
				{Path: ManifestFileName, Content: tc.wantManifest}}
			if !slices.Equal(got, want) {
				t.Errorf("composed\n%+v\nwant\n%+v", got, want)
			}
			var gotDiags []string
			for _, d := range diags {
				gotDiags = append(gotDiags, d.Severity+": "+d.Message)
			}
			if !slices.Equal(gotDiags, tc.wantDiags) {
				t.Errorf("reported %q\nwant %q", gotDiags, tc.wantDiags)
			}
		})
	}
}

// TestTheCompositionKeepsTheImportersAnnotationBindings drives composeDraftSet
// over a Manifest the importer drafted declaring an annotation binding in its
// features mapping beside a realization it chose, and reads the composed Manifest
// back: the binding is there as drafted, beside the realization.
//
// 🔴 PLANTED DEFECT: in composeTier (internal/importops/compose.go) leave
// tier.annotations unset: the binding row reddens.
func TestTheCompositionKeepsTheImportersAnnotationBindings(t *testing.T) {
	t.Parallel()
	files := []abi.ScaffoldFile{
		{Path: "aggregate.yaml", Content: mappingAggregateHead + "  components:\n    a:\n      type: example/x\n" +
			"      features:\n        tls:\n          type: example/audit\n"},
		{Path: "release.yaml", Content: mappingReleaseHead + "  manifest: \"\"\n  components:\n    a: {}\n"},
		{Path: ManifestFileName, Content: "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: chosen\n" +
			"  version: 9.9.9\nspec:\n  mappings:\n    features:\n      type:\n        example/audit: compose/audit-b\n" +
			"      annotations:\n        team.example/owner:\n          feature: example/audit\n          maps:\n" +
			"            values:\n              - owner\n"},
	}
	got, diags, err := composeDraftSet("compose", mappingStub(t), files)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("reported %+v; want nothing", diags)
	}
	var manifest types.Manifest
	if err := yaml.Unmarshal([]byte(got[2].Content), &manifest); err != nil {
		t.Fatalf("the composed Manifest does not read back: %v\n%s", err, got[2].Content)
	}
	features := manifest.Spec.Mappings.Features
	if features == nil || features.Annotations == nil {
		t.Fatalf("the composed Manifest carries no features annotations:\n%s", got[2].Content)
	}
	binding, ok := features.Annotations.Get("team.example/owner")
	if !ok || binding.Feature != "example/audit" || !slices.Equal(binding.Maps.Values, []string{"owner"}) {
		t.Errorf("the binding reads back as %+v (found %v); want it as drafted:\n%s", binding, ok, got[2].Content)
	}
	if ref, _ := features.Type.Get("example/audit"); ref != "compose/audit-b" {
		t.Errorf("the realization reads back as %q; want compose/audit-b:\n%s", ref, got[2].Content)
	}
}
