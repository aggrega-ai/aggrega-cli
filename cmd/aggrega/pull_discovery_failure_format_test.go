// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestPullDiscoveryFailureNoticeCarriesTheConditionCode holds the notice a pull
// prints when its external-input discovery render could not finish to the
// rendering the command's own refusals use: the failure it carries opens with the
// bracketed head naming the condition's short code, on the lines beneath the
// notice's first sentence and indented under it, and the pull still completes.
//
// The workspace maps a Recipe for one component type and none for the other while
// a catalog Recipe implements it, so the pull resolves every dependency and the
// render it runs to discover external inputs stops on the unmapped instance — the
// condition a validate of the same Release refuses with that short code.
func TestPullDiscoveryFailureNoticeCarriesTheConditionCode(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\noffline: true\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
    worker:
      type: test/worker
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
`)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
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
        app: {}
        worker: {}
`)
	catalogDir := filepath.Join(root, "catalog")
	for _, typ := range []string{"svc", "worker"} {
		writeFixture(t, filepath.Join(catalogDir, "component", "test", typ, "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/`+typ+`
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	}
	for recipe, implements := range map[string]string{"svc": "test/svc", "worker-recipe": "test/worker"} {
		writeFixture(t, filepath.Join(catalogDir, "recipe", "compose", recipe, "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: `+recipe+`
  version: "1.0.0"
spec:
  capability: compose
  implements: `+implements+`
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	}

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	var diag, out bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: &diag, Out: &out, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("a pull whose discovery render stops must still complete: %v\ndiagnostics:\n%s", err, diag.String())
	}

	code, ok := cerrors.ShortCodeOf(catalog.ErrNoRecipeResolved())
	if !ok || code == "" {
		t.Fatal("the unmapped-instance condition carries no short code")
	}
	lines := strings.Split(diag.String(), "\n")
	opening := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "  external inputs: could not finish rendering ") {
			opening = i
			break
		}
	}
	if opening < 0 {
		t.Fatalf("the pull printed no discovery-failure notice:\n%s", diag.String())
	}
	if !strings.HasSuffix(lines[opening], " to discover them:") {
		t.Errorf("the notice's first sentence must end where the failure's own rendering begins:\n%q", lines[opening])
	}
	if opening+1 >= len(lines) {
		t.Fatalf("the notice carries no failure beneath its first sentence:\n%s", diag.String())
	}
	head := lines[opening+1]
	if !strings.HasPrefix(head, "    [") || !strings.Contains(head, " "+code+"]") {
		t.Errorf("the failure beneath the notice must open, indented under it, with a head naming %s:\n%q\nfull diagnostics:\n%s", code, head, diag.String())
	}
	for _, want := range []string{
		"  external inputs: dependency pins were written and are complete;",
		"  external inputs: a build of this Release halts naming the unpinned URL;",
	} {
		if !strings.Contains(diag.String(), want) {
			t.Errorf("the notice's other lines must stand unchanged; missing %q in:\n%s", want, diag.String())
		}
	}
	if !strings.Contains(out.String(), "External inputs were not fully discovered") {
		t.Errorf("the completion output does not say the pin set is incomplete; got:\n%s", out.String())
	}
}
