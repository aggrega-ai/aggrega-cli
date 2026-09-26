// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lockfileFromClosure builds a lockfile whose verifiable axes mirror the supplied
// closure field-for-field. Verify against the same closure is clean by
// construction; verify against a differently-shaped closure surfaces the delta
// as drift. It is the test analogue of what a real pull writes after a full
// closure descent — a lockfile that pins exactly the reconstructed axes and
// records the Release files the resolution read.
func lockfileFromClosure(rel string, cl closure.Closure) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{Version: 1, Release: rel, GeneratedAt: "2026-06-03T10:00:00Z"}
	for _, d := range cl.ReleaseDocuments {
		lf.ReleaseDocuments = append(lf.ReleaseDocuments, lockfile.ReleaseDocument{Path: d.Path, Digest: d.Digest})
	}
	for _, e := range cl.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{URI: e.URI, ResolvedVersion: e.ResolvedVersion, Digest: e.Digest})
	}
	for _, p := range cl.Plugins {
		lf.Plugins = append(lf.Plugins, lockfile.Plugin{URI: p.URI, ResolvedVersion: p.ResolvedVersion, Digest: p.Digest})
	}
	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	return lf
}

// libraryRefWorkspace lays a workspace whose Component schema pulls a field from a
// SchemaLibrary via a cross-document $ref. ComputeClosure does not descend the
// library axis, so a compute-only serve closure leaves SchemaLibraries empty; the
// hermetic augment pass (which the CLI build runs and serve now runs too)
// discovers and pins the library. This is the axis whose absence produced the
// spurious serve "no longer used" drift, reproducible without a registry tier
// (the library resolves from the local catalog).
func libraryRefWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      storage:
        $ref: "DbSizing#/schemas/Storage"
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: "1.0.0"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/svc: demo-server-synthesized
`)
	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	return ws, filepath.Base(relPath)
}

// writeFixtureLockfile puts a lockfile on disk beside its Release, which is what
// the graph request reads: the inspection loads the lockfile itself rather than
// being handed one, so a fixture that only held an in-memory value would drive a
// path no client can reach.
func writeFixtureLockfile(t *testing.T, path string, lf *lockfile.Lockfile) {
	t.Helper()
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("write lockfile %s: %v", path, err)
	}
}

// serveInspectionClosure returns the closure the graph request computes for rel
// — the same value handleIrGraph hands verifyDriftAndPublish. It drives
// previewRelease, the daemon's own entry, so what is asserted below is the
// closure production drift-verifies against and not a closure assembled for the
// test.
func serveInspectionClosure(t *testing.T, srv *Server, rel string) closure.Closure {
	t.Helper()
	preview, env := srv.previewRelease(context.Background(), rel, srv.irGraphPipelineOptions())
	if env != nil {
		t.Fatalf("previewRelease %s: %s", rel, env.Message)
	}
	return preview.resolved.Closure
}

// TestServeReconstructsClosureAxesFromLockfile proves the daemon's drift path
// brings its closure onto the same hermetic footing the CLI build uses.
// ComputeClosure alone leaves the cross-document library axis empty, so a
// lockfile pinning that library would drift-report it as "no longer used" — a
// spurious warning. The graph request's preview folds the library axes and
// reconstructs the rest, so the same lockfile verifies clean.
//
// It drives previewRelease rather than a helper of its own, because the closure
// under test is the one the drift verify is handed: a test that reconstructed
// separately could agree with production today and stop agreeing without
// failing.
func TestServeReconstructsClosureAxesFromLockfile(t *testing.T) {
	t.Parallel()
	ws, rel := libraryRefWorkspace(t)
	releasePath := filepath.Join(ws, rel)
	lockfilePath := lockfile.PathFor(releasePath)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)

	// Compute-only closure (no reconstruction): the library axis is empty — the
	// serve drift-false-positive precondition.
	computeView := srv.lazyCatalog.ForRequest(nil)
	computed, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            computeView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   srv.workspaceRoot,
		WorkspaceConfig: srv.wsConfig,
	})
	if cerr != nil {
		t.Fatalf("compute-only ResolveClosure: %v", cerr)
	}
	if len(computed.Closure.SchemaLibraries) != 0 {
		t.Fatalf("precondition: ComputeClosure should leave the schema-library axis empty, got %d", len(computed.Closure.SchemaLibraries))
	}

	// A lockfile pinned from a full (reconstructed) closure. Run the graph
	// request's own preview once to learn the honest axis set, then pin it.
	writeFixtureLockfile(t, lockfilePath, &lockfile.Lockfile{Version: 1, Release: "./" + rel, GeneratedAt: "2026-06-03T10:00:00Z"})
	seedClosure := serveInspectionClosure(t, srv, rel)
	if len(seedClosure.SchemaLibraries) == 0 {
		t.Fatalf("reconstruction did not populate the schema-library axis; the hermetic serve path is not running")
	}
	pinned := lockfileFromClosure("./"+rel, seedClosure)

	// The pinned lockfile drift-verifies CLEAN against the closure the graph
	// request computes (no false "no longer used"), and the daemon's own drift
	// check publishes nothing for it.
	writeFixtureLockfile(t, lockfilePath, pinned)
	reconClosure := serveInspectionClosure(t, srv, rel)
	if err := lockfile.Verify(pinned, reconClosure); err != nil {
		t.Fatalf("drift against the reconstructed closure should be clean, got: %v", err)
	}
	srv.verifyDriftAndUpdateCache(lockfilePath, reconClosure)
	if diags := srv.snapshotDiagnostics(lockfilePath); len(diags) != 0 {
		t.Fatalf("an undrifted lockfile should publish no drift diagnostic, got %d: %+v", len(diags), diags)
	}

	// The drifted direction, through the same check: a lockfile pinning an entity
	// the closure does not reach must still be reported.
	drifted := lockfileFromClosure("./"+rel, reconClosure)
	drifted.Entities = append(drifted.Entities, lockfile.Entity{
		URI: "demo/withdrawn", ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64),
	})
	srv.verifyDriftAndUpdateCache(lockfilePath, computed.Closure) // reset the entry off the clean state
	writeFixtureLockfile(t, lockfilePath, drifted)
	srv.verifyDriftAndUpdateCache(lockfilePath, reconClosure)
	driftDiags := srv.snapshotDiagnostics(lockfilePath)
	if len(driftDiags) == 0 {
		t.Fatal("a drifted lockfile must still report drift; got no diagnostic")
	}
	if !strings.Contains(driftDiags[0].Message, "drifted") {
		t.Fatalf("drift diagnostic does not name the condition: %q", driftDiags[0].Message)
	}

	// Load-bearing control: the SAME pinned lockfile drift-reports against a
	// compute-only closure — proving the reconstruction is what removes the false
	// positive.
	err := lockfile.Verify(pinned, computed.Closure)
	if err == nil {
		t.Fatal("expected drift against the compute-only closure (the false positive reconstruction removes), got none")
	}
	if !strings.Contains(err.Error(), "schema-library") || !strings.Contains(err.Error(), "no longer used") {
		t.Fatalf("expected a 'schema-library ... no longer used' false positive without reconstruction, got: %v", err)
	}
}

// TestServeByteIdenticalSingleProviderRender proves the lockfile-hermetic provider
// resolution serve now uses (ProviderPluginForTarget with the lockfile bindings)
// renders a single-provider closure byte-identically to the plain by-name
// resolution — so bringing serve onto the hermetic path is inert for the common
// case and changes bytes only where it must (multi-provider disambiguation).
func TestServeByteIdenticalSingleProviderRender(t *testing.T) {
	t.Parallel()
	ws, rel := renderDialWorkspace(t)
	releasePath := filepath.Join(ws, rel)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil || lf == nil {
		t.Fatalf("load lockfile: %v", err)
	}

	render := func(withLockfile *lockfile.Lockfile) map[string][]byte {
		view := srv.lazyCatalog.ForRequest(catalog.PinsFromLockfile(lf))
		resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
			View:            view,
			ReleasePath:     releasePath,
			WorkspaceRoot:   srv.workspaceRoot,
			WorkspaceConfig: srv.wsConfig,
		})
		if cerr != nil {
			t.Fatalf("ResolveClosure: %v", cerr)
		}
		res, err := compilerops.BuildToBuffer(context.Background(), compilerops.BuildInputs{
			View:                             view,
			Resolved:                         resolved,
			ReleasePath:                      releasePath,
			WorkspaceConfig:                  srv.wsConfig,
			WorkspaceRoot:                    srv.workspaceRoot,
			Lockfile:                         withLockfile,
			CompilerVersion:                  srv.compilerVersion,
			TolerateCatalogViewInconsistency: true,
		}, pipeline.Options{})
		if err != nil {
			t.Fatalf("BuildToBuffer (lockfile=%v): %v", withLockfile != nil, err)
		}
		return res.Completed().Artifacts
	}

	hermetic := render(lf) // provider resolution reads the lockfile bindings
	byName := render(nil)  // provider resolution falls to by-name (pre-change path)

	if len(hermetic) == 0 {
		t.Fatal("render produced no artifacts; the byte-identity assertion would be vacuous")
	}
	if len(hermetic) != len(byName) {
		t.Fatalf("artifact count differs: hermetic=%d byName=%d", len(hermetic), len(byName))
	}
	for path, want := range byName {
		got, ok := hermetic[path]
		if !ok {
			t.Fatalf("hermetic render missing artifact %q", path)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("artifact %q differs between hermetic and by-name provider resolution", path)
		}
	}
}

// TestServeMutatorConfigInvariance proves BuildToBuffer reconstructs the mutator
// attachment set from the LOCKFILE, never live workspace config. The mutator runs
// at IR-build (before render), so it fires even under SkipRender. A live config
// carrying an unresolvable mutator is overridden to the lockfile's (empty) set,
// so the build succeeds; with no lockfile the live config drives the pipeline and
// the unresolvable mutator halts it — the negative control proving the override
// is load-bearing.
func TestServeMutatorConfigInvariance(t *testing.T) {
	t.Parallel()
	ws, rel := renderDialWorkspace(t)
	releasePath := filepath.Join(ws, rel)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil || lf == nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 0 {
		t.Fatalf("fixture lockfile pins %d mutator(s); the invariance test needs an empty pinned set", len(lf.Mutators))
	}

	build := func(withLockfile *lockfile.Lockfile) error {
		view := srv.lazyCatalog.ForRequest(catalog.PinsFromLockfile(lf))
		resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
			View:            view,
			ReleasePath:     releasePath,
			WorkspaceRoot:   srv.workspaceRoot,
			WorkspaceConfig: srv.wsConfig,
		})
		if cerr != nil {
			t.Fatalf("ResolveClosure: %v", cerr)
		}
		wsCfg := srv.wsConfig
		// A live-config label-transform governance attachment whose URI cannot
		// resolve: if the pipeline reads it, Step 2b halts on the unresolvable plugin.
		wsCfg.Plugins.Governance = []workspaceconfig.PluginRef{{URI: "bogus://unresolvable-mutator", Role: plugintype.RoleLabelTransform}}
		_, err := compilerops.BuildToBuffer(context.Background(), compilerops.BuildInputs{
			View:                             view,
			Resolved:                         resolved,
			ReleasePath:                      releasePath,
			WorkspaceConfig:                  wsCfg,
			WorkspaceRoot:                    srv.workspaceRoot,
			Lockfile:                         withLockfile,
			CompilerVersion:                  srv.compilerVersion,
			TolerateCatalogViewInconsistency: true,
		}, pipeline.Options{SkipRender: true})
		return err
	}

	// With the (empty-mutator) lockfile, the override empties the live config's
	// mutator set, so the unresolvable mutator never runs and the build succeeds.
	if err := build(lf); err != nil {
		t.Fatalf("build with an empty-mutator lockfile should ignore the live-config mutator, got: %v", err)
	}

	// Negative control: no lockfile means the live config drives the pipeline, so
	// the unresolvable mutator halts the build.
	err = build(nil)
	if err == nil {
		t.Fatal("expected the live-config unresolvable mutator to halt the build when no lockfile overrides it")
	}
	if !strings.Contains(err.Error(), "mutator") {
		t.Fatalf("expected a mutator-resolution halt, got: %v", err)
	}
}

// multiProviderRenderWorkspace lays a workspace whose single helm node is served
// by TWO provider plugins declaring the same `helm` capability — so a bare
// by-name provider lookup is ambiguous. The lockfile pins the binding selecting
// one, so the hermetic render resolves it. The bound plugin is the embed-backed
// helm renderer; the competing plugin is a bare entity that the binding filters
// out before resolution, so it needs no artifact.
func multiProviderRenderWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	const svcComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), svcComponent)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	// The selected provider Plugin — its embed-backed guest renders the node.
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm
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
	// A competing provider Plugin serving the SAME capability under a different
	// identity, making a bare by-name lookup ambiguous.
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-alt.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-alt
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
	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/svc: demo-server-synthesized
`)
	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	// The lockfile pins the provider binding selecting the embed-backed helm plugin
	// so the ambiguous target resolves without a config-time plugins.providers entry.
	// The component row records the component's own content digest, as a pull would:
	// the lockfile refuses a row without a well-formed one.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0", Digest: contentDigestHex([]byte(svcComponent)),
		}},
		Providers: []lockfile.ProviderBinding{{Capability: "helm", Plugin: "provider/helm"}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, filepath.Base(relPath)
}

