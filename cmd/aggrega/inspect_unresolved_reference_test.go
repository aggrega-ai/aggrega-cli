// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// unresolvedRefComponent is the one Component the catalog holds.
const unresolvedRefComponent = `apiVersion: aggrega.ai/v1alpha1
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

// unresolvedRefModule is the module the Release's Aggregate imports, with its one
// component's type left to the row.
const unresolvedRefModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: example/store
  version: "1.0.0"
spec:
  components:
    store:
      type: %s
`

// unresolvedRefRoot is the Release's own Aggregate: one component of its own,
// whose type is left to the row, and the module above.
const unresolvedRefRoot = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: unresolved-reference-app
  version: "1.0.0"
spec:
  components:
    web:
      type: %s
  aggregates:
    mod:
      type: example/store
`

const unresolvedRefManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: unresolved-reference-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const unresolvedRefRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: unresolved-reference-release
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

// TestInspectRefusesAReferenceNoCatalogHoldsAsValidationDoes drives `aggrega
// inspect <release>` through the real command tree over a Release whose Aggregate,
// or a module that Aggregate imports, names a component type no catalog holds.
//
// The inspection draws the graph a build of the Release would compile, and the
// checks that compile it are entitled to assume every reference resolved. So the
// inspection resolves the Release's dependencies first, as a validation of the same
// Release does, and refuses an unresolvable reference there: as the unresolvable
// reference it is, naming the type, and never as a disagreement between the
// compiler's own views of the catalog. A type written as a ./ path is refused the
// same way — a type is never read as a file, and the file the row's path names is
// present, so a refusal cannot be the file's absence.
//
// Each refused row asserts the condition through its accessor, the reference the
// refusal carries, and the printed diagnostic, which must be the one `aggrega
// validate --release` prints for the same Release byte for byte. The first row is
// the positive control: the same shape with every reference resolvable is drawn,
// module node included, so a refusal below is the row's fault and not the
// fixture's.
func TestInspectRefusesAReferenceNoCatalogHoldsAsValidationDoes(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr, and
	// the environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const (
		known  = "example/web"
		absent = "example/absent"
		path   = "./components/web.yaml"
	)
	for _, tc := range []struct {
		name       string
		rootType   string
		moduleType string
		// wantRefused is the reference the refusal must carry; "" for the control.
		wantRefused string
	}{
		{"control: every reference resolves", known, known, ""},
		{"the Aggregate's own component names a type no catalog holds", absent, known, absent},
		{"a module's component names a type no catalog holds", known, absent, absent},
		{"the Aggregate's own component names its type by a path", path, known, path},
		{"a module's component names its type by a path", known, path, path},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, releasePath := unresolvedRefWorkspace(t, tc.rootType, tc.moduleType)

			inspectArgs := []string{"--workspace-root", root, "inspect", releasePath}
			validateArgs := []string{"--workspace-root", root, "validate", "--release", releasePath}

			if tc.wantRefused == "" {
				stdout, code, printed := runPrinted(t, inspectArgs)
				if code != 0 {
					t.Fatalf("a Release whose every reference resolves was refused (exit %d):\n%s", code, printed)
				}
				for _, want := range []string{"Component: web", "Component: mod.store"} {
					if !strings.Contains(stdout, want) {
						t.Errorf("the drawn Release lacks %q:\n%s", want, stdout)
					}
				}
				return
			}

			err := runThroughTheCommandTree(context.Background(), inspectArgs)
			if err == nil {
				t.Fatalf("inspect drew a Release naming %q, which no catalog holds", tc.wantRefused)
			}
			if errors.Is(err, resolver.ErrGuardEntityViewInconsistency()) {
				t.Errorf("refused as a disagreement between the compiler's own views: %v", err)
			}
			if !errors.Is(err, closure.ErrUnresolvableURI()) {
				t.Errorf("want the unresolvable-reference condition; got %v", err)
			}
			var unresolved *closure.UnresolvedEntityError
			if !errors.As(err, &unresolved) || unresolved.URI != tc.wantRefused {
				t.Errorf("the refusal does not carry the reference %q: %v", tc.wantRefused, err)
			}

			_, inspectCode, inspectPrinted := runPrinted(t, inspectArgs)
			_, validateCode, validatePrinted := runPrinted(t, validateArgs)
			if inspectCode != 1 || validateCode != 1 {
				t.Errorf("exit codes: inspect %d, validate %d; want 1 from both", inspectCode, validateCode)
			}
			if inspectPrinted != validatePrinted {
				t.Errorf("inspect and validate print different diagnostics for one refusal\n--- inspect ---\n%s\n--- validate ---\n%s",
					inspectPrinted, validatePrinted)
			}
			if !strings.Contains(inspectPrinted, fmt.Sprintf("%q", tc.wantRefused)) {
				t.Errorf("the printed diagnostic does not name the reference %q:\n%s", tc.wantRefused, inspectPrinted)
			}
		})
	}
}

