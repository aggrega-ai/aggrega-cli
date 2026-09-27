// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// rowDigestAt is the digest a lockfile's entity row records for the entity
// authored in the document at path, taken through the product's own reading of it
// (catalog.AuthoredEntityDigestAt) so an expectation cannot fall out of step with
// the rule: over what a publication of the entity would carry — the document, the
// files it declares and, for a Recipe or FeatureRecipe, the lock beside it.
func rowDigestAt(t *testing.T, path string) string {
	t.Helper()
	d, err := catalog.AuthoredEntityDigestAt(path)
	if err != nil {
		t.Fatalf("row digest of %s: %v", path, err)
	}
	return d
}

// publishedRowDigest is the digest a lockfile row records for bytes a registry
// published — as a fetch or a cache slot holds them — through the product's own
// rule (catalog.PinDigest): for an entity, over what the publication carries, so a
// document published alone digests as its one-entry set rather than as its bytes.
func publishedRowDigest(t *testing.T, published []byte) string {
	t.Helper()
	d, err := catalog.PinDigest(published)
	if err != nil {
		t.Fatalf("row digest of published bytes: %v", err)
	}
	return d
}

// fullURICollisionFixture authors a buildable workspace whose Recipe is
// referenced from the Manifest by its FULL provider-qualified URI
// (helm/server:1.0.0) so it lands as a pinned closure ENTITY, and whose Recipe
// carries a $import artifact so the resolution-phase freeze actually WRITES
// artifactDigests into the lock beside the recipe — the workspace acquires a
// file at freeze without the Recipe itself needing a remote source or a network.
// The Recipe's bare name (server) deliberately COLLIDES with the Component's
// bare name (server) — the exact condition under which a by-bare-name
// reconciliation would corrupt the Component's recorded digest. Returns the
// release path.
func fullURICollisionFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	// auto_pull off so `build` reads the pull-frozen lockfile and verifies drift
	// against it, rather than re-pulling and healing any lag — the exact state the
	// one-pull fixpoint must hold in. pin_sources_at_pull on, so the pull runs the
	// pin step and writes the lock this fixpoint is about rather than halting on a
	// recipe whose lock does not record its imported file yet.
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\npin_sources_at_pull: true\n")
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
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: drift-rel
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
	// The recipe is referenced by its FULL provider-qualified URI, not the bare
	// short form — this is what makes it land as a pinned closure entity.
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
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	// The $import target: a loose file next to the recipe whose bytes the freeze
	// hashes into the artifactDigests of the lock beside the recipe.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "extra.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: extra
data:
  origin: imported
`)
	// The recipe's bare name (server) collides with the Component's bare name.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  artifacts:
    extra.yaml:
      $import: ./extra.yaml
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
	return filepath.Join(root, "release.yaml")
}

// TestFreezeFullURIRecipeOnePullFixpoint proves the one-pull fixpoint for a
// recipe referenced by full URI whose freeze writes a lock beside it: after a
// SINGLE pull, the lockfile records the row the recipe actually has — over what a
// publication of it carries, the lock the freeze just wrote included — so a build
// (which verifies the lockfile against a freshly-recomputed closure) sees no drift
// — no "lockfile out of date" halt and no need for a second, healing pull. It
// also asserts the collision guard: the Component that shares the recipe's bare
// name keeps its own untouched row in the lockfile.
//
// The freeze is observed on the lock, because that is the file it writes. A
// freeze that wrote nothing at all would leave the lockfile trivially in
// agreement with a recipe nobody touched, which is why the lock's presence and
// content are asserted before the fixpoint claim is made — and why the row taken
// before the pull must differ from the one after it.
func TestFreezeFullURIRecipeOnePullFixpoint(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(root, "catalog", "recipes", "server.yaml")
	componentPath := filepath.Join(root, "catalog", "components", "server.yaml")

	// The row of the Component, which the freeze never touches — the value the
	// lockfile must carry for the Component both before and after the fix.
	componentDigest := rowDigestAt(t, componentPath)
	// The recipe's row before the freeze: no lock beside it yet. A lockfile
	// carrying this after the pull is the one-pull-lag bug.
	preFreezeRecipeDigest := rowDigestAt(t, recipePath)

	// A single pull: resolves the closure, freezes the recipe's pins into the
	// lock beside it, and writes the lockfile.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// The freeze must actually have written pins — otherwise the test would not
	// exercise the frozen path at all.
	lockPath := recipepin.LockPath(recipePath)
	lockBody := string(mustReadFile(t, lockPath))
	if !strings.Contains(lockBody, "artifactDigests") {
		t.Fatalf("freeze did not write artifactDigests into %s; the test does not exercise the frozen path:\n%s", lockPath, lockBody)
	}
	// The recipe's row as it stands after the freeze — over the document and the
	// lock beside it — the value the lockfile's recipe entity must carry, and the
	// value the build recomputes from the files.
	recipeDigest := rowDigestAt(t, recipePath)
	if recipeDigest == componentDigest {
		t.Fatalf("test setup degenerate: recipe and component share a row digest")
	}
	if recipeDigest == preFreezeRecipeDigest {
		t.Fatalf("test setup degenerate: the freeze's lock did not move the recipe's row, so the lag this test names cannot show")
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}

	// Exactly one lockfile entity carries the recipe's POST-freeze digest (the fix)
	// and exactly one carries the Component's untouched digest (the collision
	// guard), and they are distinct entities matched by URI.
	var recipeEntity, componentEntity *lockfile.Entity
	for i := range lf.Entities {
		e := &lf.Entities[i]
		switch e.Digest {
		case recipeDigest:
			if recipeEntity != nil {
				t.Fatalf("two lockfile entities carry the recipe post-freeze digest: %q and %q", recipeEntity.URI, e.URI)
			}
			recipeEntity = e
		case componentDigest:
			if componentEntity != nil {
				t.Fatalf("two lockfile entities carry the component digest: %q and %q", componentEntity.URI, e.URI)
			}
			componentEntity = e
		}
	}
	if recipeEntity == nil {
		t.Fatalf("no lockfile entity carries the recipe's post-freeze row digest %s; lockfile records the pre-freeze recipe (the one-pull-lag bug):\n%+v", recipeDigest, lf.Entities)
	}
	if componentEntity == nil {
		t.Fatalf("no lockfile entity carries the component's row digest %s (collision guard: the fix corrupted the component digest):\n%+v", componentDigest, lf.Entities)
	}
	if recipeEntity.URI == componentEntity.URI {
		t.Fatalf("recipe and component collapsed to one entity URI %q — a by-name reconciliation corrupted the collision", recipeEntity.URI)
	}

	// The one-pull fixpoint: a build after a single pull verifies the frozen
	// lockfile against a freshly-recomputed closure and must NOT report drift.
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, buildDiagOptions{quiet: true}, buildops.RunOptions{OutDir: filepath.Join(root, "dist", "main")}, io.Discard, io.Discard); err != nil {
		if errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Fatalf("build after a single pull reported lockfile drift — the one-pull-lag bug: %v", err)
		}
		t.Fatalf("build after a single pull failed: %v", err)
	}

	// Guard against a SKIP masking a non-run: the build must have produced dist output.
	if len(snapshotDist(t, filepath.Join(root, "dist"))) == 0 {
		t.Fatalf("build produced no dist output; the fixpoint assertion never exercised a real render")
	}
}
