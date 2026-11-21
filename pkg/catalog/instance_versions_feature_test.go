// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The tests in this file hold the features tier of the instance-version table to
// the rule the components tier states: a Feature reference resolves to the highest
// version its Manifest features key and its FeatureRecipe's implements admit, the
// FeatureRecipe's implements judged without extends ancestry.

// instanceVersionsFeatureYAML is a minimal loadable Feature of name at version.
func instanceVersionsFeatureYAML(name, version string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: " + name +
		"\n  version: \"" + version + "\"\nspec: {}\n"
}

// instanceVersionsFeatureRecipeYAML is a minimal loadable FeatureRecipe of the
// compose capability, named name at version, declaring implements.
func instanceVersionsFeatureRecipeYAML(name, version, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: " + name +
		"\n  version: \"" + version + "\"\nspec:\n  capability: compose\n  implements: \"" + implements + "\"\n"
}

// instanceVersionsFeatureCatalog is the catalog every case here reads: the Feature
// core/secret-value at the versions given, and the FeatureRecipe
// compose/dotenv-secret at 1.0.0 implementing core/secret-value:1.0.0.
func instanceVersionsFeatureCatalog(t *testing.T, featureVersions ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, version := range featureVersions {
		writeCatalogEntity(t, dir, "feature/core/secret-value/"+version+"/feature.yaml",
			instanceVersionsFeatureYAML("core/secret-value", version))
	}
	writeCatalogEntity(t, dir, "feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml",
		instanceVersionsFeatureRecipeYAML("dotenv-secret", "1.0.0", "core/secret-value:1.0.0"))
	return dir
}

// instanceVersionsFeaturesManifest is a Manifest whose features mapping carries
// the type-axis entries given, each a key beside the FeatureRecipe it maps to.
func instanceVersionsFeaturesManifest(typeEntries [][2]string) *types.Manifest {
	tier := &types.MappingTier{}
	for _, e := range typeEntries {
		tier.Type.Set(e[0], e[1])
	}
	return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: tier}}}
}

// featureInstance is a feature attachment named name, of the type reference,
// written in the Aggregate's file.
func featureInstance(name, reference string) closure.InstanceReference {
	return closure.InstanceReference{
		Path:      "aggregate.spec.components.web.features." + name + ".type",
		Name:      name,
		Reference: reference,
		File:      instanceVersionsAggregateFile,
		Feature:   true,
		Document:  closure.InAggregate,
	}
}

// TestInstanceVersionsFeatureHeldAtRecipeVersion holds a Feature of two held
// versions to the version the FeatureRecipe its key maps to implements: the bare
// key core/secret-value maps to a FeatureRecipe implementing 1.0.0, so the
// instance resolves to 1.0.0 although 1.1.0 is held.
func TestInstanceVersionsFeatureHeldAtRecipeVersion(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsFeatureCatalog(t, "1.0.0", "1.1.0")
	manifest := instanceVersionsFeaturesManifest([][2]string{{"core/secret-value", "compose/dotenv-secret:1.0.0"}})
	for _, v := range instanceVersionViews(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease,
				[]closure.InstanceReference{featureInstance("db_password", "core/secret-value")},
				manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got, ok := table.FeatureSelected.Get("core/secret-value"); !ok || got != "1.0.0" {
				t.Errorf("feature selected %q (held %t); want 1.0.0", got, ok)
			}
			if _, held := table.Selected.Get("core/secret-value"); held {
				t.Error("a feature reference has a row in the component table")
			}
		})
	}
}

// TestInstanceVersionsFeatureSingleVersionUnchanged resolves a Feature held at one
// version to that version, mapped or not.
func TestInstanceVersionsFeatureSingleVersionUnchanged(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsFeatureCatalog(t, "1.0.0")
	instances := []closure.InstanceReference{featureInstance("db_password", "core/secret-value")}
	for _, v := range instanceVersionViews(t, dir) {
		for name, manifest := range map[string]*types.Manifest{
			"mapped":   instanceVersionsFeaturesManifest([][2]string{{"core/secret-value", "compose/dotenv-secret:1.0.0"}}),
			"unmapped": instanceVersionsFeaturesManifest(nil),
		} {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
				if err != nil {
					t.Fatalf("resolve: %v", err)
				}
				if got, ok := table.FeatureSelected.Get("core/secret-value"); !ok || got != "1.0.0" {
					t.Errorf("feature selected %q (held %t); want 1.0.0", got, ok)
				}
			})
		}
	}
}

