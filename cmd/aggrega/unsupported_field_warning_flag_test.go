// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// unsupportedFieldMarker returns the user-facing text of one diagnostic of the
// unsupported-field class, taken from the class's own constructor rather than
// written out as a literal, and fails the test if that constructor does not
// carry the code the display filter matches on.
//
// The warning surface a user reads carries only the diagnostic's rendered text —
// the code itself never appears in the emitted line — so a test driven through
// the command surface has no way to match the code directly. Deriving the
// expected text from the constructor is the next best thing and buys the
// property that matters: the wording lives in exactly one place, so rewording
// the diagnostic cannot fail these tests, while repointing the constructor at a
// different class fails them loudly.
func unsupportedFieldMarker(t *testing.T, target, field string) string {
	t.Helper()
	d := rendercore.UnsupportedFieldDiagnostic(target, field)
	if d.Code != renderer.UnsupportedFieldCode {
		t.Fatalf("constructor carries code %q, want the class code %q the display filter matches on",
			d.Code, renderer.UnsupportedFieldCode)
	}
	if d.Message == "" {
		t.Fatal("constructor produced an empty message; nothing to match on the warning surface")
	}
	return d.Message
}

// runAggrega drives one invocation through the real command tree — a fresh root,
// cobra's own flag parsing, and the root's persistent pre-run (which installs the
// workspace resolver every command body reads). Going through the root is the
// point: it is the only path that exercises the flag registration, the parse, and
// the command body's own use of the parsed value. Cobra's own streams are
// captured so a failure's text is available to the caller, while the diagnostic
// surface under test stays on the process stderr the pipeline writes to.
func runAggrega(t *testing.T, args ...string) (cobraOut string, err error) {
	t.Helper()
	var buf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err = root.Execute()
	return buf.String(), err
}

