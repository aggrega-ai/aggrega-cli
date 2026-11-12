// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The tests in this file hold the lockfile's identity of an entity: its kind and
// its reference together. A Release whose Aggregate and Manifest carry one name —
// every Release an importer drafts names both by the one name of the draft — pins each by a row
// of its own, so an edit to either is drift exactly as it is where the two names
// differ. With the identity reduced to the reference alone, the two collapse into
// one row: the lockfile pins the Manifest only, and a build over an edited
// Aggregate passes against the stale lockfile.

// sharedNameRelease is the reference the Release names both its Aggregate and its
// Manifest by.
const sharedNameRelease = "imported/flow"

// sharedNameFixture writes a buildable workspace whose Release names its Aggregate
// and its Manifest by one name, each found beside the Release by that name, and
// returns the Release's path. Nothing in it reaches a network: the Component, the
// Recipe and the rendering plugin are authored in the workspace's catalog
// directory, and the Recipe declares no remote source.
func sharedNameFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: `+sharedNameRelease+`
  version: "1.0.0"
  description: The drafted flow.
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: `+sharedNameRelease+`
  version: "1.0.0"
  description: The drafted realization.
spec:
  mappings:
    components:
      type:
        server: server-chart
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: shared-name
  environment: testing
  version: "1.0.0"
spec:
  aggregate: `+sharedNameRelease+`
  manifest: `+sharedNameRelease+`
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server-chart.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-chart
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	return filepath.Join(root, "release.yaml")
}

// pullSharedName resolves the fixture's Release the way `aggrega pull --release`
// does, writing its lockfile.
func pullSharedName(t *testing.T, root, releasePath string) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// buildSharedName builds the fixture's Release the way `aggrega build --release`
// does, against the lockfile on disk.
func buildSharedName(t *testing.T, root, releasePath string) error {
	t.Helper()
	return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, []string{filepath.Join(root, "catalog")}, false, io.Discard, io.Discard)
}

// TestSharedNameLockfileHoldsAnAggregateRowAndAManifestRow holds what a pull
// records for a Release whose Aggregate and Manifest share a name: two rows of that
// reference, one of each kind, each carrying the digest of its own document.
func TestSharedNameLockfileHoldsAnAggregateRowAndAManifestRow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath := sharedNameFixture(t, root)
	if err := pullSharedName(t, root, releasePath); err != nil {
		t.Fatalf("pull: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	digests := map[string]string{}
	for _, e := range lf.Entities {
		if e.URI != sharedNameRelease {
			continue
		}
		if _, dup := digests[e.Kind]; dup {
			t.Fatalf("two rows of %q carry kind %q:\n%+v", sharedNameRelease, e.Kind, lf.Entities)
		}
		digests[e.Kind] = e.Digest
	}
	for _, kind := range []string{closure.KindAggregate, closure.KindManifest} {
		if digests[kind] == "" {
			t.Errorf("the lockfile holds no %s row of %q; rows of that reference: %v", kind, sharedNameRelease, digests)
		}
	}
	if len(digests) != 2 {
		t.Errorf("rows of %q by kind = %v; want exactly an aggregate row and a manifest row", sharedNameRelease, digests)
	}
	if a, m := digests[closure.KindAggregate], digests[closure.KindManifest]; a != "" && a == m {
		t.Errorf("the aggregate row and the manifest row carry one digest %s; each must be its own document's", a)
	}
	body := string(mustReadFile(t, lockfile.PathFor(releasePath)))
	for _, want := range []string{"kind: aggregate", "kind: manifest"} {
		if !strings.Contains(body, want) {
			t.Errorf("the written lockfile does not carry %q:\n%s", want, body)
		}
	}
}

// TestAComponentAndARecipeOfOneNameEachPinTheirOwnDocument holds the same identity
// over a catalog directory: the authoring fixture's Component and Recipe are both
// named `server`, and the Manifest maps the type to the Recipe by that name. A pull
// records one row of each kind, each carrying the digest of its own document, so
// the Recipe reference resolves to the Recipe rather than to the Component that
// shares its name.
func TestAComponentAndARecipeOfOneNameEachPinTheirOwnDocument(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	want := map[string]string{
		closure.KindComponent: rowDigestAt(t, filepath.Join(root, "catalog", "components", "server.yaml")),
		closure.KindRecipe:    rowDigestAt(t, filepath.Join(root, "catalog", "recipes", "server.yaml")),
	}
	if want[closure.KindComponent] == want[closure.KindRecipe] {
		t.Fatal("test setup degenerate: the Component and the Recipe digest alike")
	}
	got := map[string]string{}
	for _, e := range lf.Entities {
		if e.URI == "server" {
			got[e.Kind] = e.Digest
		}
	}
	if len(got) != len(want) {
		t.Errorf("rows of %q by kind = %v; want one component row and one recipe row", "server", got)
	}
	for kind, digest := range want {
		if got[kind] != digest {
			t.Errorf("the %s row of %q records %q; want its own document's digest %q", kind, "server", got[kind], digest)
		}
	}
}

// TestEditingEitherSameNamedDocumentIsDrift drives the sequence a user meets: pull
// a Release whose Aggregate and Manifest share a name, build it, edit one of the
// two, build again. The untouched build must pass, the edited one must halt with
// the drift condition naming the edited document's row, and a pull must clear it.
func TestEditingEitherSameNamedDocumentIsDrift(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, file, from, to, kind string
	}{
		{"the Aggregate", "aggregate.yaml", "The drafted flow.", "The drafted flow, edited.", closure.KindAggregate},
		{"the Manifest", "manifest.yaml", "The drafted realization.", "The drafted realization, edited.", closure.KindManifest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			releasePath := sharedNameFixture(t, root)
			if err := pullSharedName(t, root, releasePath); err != nil {
				t.Fatalf("pull: %v", err)
			}
			if err := buildSharedName(t, root, releasePath); err != nil {
				t.Fatalf("build over the untouched documents must pass: %v", err)
			}

			path := filepath.Join(root, tc.file)
			body := string(mustReadFile(t, path))
			if !strings.Contains(body, tc.from) {
				t.Fatalf("test setup: %s does not carry %q", path, tc.from)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(body, tc.from, tc.to, 1)), 0o644); err != nil {
				t.Fatalf("edit %s: %v", path, err)
			}

			err := buildSharedName(t, root, releasePath)
			if err == nil {
				t.Fatalf("build after editing %s passed against the lockfile written before the edit", tc.name)
			}
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("build after editing %s: want the lockfile-drift condition; got %v", tc.name, err)
			}
			if want := `entity "` + sharedNameRelease + `" (` + tc.kind + `) changed`; !strings.Contains(err.Error(), want) {
				t.Errorf("the drift report does not name the edited row %q:\n%v", want, err)
			}

			if err := pullSharedName(t, root, releasePath); err != nil {
				t.Fatalf("pull after the edit: %v", err)
			}
			if err := buildSharedName(t, root, releasePath); err != nil {
				t.Errorf("build after pulling the edit must pass: %v", err)
			}
		})
	}
}
