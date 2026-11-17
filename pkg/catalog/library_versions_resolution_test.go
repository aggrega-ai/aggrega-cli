// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// The proofs in this file cover how a library reference is answered once a catalog
// holds several versions of one library name: the catalog tier of a dependency
// resolution, the condition a floor no held version satisfies is reported with,
// the discovery that descends a resolved library's own references, and a read of
// one library by reference. Every proof runs once per library kind, and the
// resolution proofs once per catalog backing, because the four kinds and the two
// backings share one selection rule.

// resolutionCacheDir is the content cache the resolution proofs name: present in
// no workspace they build, so no reference is answered from it.
const resolutionCacheDir = ".aggrega/cache"

// libraryEntry is the one entry each kind's document in libraryKindCases declares.
var libraryEntry = map[string]string{
	"SchemaLibrary":   "Settings",
	"MappingLibrary":  "Values",
	"WorkloadLibrary": "Deployment",
	"ArtifactLibrary": "WidgetCRD",
}

// axisOfKind is the library axis whose members publish under kind.
func axisOfKind(t *testing.T, kind string) closure.LibraryAxis {
	t.Helper()
	for _, axis := range closure.LibraryAxes() {
		if axis.Kind == kind {
			return axis
		}
	}
	t.Fatalf("no library axis publishes under %s", kind)
	return closure.LibraryAxis{}
}

// fileDigest is the sha256 digest of a library file's bytes, computed here rather
// than through the catalog, so a resolution digesting another copy cannot agree
// with it by sharing its code.
func fileDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// heldVersionsCatalog writes one document of kind k per version into a catalog
// directory of a fresh workspace, and returns the workspace root and the directory.
func heldVersionsCatalog(t *testing.T, k libraryKindCase, versions ...string) (string, string) {
	t.Helper()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	for _, v := range versions {
		writeFixture(t, dir, "lib-"+v+".yaml", k.doc(v))
	}
	return ws, dir
}

// resolutionBackings returns the dependency-resolution lookup of each catalog
// backing over the catalog directory dir, with no registry to reach.
func resolutionBackings(t *testing.T, ws, dir string) map[string]closure.Lookup {
	t.Helper()
	eager, err := LoadAllWithNamedDirs(ws, "", nil, dir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	return map[string]closure.Lookup{
		"eager": eager.ClosureLookupWithReuse(nil, ws, resolutionCacheDir, nil, nil, nil),
		"lazy":  NewLazyLookup(ws, resolutionCacheDir, []string{dir}, nil).ClosureLookup(nil, ws, resolutionCacheDir, nil, nil, nil),
	}
}

// discoverReference runs the dependency resolution of axis over one consumer
// referencing ref, descending no library body.
func discoverReference(t *testing.T, cl *closure.Closure, axis closure.LibraryAxis, lookup closure.Lookup, ref string) error {
	t.Helper()
	ctx := context.Background()
	if axis.Kind == "SchemaLibrary" {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(fmt.Sprintf("$ref: %q\n", ref)), &node); err != nil {
			t.Fatalf("consumer schema: %v", err)
		}
		return cl.DiscoverSchemaLibraries(ctx, lookup,
			[]closure.SchemaConsumer{{URI: "core/consumer", Kind: closure.KindComponent, Schema: &node}},
			func(closure.ResolvedLibrary) ([]*yaml.Node, bool, error) { return nil, false, nil })
	}
	return cl.DiscoverTemplateLibraries(ctx, axis, lookup,
		[]closure.TemplateConsumer{{URI: "consumer", Kind: closure.KindRecipe, Reference: ref}},
		func(closure.ResolvedLibrary) ([]string, bool, error) { return nil, false, nil })
}

// TestTier1LibraryReportsSelectedCopy: the catalog tier answers a library
// reference with the copy the selection picks among the versions it holds, and
// reports that copy's own version and the digest of that copy's own bytes — never
// the version the reference asked for beside another copy's digest.
func TestTier1LibraryReportsSelectedCopy(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		held []string
		ref  string
		want string
	}{
		{name: "a floor naming the lower held version", held: []string{"1.0.0", "1.1.0"}, ref: "Lib:1.0.0", want: "1.0.0"},
		{name: "a floor naming the higher held version", held: []string{"1.0.0", "1.1.0"}, ref: "Lib:1.1.0", want: "1.1.0"},
		{name: "a bare reference", held: []string{"1.0.0", "1.1.0"}, ref: "Lib", want: "1.1.0"},
		{name: "a floor between two held versions", held: []string{"1.0.0", "1.2.0"}, ref: "Lib:1.1.0", want: "1.2.0"},
	}
	for _, k := range libraryKindCases() {
		axis := axisOfKind(t, k.kind)
		for _, row := range rows {
			t.Run(k.kind+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				ws, dir := heldVersionsCatalog(t, k, row.held...)
				for backing, lookup := range resolutionBackings(t, ws, dir) {
					res, err := lookup.ResolveLibrary(context.Background(), axis, row.ref)
					if err != nil {
						t.Fatalf("%s: ResolveLibrary(%q): %v", backing, row.ref, err)
					}
					if res.NotFound {
						t.Fatalf("%s: ResolveLibrary(%q) found nothing; the catalog holds %v", backing, row.ref, row.held)
					}
					if res.ResolvedVersion != row.want {
						t.Errorf("%s: ResolveLibrary(%q) reports version %q; want %q", backing, row.ref, res.ResolvedVersion, row.want)
					}
					if want := fileDigest(k.doc(row.want)); res.Digest != want {
						t.Errorf("%s: ResolveLibrary(%q) reports digest %s; want %s, the digest of the %s file's bytes",
							backing, row.ref, res.Digest, want, row.want)
					}
				}
			})
		}
	}
}

