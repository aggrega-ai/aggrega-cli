// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// imageChosenFailure is the sentence the fixture Recipe's assertion fails with.
const imageChosenFailure = "choose an image by setting infra.image in the Release"

// assertionWorkspace writes a Release, the Aggregate and Manifest it names, and a
// catalog whose Recipe asserts that the one component instance's infra.image was
// chosen — a value its Component defaults to "unset" and only a Release sets to
// anything else. image is what the Release sets infra.image to, or "" for a
// Release that leaves it to the default. It returns the workspace root, the
// catalog directory, and the paths of the Release and the Recipe.
func assertionWorkspace(t *testing.T, image string) (root, catalogRoot, releasePath, recipePath string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: assertion-warning
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
`)
	imageLine := ""
	if image != "" {
		imageLine = "            image: " + image + "\n"
	}
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: assertion-warning-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`+imageLine)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: assertion-warning-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      image: { type: string, default: "unset" }
      replicas: { type: integer, default: 1 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	recipePath = filepath.Join(catalogRoot, "recipes", "helm", "server.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  assertions:
    image_chosen: '{{ if eq .infra.image "unset" }}{{ fail "`+imageChosenFailure+`" }}{{ end }}'
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ .infra.replicas }}
    containers:
      server:
        image: "{{ .infra.image }}"
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	return root, catalogRoot, filepath.Join(root, "release.yaml"), recipePath
}

// TestValidateReportsAFailedAssertionAsAWarningAndABuildStillHalts holds both
// sides of how a failed assertion is reported, over one fixture.
//
// Validating the Recipe on its own writes no Release of the author's, so the
// assertion over infra.image fails there whether or not the Recipe is sound. The
// command reports it as a warning on stderr — the assertion's own sentence and the
// one saying a Release must satisfy it — and exits 0, and the report on stdout
// says the render stopped on it. A build always has a Release: one that leaves
// infra.image to the default halts on the same assertion with an error, exactly as
// a build always has, and one that sets it builds — the control showing that the
// assertion, and nothing else in the fixture, is what halted the first.
//
// Not parallel: the warning is read off the process's standard error, which the
// command writes to directly.
func TestValidateReportsAFailedAssertionAsAWarningAndABuildStillHalts(t *testing.T) {
	const awaitsRelease = "A Release must satisfy this assertion before a build of it can pass."

	t.Run("validating the Recipe on its own warns and exits 0", func(t *testing.T) {
		_, catalogRoot, _, recipePath := assertionWorkspace(t, "")
		var stdout bytes.Buffer
		var verr error
		stderr := captureStderr(t, func() {
			verr = dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), recipePath, "",
				[]string{catalogRoot}, false, &stdout)
		})
		if verr != nil {
			t.Fatalf("validate failed the Recipe rather than warning about its assertion:\n%s\nstdout:\n%s\nstderr:\n%s",
				formatCompilerError(verr), stdout.String(), stderr)
		}
		for _, want := range []string{"Resolution: valid", "Render: stopped on a failed assertion, reported as a warning"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("the report does not carry %q:\n%s", want, stdout.String())
			}
		}
		var warning string
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "  warning: ") && strings.Contains(line, imageChosenFailure) {
				warning = line
			}
		}
		if warning == "" {
			t.Fatalf("stderr carries no warning line naming the assertion:\n%s", stderr)
		}
		if !strings.HasSuffix(warning, awaitsRelease) {
			t.Errorf("the warning is not closed by the sentence saying a Release must satisfy it:\n%s", warning)
		}
	})

	t.Run("a build of a Release leaving the value to the default halts on the assertion", func(t *testing.T) {
		_, catalogRoot, releasePath, _ := assertionWorkspace(t, "")
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
		if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
			t.Fatalf("prime lockfile via pull: %v", err)
		}
		var diag, out bytes.Buffer
		err := dispatchBuild(context.Background(), ws, releasePath, []string{catalogRoot}, false, &diag, &out)
		if err == nil {
			t.Fatalf("the build returned nil; want it to halt on the assertion\ndiagnostics:\n%s", diag.String())
		}
		if !errors.Is(err, renderer.ErrAssertionFailed()) {
			t.Errorf("the build halted on something other than the assertion:\n%s", formatCompilerError(err))
		}
		if !chainContains(err, imageChosenFailure) {
			t.Errorf("the build's error does not carry the assertion's sentence:\n%s", formatCompilerError(err))
		}
	})

	t.Run("a build of a Release choosing the value passes the assertion", func(t *testing.T) {
		_, catalogRoot, releasePath, _ := assertionWorkspace(t, "registry.example/server:1.0")
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
		if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
			t.Fatalf("prime lockfile via pull: %v", err)
		}
		var diag, out bytes.Buffer
		if err := dispatchBuild(context.Background(), ws, releasePath, []string{catalogRoot}, false, &diag, &out); err != nil {
			t.Fatalf("the build of a Release satisfying the assertion failed:\n%s", formatCompilerError(err))
		}
	})
}
