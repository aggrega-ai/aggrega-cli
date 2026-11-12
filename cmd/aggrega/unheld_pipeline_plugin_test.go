// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

// The tests in this file drive what a user meets whose FeatureRecipe runs a
// pipeline step naming a Plugin that nothing holds: no catalog directory, no
// cache slot, no registry and not the binary. The pull refuses it before it
// writes anything, naming the Plugin and the FeatureRecipe that names it, with
// the condition and the sentence the build raises when it loads that step — so a
// pull never leaves behind a lockfile its build then refuses.

// unheldPluginEntityURI is the Plugin the FeatureRecipe's pipeline step names.
const unheldPluginEntityURI = "pipeline-step/gateway-step:1.0.0"

// unheldPluginNamedBy is the FeatureRecipe whose pipeline names the Plugin, as a
// refusal names it.
const unheldPluginNamedBy = "helm/control:1.0.0"

// unheldPluginWorkspace writes an offline workspace whose catalog directory holds
// a Component, its Recipe, a Feature the Aggregate attaches, and that Feature's
// FeatureRecipe, whose one pipeline step names unheldPluginEntityURI. When guest is
// non-nil the Plugin entity is authored in the catalog directory too, with guest
// beside it under the filename the plugin cascade reads it by. It returns the
// workspace root, the Release path and the Plugin's directory.
func unheldPluginWorkspace(t *testing.T, guest []byte) (root, releasePath, pluginDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n")
	writeFixture(t, filepath.Join(catalogDir, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
`)
	writeFixture(t, filepath.Join(catalogDir, "feature", "test", "control", "1.0.0", "feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: test/control
  version: "1.0.0"
  labels:
    feature.aggrega.ai/kind: secret
spec:
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "feature-recipe", "helm", "control", "1.0.0", "feature-recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: control
  version: "1.0.0"
spec:
  capability: helm
  implements: test/control
  artifacts:
    control-config.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: control-config
      data:
        source: frp
  pipeline:
    - id: patch
      plugin: `+unheldPluginEntityURI+`
      function: step
`)
	pluginDir = filepath.Join(catalogDir, "plugin", "pipeline-step", "gateway-step", "1.0.0")
	if guest != nil {
		writeFixture(t, filepath.Join(pluginDir, "plugin.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: gateway-step
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  exports:
    - name: step
      operates: patch
      scope: own
`)
		writeFixtureBytes(t, filepath.Join(pluginDir, pluginpath.PluginLeaf("pipeline-step/gateway-step")), guest)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
      features:
        control:
          type: test/control
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
    features:
      type:
        test/control:1.0.0: helm/control:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
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
`)
	return root, releasePath, pluginDir
}

// unheldPluginPull runs the real pull over the workspace at root.
func unheldPluginPull(t *testing.T, root, releasePath string) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		DiagOut:     &bytes.Buffer{},
		Out:         &bytes.Buffer{},
	})
}

// unheldPluginBuild runs the real build over the workspace at root, writing under it.
func unheldPluginBuild(t *testing.T, root, releasePath string) error {
	t.Helper()
	var diag, out bytes.Buffer
	return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, nil, false, &diag, &out)
}

// assertRefusesTheUnheldPlugin fails unless err is the refusal for the Plugin no
// source holds: the plugin-entity-not-found condition, matched through its
// accessor, carrying the sentence the one composition of that refusal writes for a
// well-formed reference, which names the Plugin and the two ways to supply it.
func assertRefusesTheUnheldPlugin(t *testing.T, surface string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("the %s accepted a pipeline step naming a Plugin no source holds", surface)
	}
	printed := formatCompilerError(err)
	if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) {
		t.Fatalf("the %s refused, but not with the plugin-entity-not-found condition:\n%s", surface, printed)
	}
	sentence := pluginpath.UnresolvedEntity(unheldPluginEntityURI, pluginpath.SurfaceEntityOnly).Error()
	if !strings.Contains(printed, sentence) {
		t.Errorf("the %s refusal does not carry the sentence a well-formed reference nothing holds is reported with\nwant within it: %s\ngot:\n%s",
			surface, sentence, printed)
	}
	for _, want := range []string{unheldPluginEntityURI, "is a Plugin entity URI", "place the Plugin entity in a catalog directory", "publish it to a registry"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the %s refusal does not say %q:\n%s", surface, want, printed)
		}
	}
	if strings.Contains(printed, "must be a plugin-type-scoped") {
		t.Errorf("the %s refusal tells the author to rewrite a reference that is already well formed:\n%s", surface, printed)
	}
}

