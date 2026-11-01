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

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerBindingFixture lays a workload-synthesis Release down at root with a
// catalog that serves the "helm" capability through the two named provider
// plugins. Each plugin routes to a distinct provider .wasm, so which one renders a
// node is observable in the emitted bytes. Returns the release path and the
// catalog root.
//
// altPlugin controls whether a SECOND provider plugin is written (serving the
// same target through the compose-provider guest, laid beside its entity), which
// is what makes the target ambiguous and the binding load-bearing.
func providerBindingFixture(t *testing.T, root string, altPlugin bool) (releasePath, catalogRoot string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: binding-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: binding-smoke-release
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
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: binding-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	// The primary provider plugin (routes to the embedded helm .wasm).
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	if altPlugin {
		// A second publisher serving the SAME target through a different provider
		// .wasm identity — makes "helm" ambiguous, so a binding must select one.
		writeFixture(t, filepath.Join(catalogRoot, "base", "providers", "helm-renderer-alt.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer-alt
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
		// The entity is authored in a catalog directory, so a build reads its .wasm
		// from beside it, and a pull that selects it refuses when no source holds that
		// artifact. The compose provider's guest bytes are what route this publisher
		// through a different .wasm.
		composeGuest, carried := embedded.Lookup("provider/compose")
		if !carried {
			t.Fatalf("the binary carries no provider/compose guest; available: %v", embedded.URIs())
		}
		writeFixtureBytes(t, filepath.Join(catalogRoot, "base", "providers", pluginpath.PluginLeaf("provider/helm-renderer-alt")), composeGuest)
	}
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
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
	return filepath.Join(root, "release.yaml"), catalogRoot
}

// writeWorkspaceConfig writes an .aggrega-config.yaml at root and resolves the
// workspace from it. The workspace:{} block anchors the root at root so
// resolution never climbs to an ambient ancestor config.
func resolveWorkspaceWithConfig(t *testing.T, root, body string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), body)
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve workspace: %v", err)
	}
	return rw
}

// snapshotDir walks dir and returns a map of relative-path → file content, for
// byte-identity comparison of two build outputs.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func snapshotEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// nonInteractive forces the pull binding-helper's TTY gate off for the duration
// of a test, so a run in a real terminal never tries to prompt on stdin.
func nonInteractive(t *testing.T) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = prev })
}

// TestProviderBinding_BuildIsInvariantToConfigAfterPull is the headline
// determinism proof: after a pull resolves the render engine for a target and
// records it in the lockfile, changing the workspace-config plugins.providers and
// rebuilding WITHOUT a re-pull produces byte-identical output — the build reads
// the lockfile, not the config. A re-pull then adopts the new binding into the
// lockfile.
func TestProviderBinding_BuildIsInvariantToConfigAfterPull(t *testing.T) {
	// Not parallel: mutates the process-global isTerminal seam other tests read.
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, true)
	catDirs := []string{catalogRoot}

	// Pull with the target bound to the helm provider.
	rwHelm := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  providers:
    - capability: helm
      uri: provider/helm
`)
	if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwHelm, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (helm binding): %v", err)
	}

	// First build under the helm binding.
	run1 := rootedBuildOptions(t, filepath.Join(root, "build1"), buildops.RunOptions{})
	out1 := run1.OutDir
	if err := dispatchBuildWithDiag(context.Background(), rwHelm, releasePath, catDirs, false, buildDiagOptions{quiet: true}, run1, io.Discard, io.Discard); err != nil {
		t.Fatalf("build 1: %v", err)
	}
	snap1 := snapshotDir(t, out1)
	if len(snap1) == 0 {
		t.Fatal("build 1 produced no artifacts")
	}

	// Change the config binding to the OTHER provider, then rebuild WITHOUT a
	// re-pull. The lockfile still records the helm binding, so the output must be
	// byte-identical — build never reads the config for engine selection.
	rwCompose := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  providers:
    - capability: helm
      uri: provider/helm-renderer-alt
`)
	run2 := rootedBuildOptions(t, filepath.Join(root, "build2"), buildops.RunOptions{})
	out2 := run2.OutDir
	if err := dispatchBuildWithDiag(context.Background(), rwCompose, releasePath, catDirs, false, buildDiagOptions{quiet: true}, run2, io.Discard, io.Discard); err != nil {
		t.Fatalf("build 2 (config changed, no re-pull): %v", err)
	}
	snap2 := snapshotDir(t, out2)
	if !snapshotEqual(snap1, snap2) {
		t.Fatalf("build output changed after a plugins.providers config change with no re-pull; build must read the lockfile, not the config")
	}

	// A re-pull adopts the new binding into the lockfile.
	if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwCompose, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("re-pull (compose binding): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	got := ""
	for _, b := range lf.Providers {
		if b.Capability == "helm" {
			got = b.Plugin
		}
	}
	if got != "provider/helm-renderer-alt" {
		t.Fatalf("re-pull did not adopt the new binding; lockfile providers[helm]=%q, want the alt-provider identity", got)
	}
}

