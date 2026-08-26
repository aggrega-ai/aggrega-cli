// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's config-invariance coverage for the mutator socket. The mutator
// rewrites the IR and therefore shapes the rendered output bytes, so the arc law
// requires its output-determining inputs — the ordered attachment set and, per
// attachment, the plugin coordinate and export function — to be pinned at pull
// and reconstructed from the lockfile at build, driven only by the lockfile and
// never by live workspace config ("re-pull to change").
//
// Dropping an attachment from config changes the closure entity set and so trips
// the lockfile-drift halt (a loud halt, not a silent output move). The SILENT
// gap this test guards is a change that leaves the entity set identical — so it
// never trips drift — yet would move output if the runtime attachment were read
// live: a change to the attachment's export function. This test proves the build
// reconstructs the function from the lockfile, so such a change made after the
// pull does not move build output.
//
// The mutator here is a local workspace-catalog plugin, so its .wasm is resolved
// through the co-located tier (the .wasm laid down beside its Plugin entity in the
// catalog) and carries no registry artifact pin — which keeps the test focused on
// the runtime attachment reconstruction under test rather than on the separate
// registry-artifact reconstruction path.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// localMutatorWorkspace lays a workspace whose local catalog serves a compose
// component and its Recipe, plus a mutator Plugin entity whose .wasm is laid down
// co-located beside its plugin.yaml in the catalog. The Recipe emits every
// node-label value into a compose container variable, so a label the mutator
// injects surfaces deterministically in the rendered artifact. Returns the
// workspace root and the catalog dir; the caller writes the .aggrega-config.yaml
// (so it can vary the plugins block between pull and the post-pull rebuild).
func localMutatorWorkspace(t *testing.T, mutatorBytes []byte) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
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
        app: {}