// TestLibraryFloorAboveHeldVersions: a library reference whose floor no version
// the catalog holds satisfies, with no content cache entry and no registry to ask,
// halts the dependency resolution with the unsatisfiable-version condition, naming
// the library with the floor; a floor on another major than every held version
// halts alike; a name the catalog does not hold keeps the not-found condition; and
// a floor the catalog satisfies resolves.
func TestLibraryFloorAboveHeldVersions(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		ref  string
		// want is the condition the walk halts with; nil for the control.
		want error
		// not is a condition the halt must not carry.
		not error
		// named is what the halt names.
		named string
	}{
		{name: "a floor the catalog holds (control)", ref: "Lib:1.0.0"},
		{name: "a floor above every held version", ref: "Lib:1.1.0",
			want: ErrUnsatisfiableEntityVersion(), not: closure.ErrUnresolvableURI(), named: "Lib:1.1.0"},
		{name: "a floor on another major", ref: "Lib:2.0.0",
			want: ErrUnsatisfiableEntityVersion(), not: closure.ErrUnresolvableURI(), named: "Lib:2.0.0"},
		{name: "a name the catalog does not hold", ref: "Absent:1.0.0",
			want: closure.ErrUnresolvableURI(), not: ErrUnsatisfiableEntityVersion(), named: "Absent"},
	}
	for _, k := range libraryKindCases() {
		axis := axisOfKind(t, k.kind)
		for _, row := range rows {
			t.Run(k.kind+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				ws, dir := heldVersionsCatalog(t, k, "1.0.0")
				ref := row.ref + "#" + axis.FragmentPrefix + libraryEntry[k.kind]
				for backing, lookup := range resolutionBackings(t, ws, dir) {
					var cl closure.Closure
					err := discoverReference(t, &cl, axis, lookup, ref)
					if row.want == nil {
						if err != nil {
							t.Errorf("%s: the control %s was refused: %v", backing, row.ref, err)
						}
						continue
					}
					if !errors.Is(err, row.want) || errors.Is(err, row.not) {
						t.Errorf("%s: %s halted with %v; want it to carry %v and not %v", backing, row.ref, err, row.want, row.not)
						continue
					}
					if !strings.Contains(err.Error(), row.named) {
						t.Errorf("%s: the halt %q does not name %s", backing, err.Error(), row.named)
					}
				}
			})
		}
	}
}

// augmentLibraryKind is one library kind as the discovery proof drives it: the
// document of a library whose one entry references another library on the same
// axis, the document of a library referencing nothing, and how a Recipe or a
// Component consumes the first.
type augmentLibraryKind struct {
	kind string
	// referencing is the spec of a library whose entry references target.
	referencing func(target string) string
	// leaf is the spec of a library referencing nothing.
	leaf string
	// fragment is the fragment every reference on the axis names.
	fragment string
}

func augmentLibraryKinds() []augmentLibraryKind {
	return []augmentLibraryKind{
		{
			kind: "SchemaLibrary",
			referencing: func(target string) string {
				return fmt.Sprintf("  schemas:\n    S:\n      inner:\n        $ref: %q\n", target+"#/schemas/S")
			},
			leaf:     "  schemas:\n    S:\n      level: { type: string }\n",
			fragment: "#/schemas/S",
		},
		{
			kind: "MappingLibrary",
			referencing: func(target string) string {
				return fmt.Sprintf("  mappings:\n    X: |\n      $import: %s\n", target+"#/mappings/X")
			},
			leaf:     "  mappings:\n    X: 'level: 1'\n",
			fragment: "#/mappings/X",
		},
		{
			kind: "WorkloadLibrary",
			referencing: func(target string) string {
				return fmt.Sprintf("  workloads:\n    W: |\n      $import: %s\n", target+"#/workloads/W")
			},
			leaf:     "  workloads:\n    W: 'kind: Deployment'\n",
			fragment: "#/workloads/W",
		},
		{
			kind: "ArtifactLibrary",
			referencing: func(target string) string {
				return fmt.Sprintf("  artifacts:\n    A:\n      f.yaml: |\n        $import: %s\n", target+"#/artifacts/A")
			},
			leaf:     "  artifacts:\n    A:\n      f.yaml: \"kind: ConfigMap\"\n",
			fragment: "#/artifacts/A",
		},
	}
}

