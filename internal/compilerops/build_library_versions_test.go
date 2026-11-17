// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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
			resolved.LibrariesResolved = true

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

// TestValidateReleaseReadsHeldSchemaLibrary: a Release validated through the
// validation closure — resolved without its library axes — reads a SchemaLibrary a
// Component's schema references at the version a bare reference selects, so a
// library the catalog holds is found rather than reported as missing.
func TestValidateReleaseReadsHeldSchemaLibrary(t *testing.T) {
	ws, releasePath := libraryVersionsWorkspace(t)
	for name, body := range map[string]string{
		"catalog/component/demo/app/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      settings:
        $ref: "SettingsLib#/schemas/Settings"
`,
		"catalog/libraries/settings-lib.yaml": `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: SettingsLib
  version: "1.0.0"
spec:
  schemas:
    Settings:
      level: { type: string }
`,
	} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wsCfg := workspaceconfig.Default()
	view := catalog.NewLazyLookup(ws, wsCfg.ResolvedArtifactCacheDir(), []string{filepath.Join(ws, "catalog")}, nil).ForRequest(nil)
	resolved, cerr := ResolveValidationClosure(context.Background(), ValidationClosureInputs{
		View: view, ReleasePath: releasePath, WorkspaceRoot: ws, WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveValidationClosure: stage=%d %v", cerr.Stage, cerr)
	}
	vr := Validate(context.Background(), ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: wsCfg,
		CompilerVersion: "test",
	})
	if len(vr.ClosureExtendsErrors) > 0 {
		t.Errorf("extends gate refused: %v", vr.ClosureExtendsErrors)
	}
	if vr.PillarError != nil {
		t.Errorf("a Component referencing the held SchemaLibrary SettingsLib was refused: %v", vr.PillarError)
	}
	if vr.RunErr != nil {
		t.Errorf("the validation pipeline failed: %v", vr.RunErr)
	}
}

// TestValidateReleaseReadsLockedSchemaLibraryVersion: a Component whose schema
// references Lib:1.0.0's Settings, which Lib 1.1.0 no longer declares, is valid
// for a Release validated with a lockfile recording Lib at 1.0.0, the version a
// build reads. Without a lockfile the validation reads the version a bare
// reference selects, 1.1.0, and refuses the reference as unresolved.
func TestValidateReleaseReadsLockedSchemaLibraryVersion(t *testing.T) {
	files := map[string]string{
		"catalog/component/demo/app/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      settings:
        $ref: "Lib:1.0.0#/schemas/Settings"
`,
	}
	for v, entry := range map[string]string{"1.0.0": "Settings", "1.1.0": "Renamed"} {
		files["catalog/libraries/schema-lib-"+v+".yaml"] = fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Lib
  version: "%s"
spec:
  schemas:
    %s:
      level: { type: string }
`, v, entry)
	}
	locked := &lockfile.Lockfile{SchemaLibraries: []lockfile.SchemaLibrary{{URI: "Lib", ResolvedVersion: "1.0.0"}}}

	for _, tc := range []struct {
		name   string
		record *lockfile.Lockfile
		valid  bool
	}{
		{name: "with a lockfile recording 1.0.0", record: locked, valid: true},
		{name: "without a lockfile", record: nil, valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, releasePath := libraryVersionsWorkspace(t)
			for name, body := range files {
				path := filepath.Join(ws, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			wsCfg := workspaceconfig.Default()
			view := catalog.NewLazyLookup(ws, wsCfg.ResolvedArtifactCacheDir(), []string{filepath.Join(ws, "catalog")}, nil).ForRequest(nil)
			resolved, cerr := ResolveValidationClosure(context.Background(), ValidationClosureInputs{
				View: view, ReleasePath: releasePath, WorkspaceRoot: ws, WorkspaceConfig: wsCfg,
			})
			if cerr != nil {
				t.Fatalf("ResolveValidationClosure: stage=%d %v", cerr.Stage, cerr)
			}
			vr := Validate(context.Background(), ValidateInputs{
				View:                     view,
				Closure:                  resolved.Closure,
				ClosureLibrariesResolved: resolved.LibrariesResolved,
				LibraryRecord:            tc.record,
				Release:                  resolved.Release,
				ReleasePath:              releasePath,
				Manifest:                 resolved.Manifest,
				AggBytes:                 resolved.AggBytes,
				AggregatePath:            resolved.AggregatePath,
				WorkspaceConfig:          wsCfg,
				CompilerVersion:          "test",
			})
			if tc.valid {
				if vr.PillarError != nil {
					t.Errorf("a Component referencing Lib:1.0.0 was refused under a lockfile recording 1.0.0: %v", vr.PillarError)
				}
				return
			}
			if !errors.Is(vr.PillarError, compactform.ErrUnresolvedSchemaLibraryRef()) {
				t.Errorf("without a lockfile the pillar error = %v, want the unresolved SchemaLibrary reference condition", vr.PillarError)
			}
		})
	}
}
