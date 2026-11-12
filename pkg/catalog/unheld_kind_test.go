// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestHeldKindsAreTheIndexDispatchArms binds heldKinds to the arms of
// indexKind's dispatch over every entity kind and a misspelled one: a kind is
// held exactly when the dispatch reaches an arm for it, which a document of that
// kind shows by being indexed or refused rather than passed over in silence. A
// kind gaining or losing an arm fails here until heldKinds follows, so the list
// the diagnostic names and the kinds a catalog indexes cannot drift apart.
func TestHeldKindsAreTheIndexDispatchArms(t *testing.T) {
	t.Parallel()
	kinds := append(entitykind.Kinds(), entitykind.Kind("Componnent"), entitykind.Kind(""))
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			data := []byte("apiVersion: aggrega.ai/v1beta1\nkind: " + string(k) + "\nmetadata:\n  name: probe/held\n  version: 1.0.0\n")
			c := newPassOneCatalog("")
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("indexKind panicked on a minimal %q document: %v", k, r)
					}
				}()
				err = c.indexKind(string(k), data, "probe.yaml")
			}()
			reached := err != nil || len(c.entityBytes) > 0
			if held := slices.Contains(heldKinds, k); reached != held {
				t.Errorf("kind %q: the dispatch reaches an arm = %v, heldKinds lists it = %v", k, reached, held)
			}
			resident := slices.Contains(residentKinds, k)
			if resident && reached {
				t.Errorf("kind %q is both indexed and listed as a kind a catalog only carries", k)
			}
			if got, want := kindNotHeld(string(k)), !reached && !resident; got != want {
				t.Errorf("kindNotHeld(%q) = %v, want %v", k, got, want)
			}
		})
	}
}

// TestUnloadableDocumentReportsAKindNoCatalogHolds holds the whole-catalog
// checks' one predicate to the unknown-kind condition, one row per shape: a
// misspelled kind, a kind in the wrong case, and no kind at all are each refused
// naming the file and the kind; a held kind, a Workload file a Recipe imports, a
// Manifest, a Release and YAML that is not an Aggrega document are passed over.
// The header a caller decodes into does not change the answer.
func TestUnloadableDocumentReportsAKindNoCatalogHolds(t *testing.T) {
	t.Parallel()
	const path = "catalog/example/datastore/mariadb-probe/component.yaml"
	type lintLikeHeader struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Capability string `yaml:"capability"`
		} `yaml:"spec"`
	}
	for _, tc := range []struct {
		name    string
		data    string
		header  func() any
		refused bool
		written string
	}{
		{
			name:    "a misspelled kind",
			data:    "apiVersion: aggrega.ai/v1beta1\nkind: Componnent\nmetadata:\n  name: example/datastore/mariadb-probe\n",
			refused: true,
			written: `kind "Componnent"`,
		},
		{
			name:    "a misspelled kind read through a caller's own header",
			data:    "apiVersion: aggrega.ai/v1beta1\nkind: Componnent\nmetadata:\n  name: example/datastore/mariadb-probe\n",
			header:  func() any { return &lintLikeHeader{} },
			refused: true,
			written: `kind "Componnent"`,
		},
		{
			name:    "a kind in the wrong case",
			data:    "apiVersion: aggrega.ai/v1beta1\nkind: component\nmetadata:\n  name: probe\n",
			refused: true,
			written: `kind "component"`,
		},
		{
			name: "a Workload file a Recipe imports from its own directory",
			data: "apiVersion: aggrega.ai/v1beta1\nkind: Workload\nform: stateless\ncontainers:\n  app: { image: \"nginx:1.27\" }\n",
		},
		{
			name:    "no kind at all",
			data:    "apiVersion: \"aggrega.ai/v1beta1\"\nmetadata:\n  name: probe\n",
			refused: true,
			written: "no kind",
		},
		{
			name: "a held kind",
			data: "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n",
		},
		{
			name: "a Manifest",
			data: "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: probe\n",
		},
		{
			name: "a Release",
			data: "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe\n",
		},
		{
			name: "a Kubernetes document",
			data: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: probe\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var header any
			if tc.header != nil {
				header = tc.header()
			}
			var got error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("UnloadableDocument panicked: %v", r)
					}
				}()
				got = UnloadableDocument(path, []byte(tc.data), nil, header)
			}()
			if !tc.refused {
				if got != nil {
					t.Fatalf("a document every load reads or passes over by design was refused: %v", got)
				}
				return
			}
			if !errors.Is(got, ErrUnknownCatalogDocumentKind()) {
				t.Fatalf("the answer does not carry the unknown-kind condition: %v", got)
			}
			if errors.Is(got, resolver.ErrYAMLLoad()) {
				t.Errorf("a document that parses and decodes was answered as one that could not be loaded: %v", got)
			}
			var ce *cerrors.CompilerError
			if !errors.As(got, &ce) || ce.Source().File != path {
				t.Errorf("the answer does not name the file %s: %v", path, got)
			}
			text := got.Error()
			if !strings.Contains(text, tc.written) {
				t.Errorf("the answer does not name the kind written (%s): %v", tc.written, got)
			}
			for _, k := range heldKinds {
				if !strings.Contains(text, string(k)) {
					t.Errorf("the answer does not name the held kind %s: %v", k, got)
				}
			}
			if code, ok := cerrors.ShortCodeOf(got); !ok || code != "E0371" {
				t.Errorf("short code = %q, %v; want E0371", code, ok)
			}
		})
	}
}

