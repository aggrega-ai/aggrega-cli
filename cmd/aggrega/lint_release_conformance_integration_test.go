// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// composeRecipe returns a minimal compose Recipe implementing the given component
// type — enough for the closure to resolve and the value-conformance pass to run.
func composeRecipe(name, implements string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ` + name + `
  version: "1.0.0"
spec:
  target: compose
  implements: ` + implements + `
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`
}

const confRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`

// TestIntegration_LintRelease_ValueConformance proves `aggrega lint --release`
// validates the Release's authored values against the closure's composed schemas
// (the build's Step-1 conformance, reused — no fork): conforming passes, a
// violation errors (non-zero exit), an inherited-pillar value is checked against
// the COMPOSED (extends) schema, and a `{{ .parameters.* }}` value is validated
// POST-resolution.
func TestIntegration_LintRelease_ValueConformance(t *testing.T) {
	std := standardCatalogDir(t)

	cases := []struct {
		name      string
		catalog   map[string]string // filename under catalog/ -> YAML
		manifest  string
		aggregate string
		release   string // confRelease unless a case needs parameters
		wantErr   bool
	}{
		{
			name: "conforming_value_passes",
			catalog: map[string]string{
				"comp.yaml":   confComp("test/conf-comp", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"recipe.yaml": composeRecipe("conf", "test/conf-comp"),
			},
			manifest:  confManifest("test/conf-comp:1.0.0", "compose/conf:1.0.0"),
			aggregate: confAggregate("test/conf-comp", "        x: 7"),
			wantErr:   false,
		},
		{
			name: "violating_value_errors",
			catalog: map[string]string{
				"comp.yaml":   confComp("test/conf-comp", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"recipe.yaml": composeRecipe("conf", "test/conf-comp"),
			},
			manifest:  confManifest("test/conf-comp:1.0.0", "compose/conf:1.0.0"),
			aggregate: confAggregate("test/conf-comp", "        x: 3"), // 3 < minimum 5
			wantErr:   true,
		},
		{
			name: "extends_inherited_pillar_value_checked_against_composed_schema",
			catalog: map[string]string{
				// Parent constrains x (minimum 5); child extends, does NOT redeclare x.
				"parent.yaml": confComp("test/conf-parent", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"child.yaml":  confChild("test/conf-child", "test/conf-parent", "    values:\n      name: { type: string }\n"),
				"recipe.yaml": composeRecipe("conf-child", "test/conf-child"),
			},
			manifest:  confManifest("test/conf-child:1.0.0", "compose/conf-child:1.0.0"),
			aggregate: confAggregate("test/conf-child", "        x: 3\n        name: a"), // x=3 violates the INHERITED minimum 5
			wantErr:   true,
		},
		{
			name: "templated_value_validated_post_resolution",
			catalog: map[string]string{
				"comp.yaml":   confComp("test/conf-comp", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"recipe.yaml": composeRecipe("conf", "test/conf-comp"),
			},
			manifest: confManifest("test/conf-comp:1.0.0", "compose/conf:1.0.0"),
			// x references an Aggregate parameter; the Release fulfills it with 3 →
			// resolves to 3 → violates minimum 5 (proving conformance runs on
			// POST-resolution values).
			aggregate: `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  parameters:
    size:
      type: integer
  components:
    app:
      type: test/conf-comp
      values:
        x: "{{ .parameters.size }}"
`,
			release: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    size: 3
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			cd := filepath.Join(ws, "catalog")
			for name, body := range tc.catalog {
				writeFixture(t, filepath.Join(cd, name), body)
			}
			writeFixture(t, filepath.Join(ws, "manifest.yaml"), tc.manifest)
			writeFixture(t, filepath.Join(ws, "aggregate.yaml"), tc.aggregate)
			rel := tc.release
			if rel == "" {
				rel = confRelease
			}
			writeFixture(t, filepath.Join(ws, "release.yaml"), rel)
			writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs:\n    - "+cd+"\n    - "+std+"\n")
			t.Chdir(ws)

			err := lintRelease(nil, "release.yaml", os.Stderr, os.Stdout)
			if tc.wantErr && err == nil {
				t.Fatalf("%s: expected a value-conformance error; got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%s: expected conformance to pass; got %v", tc.name, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "conformance") {
				t.Errorf("%s: error should be a value-conformance failure; got %v", tc.name, err)
			}
		})
	}
}

func confComp(name, schemaBody string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: ` + name + `
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
` + schemaBody
}

func confChild(name, extends, schemaBody string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: ` + name + `
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: ` + extends + `
  schema:
` + schemaBody
}

func confManifest(typeURI, recipeURI string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        ` + typeURI + `: ` + recipeURI + `
`
}

func confAggregate(typeURI, valuesBody string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: ` + typeURI + `
      values:
` + valuesBody + "\n"
}