// composeUnsupportedFieldWorkspace lays down a complete single-node workspace
// whose recipe renders through the compose target and whose Workload declares
// replicas — a field compose cannot express, so a real render of this fixture
// emits the unsupported-field class. It returns the Release path, the catalog
// root, and the recipe's own path (the entity-scope target).
func composeUnsupportedFieldWorkspace(t *testing.T, root string) (releaseFile, catalogRoot, recipeFile string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: warn-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: warn-smoke-release
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
            replicas: 2
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: warn-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-compose-recipe:1.0.0
`)

	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	// The compose provider entity the recipe's capability binding resolves to, so
	// the lockfile walk finds it in this catalog rather than reaching for a registry.
	seedEmbeddedPlugins(t, filepath.Join(catalogRoot, "providers"), "provider/compose")
	recipeFile = filepath.Join(catalogRoot, "recipes", "compose", "server.yaml")
	writeFixture(t, recipeFile, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-compose-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return filepath.Join(root, "release.yaml"), catalogRoot, recipeFile
}

// renderedTree reads every regular file under dir into a path-keyed map so two
// runs' rendered output can be compared byte for byte. It fails the test when
// the tree holds no files at all, since an empty tree would make any comparison
// against it vacuously true.
func renderedTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if len(out) == 0 {
		t.Fatalf("no rendered files under %s", dir)
	}
	return out
}

// TestBuildCmd_UnsupportedFieldWarningFlagReachesTheFilter pins the build
// command's half of the flag's wiring. The suppression is decided deep in the
// render-warning display pass, and the flag reaches that decision through a
// chain the command body assembles; a test that exercised the display pass
// directly would prove the filter filters while leaving the chain unpinned.
// So this drives two real builds of the same fixture through the command tree
// and reads the warning surface a user reads: the class is present without the
// flag and gone with it, and both builds still render.
func TestBuildCmd_UnsupportedFieldWarningFlagReachesTheFilter(t *testing.T) {
	// Not parallel: the warning surface under assertion is the process stderr, and
	// the test moves the process working directory.
	work := t.TempDir()
	t.Chdir(work)
	releaseFile, catalogRoot, _ := composeUnsupportedFieldWorkspace(t, work)

	// Build is read-only about dependency resolution: prime the lockfile first.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: work}), ReleasePath: releaseFile, CatalogDirs: []string{catalogRoot}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}

	marker := unsupportedFieldMarker(t, "compose", "replicas")

	var unflaggedErr, flaggedErr error
	var unflaggedOut, flaggedOut string
	shown := captureStderr(t, func() {
		unflaggedOut, unflaggedErr = runAggrega(t, "build", "--release", releaseFile, "--catalog", catalogRoot)
	})
	if unflaggedErr != nil {
		t.Fatalf("build without the flag failed: %v\ncobra output:\n%s\nstderr:\n%s", unflaggedErr, unflaggedOut, shown)
	}
	if !strings.Contains(shown, marker) {
		t.Fatalf("the unsupported-field class is missing from the default warning surface, so this fixture proves nothing about suppression.\nwant to contain: %s\ngot:\n%s", marker, shown)
	}
	unflaggedRender := renderedTree(t, filepath.Join(work, "dist"))

	suppressed := captureStderr(t, func() {
		flaggedOut, flaggedErr = runAggrega(t, "build",
			"--release", releaseFile, "--catalog", catalogRoot, "--"+flagNoUnsupportedFieldWarnings)
	})
	if flaggedErr != nil {
		t.Fatalf("build with --%s failed: %v\ncobra output:\n%s\nstderr:\n%s",
			flagNoUnsupportedFieldWarnings, flaggedErr, flaggedOut, suppressed)
	}
	if strings.Contains(suppressed, marker) {
		t.Errorf("--%s did not reach the warning filter: the class still surfaced.\nunwanted: %s\ngot:\n%s",
			flagNoUnsupportedFieldWarnings, marker, suppressed)
	}

	// The suppressed run must have rendered, not merely stayed quiet: a build that
	// halted before the render would also print no warning, and that false green
	// is exactly what this comparison closes. Byte-equality of the two trees is
	// the same assertion the flag's own description makes — that it filters the
	// display and leaves the emitted artifacts alone.
	flaggedRender := renderedTree(t, filepath.Join(work, "dist"))
	if len(flaggedRender) != len(unflaggedRender) {
		t.Fatalf("suppressed build rendered %d file(s), unsuppressed rendered %d; the flag must not change what is emitted",
			len(flaggedRender), len(unflaggedRender))
	}
	for rel, want := range unflaggedRender {
		got, ok := flaggedRender[rel]
		if !ok {
			t.Errorf("suppressed build did not emit %s", rel)
			continue
		}
		if got != want {
			t.Errorf("suppressed build emitted different bytes for %s; the flag must not change the rendered output", rel)
		}
	}
}

// TestValidateCmd_UnsupportedFieldWarningFlagReachesTheFilter pins the validate
// command's half, which is a separate registration feeding a separate variable
// down a separate call path — so the build test above cannot stand in for it.
// The entity scope drives a Recipe through a real render around a synthesized
// minimal Release, which is the scope the flag's help text says it applies
// to, and the same warning surface carries the result.
func TestValidateCmd_UnsupportedFieldWarningFlagReachesTheFilter(t *testing.T) {
	// Not parallel: the warning surface is the process stderr, which this moves.
	work := t.TempDir()
	t.Chdir(work)
	_, catalogRoot, recipeFile := composeUnsupportedFieldWorkspace(t, work)

	marker := unsupportedFieldMarker(t, "compose", "replicas")

	var unflaggedErr, flaggedErr error
	var unflaggedOut, flaggedOut string
	shown := captureStderr(t, func() {
		unflaggedOut, unflaggedErr = runAggrega(t, "validate", recipeFile, "--catalog", catalogRoot)
	})
	if unflaggedErr != nil {
		t.Fatalf("validate without the flag failed: %v\ncobra output:\n%s\nstderr:\n%s", unflaggedErr, unflaggedOut, shown)
	}
	if !strings.Contains(unflaggedOut, "Render:") {
		t.Fatalf("the entity scope did not reach its render tier, so this fixture proves nothing about suppression.\nreport:\n%s", unflaggedOut)
	}
	if !strings.Contains(shown, marker) {
		t.Fatalf("the unsupported-field class is missing from the default warning surface.\nwant to contain: %s\ngot:\n%s", marker, shown)
	}

	suppressed := captureStderr(t, func() {
		flaggedOut, flaggedErr = runAggrega(t, "validate", recipeFile,
			"--catalog", catalogRoot, "--"+flagNoUnsupportedFieldWarnings)
	})
	if flaggedErr != nil {
		t.Fatalf("validate with --%s failed: %v\ncobra output:\n%s\nstderr:\n%s",
			flagNoUnsupportedFieldWarnings, flaggedErr, flaggedOut, suppressed)
	}
	if strings.Contains(suppressed, marker) {
		t.Errorf("--%s did not reach the warning filter: the class still surfaced.\nunwanted: %s\ngot:\n%s",
			flagNoUnsupportedFieldWarnings, marker, suppressed)
	}
	// Same false-green guard as the build arm: the suppressed run must have
	// rendered for its silence to mean anything.
	if !strings.Contains(flaggedOut, "Render:") {
		t.Errorf("suppressed validate did not reach its render tier, so its silence is not evidence of suppression.\nreport:\n%s", flaggedOut)
	}
}
