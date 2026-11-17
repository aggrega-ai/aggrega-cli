// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// libVerKind selects which library kind a several-versions fixture exercises.
type libVerKind int

const (
	libVerMapping libVerKind = iota
	libVerSchema
)

// libVerWorkspace is one workspace whose catalog directories hold library
// copies side by side. The Recipe (mapping kind) or the Component (schema kind)
// reaches the library through ref, and each copy's body names its own version
// so a render shows which copy was read.
type libVerWorkspace struct {
	t       *testing.T
	root    string
	kind    libVerKind
	release string
}

// libName is the library name the fixture's references use.
func (w *libVerWorkspace) libName() string {
	if w.kind == libVerSchema {
		return "Schemas"
	}
	return "Lib"
}

// newLibVerWorkspace authors the Aggregate, Release, Manifest and the base
// catalog directory (Component, compose Recipe with a local source), and
// declares the workspace's catalog directories: base first, then the directories
// the caller names in lookup order. ref is the library reference as authored.
func newLibVerWorkspace(t *testing.T, kind libVerKind, ref string, libDirs ...string) *libVerWorkspace {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	w := &libVerWorkspace{t: t, root: root, kind: kind, release: filepath.Join(root, "release.yaml")}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: probe-aggregate
  version: "1.0.0"
spec:
  components:
    web:
      type: probe/web
`)
	writeFixture(t, w.release, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: probe-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components:
        web: {}
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: probe-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        probe/web: probe-web
`)
	base := filepath.Join(root, "base")
	writeFixture(t, filepath.Join(base, "recipes", "sources", "web.yaml"), "services:\n  web:\n    image: probe-web:latest\n")
	if kind == libVerMapping {
		writeFixture(t, filepath.Join(base, "components", "web.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: probe/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
		writeFixture(t, filepath.Join(base, "recipes", "web.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe-web
  version: "1.0.0"
spec:
  capability: compose
  implements: probe/web
  source: ./sources/web.yaml
  valuesMapping:
    $import: "%s#/mappings/Env"
`, ref))
	} else {
		writeFixture(t, filepath.Join(base, "components", "web.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: probe/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
    infra:
      probe:
        $ref: "%s#/schemas/Tag"
`, ref))
		writeFixture(t, filepath.Join(base, "recipes", "web.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: probe-web
  version: "1.0.0"
spec:
  capability: compose
  implements: probe/web
  source: ./sources/web.yaml
  valuesMapping: |
    {"services": {"web": {"environment": {"PROBE_BODY": "{{ .infra.probe.tag }}"}}}}
`)
	}
	cfg := "workspace:\n  catalog_dirs:\n    - base\n"
	for _, d := range libDirs {
		cfg += "    - " + d + "\n"
	}
	cfg += "offline: true\n"
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), cfg)
	return w
}

// addLib writes version of the library into dir (relative to the workspace),
// its body carrying the version so a render names the copy it read. It returns
// the file's sha256 digest in the form the lockfile records.
func (w *libVerWorkspace) addLib(dir, version string) string {
	w.t.Helper()
	var body string
	if w.kind == libVerSchema {
		body = fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Schemas
  version: "%[1]s"
spec:
  schemas:
    Tag:
      tag: { type: string, default: "from-%[1]s" }
`, version)
	} else {
		body = fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: Lib
  version: "%[1]s"
spec:
  mappings:
    Env: |
      {"services": {"web": {"environment": {"PROBE_BODY": "from-%[1]s"}}}}
`, version)
	}
	path := filepath.Join(w.root, dir, "lib-"+version+".yaml")
	writeFixture(w.t, path, body)
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// removeLib deletes the copy addLib wrote.
func (w *libVerWorkspace) removeLib(dir, version string) {
	w.t.Helper()
	if err := os.Remove(filepath.Join(w.root, dir, "lib-"+version+".yaml")); err != nil {
		w.t.Fatalf("remove library copy: %v", err)
	}
}

// setRef rewrites the authored library reference.
func (w *libVerWorkspace) setRef(from, to string) {
	w.t.Helper()
	for _, rel := range []string{"base/recipes/web.yaml", "base/components/web.yaml"} {
		p := filepath.Join(w.root, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			w.t.Fatal(err)
		}
		s := strings.Replace(string(b), `"`+from+`#`, `"`+to+`#`, 1)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
}

// pull resolves the Release, pinning its sources, and returns the diagnostics.
func (w *libVerWorkspace) pull() (diag string, err error) {
	w.t.Helper()
	var d, out bytes.Buffer
	pin := true
	ws := resolveTempWorkspace(w.t, tempWorkspaceOptions{Root: w.root})
	// The catalog load announces a shadowed library copy on the process stderr.
	stderr := captureStderr(w.t, func() {
		err = pullRun(context.Background(), pullConfig{
			ws:          ws,
			ReleasePath: w.release,
			PinSources:  &pin,
			DiagOut:     &d,
			Out:         &out,
		})
	})
	return d.String() + stderr, err
}

// build renders the Release from a clean output directory and returns the
// diagnostics and the rendered tree.
func (w *libVerWorkspace) build() (diag, tree string, err error) {
	w.t.Helper()
	dist := filepath.Join(w.root, "dist")
	if rerr := os.RemoveAll(dist); rerr != nil {
		w.t.Fatal(rerr)
	}
	var d, out bytes.Buffer
	ws := resolveTempWorkspace(w.t, tempWorkspaceOptions{Root: w.root})
	stderr := captureStderr(w.t, func() {
		err = dispatchBuild(context.Background(), ws, w.release, nil, false, &d, &out)
	})
	return d.String() + stderr, readDistTree(w.t, dist), err
}

// mustPull is pull failing the test on error.
func (w *libVerWorkspace) mustPull() string {
	w.t.Helper()
	diag, err := w.pull()
	if err != nil {
		w.t.Fatalf("pull: %v\ndiagnostics:\n%s", err, diag)
	}
	return diag
}

// mustBuild is build failing the test on error.
func (w *libVerWorkspace) mustBuild() (diag, tree string) {
	w.t.Helper()
	diag, tree, err := w.build()
	if err != nil {
		w.t.Fatalf("build: %v\ndiagnostics:\n%s", err, diag)
	}
	return diag, tree
}

// libRow returns the lock row of the fixture's library: its resolved version and
// digest. The test fails when the lock records no row or more than one.
func (w *libVerWorkspace) libRow() (version, digest string) {
	w.t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(w.release))
	if err != nil {
		w.t.Fatalf("load lockfile: %v", err)
	}
	var rows []lockfile.LibraryPin
	if w.kind == libVerSchema {
		for _, r := range lf.SchemaLibraries {
			rows = append(rows, lockfile.LibraryPin{URI: r.URI, ResolvedVersion: r.ResolvedVersion, Digest: r.Digest})
		}
	} else {
		for _, r := range lf.MappingLibraries {
			rows = append(rows, lockfile.LibraryPin{URI: r.URI, ResolvedVersion: r.ResolvedVersion, Digest: r.Digest})
		}
	}
	if len(rows) != 1 || rows[0].URI != w.libName() {
		w.t.Fatalf("lock rows = %+v, want exactly one row for %s", rows, w.libName())
	}
	return rows[0].ResolvedVersion, rows[0].Digest
}

// wantLibBody asserts tree carries the body of version and none of the others.
func wantLibBody(t *testing.T, tree, version string, others ...string) {
	t.Helper()
	if !strings.Contains(tree, "from-"+version) {
		t.Errorf("rendered output lacks the body of %s:\n%s", version, tree)
	}
	for _, o := range others {
		if strings.Contains(tree, "from-"+o) {
			t.Errorf("rendered output carries the body of %s, which was not selected:\n%s", o, tree)
		}
	}
}

// TestLibraryVersionsSeveralHeldMapping proves, through pull, build and validate,
// that a catalog holding several versions of one library gives each Release
// exactly the version its reference selects, and that the lock records that
// copy's own version and content digest.
func TestLibraryVersionsSeveralHeldMapping(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib", "libs")
		w.addLib("libs", "1.0.0")
		d11 := w.addLib("libs", "1.1.0")
		diag := w.mustPull()
		if v, d := w.libRow(); v != "1.1.0" || d != d11 {
			t.Fatalf("lock row = (%s,%s), want (1.1.0,%s)", v, d, d11)
		}
		bdiag, tree := w.mustBuild()
		wantLibBody(t, tree, "1.1.0", "1.0.0")
		if strings.Contains(diag+bdiag, "shadowed") {
			t.Errorf("two versions in one directory are not a shadow; diagnostics:\n%s%s", diag, bdiag)
		}
	})

	t.Run("floor-low", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib:1.0.0", "libs")
		d10 := w.addLib("libs", "1.0.0")
		w.addLib("libs", "1.1.0")
		w.mustPull()
		if v, d := w.libRow(); v != "1.0.0" || d != d10 {
			t.Fatalf("lock row = (%s,%s), want (1.0.0,%s)", v, d, d10)
		}
		_, tree := w.mustBuild()
		wantLibBody(t, tree, "1.0.0", "1.1.0")
	})

	t.Run("floor-raised", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib:1.0.0", "libs")
		d10 := w.addLib("libs", "1.0.0")
		d11 := w.addLib("libs", "1.1.0")
		w.mustPull()
		w.setRef("Lib:1.0.0", "Lib:1.1.0")
		_, _, err := w.build()
		if err == nil {
			t.Fatal("build must halt on a lock recording a copy the raised floor no longer selects")
		}
		want := fmt.Sprintf("lockfile=(1.0.0,%s) current=(1.1.0,%s)", d10, d11)
		if !errors.Is(err, lockfile.ErrLockfileDrift()) || !strings.Contains(err.Error(), want) {
			t.Fatalf("build error = %v, want the lockfile-drift refusal containing %q", err, want)
		}
		w.mustPull()
		if v, d := w.libRow(); v != "1.1.0" || d != d11 {
			t.Fatalf("lock row after pull = (%s,%s), want (1.1.0,%s)", v, d, d11)
		}
		_, tree := w.mustBuild()
		wantLibBody(t, tree, "1.1.0", "1.0.0")
	})

	t.Run("floor-unheld", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib:1.1.0", "libs")
		w.addLib("libs", "1.0.0")
		_, err := w.pull()
		if err == nil || !errorsIsUnsatisfiable(err) {
			t.Fatalf("pull error = %v, want the unsatisfiable-version refusal", err)
		}
		if !strings.Contains(err.Error(), `"Lib:1.1.0"`) || !strings.Contains(err.Error(), "Lib:1.0.0") {
			t.Errorf("refusal must name the floor and the held versions, got: %v", err)
		}
	})

	t.Run("two-directories", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib", "libs-a", "libs-b")
		d10 := w.addLib("libs-a", "1.0.0")
		w.addLib("libs-b", "1.1.0")
		diag := w.mustPull()
		if v, d := w.libRow(); v != "1.0.0" || d != d10 {
			t.Fatalf("lock row = (%s,%s), want (1.0.0,%s): the first directory holding the name owns it", v, d, d10)
		}
		if !strings.Contains(diag, "lib-1.1.0.yaml") || !strings.Contains(diag, "shadowed") {
			t.Errorf("the later directory's copy must be announced as shadowed; diagnostics:\n%s", diag)
		}
		bdiag, tree := w.mustBuild()
		wantLibBody(t, tree, "1.0.0", "1.1.0")
		if !strings.Contains(bdiag, "lib-1.1.0.yaml") || !strings.Contains(bdiag, "shadowed") {
			t.Errorf("build must announce the shadowed copy; diagnostics:\n%s", bdiag)
		}
	})

	t.Run("validate-catalog", func(t *testing.T) {
		w := newLibVerWorkspace(t, libVerMapping, "Lib", "libs")
		w.addLib("libs", "1.0.0")
		w.addLib("libs", "1.1.0")
		var err error
		stderr := captureStderr(t, func() {
			err = validateCatalogWideFromWorkspace(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: w.root}), nil)
		})
		if err != nil {
			t.Fatalf("whole-catalog validate with two versions of Lib in one directory: %v\n%s", err, stderr)
		}
	})
}