// TestProviderBinding_AmbiguousTargetHaltsLoudly proves that when two provider
// plugins serve one target and no binding disambiguates, a non-interactive pull
// halts loudly with ErrAmbiguousProviderPlugin, naming both candidate plugins.
func TestProviderBinding_AmbiguousTargetHaltsLoudly(t *testing.T) {
	// Not parallel: mutates the process-global isTerminal seam other tests read.
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, true)
	rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\n")

	err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: rw, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}})
	if err == nil {
		t.Fatal("pull over an ambiguous target with no binding returned nil; expected a loud halt")
	}
	if !errors.Is(err, catalog.ErrAmbiguousProviderPlugin()) {
		t.Fatalf("ambiguity halt must be ErrAmbiguousProviderPlugin; got: %v", err)
	}
	if !chainContains(err, "(helm, helm-renderer-alt)") {
		t.Fatalf("ambiguity halt must name both candidate plugins; got: %v", err)
	}
}

// TestProviderBinding_JudgedWhetherOrNotARecipeConsultsIt drives the commands a
// user runs — pull, build and validate over one Release — against a workspace
// whose provider binding no Recipe in that Release asks for. The fixture's one
// Recipe asks for helm, so a binding for any other capability is consulted by no
// resolution, and before this judgement it was reported by nothing.
//
// A binding naming a plugin that does not serve its capability (the helm
// provider bound to a misspelt "hlem") halts the build, asserted against the
// condition's accessor, and fails validation with the condition's registered
// sentence on the diagnostic stream. A capability-only binding no plugin serves
// is displayed as a warning while both commands succeed. The controls are legal
// bindings: the capability the Recipe asks for bound to its plugin, and a served
// capability no Recipe in this Release uses — a binding another Release in the
// workspace would read — which must be reported as nothing.
//
// Each command reports on the diagnostic writer it is handed, so that is where
// every row reads the report. The process's own standard error is read as well,
// and must carry none of it: a report written there instead would reach whoever
// launched a long-running process rather than the caller that asked.
//
// Not parallel: it mutates the process-global terminal seam and redirects the
// process diagnostic stream.
func TestProviderBinding_JudgedWhetherOrNotARecipeConsultsIt(t *testing.T) {
	nonInteractive(t)
	halt := catalog.ErrProviderBindingUnsatisfied()
	advisory := catalog.ErrProviderBindingCapabilityUnserved()
	cases := []struct {
		name      string
		providers string
		halts     bool
		advises   bool
	}{
		{
			name:      "a uri binding whose plugin does not serve its capability halts build and validate",
			providers: "    - capability: hlem\n      uri: provider/helm\n",
			halts:     true,
		},
		{
			name:      "a capability-only binding nothing serves is a warning and both commands succeed",
			providers: "    - capability: hlem\n",
			advises:   true,
		},
		{
			name:      "control: the capability the Recipe asks for, bound to its plugin, is silent",
			providers: "    - capability: helm\n      uri: provider/helm\n",
		},
		{
			name:      "control: a served capability no Recipe in this Release uses is silent",
			providers: "    - capability: compose\n      uri: provider/compose\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath, catalogRoot := providerBindingFixture(t, root, false)
			catDirs := []string{catalogRoot}
			rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\nplugins:\n  providers:\n"+tc.providers)

			if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
				t.Fatalf("pull: %v", err)
			}

			var buildErr error
			var buildDiag bytes.Buffer
			buildStderr := captureStderr(t, func() {
				buildErr = dispatchBuildWithDiag(context.Background(), rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, rootedBuildOptions(t, root, buildops.RunOptions{}), &buildDiag, io.Discard)
			})
			var validateErr error
			var diag, out bytes.Buffer
			validateStderr := captureStderr(t, func() {
				validateErr = validateRelease(context.Background(), rw, releasePath, catDirs, compilerops.VerbosityDetailed, false, false, &diag, &out)
			})
			for surface, stream := range map[string]string{"build": buildStderr, "validate": validateStderr} {
				if strings.Contains(stream, advisory.Error()) || strings.Contains(stream, halt.Error()) {
					t.Errorf("%s: the binding was reported on the process's standard error rather than on the writer the command was handed:\n%s", surface, stream)
				}
			}

			switch {
			case tc.halts:
				if !errors.Is(buildErr, halt) {
					t.Errorf("build: want ErrProviderBindingUnsatisfied, got %v", buildErr)
				}
				if !chainContains(buildErr, `"provider/helm" serves "helm"`) || !chainContains(buildErr, `"hlem" is one edit from "helm"`) {
					t.Errorf("build: the halt must name what the bound plugin serves and the near miss; got %v", buildErr)
				}
				if validateErr == nil {
					t.Errorf("validate: a binding the build halts on passed validation")
				}
				if !strings.Contains(diag.String(), halt.Error()) {
					t.Errorf("validate: the diagnostic stream must carry the condition's sentence %q; got:\n%s", halt.Error(), diag.String())
				}
			case tc.advises:
				if buildErr != nil {
					t.Fatalf("build: an advisory must not halt; got %v", buildErr)
				}
				if validateErr != nil {
					t.Fatalf("validate: an advisory must not fail validation; got %v\n%s", validateErr, diag.String())
				}
				for surface, stream := range map[string]string{"build": buildDiag.String(), "validate": diag.String()} {
					if !strings.Contains(stream, "warning: "+advisory.Error()) || !strings.Contains(stream, `"hlem", which is one edit from "helm"`) {
						t.Errorf("%s: the warning must carry the condition's sentence and the near miss; got:\n%s", surface, stream)
					}
				}
			default:
				if buildErr != nil {
					t.Fatalf("build: a legal binding halted the build: %v", buildErr)
				}
				if validateErr != nil {
					t.Fatalf("validate: a legal binding failed validation: %v\n%s", validateErr, diag.String())
				}
				for surface, stream := range map[string]string{"build": buildDiag.String(), "validate": diag.String()} {
					if strings.Contains(stream, advisory.Error()) || strings.Contains(stream, halt.Error()) {
						t.Errorf("%s: a legal binding was reported:\n%s", surface, stream)
					}
				}
			}
		})
	}
}

