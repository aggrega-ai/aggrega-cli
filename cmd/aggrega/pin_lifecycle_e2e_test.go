// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// What this file proves.
//
// An entity's pins are recorded by one step — the author's own `aggrega pin` —
// which needs no Release, and which `pull --pin-sources` and `push --pin-sources`
// run on the author's behalf. By default a pull records no pin and halts on an
// entity whose lock does not hold them; a workspace setting per operation makes
// pinning that operation's default; and the flag overrides its own setting in
// both directions.
//
// The single path is proved twice: structurally, by the pin step's own guard over
// the module's call graph, and here by invocation — the three callers write the
// same bytes from the same starting state.

// pinLifecycleWorkspace lays down a workspace holding the `server` Recipe in a
// per-version directory of its own, with an authored-identity chart source served
// at chartURL and one imported file, and NO Release. withRelease adds the
// Aggregate, Manifest and Release that build it. config is the workspace
// configuration's body after its root marker.
func pinLifecycleWorkspace(t *testing.T, root, chartURL, config string, withRelease bool) (recipePath string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n"+config)
	recipeDir := filepath.Join(root, "catalog", "recipe", "helm", "server", "1.0.0")
	recipePath = filepath.Join(recipeDir, "recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: helm-https-chart
    url: `+chartURL+`
    version: "1.0.0"
  artifacts:
    extra.yaml:
      $import: ./extra.yaml
`)
	writeFixture(t, filepath.Join(recipeDir, "extra.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: extra\ndata:\n  k: v\n")
	if !withRelease {
		return recipePath
	}
	writeFixture(t, filepath.Join(root, "catalog", "component", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(root, "catalog", "providers"))
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
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
        server: helm/server:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: pin-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	return recipePath
}

func pinLifecyclePull(t *testing.T, root string, pinSources *bool) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		PinSources:  pinSources,
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

func pinLifecyclePush(t *testing.T, root, recipePath string, pinSources *bool) error {
	t.Helper()
	return pushRun(context.Background(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: recipePath,
		Registry:   "example.com",
		PushTarget: memory.New(),
		PinSources: pinSources,
		Out:        io.Discard,
	})
}

func boolPtr(v bool) *bool { return &v }

// TestTheThreeCallersOfThePinStepWriteTheSameBytes is clause 6's (a), (c), (d) and
// (e) over one workspace: the author's own step with no Release in reach, then a
// pull asked to pin, then a publication asked to pin, each from a removed lock —
// and the three locks compared byte for byte, with the authored Recipe held
// byte-identical throughout.
func TestTheThreeCallersOfThePinStepWriteTheSameBytes(t *testing.T) {
	srv := serveChart(t, pinChartTgz(t))
	root := t.TempDir()
	t.Chdir(root)
	recipePath := pinLifecycleWorkspace(t, root, srv.URL+"/pinchart.tgz", "", false)
	lockPath := recipepin.LockPath(recipePath)
	recipeBefore := mustReadFile(t, recipePath)

	var out bytes.Buffer
	if err := pinRun(context.Background(), &out, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipePath, false); err != nil {
		t.Fatalf("aggrega pin with no Release in the workspace: %v", err)
	}
	byPin := mustReadFile(t, lockPath)
	for _, want := range []string{"sourceDigests", "artifactDigests", srv.URL + "/pinchart.tgz"} {
		if !strings.Contains(string(byPin), want) {
			t.Fatalf("the author's own step must record both classes; %q is missing from:\n%s", want, byPin)
		}
	}

	pinLifecycleWorkspace(t, root, srv.URL+"/pinchart.tgz", "", true)
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove the lock: %v", err)
	}
	if err := pinLifecyclePull(t, root, boolPtr(true)); err != nil {
		t.Fatalf("pull --pin-sources: %v", err)
	}
	if got := mustReadFile(t, lockPath); !bytes.Equal(got, byPin) {
		t.Errorf("pull --pin-sources wrote other bytes than aggrega pin;\n--- pin ---\n%s\n--- pull ---\n%s", byPin, got)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove the lock: %v", err)
	}
	if err := pinLifecyclePush(t, root, recipePath, boolPtr(true)); err != nil {
		t.Fatalf("push --pin-sources: %v", err)
	}
	if got := mustReadFile(t, lockPath); !bytes.Equal(got, byPin) {
		t.Errorf("push --pin-sources wrote other bytes than aggrega pin;\n--- pin ---\n%s\n--- push ---\n%s", byPin, got)
	}

	if got := mustReadFile(t, recipePath); !bytes.Equal(got, recipeBefore) {
		t.Errorf("a caller of the pin step wrote the authored Recipe")
	}
}

// TestPullHaltsOnAnUnpinnedEntityAndWritesNothing is the default: a pull over an
// entity whose lock does not record its pins stops under the registered
// condition, names each class it is missing and the step that records them, and
// leaves neither the lock nor the lockfile behind.
func TestPullHaltsOnAnUnpinnedEntityAndWritesNothing(t *testing.T) {
	srv := serveChart(t, pinChartTgz(t))
	root := t.TempDir()
	t.Chdir(root)
	recipePath := pinLifecycleWorkspace(t, root, srv.URL+"/pinchart.tgz", "", true)

	err := pinLifecyclePull(t, root, nil)
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("a default pull over an unpinned entity must halt under the unpinned-entity condition; got %v", err)
	}
	for _, want := range []string{"remote source", "imported file", "aggrega pin", "pin_sources_at_pull"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt must name %q:\n%v", want, err)
		}
	}
	if _, serr := os.Stat(recipepin.LockPath(recipePath)); !os.IsNotExist(serr) {
		t.Errorf("a halted pull wrote the lock; stat err = %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(root, "release.lock")); !os.IsNotExist(serr) {
		t.Errorf("a halted pull wrote the lockfile; stat err = %v", serr)
	}

	// The positive control: the same pull over the entity once it is pinned.
	if err := pinRun(context.Background(), io.Discard, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipePath, false); err != nil {
		t.Fatalf("aggrega pin: %v", err)
	}
	if err := pinLifecyclePull(t, root, nil); err != nil {
		t.Fatalf("a default pull over a pinned entity must succeed: %v", err)
	}
}

// TestPinSourcesOverridesItsOwnKeyInBothDirections is the grid for the two
// commands, each against its own key: the key turning pinning on with the flag
// silent, and the flag forcing the other answer for one invocation in each
// direction. A precedence that only let the flag turn pinning ON would pass
// every cell but the ones driving --pin-sources=false against a key that is on.
func TestPinSourcesOverridesItsOwnKeyInBothDirections(t *testing.T) {
	srv := serveChart(t, pinChartTgz(t))
	url := srv.URL + "/pinchart.tgz"

	t.Run("pull, key on, flag silent: pins", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "pin_sources_at_pull: true\n", true)
		if err := pinLifecyclePull(t, root, nil); err != nil {
			t.Fatalf("pull: %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); err != nil {
			t.Errorf("a workspace configured to pin at pull wrote no lock: %v", err)
		}
	})
	t.Run("pull, key on, --pin-sources=false: halts", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "pin_sources_at_pull: true\n", true)
		if err := pinLifecyclePull(t, root, boolPtr(false)); !errors.Is(err, recipepin.ErrEntityNotPinned()) {
			t.Fatalf("--pin-sources=false must restore the halt over a workspace configured to pin; got %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); !os.IsNotExist(err) {
			t.Errorf("a halted pull wrote the lock; stat err = %v", err)
		}
	})
	t.Run("pull, key off, --pin-sources: pins", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "pin_sources_at_pull: false\n", true)
		if err := pinLifecyclePull(t, root, boolPtr(true)); err != nil {
			t.Fatalf("pull --pin-sources: %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); err != nil {
			t.Errorf("--pin-sources wrote no lock: %v", err)
		}
	})
	t.Run("push, key on, flag silent: pins before publishing", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "pin_sources_at_push: true\n", false)
		if err := pinLifecyclePush(t, root, recipePath, nil); err != nil {
			t.Fatalf("push: %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); err != nil {
			t.Errorf("a workspace configured to pin at push wrote no lock: %v", err)
		}
	})
	t.Run("push, key on, --pin-sources=false: publishes unpinned", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "pin_sources_at_push: true\n", false)
		if err := pinLifecyclePush(t, root, recipePath, boolPtr(false)); err != nil {
			t.Fatalf("push --pin-sources=false: %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); !os.IsNotExist(err) {
			t.Errorf("--pin-sources=false must skip the step over a workspace configured to pin; stat err = %v", err)
		}
	})
	t.Run("push, key off, --pin-sources: pins before publishing", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		recipePath := pinLifecycleWorkspace(t, root, url, "", false)
		if err := pinLifecyclePush(t, root, recipePath, boolPtr(true)); err != nil {
			t.Fatalf("push --pin-sources: %v", err)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); err != nil {
			t.Errorf("--pin-sources wrote no lock: %v", err)
		}
	})
}

// TestTheFlagReadsAsThreeAnswers holds the command line's reading of the flag to
// the three answers the precedence needs: silent, pin, and do not pin. A plain
// boolean would read a silent invocation as "do not pin" and override a
// workspace configured to pin on every run.
func TestTheFlagReadsAsThreeAnswers(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want *bool
	}{
		{nil, nil},
		{[]string{"--pin-sources"}, boolPtr(true)},
		{[]string{"--pin-sources=true"}, boolPtr(true)},
		{[]string{"--pin-sources=false"}, boolPtr(false)},
	} {
		var v bool
		cmd := &cobra.Command{Use: "probe"}
		cmd.Flags().BoolVar(&v, "pin-sources", false, "")
		if err := cmd.ParseFlags(tc.args); err != nil {
			t.Fatalf("parse %v: %v", tc.args, err)
		}
		got := explicitBool(cmd, "pin-sources", v)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%v: a silent invocation must defer to the workspace; got %v", tc.args, *got)
		case tc.want != nil && (got == nil || *got != *tc.want):
			t.Errorf("%v: want %v; got %v", tc.args, *tc.want, got)
		}
	}
}
