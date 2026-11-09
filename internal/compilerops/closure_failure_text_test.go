// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// failureTextComponent is the one Component the rows below deploy. Its infra pillar
// refers to a SchemaLibrary when the row hands it one, which is a reference only the
// library fold reads: the closure walk resolves the Component without it.
const failureTextComponent = `apiVersion: aggrega.ai/v1beta1
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

const failureTextLibraryRef = `      storage:
        $ref: "Absent#/schemas/Storage"
`

const failureTextAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: failure-text-app
  version: "1.0.0"
spec:
  components:
    web:
      type: %TYPE%
`

const failureTextManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: failure-text-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const failureTextRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: failure-text-release
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
`

// TestAResolutionFailureCarriesTheResolutionsOwnText pins the text a failure to
// resolve a Release's dependencies carries out of ResolveClosure: the resolution's
// own words — what could not be resolved and where it was reached from — with no
// head naming the stage in the code's words. Every surface prints this text: the
// editor behind the stage's public name, a terminal beneath the condition's code,
// a tool beneath its own lead sentence. So a head written here is a head every
// reader meets.
//
// The two failing rows are the two places a resolution fails after the Release, its
// Manifest and its Aggregate have loaded: the closure walk, refusing a type no
// catalog holds, and the library fold, refusing a library no catalog holds. Each row
// names the one reference it breaks and checks the failure names that reference, so
// a row cannot pass on a failure raised somewhere else. The first row is the
// positive control: the same workspace with nothing broken resolves.
func TestAResolutionFailureCarriesTheResolutionsOwnText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		deployType string
		libraryRef bool
		// names is what the failure must name, or empty for a row that resolves.
		names string
	}{
		{name: "a Release whose every reference resolves is not refused",
			deployType: "example/web"},
		{name: "the closure walk refusing a type no catalog holds",
			deployType: "example/absent", names: `"example/absent"`},
		{name: "the library fold refusing a library no catalog holds",
			deployType: "example/web", libraryRef: true, names: `library "Absent"`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir := filepath.Join(root, "catalog")
			component := failureTextComponent
			if tc.libraryRef {
				component += failureTextLibraryRef
			}
			writeCancelFixture(t, filepath.Join(catalogDir, "components", "web.yaml"), component)
			writeCancelFixture(t, filepath.Join(root, "aggregate.yaml"),
				strings.Replace(failureTextAggregate, "%TYPE%", tc.deployType, 1))
			writeCancelFixture(t, filepath.Join(root, "manifest.yaml"), failureTextManifest)
			releasePath := filepath.Join(root, "release.yaml")
			writeCancelFixture(t, releasePath, failureTextRelease)

			_, cerr := ResolveClosure(context.Background(), ClosureInputs{
				View:            catalog.NewLazyLookup(root, "", []string{catalogDir}, nil).ForRequest(nil),
				ReleasePath:     releasePath,
				WorkspaceRoot:   root,
				WorkspaceConfig: workspaceconfig.Default(),
				Augment:         true,
			})

			if tc.names == "" {
				if cerr != nil {
					t.Fatalf("a Release whose every reference resolves was refused: stage=%d %v", cerr.Stage, cerr)
				}
				return
			}
			if cerr == nil {
				t.Fatalf("the reference %s resolved nothing and the resolution refused nothing", tc.names)
			}
			if cerr.Stage != ClosureStageComputeClosure {
				t.Errorf("refused at stage %d; want the closure-compute stage %d", cerr.Stage, ClosureStageComputeClosure)
			}
			if !errors.Is(cerr, closure.ErrUnresolvableURI()) {
				t.Errorf("want the unresolvable-reference condition; got %v", cerr)
			}
			text := cerr.Error()
			if !strings.Contains(text, tc.names) {
				t.Errorf("the failure does not name %s, so it was raised somewhere other than this row breaks:\n%s", tc.names, text)
			}
			if strings.HasPrefix(text, "closure") {
				t.Errorf("the failure opens with a head naming the stage in the code's words:\n%s", text)
			}
			if cause := errors.Unwrap(cerr); cause == nil || text != cause.Error() {
				t.Errorf("the failure's text is not the resolution's own: got %q over a cause reading %v", text, cause)
			}
		})
	}
}
