// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// The tests in this file drive what an author and an operator meet when a
// parameter's `min` or `max` is in play, through the real pull and build. The
// compact form gives the pair one meaning per type — a number's value, a
// string's length, a list's item count — and the parameter check applies it the
// same way: a Release value outside the bound halts naming the parameter, the
// size and the bound, and a value inside both builds. On a type the pair means
// nothing on, the Aggregate's declaration itself is refused, naming the key.

// parameterBoundsWorkspace lays down a workspace whose Aggregate declares one
// parameter, `p`, as declaration (the lines beneath its key), and whose Release
// supplies value for it.
func parameterBoundsWorkspace(t *testing.T, root, declaration, value string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/web/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/web
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/compose/web/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: web
  version: 1.0.0
spec:
  capability: compose
  implements: acme/web:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "web:1"
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: bounds-app
  version: 1.0.0
spec:
  parameters:
    p:
`+declaration+`
  components:
    web:
      type: acme/web:1.0.0
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: bounds-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/web:1.0.0: compose/web:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: bounds
  environment: local
  version: 1.0.0
spec:
  aggregate: bounds-app
  manifest: bounds-manifest
  parameters:
    p: `+value+`
  boundaries:
    main:
      directory: out
      components:
        web: {}
`)
	return catalogDir, releasePath
}

// TestAParameterOutsideItsMinOrMaxHaltsTheBuild drives one row per meaning the
// pair takes and one per type it means nothing on, each refused row carrying one
// fault and each paired with a control the same workspace builds.
func TestAParameterOutsideItsMinOrMaxHaltsTheBuild(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	const (
		hostList = "      type: array\n      items: { type: string, format: hostname }\n      min: 1\n      max: 2"
		region   = "      type: string\n      min: 3"
	)
	for _, tc := range []struct {
		name        string
		declaration string
		value       string
		want        []string
		control     string
		controlDecl string
	}{
		{
			name: "an empty list under min 1", declaration: hostList, value: "[]",
			want:    []string{".spec.parameters.p", `parameter "p" holds 0 item(s), below min 1`},
			control: "[a.example.com]",
		},
		{
			name: "three items under max 2", declaration: hostList,
			value:   "[a.example.com, b.example.com, c.example.com]",
			want:    []string{".spec.parameters.p", `parameter "p" holds 3 item(s), above max 2`},
			control: "[a.example.com]",
		},
		{
			name: "a string shorter than min 3", declaration: region, value: "eu",
			want:    []string{".spec.parameters.p", `parameter "p" length 2, below min 3`},
			control: "eu-west",
		},
		{
			name: "min on a boolean parameter", declaration: "      type: boolean\n      min: 1", value: "true",
			want: []string{
				".spec.parameters.p.min",
				"parameter \"p\" of type boolean declares `min`, which bounds nothing on that type",
			},
			control: "true", controlDecl: "      type: boolean",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Each case compiles a workspace of its own, so no lockfile the refused
			// one wrote is read by the control.
			run := func(declaration, value string) error {
				t.Helper()
				root := t.TempDir()
				catalogDir, rel := parameterBoundsWorkspace(t, root, declaration, value)
				if err := pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: rel,
					CatalogDirs: []string{catalogDir},
					DiagOut:     io.Discard,
					Out:         io.Discard,
					diag:        buildDiagOptions{quiet: true},
				}); err != nil {
					return err
				}
				return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					rel, []string{catalogDir}, false, io.Discard, io.Discard)
			}

			err := run(tc.declaration, tc.value)
			if err == nil {
				t.Fatal("a parameter outside its declared bound, or a bound that bounds nothing, built")
			}
			printed := formatCompilerError(err)
			if !errors.Is(err, resolver.ErrParameterContractViolation()) {
				t.Fatalf("the halt must be the parameter-contract condition:\n%s", printed)
			}
			for _, w := range append([]string{"E0164"}, tc.want...) {
				if !strings.Contains(printed, w) {
					t.Errorf("the printed diagnostic does not carry %q:\n%s", w, printed)
				}
			}

			controlDecl := tc.controlDecl
			if controlDecl == "" {
				controlDecl = tc.declaration
			}
			if err := run(controlDecl, tc.control); err != nil {
				t.Fatalf("the control must build:\n%s", formatCompilerError(err))
			}
		})
	}
}
