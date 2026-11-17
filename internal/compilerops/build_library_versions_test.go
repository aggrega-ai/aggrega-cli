// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// libraryVersionsWorkspace lays out a workspace whose one compose Recipe imports
// its values from the MappingLibrary Lib, which the catalog holds at 1.0.0 and
// 1.1.0 with a different entry body in each.
func libraryVersionsWorkspace(t *testing.T) (ws, releasePath string) {
	t.Helper()
	ws = t.TempDir()
	files := map[string]string{
		"release.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app-b:
      directory: ./dist/app
      components:
        app: {}
`,
		"aggregate.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: demo/app
`,
		"manifest.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/app:1.0.0: compose/app:1.0.0
`,
		"catalog/component/demo/app/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`,
		"catalog/recipe/compose/app/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: app
  version: "1.0.0"
spec:
  capability: compose
  implements: demo/app
  source: ./sources/app.yaml
  valuesMapping:
    $import: "Lib#/mappings/Values"
`,
		"catalog/recipe/compose/app/1.0.0/sources/app.yaml": "services:\n  app:\n    image: app:latest\n",
	}
	for _, v := range []string{"1.0.0", "1.1.0"} {
		files["catalog/libraries/lib-"+v+".yaml"] = fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: Lib
  version: "%s"
spec:
  mappings:
    Values: |
      {"services": {"app": {"environment": {"LIBRARY_BODY": "body-of-%s"}}}}
`, v, v)
	}
	for name, body := range files {
		path := filepath.Join(ws, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws, filepath.Join(ws, "release.yaml")
}

// TestBuildToBufferRendersClosureLibraryVersion: a build reads a library at the
// version its Release's closure resolved. With Lib held at 1.0.0 and 1.1.0 and the
// closure resolved at 1.0.0 the artifact carries 1.0.0's entry body, and resolved
// at 1.1.0 it carries 1.1.0's — whichever version a bare reference would select.
func TestBuildToBufferRendersClosureLibraryVersion(t *testing.T) {
	for _, resolvedVersion := range []string{"1.0.0", "1.1.0"} {
		other := map[string]string{"1.0.0": "1.1.0", "1.1.0": "1.0.0"}[resolvedVersion]
		t.Run(resolvedVersion, func(t *testing.T) {
			ws, releasePath := libraryVersionsWorkspace(t)
			wsCfg := workspaceconfig.Default()
			view := catalog.NewLazyLookup(ws, wsCfg.ResolvedArtifactCacheDir(), []string{filepath.Join(ws, "catalog")}, nil).ForRequest(nil)
			resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
				View: view, ReleasePath: releasePath, WorkspaceRoot: ws, WorkspaceConfig: wsCfg,
			})
			if cerr != nil {
				t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
			}
			// The closure a pull records names each library at the version it
			// resolved; ResolveClosure alone discovers no library, so the record
			// under test is written here.
			resolved.Closure.MappingLibraries = []closure.ResolvedLibrary{{URI: "Lib", ResolvedVersion: resolvedVersion}}

			res, err := BuildToBuffer(context.Background(), BuildInputs{
				View:                             view,
				Resolved:                         resolved,
				ReleasePath:                      releasePath,
				WorkspaceConfig:                  wsCfg,
				WorkspaceRoot:                    ws,
				CompilerVersion:                  "test",
				TolerateCatalogViewInconsistency: true,
			}, pipeline.Options{})
			if err != nil {
				t.Fatalf("BuildToBuffer: %v", err)
			}
			completed := res.Completed()
			if completed == nil {
				t.Fatalf("the build did not complete: %+v", res)
			}
			var joined []byte
			for _, b := range completed.Artifacts {
				joined = append(joined, b...)
			}
			if !bytes.Contains(joined, []byte("body-of-"+resolvedVersion)) {
				t.Errorf("the artifact does not carry Lib %s's body:\n%s", resolvedVersion, joined)
			}
			if bytes.Contains(joined, []byte("body-of-"+other)) {
				t.Errorf("the artifact carries Lib %s's body though the closure resolved %s", other, resolvedVersion)
			}
		})
	}
}