// TestAPullAndItsBuildRefuseAPipelinePluginNoSourceHolds drives the pull and the
// build on the same input, a FeatureRecipe pipeline step naming a Plugin no source
// holds.
//
// The pull refuses with the plugin-entity-not-found condition, naming the Plugin
// and the FeatureRecipe that names it, and writes neither a lockfile nor a trust
// record. The build's own refusal of the same reference — reached over the lockfile
// a pull wrote while the Plugin still sat in the catalog directory, so the build
// gets as far as loading the step — carries the same condition and the same
// sentence. The control is that earlier state: the Plugin in the catalog directory
// with its guest beside it, where the pull and the build both complete; the pull
// over the removed Plugin then refuses and leaves that lockfile as it was. A pull
// that refused every pipeline Plugin would fail the control, and one that refused
// nothing would fail the first half.
func TestAPullAndItsBuildRefuseAPipelinePluginNoSourceHolds(t *testing.T) {
	t.Run("no source holds the Plugin", func(t *testing.T) {
		root, releasePath, _ := unheldPluginWorkspace(t, nil)

		err := unheldPluginPull(t, root, releasePath)
		assertRefusesTheUnheldPlugin(t, "pull", err)
		if printed := formatCompilerError(err); !strings.Contains(printed, unheldPluginNamedBy) {
			t.Errorf("the pull refusal does not name the FeatureRecipe %s whose pipeline names the Plugin:\n%s", unheldPluginNamedBy, printed)
		}
		if _, statErr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the refused pull wrote a lockfile (stat: %v); a refusal writes nothing", statErr)
		}
		if _, statErr := os.Stat(pluginstrust.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the refused pull wrote a trust record (stat: %v); a refusal writes nothing", statErr)
		}
	})

	t.Run("the Plugin sits in a catalog directory, then is removed", func(t *testing.T) {
		guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm"))
		if err != nil {
			t.Fatalf("read the guest fixture: %v", err)
		}
		root, releasePath, pluginDir := unheldPluginWorkspace(t, guest)

		if err := unheldPluginPull(t, root, releasePath); err != nil {
			t.Fatalf("control: the pull refused a Plugin authored in the catalog directory with its guest beside it:\n%s", formatCompilerError(err))
		}
		if err := unheldPluginBuild(t, root, releasePath); err != nil {
			t.Fatalf("control: the build refused what its pull resolved:\n%s", formatCompilerError(err))
		}
		if rendered := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(rendered, "control-config") {
			t.Errorf("control: the build did not render the FeatureRecipe whose pipeline runs the step; got:\n%s", rendered)
		}
		heldLock := mustReadFile(t, lockfile.PathFor(releasePath))

		if err := os.RemoveAll(pluginDir); err != nil {
			t.Fatalf("remove the Plugin from the catalog directory: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
			t.Fatalf("clear the rendered tree: %v", err)
		}
		assertRefusesTheUnheldPlugin(t, "build", unheldPluginBuild(t, root, releasePath))

		err = unheldPluginPull(t, root, releasePath)
		assertRefusesTheUnheldPlugin(t, "pull", err)
		if after := mustReadFile(t, lockfile.PathFor(releasePath)); !bytes.Equal(after, heldLock) {
			t.Errorf("the refused pull rewrote the lockfile a completed pull had written\nbefore:\n%s\nafter:\n%s", heldLock, after)
		}
	})
}
