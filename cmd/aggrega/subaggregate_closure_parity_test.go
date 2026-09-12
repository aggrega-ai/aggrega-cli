// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// transitiveSubAggregateFixture authors a workspace whose Aggregate chain is two
// levels deep — `parity-top` imports `parity-mid`, which imports `parity-leaf` —
// and whose Manifest maps the leaf's component type by a VERSIONED key while the
// Aggregates reference that type bare.
//
// Both properties are load-bearing, and each isolates a way the two closure
// orchestrations can disagree:
//
//   - Depth two. The closure walk reads one in-memory Aggregate value, so it pins
//     `parity-mid` (a direct reference from the top level) but cannot see
//     `parity-leaf`, which is named only inside `parity-mid`'s body. Only the
//     descent, which loads a resolved body and re-walks it, reaches it.
//   - A versioned Manifest mapping key. `parity-leaf` names its component type
//     bare (`parity-worker`), while the Manifest maps `parity-worker:1.0.0`.
//     Closure entities are keyed by the raw reference string, so the bare and the
//     versioned form are two distinct entities and the descent's contribution
//     cannot be masked by the mapping key having already pinned the same string.
//
// A fixture missing either property yields two closures that agree whether or not
// the descent runs, which is exactly the blind spot that let a silently-skipped
// descent ship.
func transitiveSubAggregateFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "parity-top.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-top
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
  aggregates:
    mid:
      type: parity-mid
`)
	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "parity-mid.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-mid
  version: "1.0.0"
spec:
  components:
    svc:
      type: server
      values:
        port: 9090
  aggregates:
    leaf:
      type: parity-leaf
`)
	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "parity-leaf.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-leaf
  version: "1.0.0"
spec:
  components:
    worker:
      type: parity-worker
      values:
        port: 9190
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: parity-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: parity-top
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
        mid.svc:
          infra:
            replicas: 1
        mid.leaf.worker:
          infra:
            replicas: 1
