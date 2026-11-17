// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops/compileropstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// libraryReferenceWorkspace is a workspace whose one Component, demo/app, takes
// its infra settings from a SchemaLibrary entry, mapped by its Manifest to a
// compose Recipe. Each state below rewrites the Component's reference and the
// SchemaLibrary versions the catalog holds.
type libraryReferenceWorkspace struct {
	root, releasePath, aggregatePath, catalogDir string
}

func newLibraryReferenceWorkspace(t *testing.T) libraryReferenceWorkspace {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
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
		"catalog/recipe/compose/app/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: app
  version: "1.0.0"
spec:
  capability: compose
  implements: demo/app
  source: ./sources/app.yaml
`,
		"catalog/recipe/compose/app/1.0.0/sources/app.yaml": "services:\n  app:\n    image: app:latest\n",
	} {
		writeServeFile(t, filepath.Join(root, name), body)
	}
	return libraryReferenceWorkspace{
		root:          root,
		releasePath:   filepath.Join(root, "release.yaml"),
		aggregatePath: filepath.Join(root, "aggregate.yaml"),
		catalogDir:    filepath.Join(root, "catalog"),
	}
}

// reference makes the Component's infra settings the SchemaLibrary entry ref names.
func (w libraryReferenceWorkspace) reference(t *testing.T, ref string) {
	t.Helper()
	writeServeFile(t, filepath.Join(w.catalogDir, "component/demo/app/1.0.0/component.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
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
        $ref: %q
`, ref))
}

