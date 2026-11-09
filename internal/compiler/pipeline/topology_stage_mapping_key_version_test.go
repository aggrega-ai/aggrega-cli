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
// through a Manifest mapping key whose version is not the instance's own — a key
// naming no version, or one naming another. They compile a workspace for real,
// varying the mapping table and the instance's authored type, so what halts is a
// property of the compiled documents rather than of an assertion.
//
// The workspace's catalog holds core/postgres at 1.0.0, 1.4.0 and 2.0.0; acme/pg at
// 1.0.0 and 2.0.0, each extending core/postgres at its own major; and four Recipes:
// one implementing core/postgres:1.0.0, one core/postgres:2.0.0, one the caret
// range core/postgres:^1.0.0 and one the unversioned type. One catalog therefore
// carries the version mismatch, every way of resolving it, and types served
// through their base.

const keyVersionComponentYAML = `apiVersion: aggrega.ai/v1beta1
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

const keyVersionExtendingComponentYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/pg
  version: %VERSION%
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: datastore
spec:
  extends: core/postgres:%VERSION%
  schema: inherited
`

const keyVersionRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: %NAME%
  version: 1.0.0
spec:
  capability: helm
  implements: "%IMPLEMENTS%"
  provides:
    host: "{{ .context.rendered_name }}-db"
  workload: |
    apiVersion: aggrega.ai/v1beta1
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

const keyVersionReleaseYAML = `apiVersion: aggrega.ai/v1beta1
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

// keyVersionAggregate is an Aggregate declaring one instance, main-db, of the type
// given.
func keyVersionAggregate(componentType string) string {
	return `apiVersion: aggrega.ai/v1beta1
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