// TestServeMultiProviderResolvesFromLockfileBinding proves serve's render dispatch
// reads the provider binding from the lockfile: a capability served by more than
// one provider plugin — ambiguous to a bare by-name lookup — renders without an
// ErrAmbiguousProviderPlugin halt because the pinned binding disambiguates it.
func TestServeMultiProviderResolvesFromLockfileBinding(t *testing.T) {
	t.Parallel()
	ws, rel := multiProviderRenderWorkspace(t)
	releasePath := filepath.Join(ws, rel)

	// A bare by-name lookup over this catalog IS ambiguous — proving the binding is
	// load-bearing rather than incidental.
	var din, dout bytes.Buffer
	dsrv := newPinIsolationServer(t, ws, &din, &dout, 1)
	if _, _, err := dsrv.lazyCatalog.ForRequest(nil).ProviderPluginByName("helm"); err == nil {
		t.Fatal("precondition: by-name provider lookup over a two-provider catalog should be ambiguous")
	}

	// The serve render path (build-on-inspect on, so render actually runs and the
	// provider is resolved) must NOT halt on ambiguity — the lockfile binding wins.
	graph := irGraphWithDial(t, ws, rel, true)
	if graph == nil {
		t.Fatalf("serve irGraph produced no graph for %s (a provider-resolution halt would surface as an error envelope)", releasePath)
	}
	var parsed struct {
		Spec struct {
			Nodes map[string]json.RawMessage `json:"nodes"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(graph, &parsed); err != nil {
		t.Fatalf("unmarshal graph: %v", err)
	}
	if len(parsed.Spec.Nodes) == 0 {
		t.Fatalf("expected at least one rendered node in the IR graph, got none")
	}
}

// transitiveSubAggregateWorkspace lays a workspace whose Aggregate chain is two
// levels deep — `serve-top` imports `serve-mid`, which imports `serve-leaf` — and
// whose Manifest maps the leaf's component type by a VERSIONED key while the leaf
// names that type bare.
//
// Both properties are load-bearing, and each removes a way the fixture could pass
// without the transitive descent running:
//
//   - Depth two. The closure walk reads one in-memory Aggregate value, so it pins
//     `serve-mid` (named directly in the top-level body) but cannot see
//     `serve-leaf`, which is named only inside `serve-mid`'s body. Only a pass that
//     loads a resolved body and re-walks it reaches the leaf, and only one that then
//     re-walks the LEAF reaches the leaf's own component type.
//   - A versioned Manifest mapping key. `serve-leaf` names its component type bare
//     (`serve-worker`) while the Manifest maps `serve-worker:1.0.0`. Closure
//     entities are keyed by the raw reference string, so the bare and the versioned
//     form are two distinct entities and the mapping cannot mask the descent's
//     contribution by having already pinned the same string.
//
// A fixture missing either property yields the same closure whether the descent
// recurses or stops after the first level, which is the blind spot that makes a
// single-level fixture unable to test a transitive wiring.
func transitiveSubAggregateWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "serve-top.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: serve-top
  version: "1.0.0"
spec:
  components:
    api:
      type: serve-front
  aggregates:
    mid:
      type: serve-mid
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "serve-mid.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: serve-mid
  version: "1.0.0"
spec:
  components:
    svc:
      type: serve-front
  aggregates:
    leaf:
      type: serve-leaf
`)
	// The leaf's component type is named BARE here and mapped VERSIONED in the
	// Manifest, so the bare entity enters the closure only by re-walking this body.
	writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "serve-leaf.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: serve-leaf
  version: "1.0.0"
spec:
  components:
    worker:
      type: serve-worker
`)
	for _, comp := range []string{"serve-front", "serve-worker"} {
		writeFixtureFile(t, filepath.Join(catalogDir, "components", comp+".yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: `+comp+`
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
		writeFixtureFile(t, filepath.Join(catalogDir, "recipes", comp+".yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: `+comp+`
  version: "1.0.0"
spec:
  capability: helm
  implements: `+comp+`
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: "test-server:latest"
`)
	}
	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        serve-front: serve-front
        serve-worker:1.0.0: serve-worker:1.0.0
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: serve-top
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        api: {}
        mid.svc: {}
        mid.leaf.worker: {}
