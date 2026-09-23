// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// What this file proves.
//
// A pull pins every remote source a Release resolves in that Release's lockfile,
// and additionally records a pin in the lock beside the entity that declares the
// source — but only for a source whose identity its author wrote out in full. A
// source whose identity is filled in from a template names an artifact only once a
// Release's own values fill it in, so two Releases sharing the entity may resolve
// it to two artifacts, and a file every Release shares cannot hold a value one of
// them decided.
//
// One Release carries both shapes, so the assertions are made over a single pull
// and neither half can pass because the other was not exercised: the lockfile must
// pin both sources, the lock beside the authored-identity Recipe must record its
// pin, and the templated one must have no lock at all. The authored-identity Recipe
// is used by two component instances, which resolve it to one artifact, so its lock
// must record that artifact once rather than once per instance.

// sourcePinIdentityWorkspace lays down a Release with three components: `api`, whose
// Recipe's source version is a template, and `worker-a` and `worker-b`, whose Recipe
// names every identity field literally. Each Recipe sits in a directory of its own,
// because the lock beside an entity is scoped by the directory it sits in.
func sourcePinIdentityWorkspace(t *testing.T, root string) (releasePath, catalogDir, templatedRecipe, authoredRecipe string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: pin-identity
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
    worker-a:
      type: core/worker
      values:
        port: 9090
    worker-b:
      type: core/worker
      values:
        port: 9091
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: pin-identity-release
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
        worker-a:
          infra:
            replicas: 1
        worker-b:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: pin-identity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
        core/worker: worker-recipe:1.0.0
`)
	catalogDir = filepath.Join(root, "catalog")
	for _, c := range []struct{ file, name, typ string }{
		{"server.yaml", "core/server", "server"},
		{"worker.yaml", "core/worker", "worker"},
	} {
		writeFixture(t, filepath.Join(catalogDir, "base", "components", c.file), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: `+c.name+`
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: `+c.typ+`
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	}
	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "base", "providers"))
	workload := `  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      main:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`
	templatedRecipe = filepath.Join(catalogDir, "recipes", "templated", "server.yaml")
	writeFixture(t, templatedRecipe, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  source:
    kind: helm-oci-chart
    url: oci://example.invalid/charts/server
    version: "{{ optional .infra `+"`source_version`"+` \"1.2.3\" }}"
`+workload)
	authoredRecipe = filepath.Join(catalogDir, "recipes", "authored", "worker.yaml")
	writeFixture(t, authoredRecipe, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: worker-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/worker
  source:
    kind: helm-oci-chart
    url: oci://example.invalid/charts/worker
    version: "1.0.0"
`+workload)
	return filepath.Join(root, "release.yaml"), catalogDir, templatedRecipe, authoredRecipe
}

// TestPullRecordsASourcePinBesideAnEntityOnlyForAnIdentityItsAuthorWroteOut drives
// the command's own entry point over the workspace above, twice.
func TestPullRecordsASourcePinBesideAnEntityOnlyForAnIdentityItsAuthorWroteOut(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir, templatedRecipe, authoredRecipe := sourcePinIdentityWorkspace(t, root)
	templatedBefore := mustReadFile(t, templatedRecipe)
	authoredBefore := mustReadFile(t, authoredRecipe)

	// The pull runs the pin step, as --pin-sources does: which source lands beside
	// which entity is the step's routing, and by default a pull records nothing
	// there and halts on the authored-identity Recipe instead.
	pinSources := true
	pull := func() {
		t.Helper()
		if err := pullRun(context.Background(), pullConfig{
			ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath:    releasePath,
			CatalogDirs:    []string{catalogDir},
			PinSources:     &pinSources,
			sourceResolver: verbatimSourceResolver{},
			DiagOut:        io.Discard,
			Out:            io.Discard,
			diag:           buildDiagOptions{quiet: true},
		}); err != nil {
			t.Fatalf("pull: %v", err)
		}
	}
	pull()

	// The lockfile pins BOTH sources. This is the positive control for the negative
	// below: without it, a templated source that simply never resolved would leave no
	// lock beside its Recipe for the wrong reason.
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	rows := map[string]lockfile.SourceArtifact{}
	for _, row := range lf.SourceArtifacts {
		rows[row.URL] = row
	}
	templatedRow, ok := rows["oci://example.invalid/charts/server"]
	if !ok {
		t.Fatalf("the lockfile does not pin the templated source, so nothing below is about where its pin lives; rows: %+v", lf.SourceArtifacts)
	}
	if templatedRow.PinnedVersion != "1.2.3" {
		t.Errorf("the templated source must pin the version its template evaluated to; pinned %q", templatedRow.PinnedVersion)
	}
	authoredRow, ok := rows["oci://example.invalid/charts/worker"]
	if !ok {
		t.Fatalf("the lockfile does not pin the authored-identity source; rows: %+v", lf.SourceArtifacts)
	}

	// The templated source gets no row beside its entity — and its Recipe reaches
	// nothing else a lock would record, so there is no lock at all.
	if lock, present, err := recipepin.ReadLock(templatedRecipe); err != nil {
		t.Fatalf("read the lock beside the templated recipe: %v", err)
	} else if present {
		t.Errorf("a lock was written beside a Recipe whose source identity is a template; it records %+v", lock.SourceDigests)
	}

	// The authored-identity source gets exactly one row beside its entity, however
	// many instances resolved it, and it is the lockfile's own record of that source.
	lock := readLockForTest(t, authoredRecipe)
	if len(lock.SourceDigests) != 1 {
		t.Fatalf("the lock beside the authored-identity Recipe must record its one source once; it records %d rows: %+v",
			len(lock.SourceDigests), lock.SourceDigests)
	}
	pin := lock.SourceDigests[0]
	if pin.URL != authoredRow.URL || pin.Version != authoredRow.PinnedVersion || pin.Digest != authoredRow.Digest {
		t.Errorf("the recorded pin is not the lockfile's record of the same source;\npin: %+v\nrow: %+v", pin, authoredRow)
	}

	for _, r := range []struct {
		path   string
		before []byte
	}{{templatedRecipe, templatedBefore}, {authoredRecipe, authoredBefore}} {
		if got := mustReadFile(t, r.path); !bytes.Equal(got, r.before) {
			t.Errorf("the pull rewrote the authored document %s", r.path)
		}
	}

	// A second pull over the unchanged workspace moves neither answer.
	lockBytes := mustReadFile(t, recipepin.LockPath(authoredRecipe))
	pull()
	if got := mustReadFile(t, recipepin.LockPath(authoredRecipe)); !bytes.Equal(got, lockBytes) {
		t.Errorf("a second pull rewrote the lock beside the authored-identity Recipe:\n--- first ---\n%s\n--- second ---\n%s", lockBytes, got)
	}
	if _, present, err := recipepin.ReadLock(templatedRecipe); err != nil || present {
		t.Errorf("a second pull left a lock beside the templated Recipe (present=%v, err=%v)", present, err)
	}
}