// keyVersionManifest is a Manifest whose components type tier maps one key to one
// reference.
func keyVersionManifest(key, reference string) string {
	return `apiVersion: aggrega.ai/v1beta1
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

// compileKeyVersionWorkspace writes the workspace with the Aggregate and the
// Manifest given, and drives it through the entry a surface that emits takes,
// stopping short of the render, as the sibling proofs over an instance's Recipe
// resolution do.
func compileKeyVersionWorkspace(t *testing.T, aggregate, manifest string) error {
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
	for _, v := range []string{"1.0.0", "1.4.0", "2.0.0"} {
		write("catalog/component/core/postgres/"+v+"/component.yaml",
			strings.ReplaceAll(keyVersionComponentYAML, "%VERSION%", v))
	}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		write("catalog/component/acme/pg/"+v+"/component.yaml",
			strings.ReplaceAll(keyVersionExtendingComponentYAML, "%VERSION%", v))
	}
	for _, r := range [][2]string{
		{"postgres-chart", "core/postgres:1.0.0"},
		{"postgres-chart-v2", "core/postgres:2.0.0"},
		{"postgres-chart-caret", "core/postgres:^1.0.0"},
		{"postgres-chart-any", "core/postgres"},
	} {
		write("catalog/recipe/helm/"+r[0]+"/1.0.0/recipe.yaml", strings.NewReplacer(
			"%NAME%", r[0], "%IMPLEMENTS%", r[1]).Replace(keyVersionRecipeYAML))
	}
	releasePath := write("release.yaml", keyVersionReleaseYAML)
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
	// The rule over the mapping entry runs ahead of the compilation on every
	// surface, so a row it refuses must not be read as a row this rule passed: it
	// is asked here too, and a refusal there fails the fixture rather than the row.
	if faults := cat.VerifyManifestImplementsConsistency(man, manifestPath, cat.ComponentAncestry); len(faults) > 0 {
		t.Fatalf("the mapping entry itself is refused, so this row does not reach the rule under proof: %s",
			cerrors.CauseChain(faults[0]))
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

// TestAKeyNotNamingTheInstancesVersionSendingItToAnotherVersionsRecipeHalts is the
// halt, one row per way a key whose version is not the instance's reaches it — a
// key naming no version, and a key naming another version — crossed with the two
// ways an instance's own version reaches the judgement: written on the instance,
// and resolved from a bare type to the highest version the catalog holds. A last
// row reaches the instance through a type that extends another base version than
// the key's Recipe implements.
//
// Every value the author needs to find the fault is asserted in the rendered
// finding — the instance, its type and the version it was compiled as, the key,
// the Recipe and what that Recipe implements — and so are the condition's short
// code, the locator and the category that sends the reader to the Manifest.
//
// 🔴 PLANTED DEFECT: in nodeMappingRefused (pkg/resolver/checks_node_mapped_recipe_implements.go),
// ask the second judgement with the key instead of the resolved type: every row
// here compiles, because the key itself is consistent with its Recipe.
func TestAKeyNotNamingTheInstancesVersionSendingItToAnotherVersionsRecipeHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		instanceType string
		key          string
		reference    string
		implements   string
		wantValues   []string
	}{
		{
			name:         "a key naming no version, an instance pinning the other version",
			instanceType: "core/postgres:2.0.0",
			key:          "core/postgres",
			reference:    "postgres-chart:1.0.0",
			implements:   "core/postgres:1.0.0",
			wantValues:   []string{`of type "core/postgres:2.0.0"`},
		},
		{
			name:         "a key naming no version, a bare instance the catalog resolves to the other version",
			instanceType: "core/postgres",
			key:          "core/postgres",
			reference:    "postgres-chart:1.0.0",
			implements:   "core/postgres:1.0.0",
			wantValues:   []string{`of type "core/postgres"`, `compiled as "core/postgres:2.0.0"`},
		},
		{
			name:         "a key naming another version, an instance pinning its own",
			instanceType: "core/postgres:2.0.0",
			key:          "core/postgres:1.0.0",
			reference:    "postgres-chart:1.0.0",
			implements:   "core/postgres:1.0.0",
			wantValues:   []string{`of type "core/postgres:2.0.0"`},
		},
		{
			name:         "a key naming another version, a bare instance the catalog resolves past it",
			instanceType: "core/postgres",
			key:          "core/postgres:1.0.0",
			reference:    "postgres-chart:1.0.0",
			implements:   "core/postgres:1.0.0",
			wantValues:   []string{`of type "core/postgres"`, `compiled as "core/postgres:2.0.0"`},
		},
		{
			name:         "a key naming another version of a type extending another base version",
			instanceType: "acme/pg:2.0.0",
			key:          "acme/pg:1.0.0",
			reference:    "postgres-chart:1.0.0",
			implements:   "core/postgres:1.0.0",
			wantValues:   []string{`of type "acme/pg:2.0.0"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := compileKeyVersionWorkspace(t,
				keyVersionAggregate(tc.instanceType), keyVersionManifest(tc.key, tc.reference))
			if err == nil {
				t.Fatal("an instance mapped to a Recipe implementing another version compiled without halting")
			}
			if !errors.Is(err, resolver.ErrManifestImplementsMismatch()) {
				t.Fatalf("the halt does not carry the mapping-inconsistency condition: %s", cerrors.CauseChain(err))
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
				`reached through manifest mapping key "` + tc.key + `"`,
				`mapped to recipe "` + tc.reference + `"`,
				`whose spec.implements is "` + tc.implements + `"`,
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
// halt above, one row per mapping that serves the instance's own version: the key
// naming that version; a key naming no version, or another version, mapped to a
// Recipe that implements the instance's version or a caret range covering both;
// and types served through the base they extend.
//
// Without it the proof above would pass against a rule that refused every key
// whose version differs from the instance's, or every mapping at all.
func TestTheMappingsThatServeTheInstancesVersionStillCompile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		instanceType string
		key          string
		reference    string
	}{
		{"the key naming the instance's version", "core/postgres:2.0.0", "core/postgres:2.0.0", "postgres-chart-v2:1.0.0"},
		{"a key naming no version mapped to the Recipe implementing that version", "core/postgres:2.0.0", "core/postgres", "postgres-chart-v2:1.0.0"},
		{"a bare instance and a key naming no version mapped to the Recipe its resolved version needs", "core/postgres", "core/postgres", "postgres-chart-v2:1.0.0"},
		{"a key naming another version and a Recipe implementing a caret range covering both", "core/postgres:1.4.0", "core/postgres:1.0.0", "postgres-chart-caret:1.0.0"},
		{"a key naming another version and a Recipe implementing the unversioned type", "core/postgres:2.0.0", "core/postgres:1.0.0", "postgres-chart-any:1.0.0"},
		{"a type served through the base it extends, by a key naming no version", "acme/pg:2.0.0", "acme/pg", "postgres-chart-v2:1.0.0"},
		{"a type served through the base it extends, by a key naming another version", "acme/pg:2.0.0", "acme/pg:1.0.0", "postgres-chart-any:1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := compileKeyVersionWorkspace(t,
				keyVersionAggregate(tc.instanceType), keyVersionManifest(tc.key, tc.reference))
			if err != nil {
				t.Fatalf("a mapping serving the instance's version halted: %s", cerrors.CauseChain(err))
			}
		})
	}
}