`)
	return ws, filepath.Base(relPath)
}

// closureEntityURIs lists a closure's entity URIs in closure order, which is
// canonical — so a failure message built from it is stable across runs.
func closureEntityURIs(cl closure.Closure) []string {
	out := make([]string, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		out = append(out, e.URI)
	}
	return out
}

func closureHasEntity(cl closure.Closure, uri string) bool {
	for _, e := range cl.Entities {
		if e.URI == uri {
			return true
		}
	}
	return false
}

// TestServeReconstructsTransitiveSubAggregateAxis proves the daemon's drift path
// descends the sub-aggregate axis TRANSITIVELY rather than stopping one level down.
//
// The distinction is invisible at depth one: the closure walk already pins the
// sub-aggregates named in the top-level body, so a wiring that never recurses
// produces the same closure as one that does for any single-level fixture. Depth
// two is where the two diverge, and this fixture places an entity at each of the
// two points that only a recursive descent reaches:
//
//   - `serve-leaf`, the Aggregate named only inside `serve-mid`'s body. Reaching it
//     requires loading a resolved body and re-walking it.
//   - `serve-worker`, the component type named only inside `serve-leaf`'s body.
//     Reaching it requires re-walking a body that was itself only discovered by
//     re-walking. A descent that stops after the first level still pins
//     `serve-leaf` — it is discovered while re-walking `serve-mid` — but never
//     re-walks it, so this entity is the one that distinguishes the two wirings.
//
// The compute-only closure is asserted NOT to carry either, so the fixture cannot
// pass for the wrong reason: an assertion satisfied before the descent runs would
// be measuring the walk, not the descent.
func TestServeReconstructsTransitiveSubAggregateAxis(t *testing.T) {
	t.Parallel()
	ws, rel := transitiveSubAggregateWorkspace(t)
	releasePath := filepath.Join(ws, rel)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)

	// Compute-only closure (no lockfile, so no reconstruction runs).
	computeView := srv.lazyCatalog.ForRequest(nil)
	computed, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            computeView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   srv.workspaceRoot,
		WorkspaceConfig: srv.wsConfig,
	})
	if cerr != nil {
		t.Fatalf("compute-only ResolveClosure: %v", cerr)
	}

	// The chain is wired: the DIRECT sub-aggregate is reached by the walk alone.
	// Without this the fixture could be silently broken and the assertions below
	// would still hold for a closure that descends nothing.
	if !closureHasEntity(computed.Closure, "serve-mid") {
		t.Fatalf("fixture is broken: the closure walk should pin the direct sub-aggregate %q; entities=%v",
			"serve-mid", closureEntityURIs(computed.Closure))
	}
	// The depth-two entities must NOT be reachable without the descent.
	for _, uri := range []string{"serve-leaf", "serve-worker"} {
		if closureHasEntity(computed.Closure, uri) {
			t.Fatalf("fixture is inert: the closure walk already pinned %q, so the transitive descent contributes nothing to observe; entities=%v",
				uri, closureEntityURIs(computed.Closure))
		}
	}

	// The drift path: the graph request's preview reconstructs the axes, and the
	// closure it returns is the one the drift verify is handed.
	writeFixtureLockfile(t, lockfile.PathFor(releasePath), &lockfile.Lockfile{
		Version: 1, Release: "./" + rel, GeneratedAt: "2026-06-03T10:00:00Z",
	})
	reconstructed := serveInspectionClosure(t, srv, rel)

	if !closureHasEntity(reconstructed, "serve-leaf") {
		t.Errorf("reconstructed closure is missing the depth-two Aggregate %q — the sub-aggregate descent did not re-walk %q; entities=%v",
			"serve-leaf", "serve-mid", closureEntityURIs(reconstructed))
	}
	if !closureHasEntity(reconstructed, "serve-worker") {
		t.Errorf("reconstructed closure is missing %q, the component type named only inside the depth-two Aggregate %q — the descent stopped after the first level instead of recursing; entities=%v",
			"serve-worker", "serve-leaf", closureEntityURIs(reconstructed))
	}
}