// TestProviderBinding_UnambiguousTargetNeedsNoBinding proves the zero-config path:
// a target served by exactly one provider plugin resolves and builds with no
// plugins.providers entry, and pull records the resolved binding into the
// lockfile.
func TestProviderBinding_UnambiguousTargetNeedsNoBinding(t *testing.T) {
	// Not parallel: mutates the process-global isTerminal seam other tests read.
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, false)
	catDirs := []string{catalogRoot}
	rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\n")

	if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (unambiguous, no binding): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	found := false
	for _, b := range lf.Providers {
		if b.Capability == "helm" {
			found = true
			if b.Plugin != "provider/helm" {
				t.Errorf("recorded binding plugin=%q, want the helm-provider identity", b.Plugin)
			}
		}
	}
	if !found {
		t.Fatalf("pull did not record a providers entry for the resolved target; got %+v", lf.Providers)
	}

	run := rootedBuildOptions(t, root, buildops.RunOptions{})
	out := run.OutDir
	if err := dispatchBuildWithDiag(context.Background(), rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, run, io.Discard, io.Discard); err != nil {
		t.Fatalf("build (zero-config): %v", err)
	}
	if len(snapshotDir(t, out)) == 0 {
		t.Fatal("zero-config build produced no artifacts")
	}
}
