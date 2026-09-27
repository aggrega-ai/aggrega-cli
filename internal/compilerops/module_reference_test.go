// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const moduleRefComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`

// moduleRefStore is the module a Release reaches, with its one component's type
// left to the row.
const moduleRefStore = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: example/store
  version: "1.0.0"
spec:
  components:
    store:
      type: %s
`

// moduleRefOuter is a module that imports the store module, so a row can place
// the reference one module further in.
const moduleRefOuter = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: example/outer
  version: "1.0.0"
spec:
  aggregates:
    inner:
      type: example/store
`

// moduleRefRoot is the Release's own Aggregate: one component of its own and one
// module, both left to the row.
const moduleRefRoot = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: module-reference-app
  version: "1.0.0"
spec:
  components:
    web:
      type: %s
  aggregates:
    mod:
      type: %s
`

const moduleRefManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: module-reference-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const moduleRefRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: module-reference-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        web: {}
      aggregates:
        mod: {}
`

// TestAValidationRefusesAReferenceAModuleMakesThatNothingResolves pins what a
// Release-scoped validation reports when a module the Release reaches names a type
// no catalog holds: the unresolvable-reference condition, carrying the reference
// that did not resolve — the author's fault, as a build reports it — and never
// the disagreement between the compiler's own views of the catalog, which is what
// the checks that compile the Release report when nothing resolved the module's
// references before them.
//
// One fault per row. The first row is the positive control: the same shape with
// every reference resolvable is not refused, so a refusal in the rows below is
// the missing type's and not the fixture's. The last row is the control on the
// other side: a type the Release's own Aggregate names and no catalog holds is
// the same condition, so a module's reference is refused exactly as the
// Aggregate's is.
func TestAValidationRefusesAReferenceAModuleMakesThatNothingResolves(t *testing.T) {
	t.Parallel()
	const absent = "example/absent"
	for _, tc := range []struct {
		name       string
		rootType   string
		module     string
		storeType  string
		wantAbsent string
	}{
		{"every reference the modules make resolves", "example/web", "example/store", "example/web", ""},
		{"a module's component names a type no catalog holds", "example/web", "example/store", absent, absent},
		{"a module two imports in names a type no catalog holds", "example/web", "example/outer", absent, absent},
		{"the Release's own Aggregate names a type no catalog holds", absent, "example/store", "example/web", absent},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir := filepath.Join(root, "catalog")
			writeCancelFixture(t, filepath.Join(catalogDir, "components", "web.yaml"), moduleRefComponent)
			writeCancelFixture(t, filepath.Join(catalogDir, "aggregates", "store.yaml"), fmt.Sprintf(moduleRefStore, tc.storeType))
			writeCancelFixture(t, filepath.Join(catalogDir, "aggregates", "outer.yaml"), moduleRefOuter)
			writeCancelFixture(t, filepath.Join(root, "aggregate.yaml"), fmt.Sprintf(moduleRefRoot, tc.rootType, tc.module))
			writeCancelFixture(t, filepath.Join(root, "manifest.yaml"), moduleRefManifest)
			releasePath := filepath.Join(root, "release.yaml")
			writeCancelFixture(t, releasePath, moduleRefRelease)

			_, cerr := ResolveValidationClosure(context.Background(), ValidationClosureInputs{
				View:            catalog.NewLazyLookup(root, "", []string{catalogDir}, nil).ForRequest(nil),
				ReleasePath:     releasePath,
				WorkspaceRoot:   root,
				WorkspaceConfig: workspaceconfig.Default(),
			})

			if tc.wantAbsent == "" {
				if cerr != nil {
					t.Fatalf("a Release whose every reference resolves was refused: stage=%d %v", cerr.Stage, cerr)
				}
				return
			}
			if cerr == nil {
				t.Fatalf("the reference to %q resolved nothing and the validation's resolution refused nothing", tc.wantAbsent)
			}
			if errors.Is(cerr, resolver.ErrGuardEntityViewInconsistency()) {
				t.Errorf("refused as a disagreement between the compiler's own views: %v", cerr)
			}
			if !errors.Is(cerr, closure.ErrUnresolvableURI()) {
				t.Errorf("want the unresolvable-reference condition; got %v", cerr)
			}
			var unresolved *closure.UnresolvedEntityError
			if !errors.As(cerr, &unresolved) || unresolved.URI != tc.wantAbsent {
				t.Errorf("the refusal does not carry the reference %q: %v", tc.wantAbsent, cerr)
			}
			if cerr.Stage != ClosureStageComputeClosure {
				t.Errorf("refused at stage %d; want the closure-compute stage %d", cerr.Stage, ClosureStageComputeClosure)
			}
		})
	}
}