// unresolvedRefWorkspace writes the workspace the rows above and below run over —
// a declared workspace reading the conventional catalog, a Release whose
// Aggregate names rootType and imports one module naming moduleType — and returns
// its root and the Release's path. The file a path-spelled type names is written
// too, so a refusal of that type cannot be the file's absence.
func unresolvedRefWorkspace(t *testing.T, rootType, moduleType string) (root, releasePath string) {
	t.Helper()
	root = t.TempDir()
	writeWorkspaceConfig(t, root, "workspace: {}")
	writeFixture(t, filepath.Join(root, "catalog", "components", "web.yaml"), unresolvedRefComponent)
	writeFixture(t, filepath.Join(root, "catalog", "aggregates", "store.yaml"),
		fmt.Sprintf(unresolvedRefModule, moduleType))
	writeFixture(t, filepath.Join(root, "components", "web.yaml"), unresolvedRefComponent)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), fmt.Sprintf(unresolvedRefRoot, rootType))
	writeFixture(t, filepath.Join(root, "manifest.yaml"), unresolvedRefManifest)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, unresolvedRefRelease)
	return root, releasePath
}

// TestInspectClosureRefusesAModuleReferenceNoCatalogHolds drives `aggrega inspect
// <release> --closure` through the real command tree over a Release whose
// imported module names a component type no catalog holds — as a catalog
// identity, and as a path.
//
// The listing reports what a Release is built from, and a Release one of whose
// modules names nothing the catalog holds is not built from anything, so the
// listing resolves the references the modules make as a validation of the same
// Release does and refuses on the same ground, in the same words. The control row
// is the listing of the same Release with the module's type resolvable, and its
// module is listed, so a refusal below is the row's fault and not the fixture's.
func TestInspectClosureRefusesAModuleReferenceNoCatalogHolds(t *testing.T) {
	// Not parallel, rows included: captureStderr swaps process-global os.Stderr, and
	// the environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const known = "example/web"
	for _, tc := range []struct {
		name       string
		moduleType string
		refused    bool
	}{
		{"control: the module's type is held by the catalog", known, false},
		{"the module names a type no catalog holds", "example/absent", true},
		{"the module names its type by a path", "./components/web.yaml", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, releasePath := unresolvedRefWorkspace(t, known, tc.moduleType)
			closureArgs := []string{"--workspace-root", root, "inspect", releasePath, "--closure"}
			validateArgs := []string{"--workspace-root", root, "validate", "--release", releasePath}

			if !tc.refused {
				stdout, code, printed := runPrinted(t, closureArgs)
				if code != 0 {
					t.Fatalf("the closure of a Release whose every reference resolves was refused (exit %d):\n%s",
						code, printed)
				}
				if !strings.Contains(stdout, "example/store") {
					t.Errorf("the listing does not carry the module the Release imports:\n%s", stdout)
				}
				return
			}

			err := runThroughTheCommandTree(context.Background(), closureArgs)
			if err == nil {
				t.Fatalf("--closure listed a Release whose module names %q, which no catalog holds", tc.moduleType)
			}
			if !errors.Is(err, closure.ErrUnresolvableURI()) {
				t.Errorf("want the unresolvable-reference condition; got %v", err)
			}
			var unresolved *closure.UnresolvedEntityError
			if !errors.As(err, &unresolved) || unresolved.URI != tc.moduleType {
				t.Errorf("the refusal does not carry the reference %q: %v", tc.moduleType, err)
			}

			_, closureCode, closurePrinted := runPrinted(t, closureArgs)
			_, validateCode, validatePrinted := runPrinted(t, validateArgs)
			if closureCode != 1 || validateCode != 1 {
				t.Errorf("exit codes: --closure %d, validate %d; want 1 from both", closureCode, validateCode)
			}
			if closurePrinted != validatePrinted {
				t.Errorf("--closure and validate print different diagnostics for one refusal\n--- closure ---\n%s\n--- validate ---\n%s",
					closurePrinted, validatePrinted)
			}
		})
	}
}

// runPrinted runs the command tree as the binary does and returns what it wrote to
// standard output, its exit code, and everything it printed for a reader on either
// error stream.
func runPrinted(t *testing.T, args []string) (stdout string, code int, printed string) {
	t.Helper()
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	process := captureStderr(t, func() {
		code = executeRoot(context.Background(), cmd)
	})
	return out.String(), code, errOut.String() + process
}