// TestLibraryVersionsTwoDirectoriesSchemaLibrary is the two-directories case for
// a SchemaLibrary reached by a Component's $ref: the first directory's copy
// renders and the later copy is announced as shadowed.
func TestLibraryVersionsTwoDirectoriesSchemaLibrary(t *testing.T) {
	w := newLibVerWorkspace(t, libVerSchema, "Schemas", "libs-a", "libs-b")
	d10 := w.addLib("libs-a", "1.0.0")
	w.addLib("libs-b", "1.1.0")
	diag := w.mustPull()
	if v, d := w.libRow(); v != "1.0.0" || d != d10 {
		t.Fatalf("lock row = (%s,%s), want (1.0.0,%s)", v, d, d10)
	}
	if !strings.Contains(diag, "lib-1.1.0.yaml") || !strings.Contains(diag, "shadowed") {
		t.Errorf("the later directory's copy must be announced as shadowed; diagnostics:\n%s", diag)
	}
	_, tree := w.mustBuild()
	wantLibBody(t, tree, "1.0.0", "1.1.0")
}

// TestLibraryVersionsOldLockRelabelledByPull proves a lock row recording a
// version the held copy does not carry (1.1.0, beside 1.0.0's digest) halts the
// build, and that pull rewrites the row to the copy's own version.
func TestLibraryVersionsOldLockRelabelledByPull(t *testing.T) {
	w := newLibVerWorkspace(t, libVerMapping, "Lib:1.0.0", "libs")
	d10 := w.addLib("libs", "1.0.0")
	w.mustPull()
	lockPath := lockfile.PathFor(w.release)
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	row := "uri: Lib\n      resolved_version: 1.0.0\n"
	if !strings.Contains(string(raw), row) {
		t.Fatalf("lock has no Lib row at 1.0.0:\n%s", raw)
	}
	relabelled := strings.Replace(string(raw), row, "uri: Lib\n      resolved_version: 1.1.0\n", 1)
	if err := os.WriteFile(lockPath, []byte(relabelled), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = w.build()
	if err == nil {
		t.Fatal("build must halt on a lock row whose version the held copy does not carry")
	}
	want := fmt.Sprintf("lockfile=(1.1.0,%s) current=(1.0.0,%s)", d10, d10)
	if !errors.Is(err, lockfile.ErrLockfileDrift()) || !strings.Contains(err.Error(), want) {
		t.Fatalf("build error = %v, want the lockfile-drift refusal containing %q", err, want)
	}
	w.mustPull()
	if v, d := w.libRow(); v != "1.0.0" || d != d10 {
		t.Fatalf("lock row after pull = (%s,%s), want (1.0.0,%s)", v, d, d10)
	}
	_, tree := w.mustBuild()
	wantLibBody(t, tree, "1.0.0")
}

// errorsIsUnsatisfiable reports whether err is the refusal for a floor no held
// version satisfies.
func errorsIsUnsatisfiable(err error) bool {
	return errors.Is(err, catalog.ErrUnsatisfiableEntityVersion())
}
