// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file cover the rule over a component instance reached
// through a Manifest mapping key that names no version. They compile a workspace
// for real, varying the mapping table and the instance's authored type, so what
// halts is a property of the compiled documents rather than of an assertion.
//
// The workspace's catalog holds core/postgres at 1.0.0 and 2.0.0, a Recipe
// written for each version, and acme/pg at 2.0.0 extending core/postgres:2.0.0, so
// one catalog carries the version mismatch, both ways of resolving it and a type
// served through its base.

const unversionedKeyComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/postgres
  version: %VERSION%
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  schema:
    values:
      database:
        type: string
        default: app
    infra:
      cpu_limit:
        type: string
        default: "500m"
    provides:
      host:
        type: string
`

const unversionedKeyExtendingComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/pg
  version: 2.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  extends: core/postgres:2.0.0
  schema: inherited
`

const unversionedKeyRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: %NAME%
  version: 1.0.0
spec:
  capability: helm
  implements: %IMPLEMENTS%
  provides:
    host: "{{ .context.rendered_name }}-db"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    form: stateless
    replicas: 1
    containers:
      main:
        image: postgres:16
        ports:
          db: 5432
    services:
      db:
        type: ClusterIP
        ports:
          db: 5432
`

const unversionedKeyReleaseYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: pg-release
  environment: development
  version: "1.0.0"
spec:
  aggregate: pg-app
  manifest: pg-manifest
  boundaries:
    main:
      directory: helm
      components:
        main-db:
          infra:
            cpu_limit: "1"
`

// unversionedKeyAggregate is an Aggregate declaring one instance, main-db, of the
// type given.
func unversionedKeyAggregate(componentType string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: pg-app
  version: "1.0.0"
spec:
  components:
    main-db:
      type: ` + componentType + `
`
}

// unversionedKeyManifest is a Manifest whose components type tier maps one key to
// one reference.
func unversionedKeyManifest(key, reference string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: pg-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        "` + key + `": ` + reference + `
`
}

// compileUnversionedKeyWorkspace writes the workspace with the Aggregate and the
// Manifest given, and drives it through the entry a surface that emits takes,
// stopping short of the render, as the sibling proofs over an instance's Recipe
// resolution do.
func compileUnversionedKeyWorkspace(t *testing.T, aggregate, manifest string) error {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		write("catalog/component/core/postgres/"+v+"/component.yaml",
			strings.ReplaceAll(unversionedKeyComponentYAML, "%VERSION%", v))
	}
	write("catalog/component/acme/pg/2.0.0/component.yaml", unversionedKeyExtendingComponentYAML)
	for name, implements := range map[string]string{
		"postgres-chart":    "core/postgres:1.0.0",
		"postgres-chart-v2": "core/postgres:2.0.0",
	} {
		write("catalog/recipe/helm/"+name+"/1.0.0/recipe.yaml", strings.NewReplacer(
			"%NAME%", name, "%IMPLEMENTS%", implements).Replace(unversionedKeyRecipeYAML))
	}
	releasePath := write("release.yaml", unversionedKeyReleaseYAML)
	manifestPath := write("manifest.yaml", manifest)
	aggregatePath := write("aggregate.yaml", aggregate)

	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	man, err := resolver.LoadManifestFromBytes([]byte(manifest), manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	// A catalog fault's own message names only the stage; the document and the
	// refusal are its causes, so the fixture's failures are printed with them.
	cat, err := catalog.LoadAll("", filepath.Join(dir, "catalog"))
	if err != nil {
		t.Fatalf("catalog load: %s", cerrors.CauseChain(err))
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %s", len(extErrs), cerrors.CauseChain(extErrs[0]))
	}
	_, err = RunPipeline(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          man,
			AggregateRawBytes: []byte(aggregate),
			AggregatePath:     aggregatePath,
			ReleasePath:       releasePath,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(man),
		cat.SubAggregateLoader(),
		Options{
			SkipRender:               true,
			RecipeResolutionVerifier: cat.VerifyRecipeResolution,
		},
	)
	return err
}

