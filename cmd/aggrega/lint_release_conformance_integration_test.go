// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
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
  capability: compose
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
// validates the Release's authored values against the closure's schemas (the
// build's Step-1 conformance, reused — no fork): conforming passes, a violation
// errors (non-zero exit), a value governed by an INHERITED constraint is checked
// against the base's contract, and a `{{ .parameters.* }}` value is validated
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
		// wantDiag are fragments the diagnostic stream must contain, stated per
		// case rather than shared, so each violating case pins its own fault
		// instead of every case passing on one loose substring.
		wantDiag []string
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
			wantDiag: []string{
				`.spec.components.app.values.x: authored pillar does not conform to its declared schema`,
				`is less than 5`,
			},
		},
		{
			name: "extends_inherited_pillar_value_checked_against_the_base_contract",
			catalog: map[string]string{
				// The base constrains x (minimum 5); the child extends it and authors
				// no pillar content, so x and its constraint are inherited verbatim.
				"parent.yaml": confComp("test/conf-parent", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"child.yaml":  confChild("test/conf-child", "test/conf-parent"),
				"recipe.yaml": composeRecipe("conf-child", "test/conf-child"),
			},
			manifest:  confManifest("test/conf-child:1.0.0", "compose/conf-child:1.0.0"),
			aggregate: confAggregate("test/conf-child", "        x: 3"), // x=3 violates the INHERITED minimum 5
			wantErr:   true,
			wantDiag: []string{
				`.spec.components.app.values.x: authored pillar does not conform to its declared schema`,
				`is less than 5`,
			},
		},
		{
			name: "extends_inherited_pillar_conforming_value_passes",
			catalog: map[string]string{
				"parent.yaml": confComp("test/conf-parent", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"child.yaml":  confChild("test/conf-child", "test/conf-parent"),
				"recipe.yaml": composeRecipe("conf-child", "test/conf-child"),
			},
			manifest:  confManifest("test/conf-child:1.0.0", "compose/conf-child:1.0.0"),
			aggregate: confAggregate("test/conf-child", "        x: 7"),
			wantErr:   false,
		},
		{
			name: "templated_value_validated_post_resolution",
			catalog: map[string]string{
				"comp.yaml":   confComp("test/conf-comp", "    values:\n      x: { type: integer, minimum: 5 }\n"),
				"recipe.yaml": composeRecipe("conf", "test/conf-comp"),
			},
			manifest: confManifest("test/conf-comp:1.0.0", "compose/conf:1.0.0"),
			// x references an Aggregate parameter and the Release fulfills it with
			// 3, so conformance runs on the POST-resolution value: the diagnostic
			// quotes 3, the fulfilled parameter, rather than the reference text an
			// unresolved reading would leave behind. Which constraint it fails is
			// the type rather than the minimum beside it, because the value the
			// contract measures here arrives as the string "3" — so the type is
			// what this case pins, and the minimum is carried by the two cases
			// above that author their value directly.
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
			wantDiag: []string{
				`.spec.components.app.values.x: authored pillar does not conform to its declared schema`,
				`type: 3 has type`,
			},
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

			var diag bytes.Buffer
			err := validateRelease(resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, compilerops.VerbosityDetailed, &diag, io.Discard)
			if tc.wantErr && err == nil {
				t.Fatalf("%s: expected a value-conformance error; got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%s: expected conformance to pass; got %v", tc.name, err)
			}
			// The verdict error carries only the diagnostic count, so the
			// diagnostic stream is where the fault is actually described. Each
			// violating case states the fragments its own fault produces: the
			// offending key's locator immediately followed by the conformance
			// wording, plus the constraint that failed. Requiring the locator and
			// the wording adjacent is what keeps this from passing on a
			// conformance diagnostic about some other key of the same component.
			if tc.wantErr {
				if len(tc.wantDiag) == 0 {
					t.Fatalf("%s: a violating case must state what its diagnostic says", tc.name)
				}
				for _, want := range tc.wantDiag {
					if !strings.Contains(diag.String(), want) {
						t.Errorf("%s: expected a values-conformance diagnostic containing %q; got err=%v diagnostics:\n%s",
							tc.name, want, err, diag.String())
					}
				}
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

// confChild returns an extending Component. It takes no schema body: an
// extending Component authors no pillar content and declares the inheritance
// marker, receiving its base's pillars exactly as the base authored them.
func confChild(name, extends string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: ` + name + `
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: ` + extends + `
  schema: inherited
`
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