`)
	// The leaf's component type is mapped by a VERSIONED key; the Aggregates name
	// it bare. See the fixture doc for why that asymmetry is deliberate.
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
        parity-worker:1.0.0: parity-worker:1.0.0
`)
	for _, comp := range []string{"server", "parity-worker"} {
		writeFixture(t, filepath.Join(catalogRoot, "components", comp+".yaml"), `apiVersion: aggrega.ai/v1alpha1
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
		writeFixture(t, filepath.Join(catalogRoot, "recipes", comp+".yaml"), `apiVersion: aggrega.ai/v1alpha1
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
      server:
        image: "test-server:latest"
        ports:
          web: {{ .values.port }}
`)
	}
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	return filepath.Join(root, "release.yaml")
}

// TestSubAggregateAxisAgreesAcrossLockAndBuild is the cross-side guard on the
// transitive sub-aggregate axis: the set the lock-time descent PINS and the set
// the hermetic reconstruction RE-DERIVES must be the same set.
//
// The two sides reach that axis through different orchestrations over the same
// primitives — the lock-time path drives the descent inline between passes only it
// runs, while a build or a serve drift check drives it through
// closure.ReconstructClosureAxes — and each side obtains the resolved-body loader
// from its own catalog lookup. Nothing in the type system couples them, so
// agreement is a property that has to be measured. When it fails, the failure is
// silent in the worst possible way: the side that comes up short reports the
// other side's pins as entities "no longer used by the release", and the
// remediation it prints names the very command that wrote them.
//
// Three assertions, because set equality alone is not enough:
//
//   - Set equality of entity URIs, which is the property that matters.
//   - Equal counts, which set equality cannot see: a duplicated URI on one side
//     is invisible to a set comparison.
//   - A positive floor — the transitively-reached Aggregate is present on BOTH
//     sides. Set equality is symmetric, so two sides that both skipped the
//     descent would agree perfectly; only naming an entity that exists solely
//     because the descent ran can distinguish agreement from shared blindness.
func TestSubAggregateAxisAgreesAcrossLockAndBuild(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := transitiveSubAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	// Side A — the lock-time closure, taken from what pull actually writes rather
	// than from a re-implementation of its orchestration in the test.
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	lockSide := make([]string, 0, len(lf.Entities))
	for _, e := range lf.Entities {
		lockSide = append(lockSide, e.URI)
	}

	// Side B — the hermetic reconstruction, wired exactly as the build path wires
	// it: the lazy view pinned to the lockfile, a nil puller, and the reconstruction
	// reusing the Lookup the closure was walked through.
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := workspaceconfig.ResolveCatalogDirs([]string{catalogDir}, wsCfg, root)

	view := catalog.NewLazyLookup(root, cacheDir, dirs, nil)
	view.SetPins(catalog.PinsFromLockfile(lf))
	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   root,
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: %v", cerr)
	}
	cl := resolved.Closure

	// The walk alone must NOT already carry the transitively-reached Aggregate —
	// otherwise the fixture does not exercise the descent and the comparison below
	// would pass for the wrong reason.
	if containsURI(cl.Entities, "parity-leaf") {
		t.Fatalf("fixture is inert: the closure walk already pinned %q, so the descent contributes nothing to compare", "parity-leaf")
	}

	aggLoader, aerr := closure.RequireAggregateLoader(resolved.Lookup)
	if aerr != nil {
		t.Fatalf("RequireAggregateLoader on the build-side lookup: %v", aerr)
	}
	bindings := lockfile.ProviderBindings(lf)
	reconResolver := func(ref string) (types.Plugin, bool, error) {
		return view.ProviderPluginForTarget(ref, bindings)
	}
	reconRefold := func() (closure.RecipeLoader, error) {
		view.SetPins(catalog.PinsFromClosure(cl))
		return view.ClosureRecipeLoader(), nil
	}
	if rerr := closure.ReconstructClosureAxes(context.Background(), &cl, aggLoader, view.ClosureRecipeLoader(), resolved.Lookup, reconResolver, reconRefold); rerr != nil {
		t.Fatalf("ReconstructClosureAxes: %v", rerr)
	}
	view.SetPins(catalog.PinsFromLockfile(lf))

	buildSide := make([]string, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		buildSide = append(buildSide, e.URI)
	}

	if len(lockSide) != len(buildSide) {
		t.Errorf("entity COUNT differs between the lock-time and reconstructed closures: lock=%d build=%d\n  lock : %v\n  build: %v",
			len(lockSide), len(buildSide), sortedCopy(lockSide), sortedCopy(buildSide))
	}
	if missing, extra := setDiff(lockSide, buildSide); len(missing) > 0 || len(extra) > 0 {
		t.Errorf("entity SET differs between the lock-time and reconstructed closures\n  pinned at lock time but not re-derived: %v\n  re-derived but not pinned: %v",
			missing, extra)
	}

	// The positive floor: the transitively-reached Aggregate exists on both sides
	// only because the descent ran on both sides.
	for _, side := range []struct {
		name string
		uris []string
	}{{"lock-time", lockSide}, {"reconstructed", buildSide}} {
		if !containsString(side.uris, "parity-leaf") {
			t.Errorf("%s closure is missing the transitively-reached Aggregate %q — the sub-aggregate descent did not run on that side; entities=%v",
				side.name, "parity-leaf", sortedCopy(side.uris))
		}
	}
}

// TestLazyClosureLookupServesAggregateBodies pins the capability the cross-side
// agreement rests on, at the point where it is cheapest to observe. The build and
// serve paths obtain their loader from the lazy view; a lazy view that cannot
// serve resolved bodies hands the descent nothing to walk, and the descent reads
// that as "no loader was wired" and returns without a word.
//
// This is deliberately not covered by the agreement test alone: agreement is
// symmetric and would still hold if both sides lost the capability together.
func TestLazyClosureLookupServesAggregateBodies(t *testing.T) {
	root := t.TempDir()
	view := catalog.NewLazyLookup(root, filepath.Join(root, ".aggrega", "cache"), nil, nil)
	lookup := view.ClosureLookup(nil, root, filepath.Join(root, ".aggrega", "cache"), nil)
	if _, err := closure.RequireAggregateLoader(lookup); err != nil {
		t.Fatalf("the lazy closure lookup must satisfy closure.AggregateLoader: %v", err)
	}
	pinned := view.ClosureLookupWithPins(nil, root, filepath.Join(root, ".aggrega", "cache"), nil, nil)
	if _, err := closure.RequireAggregateLoader(pinned); err != nil {
		t.Fatalf("the pinned lazy closure lookup must satisfy closure.AggregateLoader: %v", err)
	}
}

func containsURI(refs []closure.ResolvedRef, uri string) bool {
	for _, r := range refs {
		if r.URI == uri {
			return true
		}
	}
	return false
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

// setDiff reports the members of a absent from b, and the members of b absent
// from a, each sorted so a failure message is stable.
func setDiff(a, b []string) (missing, extra []string) {
	inB := make(map[string]struct{}, len(b))
	for _, x := range b {
		inB[x] = struct{}{}
	}
	inA := make(map[string]struct{}, len(a))
	for _, x := range a {
		inA[x] = struct{}{}
	}
	for x := range inA {
		if _, ok := inB[x]; !ok {
			missing = append(missing, x)
		}
	}
	for x := range inB {
		if _, ok := inA[x]; !ok {
			extra = append(extra, x)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