`)

	catalogDir = filepath.Join(root, "catalog")
	writeFixtureEntity(t, catalogDir, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, catalogDir, "recipe/compose/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: compose
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
        variables:
          NODE_LABELS: "{{ range $k, $v := .context.labels }}{{ $v }};{{ end }}"
`)
	writeFixtureEntity(t, catalogDir, "plugin/governance/tag-injector/1.0.0/plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: tag-injector
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: mutate
`)

	// The mutator .wasm resolves through the co-located tier: it sits beside its
	// plugin.yaml in the catalog directory the indexer maps into
	// PluginColocatedDirs. The filename is derived from the coordinate rather than
	// spelled here, so the file staged is the file the resolver reads.
	colocatedDir := filepath.Join(catalogDir, "plugin", "governance", "tag-injector", "1.0.0")
	leaf := pluginpath.PluginLeaf("governance/tag-injector")
	if err := os.WriteFile(filepath.Join(colocatedDir, leaf), mutatorBytes, 0o644); err != nil {
		t.Fatalf("write colocated mutator: %v", err)
	}
	return root, catalogDir
}

// resolveLocalWorkspace writes the .aggrega-config.yaml at root with the supplied
// plugins block and resolves the workspace. offline keeps the build hermetic; the
// mutator .wasm binds through the catalog's co-located tier.
func resolveLocalWorkspace(t *testing.T, root, pluginsBlock string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	cfg := "workspace: {}\noffline: true\n" + pluginsBlock
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), cfg)
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve workspace: %v", err)
	}
	return rw
}

// TestAnnotationTransformSocket_RolePinnedThroughLockfile proves the second
// transform role round-trips through the real pull -> lockfile -> reconstruction
// path: an attachment authored at role annotation-transform is pinned with its
// role and reconstructs at that role, so the build dispatches it at the
// annotation runner (Step 1) rather than the label runner. Before the role was
// pinned, a lockfile-driven build reconstructed every transform attachment as
// label-transform, silently routing an annotation attachment to the wrong runner.
//
// The proof is the pull -> lockfile capture and the reconstruction role; it does
// not build (the harness guest exports mutate, not transform_annotations — the
// lockfile role round-trip under test is independent of the guest's exports,
// which the build-stage dispatch, not the pull, matches to the role).
func TestAnnotationTransformSocket_RolePinnedThroughLockfile(t *testing.T) {
	mutatorBytes := wasmtest.EnsureFixtureBytes(t, "mutator-proof-go.wasm")
	root, catalogDir := localMutatorWorkspace(t, mutatorBytes)
	catDirs := []string{catalogDir, standardCatalogDir(t)}
	const socket = "governance/tag-injector"

	rwPull := resolveLocalWorkspace(t, root, "plugins:\n  governance:\n    - uri: "+socket+":1.0.0\n      role: annotation-transform\n")
	releasePath := filepath.Join(root, "release.yaml")
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwPull, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (annotation-transform attached): %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 1 {
		t.Fatalf("lockfile mutators = %+v; want one transform attachment pinned", lf.Mutators)
	}
	if lf.Mutators[0].Role != string(plugintype.RoleAnnotationTransform) {
		t.Fatalf("pinned transform role = %q, want %q; the annotation role must survive the pull",
			lf.Mutators[0].Role, plugintype.RoleAnnotationTransform)
	}

	refs := lockfile.MutatorAttachments(lf)
	if len(refs) != 1 || refs[0].Role != plugintype.RoleAnnotationTransform {
		t.Fatalf("reconstructed attachment = %+v; want role %q so build dispatches at the annotation runner",
			refs, plugintype.RoleAnnotationTransform)
	}
}

// TestMutatorSocket_BuildIsInvariantToConfigAfterPull is the mutator determinism
// proof: after a pull pins the mutator attachment (coordinate + export function)
// into the lockfile, changing the workspace-config mutator export function and
// rebuilding WITHOUT a re-pull produces byte-identical output — the build reads
// the pinned attachment from the lockfile, not the config. Were the runtime
// attachment read live, the flipped function (a non-existent export) would move
// the build (in fact halt it); byte-identical output proves the lockfile drove
// the mutator run.
func TestMutatorSocket_BuildIsInvariantToConfigAfterPull(t *testing.T) {
	mutatorBytes := wasmtest.EnsureFixtureBytes(t, "mutator-proof-go.wasm")
	root, catalogDir := localMutatorWorkspace(t, mutatorBytes)
	// The compose provider entity is embedded in the binary alongside its .wasm;
	// the fixture catalog supplies the component, recipe, and mutator.
	catDirs := []string{catalogDir, standardCatalogDir(t)}

	const (
		mutatorSocket     = "governance/tag-injector"
		proofMutatorLabel = "mutator-proof"
	)

	// Pull with the mutator attached (default mutate export). The pull pins the
	// attachment coordinate + function into the lockfile's mutators section.
	rwPull := resolveLocalWorkspace(t, root, "plugins:\n  governance:\n    - uri: "+mutatorSocket+":1.0.0\n      role: label-transform\n")
	releasePath := filepath.Join(root, "release.yaml")
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwPull, DiagOut: os.Stderr, Out: os.Stderr, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (mutator attached): %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != mutatorSocket {
		t.Fatalf("lockfile mutators = %+v; want one attachment pinned at coordinate %q", lf.Mutators, mutatorSocket)
	}

	// First build under the pinned attachment: the mutator runs and its injected
	// label reaches the rendered artifact.
	out1 := filepath.Join(root, "out1")
	if err := dispatchBuildWithDiag(rwPull, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out1}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 1 (pinned mutator): %v", err)
	}
	if found, tree := distTreeContains(t, out1, proofMutatorLabel); !found {
		t.Fatalf("build 1: mutator-injected label %q absent; the assertion would be vacuous:\n%s", proofMutatorLabel, tree)
	}
	snap1 := snapshotDir(t, out1)
	if len(snap1) == 0 {
		t.Fatal("build 1 produced no artifacts")
	}

	// Flip the workspace-config mutator export function to a NON-EXISTENT export,
	// leaving the attachment's URI (and therefore the closure entity set)
	// unchanged so no drift halt fires. Rebuild WITHOUT a re-pull. Were the
	// runtime function read live, the build would call the bogus export and halt;
	// because it is read from the lockfile, the build runs the pinned mutate
	// export and produces byte-identical output.
	rwFlipped := resolveLocalWorkspace(t, root, "plugins:\n  governance:\n    - uri: "+mutatorSocket+":1.0.0\n      role: label-transform\n      function: does-not-exist\n")
	out2 := filepath.Join(root, "out2")
	if err := dispatchBuildWithDiag(rwFlipped, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out2}, os.Stderr, os.Stderr); err != nil {
		t.Fatalf("build 2 (config function flipped, no re-pull): %v; build must read the pinned attachment, not the config", err)
	}
	snap2 := snapshotDir(t, out2)
	if !snapshotEqual(snap1, snap2) {
		t.Fatalf("build output changed after a plugins.mutator function change with no re-pull; build must reconstruct the mutator attachment from the lockfile, not the config")
	}
}