// TestUnloadableDocumentsHaltCarriesBothConditions holds the whole-catalog halt
// to a walk finding both a document that does not parse and one of a kind no
// catalog holds: one run names both files, and both conditions stay matchable.
func TestUnloadableDocumentsHaltCarriesBothConditions(t *testing.T) {
	t.Parallel()
	found := []error{
		UnloadableDocument("catalog/z.yaml", []byte("apiVersion: aggrega.ai/v1beta1\nkind: Componnent\nmetadata:\n  name: z\n"), nil, nil),
		UnloadableDocument("catalog/a.yaml", []byte("apiVersion: aggrega.ai/v1beta1\nspec: : [\n"), nil, nil),
	}
	halt := UnloadableDocumentsHalt(found)
	if !errors.Is(halt, ErrUnknownCatalogDocumentKind()) || !errors.Is(halt, resolver.ErrYAMLLoad()) {
		t.Fatalf("the halt does not carry both conditions: %v", halt)
	}
	chain := cerrors.CauseChain(halt)
	if a, z := strings.Index(chain, "catalog/a.yaml"), strings.Index(chain, "catalog/z.yaml"); a < 0 || z < 0 || a > z {
		t.Errorf("the halt does not name both files in path order:\n%s", chain)
	}
}

// TestTolerantLoadRecordsAKindNoCatalogHolds drives both catalog walks over one
// directory holding a misspelled-kind Aggrega document beside a held entity, a
// Manifest, a Workload file a Recipe would import, and a chart template. The strict load passes over the misspelled
// document exactly as before; the tolerant load records it, alone, as a file of
// a kind no catalog holds, with the identity it declares; and the catalog each
// walk builds is the same.
func TestTolerantLoadRecordsAKindNoCatalogHolds(t *testing.T) {
	t.Parallel()
	ws, dir, misspelled := unheldKindCatalog(t)

	strict, err := loadAllPassOne(ws, "", dir)
	if err != nil {
		t.Fatalf("the strict load halted on a document it passes over: %v", err)
	}
	tolerant, skipped, err := LoadPassOneTolerant(ws, "", dir)
	if err != nil {
		t.Fatalf("the tolerant load failed: %v", err)
	}
	for name, c := range map[string]*Catalog{"strict": strict, "tolerant": tolerant} {
		if _, ok := c.Components["acc/clean:1.0.0"]; !ok || len(c.Components) != 1 {
			t.Errorf("positive control: the %s load must index the clean Component and nothing else; got %d", name, len(c.Components))
		}
	}
	assertSoleUnheldKindRecord(t, skipped, misspelled)
}

// TestLazyLookupRecordsAKindNoCatalogHolds drives the lazy view — the catalog a
// build that fills its own lockfile, a pull, a Release-scoped validate, the
// assistant tools and the editor read — over the directory the eager test above
// reads, and holds its skip record to the same answer: the misspelled document
// alone, recorded with the unknown-kind condition and the identity it declares.
// The view still resolves the clean Component, so the record is not a symptom
// of a walk that read nothing.
func TestLazyLookupRecordsAKindNoCatalogHolds(t *testing.T) {
	t.Parallel()
	ws, dir, misspelled := unheldKindCatalog(t)
	view := NewLazyLookup(ws, filepath.Join(ws, "cache"), []string{dir}, nil)
	if _, ok := view.Component("acc/clean:1.0.0"); !ok {
		t.Fatal("positive control: the clean Component must resolve through the lazy view")
	}
	if _, ok := view.Component("example/datastore/mariadb-probe:1.0.0"); ok {
		t.Fatal("the misspelled document resolved as a Component; nothing about it may change what resolves")
	}
	if skip, ok := view.Skips()[misspelled]; !ok || !errors.Is(skip, ErrUnknownCatalogDocumentKind()) {
		t.Errorf("the raw record does not hold the misspelled document under the unknown-kind condition: %v, %v", skip, ok)
	}
	assertSoleUnheldKindRecord(t, view.SkippedEntities(), misspelled)
}

