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

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The test in this file drives what an author meets who pins the provider a
// Recipe renders through, by writing a version after its capability, through the
// real pull and build. The pin chooses which version of the plugin serving the
// capability renders the Recipe and never renames it: the Manifest maps the
// pinned Recipe by the same capability-qualified name it maps an unpinned one by,
// and the render goes through the plugin version the pin names.
//
// The version pinned is read off the render of the unpinned Recipe rather than
// written into the test, so the test pins whatever version the binary under test
// serves and never mirrors a release number.
//
// Every workspace is declared offline, so no row can reach a registry: an entity
// the local catalog does not hold is refused rather than fetched, and a row that
// would otherwise have looked one up remotely fails on the network refusal
// instead of on whatever a registry happens to answer.

// pinnedCapabilityWorkspace lays down an offline workspace whose one component
// binds the Recipe `svc`, declaring capability as given, through the Manifest
// mapping value mapping.
func pinnedCapabilityWorkspace(t *testing.T, root, capability, mapping string) (catalogDir, releasePath string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "offline: true\nworkspace: {}\n")
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/svc/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/svc
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/compose/svc/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: 1.0.0
spec:
  capability: "`+capability+`"
  implements: acme/svc:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "svc:1"
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: svc-app
  version: 1.0.0
spec:
  components:
    svc:
      type: acme/svc:1.0.0
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: svc-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/svc:1.0.0: "`+mapping+`"
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: svc
  environment: local
  version: 1.0.0
spec:
  aggregate: svc-app
  manifest: svc-manifest
  boundaries:
    main:
      directory: out
      components:
        svc: {}
`)
	return catalogDir, releasePath
}

// renderedProviderVersion reads the provider version the render stamped on the
// component's output.
func renderedProviderVersion(t *testing.T, root string) string {
	t.Helper()
	compose := string(mustReadFile(t, filepath.Join(root, "dist", "out", "compose", "docker-compose.yaml")))
	const label = "aggrega.ai/provider-version: "
	for _, line := range strings.Split(compose, "\n") {
		if _, version, ok := strings.Cut(line, label); ok {
			return strings.TrimSpace(version)
		}
	}
	t.Fatalf("the render carries no provider version:\n%s", compose)
	return ""
}

// TestAPinnedCapabilityKeepsTheRecipesName pins the pinned Recipe under the bare
// mapping (it builds, through the pinned version), and holds two refusals beside
// it, one fault each: a mapping spelling the pin into the name is refused as a
// mis-shaped reference before anything is looked up, and a pin no serving plugin
// version satisfies is refused as the pin it is — which it can be only because
// the Recipe was found under its bare name.
func TestAPinnedCapabilityKeepsTheRecipesName(t *testing.T) {
	t.Parallel()
	// Each step compiles a workspace of its own, so no lockfile one step wrote is
	// read by the next.
	run := func(capability, mapping string) (string, error) {
		t.Helper()
		root := t.TempDir()
		catalogDir, rel := pinnedCapabilityWorkspace(t, root, capability, mapping)
		if err := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: rel,
			CatalogDirs: []string{catalogDir},
			DiagOut:     io.Discard,
			Out:         io.Discard,
			diag:        buildDiagOptions{quiet: true},
		}); err != nil {
			return root, err
		}
		return root, dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			rel, []string{catalogDir}, false, io.Discard, io.Discard)
	}
	refused := func(err error, condition error, what string) string {
		t.Helper()
		if err == nil {
			t.Fatalf("%s built", what)
		}
		printed := formatCompilerError(err)
		if !errors.Is(err, condition) {
			t.Fatalf("%s must halt with %v; got:\n%s", what, condition, printed)
		}
		return printed
	}

	root, err := run("compose", "compose/svc:1.0.0")
	if err != nil {
		t.Fatalf("control: the unpinned Recipe must build:\n%s", formatCompilerError(err))
	}
	served := renderedProviderVersion(t, root)

	pinned := "compose:" + served
	root, err = run(pinned, "compose/svc:1.0.0")
	if err != nil {
		t.Fatalf("a Recipe pinning %q, mapped by its capability and name, must build:\n%s",
			pinned, formatCompilerError(err))
	}
	if got := renderedProviderVersion(t, root); got != served {
		t.Errorf("the pinned Recipe rendered through provider version %q; want the pinned %q", got, served)
	}

	_, err = run(pinned, pinned+"/svc:1.0.0")
	printed := refused(err, resolver.ErrMalformedRecipeReference(), "a mapping spelling the pin into the Recipe's name")
	for _, want := range []string{
		"E0095",
		`spec.mappings.components.type`,
		`maps to "` + pinned + `/svc:1.0.0"`,
		`carries a version pin in its capability segment "` + pinned + `"`,
		"a recipe reference names the recipe by its bare capability",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, printed)
		}
	}

	_, err = run("compose:9.9.9", "compose/svc:1.0.0")
	printed = refused(err, catalog.ErrProviderVersionUnsatisfiable(), "a pin no serving plugin version satisfies")
	if !strings.Contains(printed, `capability "compose" pins "9.9.9"`) {
		t.Errorf("the refusal does not name the pin:\n%s", printed)
	}
}