// libraryDocument is a library document of kind declaring name at version.
func libraryDocument(kind, name, version, spec string) string {
	return fmt.Sprintf("apiVersion: aggrega.ai/v1beta1\nkind: %s\nmetadata:\n  name: %s\n  version: %s\nspec:\n%s",
		kind, name, version, spec)
}

// TestAugmentFollowsResolvedLibraryVersion: the discovery descends a library's
// own references at the version the resolution selected for it. Lib 1.0.0
// references Other and Lib 1.1.0 references Third; a consumer whose floor selects
// Lib 1.0.0 reaches Other and not Third, on every library axis — where a descent
// reading the library by its name alone would read the higher version's body.
func TestAugmentFollowsResolvedLibraryVersion(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	for _, k := range augmentLibraryKinds() {
		prefix := strings.ToLower(k.kind)
		writeFixture(t, dir, prefix+"-lib-1.0.0.yaml", libraryDocument(k.kind, "Lib", "1.0.0", k.referencing("Other")))
		writeFixture(t, dir, prefix+"-lib-1.1.0.yaml", libraryDocument(k.kind, "Lib", "1.1.0", k.referencing("Third")))
		writeFixture(t, dir, prefix+"-other.yaml", libraryDocument(k.kind, "Other", "1.0.0", k.leaf))
		writeFixture(t, dir, prefix+"-third.yaml", libraryDocument(k.kind, "Third", "1.0.0", k.leaf))
	}
	writeFixture(t, dir, "consumer-component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/consumer
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      size:
        $ref: "Lib:1.0.0#/schemas/S"
`)
	writeFixture(t, dir, "consumer-recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: consumer-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/consumer
  source: ./chart
  valuesMapping:
    $import: Lib:1.0.0#/mappings/X
  workload:
    $import: Lib:1.0.0#/workloads/W
  artifacts:
    bundle:
      $import: Lib:1.0.0#/artifacts/A
`)

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, nil)
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "core/consumer:1.0.0", KindHint: closure.KindComponent},
		{URI: "helm/consumer-recipe:1.0.0", KindHint: closure.KindRecipe},
	}}
	lookup := view.ClosureLookup(nil, ws, resolutionCacheDir, nil, nil, nil)
	if err := closure.AugmentClosure(context.Background(), &cl, view, lookup); err != nil {
		t.Fatalf("AugmentClosure: %v\nskips: %v", err, view.Skips())
	}
	for _, k := range augmentLibraryKinds() {
		var got []string
		for _, lib := range axisOfKind(t, k.kind).Libraries(&cl) {
			got = append(got, lib.URI+":"+lib.ResolvedVersion)
		}
		if want := "Lib:1.0.0 Other:1.0.0"; strings.Join(got, " ") != want {
			t.Errorf("%s: the closure holds %v; want %s — the libraries Lib 1.0.0 references, and not those of Lib 1.1.0",
				k.kind, got, want)
		}
	}
}

// TestLibraryReadByReference: a read of one library by reference applies the
// selection rule to the reference as given, on the lazily enumerated view and on
// the eagerly loaded catalog alike: a floor naming a held version reads that
// version while a higher one is held, a bare reference reads the highest, a floor
// between held versions reads the lowest above it, and a floor nothing satisfies
// reads nothing.
func TestLibraryReadByReference(t *testing.T) {
	t.Parallel()
	rows := []struct {
		ref  string
		want string
	}{
		{ref: "Lib:1.0.0", want: "1.0.0"},
		{ref: "Lib", want: "1.2.0"},
		{ref: "Lib:1.1.0", want: "1.2.0"},
		{ref: "Lib:1.3.0", want: ""},
	}
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws, dir := heldVersionsCatalog(t, k, "1.0.0", "1.2.0")
			eager, err := LoadAllWithNamedDirs(ws, "", nil, dir)
			if err != nil {
				t.Fatalf("catalog load: %v", err)
			}
			view := NewLazyLookup(ws, "", []string{dir}, nil)
			reads := map[string]func(ref string) (string, bool){
				"lazy": func(ref string) (string, bool) { return k.lazyRead(view, ref) },
				"eager": func(ref string) (string, bool) {
					switch k.kind {
					case "SchemaLibrary":
						lib, ok := eager.SchemaLibrary(ref)
						return lib.Metadata.Version, ok
					case "MappingLibrary":
						lib, ok := eager.MappingLibrary(ref)
						return lib.Metadata.Version, ok
					case "WorkloadLibrary":
						lib, ok := eager.WorkloadLibrary(ref)
						return lib.Metadata.Version, ok
					case "ArtifactLibrary":
						lib, ok := eager.ArtifactLibrary(ref)
						return lib.Metadata.Version, ok
					}
					t.Fatalf("no eager read for %s", k.kind)
					return "", false
				},
			}
			for backing, read := range reads {
				for _, row := range rows {
					got, ok := read(row.ref)
					if want := row.want != ""; ok != want || got != row.want {
						t.Errorf("%s: read of %s = %q (ok %v); want %q (ok %v)", backing, row.ref, got, ok, row.want, want)
					}
				}
			}
		})
	}
}