// assertSoleUnheldKindRecord holds a skip record to exactly one entry: the
// misspelled document, as a file of a kind no catalog holds, with the identity
// it declares.
func assertSoleUnheldKindRecord(t *testing.T, skipped []SkippedEntity, misspelled string) {
	t.Helper()
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v; want exactly the misspelled document — the Manifest, the Workload and the chart template are passed over", skipped)
	}
	s := skipped[0]
	if !s.KindNotHeld() || !errors.Is(s.Err, ErrUnknownCatalogDocumentKind()) {
		t.Errorf("the record is not a file of a kind no catalog holds: %v", s.Err)
	}
	if s.Path != misspelled || s.Kind != "Componnent" || s.Name != "example/datastore/mariadb-probe" || s.Version != "1.0.0" {
		t.Errorf("the record does not carry the identity the document declares: %+v", s)
	}
}

// unheldKindCatalog writes a catalog directory holding a misspelled-kind Aggrega
// document beside a held Component, a Manifest, a Workload file a Recipe would
// import, and a chart template, and returns the workspace, the directory and the
// misspelled document's path.
func unheldKindCatalog(t *testing.T) (ws, dir, misspelled string) {
	t.Helper()
	ws = t.TempDir()
	dir = filepath.Join(ws, "catalog")
	write := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	misspelled = write("example/datastore/mariadb-probe/1.0.0/component.yaml",
		"apiVersion: aggrega.ai/v1beta1\nkind: Componnent\nmetadata:\n  name: example/datastore/mariadb-probe\n  version: 1.0.0\nspec: {}\n")
	write("acc/clean/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acc/clean
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  schema:
    provides:
      host: { type: string, required: true }
`)
	write("manifests/dev.yaml", "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: dev\n")
	write("recipe/helm/web/1.0.0/workload/workload.yaml",
		"apiVersion: aggrega.ai/v1beta1\nkind: Workload\nform: stateless\ncontainers:\n  app: { image: \"nginx:1.27\" }\n")
	write("charts/web/templates/deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n")
	return ws, dir, misspelled
}

// TestRefusalMatchingKeepsAKindNoCatalogHoldsApart holds the two matchers to
// their split: a file of a kind no catalog holds is never the sole refusal a
// report leads with, whatever its kind and name, and is matched by name and
// version whatever kind hint the reference was collected with.
func TestRefusalMatchingKeepsAKindNoCatalogHoldsApart(t *testing.T) {
	t.Parallel()
	misdeclared := SkippedEntity{
		Path: "catalog/x.yaml", Kind: "Componnent", Name: "example/datastore/mariadb-probe", Version: "1.0.0",
		Err: unheldKindError("catalog/x.yaml", "Componnent"),
	}
	workloadNamed := SkippedEntity{
		Path: "catalog/w.yaml", Kind: "Componentt", Name: "example/datastore/mariadb-probe",
		Err: unheldKindError("catalog/w.yaml", "Componentt"),
	}
	held := SkippedEntity{
		Path: "catalog/h.yaml", Kind: "Component", Name: "example/datastore/mariadb-probe", Version: "1.0.0",
		Err: errors.New("unmarshal Component"),
	}
	if _, ok := SoleRefusalFor([]SkippedEntity{misdeclared, workloadNamed}, "example/datastore/mariadb-probe:1.0.0", "component"); ok {
		t.Error("a file of a kind no catalog holds was singled out as the refusal a report leads with")
	}
	if got, ok := SoleRefusalFor([]SkippedEntity{misdeclared, held}, "example/datastore/mariadb-probe:1.0.0", "component"); !ok || got.Path != held.Path {
		t.Errorf("the held-kind refusal beside it was not singled out: %+v, %v", got, ok)
	}
	for _, tc := range []struct {
		name  string
		uri   string
		hint  string
		paths []string
	}{
		{name: "the name and version, collected as a component", uri: "example/datastore/mariadb-probe:1.0.0", hint: "component", paths: []string{"catalog/x.yaml"}},
		{name: "the bare name, whatever the hint", uri: "example/datastore/mariadb-probe", hint: "feature", paths: []string{"catalog/x.yaml", "catalog/w.yaml"}},
		{name: "a version neither declares", uri: "example/datastore/mariadb-probe:2.0.0", hint: "component"},
		{name: "another name", uri: "example/datastore/postgres:1.0.0", hint: "component"},
		{name: "no reference", uri: "", hint: "component"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			for _, s := range UnheldKindDocumentsFor([]SkippedEntity{misdeclared, held, workloadNamed}, tc.uri, tc.hint) {
				paths = append(paths, s.Path)
			}
			if !slices.Equal(paths, tc.paths) {
				t.Errorf("matched %v; want %v", paths, tc.paths)
			}
		})
	}
}
