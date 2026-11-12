// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The tests in this file drive a SchemaLibrary that only a dependency socket's
// per-edge contract references — `valueSchema: { tls: { $ref: ... } }` — through
// the real pull and build. The library is discovered from the socket's contract
// as it is from a Component's own schema: pinned in the lockfile when the catalog
// holds it, and reported by name, as a reference the dependency resolution cannot
// resolve, when it does not.

// socketContractLibraryWorkspace lays down a workspace where Component
// acme/api's socket `db` declares the per-edge contract contract, and the
// Aggregate wires api to a database through that socket with the edge values
// edgeValues. With withLibrary, the catalog holds SchemaLibrary ProbeLib.
func socketContractLibraryWorkspace(t *testing.T, root, contract, edgeValues string, withLibrary bool) (catalogDir, releasePath string) {
	t.Helper()
	// Offline, so a library the catalog does not hold is answered by the local
	// resolution alone and never by a registry the machine may or may not reach.
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true")
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/api/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    db:
      selector: "component.aggrega.ai/type=stateful-server"
      valueSchema:
        `+contract+`
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "component/acme/pg/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/pg
  version: 1.0.0
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    values: {}
`)
	for _, r := range []struct{ name, implements string }{{"api", "acme/api"}, {"pg", "acme/pg"}} {
		writeFixture(t, filepath.Join(catalogDir, "recipe/compose", r.name, "1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: `+r.name+`
  version: 1.0.0
spec:
  capability: compose
  implements: `+r.implements+`:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "`+r.name+`:1"
`)
	}
	if withLibrary {
		writeFixture(t, filepath.Join(catalogDir, "schemalibrary/probe-lib/1.0.0/library.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: ProbeLib
  version: 1.0.0
spec:
  schemas:
    Tls:
      type: object
      properties:
        mode: { type: string, enum: [strict, lax] }
`)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: socket-app
  version: 1.0.0
spec:
  components:
    db:
      type: acme/pg:1.0.0
    api:
      type: acme/api:1.0.0
      uses:
        db:
          main:
            target: db
            values:
              `+edgeValues+`
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: socket-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/api:1.0.0: compose/api:1.0.0
        acme/pg:1.0.0: compose/pg:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: socket-release
  environment: local
  version: 1.0.0
spec:
  aggregate: socket-app
  manifest: socket-manifest
  boundaries:
    main:
      directory: out
      components:
        api: {}
        db: {}
`)
	return catalogDir, releasePath
}

// pullSocketWorkspace runs the real pull over a socket-contract workspace, first
// checking that the workspace resolved offline: a pull reaching a registry would
// make the missing-library answer depend on the machine's network and credentials.
func pullSocketWorkspace(t *testing.T, root, catalogDir, releasePath string) error {
	t.Helper()
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if !ws.Config().Offline {
		t.Fatal("the workspace did not resolve offline, so the pull could reach a registry")
	}
	return pullRun(context.Background(), pullConfig{
		ws:          ws,
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// TestPull_ALibraryOnlyASocketContractReferencesIsPinned pulls a Release whose one
// SchemaLibrary reference sits in a socket's per-edge contract, and reads the
// lockfile: the library is pinned there, and the build reading that lockfile
// applies the referenced entry to the edge's values — a value the entry admits
// builds, one it refuses halts naming the edge field.
func TestPull_ALibraryOnlyASocketContractReferencesIsPinned(t *testing.T) {
	t.Parallel()
	const contract = `tls: { $ref: "ProbeLib#/schemas/Tls" }`

	root := t.TempDir()
	catalogDir, rel := socketContractLibraryWorkspace(t, root, contract, "tls: { mode: strict }", true)
	if err := pullSocketWorkspace(t, root, catalogDir, rel); err != nil {
		t.Fatalf("pull:\n%s", formatCompilerError(err))
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(rel))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	var pinned []string
	for _, l := range lf.SchemaLibraries {
		pinned = append(pinned, l.URI)
	}
	found := false
	for _, uri := range pinned {
		if uri == "ProbeLib" || strings.HasPrefix(uri, "ProbeLib:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the library the socket contract references is not pinned; schema libraries pinned: %v", pinned)
	}
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		rel, []string{catalogDir}, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("a build reading the lockfile the pull wrote must succeed:\n%s", formatCompilerError(err))
	}

	refusedRoot := t.TempDir()
	catalogDir, rel = socketContractLibraryWorkspace(t, refusedRoot, contract, "tls: { mode: none }", true)
	err = pullSocketWorkspace(t, refusedRoot, catalogDir, rel)
	if err == nil {
		t.Fatal("an edge value the referenced entry refuses was accepted")
	}
	if printed := formatCompilerError(err); !errors.Is(err, resolver.ErrSchemaConformance()) ||
		!strings.Contains(printed, "values.tls.mode") {
		t.Errorf("the refusal must be the edge value failing the referenced entry, at values.tls.mode:\n%s", printed)
	}
}

// TestPull_ALibraryASocketContractReferencesAndNoCatalogHoldsIsReportedByName
// pulls the same Release with the library absent: the pull halts as the
// dependency resolution halts on a reference it cannot resolve, naming the
// library, rather than reaching the contract and refusing it as a contract that
// could not be compiled — as the same reference in a Component's own schema is
// reported. The reference is found wherever the contract holds it.
func TestPull_ALibraryASocketContractReferencesAndNoCatalogHoldsIsReportedByName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, contract string }{
		{"on a property of the contract", `tls: { $ref: "NoSuchLib#/schemas/Tls" }`},
		{"on a property nested inside the contract",
			`tls: { type: object, properties: { mode: { $ref: "NoSuchLib#/schemas/Mode" } } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir, rel := socketContractLibraryWorkspace(t, root, tc.contract, "tls: { mode: strict }", false)
			err := pullSocketWorkspace(t, root, catalogDir, rel)
			if err == nil {
				t.Fatal("a socket contract referencing a library no catalog holds pulled")
			}
			printed := formatCompilerError(err)
			if errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
				t.Fatalf("the absent library reached the contract unreported by the dependency resolution:\n%s", printed)
			}
			if !errors.Is(err, closure.ErrUnresolvableURI()) {
				t.Fatalf("the halt must be the dependency resolution's unresolvable reference:\n%s", printed)
			}
			for _, w := range []string{"E0114", `"NoSuchLib"`} {
				if !strings.Contains(printed, w) {
					t.Errorf("the printed diagnostic does not carry %s:\n%s", w, printed)
				}
			}
		})
	}
}