// TestInstanceVersionsFeatureConflictNamesFeatureConstraints refuses a Feature
// written at 1.1.0 under a key and FeatureRecipe admitting 1.0.0 only, naming each
// constraint as a feature's.
func TestInstanceVersionsFeatureConflictNamesFeatureConstraints(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsFeatureCatalog(t, "1.0.0", "1.1.0")
	manifest := instanceVersionsFeaturesManifest([][2]string{{"core/secret-value", "compose/dotenv-secret:1.0.0"}})
	instances := []closure.InstanceReference{featureInstance("db_password", "core/secret-value:1.1.0")}
	const want = `no version of a component or feature type satisfies every constraint declared on the instance ` +
		`(Release "quickstart-web-local", at ".spec.components.web.features.db_password", ` +
		`instance "db_password", type "core/secret-value:1.1.0", written in "aggregate.yaml", ` +
		`Manifest feature key "core/secret-value", written in "manifest.yaml", ` +
		`mapped to feature recipe "compose/dotenv-secret:1.0.0", whose spec.implements is "core/secret-value:1.0.0", ` +
		`versions held "1.0.0, 1.1.0"). change the Aggregate's version, or map this type at a version its feature recipe implements`
	for _, v := range instanceVersionViews(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if !errors.Is(err, ErrConflictingVersionConstraints()) {
				t.Fatalf("err = %v; want the conflicting-version-constraints condition", err)
			}
			var raised *cerrors.CompilerError
			if !errors.As(err, &raised) {
				t.Fatalf("err = %T; want a diagnostic", err)
			}
			if raised.Message() != want {
				t.Errorf("message:\n got %s\nwant %s", raised.Message(), want)
			}
			if _, held := table.FeatureSelected.Get("core/secret-value:1.1.0"); held {
				t.Error("the table selected a version for the reference it refused")
			}
		})
	}
}

// TestInstanceVersionsKindsHoldNoSharedRow keeps a Component and a Feature written
// with one reference string apart: each resolves in its own table, and the scoped
// lookup answers each kind at its own version.
func TestInstanceVersionsKindsHoldNoSharedRow(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsCatalog(t)
	writeCatalogEntity(t, dir, "feature/core/compute/server/1.0.0/feature.yaml", instanceVersionsFeatureYAML("core/compute/server", "1.0.0"))
	writeCatalogEntity(t, dir, "feature/core/compute/server/1.1.0/feature.yaml", instanceVersionsFeatureYAML("core/compute/server", "1.1.0"))
	writeCatalogEntity(t, dir, "feature-recipe/compose/server-feature/1.0.0/recipe.yaml",
		instanceVersionsFeatureRecipeYAML("server-feature", "1.0.0", "core/compute/server:1.0.0"))
	manifest := instanceVersionsManifest([][2]string{{"core/compute/server:1.1.0", "compose/server:2.0.0"}}, nil)
	manifest.Spec.Mappings.Features = instanceVersionsFeaturesManifest(
		[][2]string{{"core/compute/server", "compose/server-feature:1.0.0"}}).Spec.Mappings.Features
	instances := []closure.InstanceReference{
		componentInstance("web", "core/compute/server"),
		featureInstance("attached", "core/compute/server"),
	}
	for _, v := range instanceVersionViews(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got, _ := table.Selected.Get("core/compute/server"); got != "1.1.0" {
				t.Errorf("component selected %q; want 1.1.0", got)
			}
			if got, _ := table.FeatureSelected.Get("core/compute/server"); got != "1.0.0" {
				t.Errorf("feature selected %q; want 1.0.0", got)
			}
		})
	}
}