// holdLibrary has the catalog hold SchemaLibrary Lib at version, declaring entries.
func (w libraryReferenceWorkspace) holdLibrary(t *testing.T, version string, entries ...string) {
	t.Helper()
	var schemas strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&schemas, "    %s:\n      level: { type: string }\n", entry)
	}
	writeServeFile(t, filepath.Join(w.catalogDir, "libraries/lib-"+version+".yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Lib
  version: %q
spec:
  schemas:
%s`, version, schemas.String()))
}

// lock writes beside the Release the lockfile a resolution of the workspace as it
// stands records, through a lookup of its own so nothing the server reads later
// was indexed before the workspace changed.
func (w libraryReferenceWorkspace) lock(t *testing.T) {
	t.Helper()
	viewFor := func(lf *lockfile.Lockfile) *catalog.LazyLookup {
		pins, err := catalog.PinsFromLockfile(lf)
		if err != nil {
			t.Fatalf("pin the fixture view to its lockfile: %v", err)
		}
		return catalog.NewLazyLookup(w.root, "", []string{w.catalogDir}, nil).ForRequest(pins)
	}
	compileropstest.WriteMatchingLockfile(t, viewFor, w.releasePath, w.root, w.config(), nil)
}

func (w libraryReferenceWorkspace) config() workspaceconfig.Config {
	cfg := workspaceconfig.Default()
	cfg.Workspace.CatalogDirs = []string{w.catalogDir}
	return cfg
}

// server is a daemon over the workspace as it stands, which counts the times a
// validation holds the Release to its lockfile into holds.
func (w libraryReferenceWorkspace) server(t *testing.T, holds *int) *Server {
	t.Helper()
	srv := newTestServer(t, w.root, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{w.catalogDir}
	srv.lazyCatalog = catalog.NewLazyLookup(w.root, "", []string{w.catalogDir}, nil)
	srv.testLockfileHoldHook = func() { *holds++ }
	return srv
}

// validateAggregate runs the Tier-4 validation of the workspace's Aggregate.
func (w libraryReferenceWorkspace) validateAggregate(t *testing.T, srv *Server) []Diagnostic {
	t.Helper()
	content, err := os.ReadFile(w.aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	return srv.runTier4Validation(context.Background(), w.releasePath, w.aggregatePath, content, parseYAML(t, string(content)))
}

// lockedValidation is the validation core's own verdict on the workspace as it
// stands, reading each library at the version its lockfile records, as the
// language server reads it.
func (w libraryReferenceWorkspace) lockedValidation(t *testing.T) *compilerops.ValidateResult {
	t.Helper()
	lf, _, err := lockfile.Load(w.root, lockfile.PathFor(w.releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	pins, err := catalog.PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("pin the oracle view: %v", err)
	}
	cfg := w.config()
	view := catalog.NewLazyLookup(w.root, "", []string{w.catalogDir}, nil).ForRequest(pins)
	resolved, cerr := compilerops.ResolveValidationClosure(context.Background(), compilerops.ValidationClosureInputs{
		View: view, ReleasePath: w.releasePath, WorkspaceRoot: w.root, WorkspaceConfig: cfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveValidationClosure: stage=%d %v", cerr.Stage, cerr)
	}
	return compilerops.Validate(context.Background(), compilerops.ValidateInputs{
		View:                     view,
		Closure:                  resolved.Closure,
		ClosureLibrariesResolved: resolved.LibrariesResolved,
		LibraryRecord:            lf,
		Release:                  resolved.Release,
		ReleasePath:              w.releasePath,
		Manifest:                 resolved.Manifest,
		AggBytes:                 resolved.AggBytes,
		AggregatePath:            resolved.AggregatePath,
		WorkspaceRoot:            w.root,
		WorkspaceConfig:          cfg,
		CompilerVersion:          "test",
	})
}

// diagnosticsWithCode returns the diagnostics carrying the condition code.
func diagnosticsWithCode(diags []Diagnostic, code string) []Diagnostic {
	var out []Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

// TestTier4ValidationReportsAStaleLockfileForARaisedLibraryFloor: a Component
// that raises its SchemaLibrary floor to use an entry only the newer version
// declares, under a lockfile still recording the older version, is answered on the
// Aggregate with the lockfile-drift condition and its remedy, naming the Release
// relative to the workspace root — what the command line reports for the same
// workspace — and not with the reference as unresolved, which the validation core
// reports when it reads the library at the recorded version.
func TestTier4ValidationReportsAStaleLockfileForARaisedLibraryFloor(t *testing.T) {
	t.Parallel()
	driftCode := conditionCodesOf(lockfile.ErrLockfileDrift()).Code
	unresolvedCode := conditionCodesOf(compactform.ErrUnresolvedSchemaLibraryRef()).Code
	if driftCode == "" || unresolvedCode == "" {
		t.Fatalf("a condition carries no code (drift %q, unresolved reference %q), so the assertions would be vacuous", driftCode, unresolvedCode)
	}

	w := newLibraryReferenceWorkspace(t)
	w.holdLibrary(t, "1.0.0", "Settings")
	w.reference(t, "Lib:1.0.0#/schemas/Settings")
	w.lock(t)
	w.holdLibrary(t, "1.1.0", "Settings", "Settings2")
	w.reference(t, "Lib:1.1.0#/schemas/Settings2")

	if vr := w.lockedValidation(t); !errors.Is(vr.PillarError, compactform.ErrUnresolvedSchemaLibraryRef()) {
		t.Fatalf("read at the recorded Lib 1.0.0 the reference is not refused as unresolved (pillar error %v), so "+
			"the workspace no longer holds the refusal a stale lockfile explains", vr.PillarError)
	}

	var holds int
	diags := w.validateAggregate(t, w.server(t, &holds))

	if got := diagnosticsWithCode(diags, unresolvedCode); len(got) != 0 {
		t.Errorf("the reference is reported unresolved though the catalog holds it at the raised floor: %v", diagMessages(got))
	}
	drift := diagnosticsWithCode(diags, driftCode)
	if len(drift) != 1 {
		t.Fatalf("want one lockfile-drift diagnostic; got %d among %v", len(drift), diagMessages(diags))
	}
	if holds != 1 {
		t.Errorf("the validation held the Release to its lockfile %d times; want once", holds)
	}
	d := drift[0]
	if d.Severity != editorSeverityError {
		t.Errorf("Severity = %q; want %q", d.Severity, editorSeverityError)
	}
	if remedy := compilerops.LockfileDriftRemedy("release.yaml"); !strings.Contains(d.Message, remedy) {
		t.Errorf("the drift diagnostic does not carry the remedy %q: %q", remedy, d.Message)
	}
	if !strings.Contains(d.Message, `schema-library "Lib"`) {
		t.Errorf("the drift diagnostic does not name the library whose recorded version moved: %q", d.Message)
	}
}

// TestTier4ValidationKeepsAnUnresolvedLibraryReferenceTheLockfileDoesNotExplain:
// a SchemaLibrary reference no version the catalog holds satisfies is reported as
// unresolved, and the Release is not held to its lockfile: the reference fails at
// the version a bare reference selects too, so the recorded versions do not
// explain it.
func TestTier4ValidationKeepsAnUnresolvedLibraryReferenceTheLockfileDoesNotExplain(t *testing.T) {
	t.Parallel()
	driftCode := conditionCodesOf(lockfile.ErrLockfileDrift()).Code
	unresolvedCode := conditionCodesOf(compactform.ErrUnresolvedSchemaLibraryRef()).Code
	if driftCode == "" || unresolvedCode == "" {
		t.Fatalf("a condition carries no code (drift %q, unresolved reference %q), so the assertions would be vacuous", driftCode, unresolvedCode)
	}

	for _, tc := range []struct {
		name  string
		setUp func(t *testing.T, w libraryReferenceWorkspace)
	}{
		{
			name: "an entry no held version declares, under a matching lockfile",
			setUp: func(t *testing.T, w libraryReferenceWorkspace) {
				w.holdLibrary(t, "1.0.0", "Settings")
				w.reference(t, "Lib:1.0.0#/schemas/Absent")
				w.lock(t)
			},
		},
		{
			name: "a library no catalog holds",
			setUp: func(t *testing.T, w libraryReferenceWorkspace) {
				w.holdLibrary(t, "1.0.0", "Settings")
				w.reference(t, "Lib:1.0.0#/schemas/Settings")
				w.lock(t)
				w.reference(t, "Gone:1.0.0#/schemas/Settings")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newLibraryReferenceWorkspace(t)
			tc.setUp(t, w)

			var holds int
			diags := w.validateAggregate(t, w.server(t, &holds))

			if holds != 0 {
				t.Errorf("the validation held the Release to its lockfile %d times; want none", holds)
			}
			if got := diagnosticsWithCode(diags, driftCode); len(got) != 0 {
				t.Errorf("a lockfile-drift diagnostic was reported: %v", diagMessages(got))
			}
			if got := diagnosticsWithCode(diags, unresolvedCode); len(got) != 1 {
				t.Errorf("want one unresolved SchemaLibrary reference diagnostic; got %d among %v", len(got), diagMessages(diags))
			}
		})
	}
}

// TestTier4ValidationKeepsAMisspeltEntryOverAStaleLockfile: an entry name
// misspelt in a Component edited after the pull is reported as unresolved, not as
// the lockfile being out of date, though the edit has made it so: the reference
// fails at the version a bare reference selects too, and a pull would leave it as
// wrong as it is. The Release is not held to its lockfile.
func TestTier4ValidationKeepsAMisspeltEntryOverAStaleLockfile(t *testing.T) {
	t.Parallel()
	driftCode := conditionCodesOf(lockfile.ErrLockfileDrift()).Code
	unresolvedCode := conditionCodesOf(compactform.ErrUnresolvedSchemaLibraryRef()).Code
	if driftCode == "" || unresolvedCode == "" {
		t.Fatalf("a condition carries no code (drift %q, unresolved reference %q), so the assertions would be vacuous", driftCode, unresolvedCode)
	}

	w := newLibraryReferenceWorkspace(t)
	w.holdLibrary(t, "1.0.0", "Settings")
	w.reference(t, "Lib:1.0.0#/schemas/Settings")
	w.lock(t)
	w.reference(t, "Lib:1.0.0#/schemas/Setings")

	lf, _, err := lockfile.Load(w.root, lockfile.PathFor(w.releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	pins, err := catalog.PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("pin the oracle view: %v", err)
	}
	oracleView := catalog.NewLazyLookup(w.root, "", []string{w.catalogDir}, nil).ForRequest(pins)
	if err := compilerops.HoldReleaseToLockfile(context.Background(), oracleView, w.releasePath, w.root, w.config(), lf); !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("the edit left the lockfile matching (drift check: %v), so the case shows nothing about a stale one", err)
	}
	if vr := w.lockedValidation(t); !errors.Is(vr.PillarError, compactform.ErrUnresolvedSchemaLibraryRef()) {
		t.Fatalf("the misspelt entry is not refused as unresolved (pillar error %v)", vr.PillarError)
	}

	var holds int
	diags := w.validateAggregate(t, w.server(t, &holds))

	if got := diagnosticsWithCode(diags, unresolvedCode); len(got) != 1 {
		t.Errorf("want one unresolved SchemaLibrary reference diagnostic; got %d among %v", len(got), diagMessages(diags))
	}
	if got := diagnosticsWithCode(diags, driftCode); len(got) != 0 {
		t.Errorf("the misspelt entry was answered with lockfile drift: %v", diagMessages(got))
	}
	if holds != 0 {
		t.Errorf("the validation held the Release to its lockfile %d times; want none", holds)
	}
}

// TestTier4ValidationThatPassesHoldsNoLockfile: a validation no gate refuses
// answers without holding the Release to its lockfile, so an edit that passes
// costs no drift check.
func TestTier4ValidationThatPassesHoldsNoLockfile(t *testing.T) {
	t.Parallel()
	w := newLibraryReferenceWorkspace(t)
	w.holdLibrary(t, "1.0.0", "Settings")
	w.reference(t, "Lib:1.0.0#/schemas/Settings")
	w.lock(t)

	var holds int
	diags := w.validateAggregate(t, w.server(t, &holds))

	if len(diags) != 0 {
		t.Errorf("a workspace its lockfile matches was answered with diagnostics: %v", diagMessages(diags))
	}
	if holds != 0 {
		t.Errorf("a validation that passed held the Release to its lockfile %d times; want none", holds)
	}
}
