// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A pull followed by a build is the ordinary way to use a Release, and the build
// that follows holds the lockfile the pull wrote to the closure the build itself
// reconstructs. The tests here drive that pair for the case where the only
// registry-published thing in the closure is a pipeline plugin a catalog-local
// FeatureRecipe names: the pull pins the plugin entity and its .wasm, and the
// build must reconstruct exactly those two rows from the pinned cache.
//
// Every build under test here is FROZEN — the workspace fills nothing — because a
// build that fills its own lockfile skips the drift verify, and a suite that only
// ever filled would never have run the comparison this file exists to hold. The
// one filling build is the reference the frozen trees are compared against.
//
// Everything here uses only what a build over an unfixed reconstruction also
// has, so the file compiles against it and its tests fail there; the parity test
// that needs the reconstruction's plugin lookup lives in a file of its own.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// registryPluginFixture is a workspace whose catalog is local and whose one
// FeatureRecipe runs a pipeline step published to the in-process registry.
type registryPluginFixture struct {
	root        string
	host        string
	catalogDirs []string
	entityURI   string // the plugin ENTITY the FeatureRecipe's step names
	artifactURI string // the plugin's .wasm, as the lockfile's plugin axis names it
	wasmDigest  string
}

// writeRegistryPluginFixture lays the workspace down and publishes the plugin.
//
// attach decides whether the Aggregate attaches the Feature the pipeline-bearing
// FeatureRecipe implements. Both shapes pin the plugin, because the pull's plugin
// descent walks every recipe the closure reaches and the Manifest's mapping puts
// the FeatureRecipe there whether or not anything attaches its Feature — so the
// unattached shape is the one that shows the reach is the closure, not the render.
//
// pluginPath and std are resolved by the caller before any chdir, because both
// helpers walk up from the working directory.
func writeRegistryPluginFixture(t *testing.T, host, pluginPath, std string, attach bool) registryPluginFixture {
	t.Helper()
	const pluginName = "gateway-step"
	root := t.TempDir()
	cat := filepath.Join(root, "catalog")
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
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
	writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
`)
	writeFixtureEntity(t, cat, "feature/test/control/1.0.0/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
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
	entityURI := types.PluginCoordinate(pipelineStepPluginType, pluginName) + ":1.0.0"
	writeFixtureEntity(t, cat, "featurerecipe/helm/control/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
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
      plugin: `+entityURI+`
      function: step
`)
	wasmDigest := publishPluginEntity(t, host, pluginName, "1.0.0", pluginPath)

	features := ""
	if attach {
		features = "      features:\n        control:\n          type: test/control\n"
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`+features)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	f := registryPluginFixture{
		root:        root,
		host:        host,
		catalogDirs: []string{cat, std},
		entityURI:   entityURI,
		artifactURI: types.PluginCoordinate(pipelineStepPluginType, pluginName),
		wasmDigest:  wasmDigest,
	}
	f.configure(t, "auto_pull: false\noffline: false")
	return f
}

// configure rewrites the workspace configuration: the catalog directories and the
// registry every surface reads, plus the fill and network postures a step asks
// for. The catalog directories live in the configuration rather than in flags so
// that the command line, the daemon and the tool protocol all resolve over the
// same stack — which is the condition under which a pull and a build must agree.
func (f registryPluginFixture) configure(t *testing.T, postures string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("workspace:\n  catalog_dirs:\n")
	for _, d := range f.catalogDirs {
		b.WriteString("    - \"" + d + "\"\n")
	}
	b.WriteString("default_registry: \"" + f.host + "\"\n")
	b.WriteString(postures)
	writeWorkspaceConfig(t, f.root, b.String())
}

func (f registryPluginFixture) pull(t *testing.T) *lockfile.Lockfile {
	t.Helper()
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}), ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	return lf
}

// build runs one command-line build and returns the rendered tree's digest,
// clearing the output directory afterwards so the next build writes afresh.
func (f registryPluginFixture) build(t *testing.T, label string) string {
	t.Helper()
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}), "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("%s: the build refused the lockfile the pull just wrote: %v", label, err)
	}
	tree := hashDistTree(t, "dist")
	if err := os.RemoveAll("dist"); err != nil {
		t.Fatalf("clear dist after %s: %v", label, err)
	}
	return tree
}

// assertPluginPinned is the positive floor under every agreement asserted below:
// the pull pinned BOTH the plugin entity and its .wasm. A build and a pull that
// agree because neither pinned anything would pass every other check here.
func (f registryPluginFixture) assertPluginPinned(t *testing.T, lf *lockfile.Lockfile) {
	t.Helper()
	var entity, artifact bool
	for _, e := range lf.Entities {
		if e.URI == f.entityURI {
			entity = true
		}
	}
	for _, p := range lf.Plugins {
		if p.URI == f.artifactURI && p.Digest == f.wasmDigest {
			artifact = true
		}
	}
	if !entity || !artifact {
		t.Fatalf("the pull must pin the registry plugin on both axes (entity %q pinned: %v; artifact %q at %s pinned: %v); "+
			"entities=%+v plugins=%+v", f.entityURI, entity, f.artifactURI, f.wasmDigest, artifact, lf.Entities, lf.Plugins)
	}
}

// TestIntegration_RegistryPipelinePlugin_PullThenFrozenBuild drives pull and then
// a frozen build, online and offline, and holds both to the tree a build that
// fills its own lockfile renders. Before the build's reconstruction reported a
// cache-folded plugin entity as cache-resolved and addressed its pinned .wasm, the
// frozen build refused the lockfile the pull had just written, naming both
// plugin rows as no longer used.
func TestIntegration_RegistryPipelinePlugin_PullThenFrozenBuild(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixture(t, startRegistry(t, nil), pluginPath, std, true)
	t.Chdir(f.root)

	lf := f.pull(t)
	f.assertPluginPinned(t, lf)

	online := f.build(t, "frozen online build")

	f.configure(t, "auto_pull: false\noffline: true")
	offline := f.build(t, "frozen offline build")

	f.configure(t, "auto_pull: true\noffline: false")
	filled := f.build(t, "build that fills its own lockfile")

	if online != offline || online != filled {
		t.Errorf("the rendered trees differ: frozen online=%s frozen offline=%s filling=%s", online, offline, filled)
	}
}

// TestIntegration_RegistryPipelinePlugin_MappedUnattachedFrozenBuild is the same
// pair for a Release that attaches nothing the plugin serves: its Manifest merely
// maps the FeatureRecipe that names it. The pull still pins the plugin, because
// the plugin descent walks every recipe the closure reaches, so the build must
// still reconstruct it.
func TestIntegration_RegistryPipelinePlugin_MappedUnattachedFrozenBuild(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixture(t, startRegistry(t, nil), pluginPath, std, false)
	t.Chdir(f.root)

	f.assertPluginPinned(t, f.pull(t))
	online := f.build(t, "frozen online build")
	f.configure(t, "auto_pull: false\noffline: true")
	if offline := f.build(t, "frozen offline build"); offline != online {
		t.Errorf("frozen online and offline builds rendered different trees: %s != %s", online, offline)
	}
}

// TestIntegration_RegistryPipelinePlugin_DaemonAndToolBuildTheLockfile drives the
// other two surfaces that build over the lockfile the pull wrote — the tool
// protocol's build and the daemon's — and the daemon's graph request, whose drift
// verdict reaches an editor as a published diagnostic. Each of the three read the
// same reconstruction the command line does, so each refused, or warned, where
// the command line refused.
func TestIntegration_RegistryPipelinePlugin_DaemonAndToolBuildTheLockfile(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixture(t, startRegistry(t, nil), pluginPath, std, true)
	t.Chdir(f.root)
	f.assertPluginPinned(t, f.pull(t))

	compilerVersion := provenanceCompilerVersion()
	if compilerVersion == "" {
		t.Fatal("this binary reports no compiler version")
	}
	if tree := renderThroughToolProtocol(t, f.root, "release.yaml", compilerVersion); len(tree) == 0 {
		t.Fatal("the tool protocol's build rendered nothing")
	}
	assertDaemonGraphAndBuild(t, f.root)
}

// assertDaemonGraphAndBuild spawns the daemon and, over one reader, asks for the
// graph and then for a build. The graph request publishes its lockfile verdict
// before it answers — the drift-cleared transition as an empty set, a drift as a
// warning — so at least one publication must arrive ahead of the answer, and
// none may carry the drift warning. The build must answer with an output
// directory holding the rendered tree.
func assertDaemonGraphAndBuild(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command(buildServeBinary(t), "serve")
	cmd.Dir = root
	cmd.Env = builtBinaryEnv(t)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stdout := bufio.NewReader(stdoutPipe)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	ceiling := responseLivenessCeiling(t)
	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if resp := readOneResponse(t, stdout, ceiling); resp["error"] != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
	if _, err := stdin.Write(framedFor(t, cmd, 2, "aggrega/irGraph", map[string]any{"release": "release.yaml"})); err != nil {
		t.Fatalf("write irGraph: %v", err)
	}
	var published []string
	for i := 0; i < 50; i++ {
		msg := readOneResponse(t, stdout, ceiling)
		if msg["id"] == nil {
			if msg["method"] == "aggrega/publishDiagnostics" {
				raw, _ := json.Marshal(msg["params"])
				published = append(published, string(raw))
			}
			continue
		}
		if e := msg["error"]; e != nil {
			t.Fatalf("the graph request refused: %+v", e)
		}
		break
	}
	if len(published) == 0 {
		t.Fatal("the graph request published no diagnostics at all, so the absence of a drift warning below proves nothing")
	}
	for _, p := range published {
		if strings.Contains(p, "drifted") || strings.Contains(p, "no longer used by the release") {
			t.Errorf("the graph request published a lockfile drift warning over the lockfile the pull just wrote:\n%s", p)
		}
	}

	if _, err := stdin.Write(framedFor(t, cmd, 3, "aggrega/build", map[string]any{"release": "release.yaml"})); err != nil {
		t.Fatalf("write build: %v", err)
	}
	var result map[string]any
	for i := 0; i < 50 && result == nil; i++ {
		msg := readOneResponse(t, stdout, ceiling)
		if msg["id"] == nil {
			continue
		}
		if e := msg["error"]; e != nil {
			t.Fatalf("the daemon's build refused the lockfile the pull just wrote: %+v", e)
		}
		result, _ = msg["result"].(map[string]any)
	}
	if result == nil {
		t.Fatal("the daemon wrote no build result")
	}
	outDir, _ := result["outputDirectory"].(string)
	if outDir == "" {
		t.Fatalf("the daemon's build result names no output directory: %+v", result)
	}
	if tree := readRenderedTree(t, outDir); len(tree) == 0 {
		t.Fatalf("the daemon's build wrote nothing under %s", outDir)
	}
}