// TestAnUnversionedKeySendingAnInstanceToAnotherVersionsRecipeHalts is the halt,
// one row per way an instance's own version reaches the judgement: written on the
// instance, and resolved from a bare type to the highest version the catalog
// holds.
//
// Every value the author needs to find the fault is asserted in the rendered
// finding — the instance, its type and the version it was compiled as, the key,
// the Recipe and what that Recipe implements — and so are the condition's short
// code, the locator and the category that sends the reader to the Manifest.
//
// 🔴 PLANTED DEFECT: in nodeMappingRefused (pkg/resolver/checks_node_mapped_recipe_implements.go),
// ask the second judgement with the key instead of the resolved type: every row
// here compiles, because the key names no version.
func TestAnUnversionedKeySendingAnInstanceToAnotherVersionsRecipeHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		instanceType string
		wantValues   []string
	}{
		{
			name:         "an instance whose authored type pins the other version",
			instanceType: "core/postgres:2.0.0",
			wantValues:   []string{`of type "core/postgres:2.0.0"`},
		},
		{
			name:         "a bare instance the catalog resolves to the other version",
			instanceType: "core/postgres",
			wantValues:   []string{`of type "core/postgres"`, `compiled as "core/postgres:2.0.0"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := compileUnversionedKeyWorkspace(t,
				unversionedKeyAggregate(tc.instanceType),
				unversionedKeyManifest("core/postgres", "postgres-chart:1.0.0"))
			if err == nil {
				t.Fatal("an instance mapped to a Recipe implementing another version compiled without halting")
			}
			if !errors.Is(err, resolver.ErrManifestImplementsMismatch()) {
				t.Fatalf("the halt does not carry the mapping-inconsistency condition: %v", err)
			}
			reported := violationsCarrying(t, err, resolver.ErrManifestImplementsMismatch())
			if len(reported) != 1 {
				t.Fatalf("findings carrying this condition = %d, want 1: %+v", len(reported), reported)
			}
			if short, ok := cerrors.ShortCodeOf(reported[0].Err); !ok || short != "E0096" {
				t.Errorf("short code = %q (present %t), want %q", short, ok, "E0096")
			}
			if reported[0].Field != ".spec.components.main-db" {
				t.Errorf("locator = %q, want %q", reported[0].Field, ".spec.components.main-db")
			}
			rendered := reported[0].Error()
			want := append([]string{
				`"main-db"`,
				`reached through manifest mapping key "core/postgres"`,
				`mapped to recipe "postgres-chart:1.0.0"`,
				`whose spec.implements is "core/postgres:1.0.0"`,
				"with a mapping key naming the version",
			}, tc.wantValues...)
			for _, w := range want {
				if !strings.Contains(rendered, w) {
					t.Errorf("the rendered finding does not carry %q: %q", w, rendered)
				}
			}
			t.Logf("rendered finding: %s", rendered)
			if got := reported[0].ToCompilerError().Category(); got != cerrors.OpsError {
				t.Errorf("category = %v, want %v", got, cerrors.OpsError)
			}
		})
	}
}

// TestTheMappingsThatServeTheInstancesVersionStillCompile is the control for the
// halt above, one row per mapping a build of the same instance takes today and
// must keep taking: the key naming the instance's version, the unversioned key
// pointed at the Recipe implementing that version, a key naming a version the
// instance does not carry, judged as the rule over the entry judges it, and a
// type served by the Recipe written against the base it extends.
//
// Without it the proof above would pass against a rule that refused every
// unversioned key, or every mapping at all.
func TestTheMappingsThatServeTheInstancesVersionStillCompile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		instanceType string
		key          string
		reference    string
	}{
		{"the key naming the instance's version", "core/postgres:2.0.0", "core/postgres:2.0.0", "postgres-chart-v2:1.0.0"},
		{"the unversioned key mapped to the Recipe implementing that version", "core/postgres:2.0.0", "core/postgres", "postgres-chart-v2:1.0.0"},
		{"a bare instance and the unversioned key mapped to the Recipe its resolved version needs", "core/postgres", "core/postgres", "postgres-chart-v2:1.0.0"},
		{"a key naming another version, judged against that version", "core/postgres:2.0.0", "core/postgres:1.0.0", "postgres-chart:1.0.0"},
		{"a type served through the base it extends", "acme/pg:2.0.0", "acme/pg", "postgres-chart-v2:1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := compileUnversionedKeyWorkspace(t,
				unversionedKeyAggregate(tc.instanceType), unversionedKeyManifest(tc.key, tc.reference))
			if err != nil {
				t.Fatalf("a mapping serving the instance's version halted: %v", err)
			}
		})
	}
}
