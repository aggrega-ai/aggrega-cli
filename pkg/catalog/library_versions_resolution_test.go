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
	"reflect"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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
		{name: "a floor below the highest held version", held: []string{"1.0.0", "1.1.0"}, ref: "Lib:1.0.0", want: "1.1.0"},
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
// halts the dependency resolution with the unsatisfiable-version condition. The
// halt names the library with the floor and then every version held, in semver
// order — 1.9.0 before 1.10.0, which a lexical order would reverse. A floor on
// another major than every held version halts alike; a name the catalog does not
// hold keeps the not-found condition and names nothing held; and a floor the
// catalog satisfies resolves.
func TestLibraryFloorAboveHeldVersions(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		held []string
		ref  string
		// want is the condition the walk halts with; nil for the control.
		want error
		// not is a condition the halt must not carry.
		not error
		// named is what the halt names.
		named string
	}{
		{name: "a floor the catalog holds (control)", held: []string{"1.0.0"}, ref: "Lib:1.0.0"},
		{name: "a floor above every held version", held: []string{"1.0.0"}, ref: "Lib:1.1.0",
			want: ErrUnsatisfiableEntityVersion(), not: closure.ErrUnresolvableURI(),
			named: `library "Lib:1.1.0"; held: Lib:1.0.0`},
		{name: "a floor above every held version of its major", held: []string{"2.0.0", "1.10.0", "1.9.0"}, ref: "Lib:1.11.0",
			want: ErrUnsatisfiableEntityVersion(), not: closure.ErrUnresolvableURI(),
			named: `library "Lib:1.11.0"; held: Lib:1.9.0, Lib:1.10.0, Lib:2.0.0`},
		{name: "a floor on another major", held: []string{"1.0.0"}, ref: "Lib:2.0.0",
			want: ErrUnsatisfiableEntityVersion(), not: closure.ErrUnresolvableURI(),
			named: `library "Lib:2.0.0"; held: Lib:1.0.0`},
		{name: "a name the catalog does not hold", held: []string{"1.0.0"}, ref: "Absent:1.0.0",
			want: closure.ErrUnresolvableURI(), not: ErrUnsatisfiableEntityVersion(), named: `library "Absent"`},
	}
	for _, k := range libraryKindCases() {
		axis := axisOfKind(t, k.kind)
		for _, row := range rows {
			t.Run(k.kind+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				ws, dir := heldVersionsCatalog(t, k, row.held...)
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
						t.Errorf("%s: the halt %q does not say %s", backing, err.Error(), row.named)
					}
					if row.want == closure.ErrUnresolvableURI() && strings.Contains(err.Error(), "held:") {
						t.Errorf("%s: the not-found halt %q names versions held", backing, err.Error())
					}
				}
			})
		}
	}
}

// servingLibraryPuller is a registry serving MappingLibrary documents, keyed
// `<Name>:<version>`: a request with a floor fetches that tag, and a bare request
// the one version served under the name. It records each request it is asked,
// written `<Name>:<floor>`, or `<Name>` for a bare one.
type servingLibraryPuller struct {
	t         *testing.T
	libraries map[string]string
	mu        sync.Mutex
	asked     []string
}

func (p *servingLibraryPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (p *servingLibraryPuller) PullLibrary(_ context.Context, name, _, floorVersion string) (*oci.PullResult, string, error) {
	p.mu.Lock()
	if floorVersion == "" {
		p.asked = append(p.asked, name)
	} else {
		p.asked = append(p.asked, name+":"+floorVersion)
	}
	p.mu.Unlock()
	version := floorVersion
	if version == "" {
		var served []string
		for identity := range p.libraries {
			if n, v, _ := strings.Cut(identity, ":"); n == name {
				served = append(served, v)
			}
		}
		if len(served) != 1 {
			return nil, "", oci.ErrFetchNotFound()
		}
		version = served[0]
	}
	body, ok := p.libraries[name+":"+version]
	if !ok {
		return nil, "", oci.ErrFetchNotFound()
	}
	mb, md := manifestBytesFor(p.t, oci.MediaTypeMappingLibrary, []byte(body))
	return &oci.PullResult{MediaType: oci.MediaTypeMappingLibrary, Bytes: []byte(body), ManifestBytes: mb, Digest: md}, version, nil
}

func (p *servingLibraryPuller) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// importingRecipe is a helm Recipe named name at 1.0.0 whose values mapping
// imports reference.
func importingRecipe(name, reference string) string {
	return fmt.Sprintf("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: %s\n  version: \"1.0.0\"\n"+
		"spec:\n  capability: helm\n  implements: core/consumer\n  source: ./chart\n  valuesMapping:\n    $import: %s\n",
		name, reference)
}

// mappingRows reads the closure's MappingLibrary section as one line per
// library: `<Name>:<version> <- <consumers>`.
func mappingRows(cl closure.Closure) []string {
	var rows []string
	for _, lib := range cl.MappingLibraries {
		var consumers []string
		for _, c := range lib.Consumers {
			consumers = append(consumers, c.ConsumerURI)
		}
		rows = append(rows, lib.URI+":"+lib.ResolvedVersion+" <- "+strings.Join(consumers, ","))
	}
	return rows
}

// TestLibraryFloorUnmetInOwningDirectory: a catalog directory holding a library's
// name owns that name, and its selection is final for it. The directory holds Lib
// 1.0.0 and Other; the content cache holds Lib 1.1.0, whose body imports Other; a
// registry serves Lib 1.1.0 too; and a Recipe imports Lib with the floor 1.1.0. The
// dependency resolution halts with the unsatisfiable-version condition, naming the
// request and the version the directory holds, and asks no registry for Lib —
// where answering from the cache or a registry would resolve a version the
// directory's ownership of the name keeps from ever being read.
func TestLibraryFloorUnmetInOwningDirectory(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	mapping := augmentLibraryKinds()[1]
	writeFixture(t, dir, "lib-1.0.0.yaml", libraryDocument(mapping.kind, "Lib", "1.0.0", mapping.leaf))
	writeFixture(t, dir, "other.yaml", libraryDocument(mapping.kind, "Other", "1.0.0", mapping.leaf))
	writeFixture(t, dir, "recipe.yaml", importingRecipe("consumer-recipe", "Lib:1.1.0#/mappings/X"))
	cached := libraryDocument(mapping.kind, "Lib", "1.1.0", mapping.referencing("Other"))
	writePinnedSlot(t, ws, resolutionCacheDir, "Lib:1.1.0", oci.MediaTypeMappingLibrary, []byte(cached))
	puller := &servingLibraryPuller{t: t, libraries: map[string]string{"Lib:1.1.0": cached}}

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, puller)
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "helm/consumer-recipe:1.0.0", KindHint: closure.KindRecipe}}}
	err := closure.AugmentClosure(context.Background(), &cl, view, view.ClosureLookup(puller, ws, resolutionCacheDir, nil, nil, nil))
	if !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
		t.Fatalf("AugmentClosure: %v; want the unsatisfiable-version condition (closure %+v)", err, cl.MappingLibraries)
	}
	if want := `library "Lib:1.1.0"; held: Lib:1.0.0`; !strings.Contains(err.Error(), want) {
		t.Errorf("the halt %q does not say %s", err.Error(), want)
	}
	if asked := puller.requests(); len(asked) != 0 {
		t.Errorf("a registry was asked for %q; the catalog directory owns the name", asked)
	}
}

// TestLibraryFloorMetByRegistryWhenNoDirectoryOwnsName: a library name only the
// pinned content cache holds goes on to the cache and registry tiers when its floor
// is not met there. The lockfile pins Lib at 1.0.0, no catalog directory holds Lib,
// and a Recipe's import floors it at 1.1.0, which a registry serves: the dependency
// resolution resolves Lib at 1.1.0 from the registry, asked for that tag. The pin
// the lockfile recorded at 1.0.0 gives way to the raised resolution, so Lib
// 1.1.0's own body is read and Other, which it imports and the registry serves, is
// in the closure — where the body Lib 1.0.0 had imports nothing.
func TestLibraryFloorMetByRegistryWhenNoDirectoryOwnsName(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	writeFixture(t, dir, "recipe.yaml", importingRecipe("consumer-recipe", "Lib:1.1.0#/mappings/X"))
	mapping := augmentLibraryKinds()[1]
	pinnedBody := libraryDocument(mapping.kind, "Lib", "1.0.0", mapping.leaf)
	served := libraryDocument(mapping.kind, "Lib", "1.1.0", mapping.referencing("Other"))
	pin := writePinnedSlot(t, ws, resolutionCacheDir, "Lib:1.0.0", oci.MediaTypeMappingLibrary, []byte(pinnedBody))
	puller := &servingLibraryPuller{t: t, libraries: map[string]string{
		"Lib:1.1.0":   served,
		"Other:1.0.0": libraryDocument(mapping.kind, "Other", "1.0.0", mapping.leaf),
	}}

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, puller)
	view.SetPins(Pins{{Kind: mapping.kind, URI: "Lib"}: pin})
	if lib, ok := view.MappingLibrary("Lib"); !ok || lib.Metadata.Version != "1.0.0" {
		t.Fatalf("the view holds Lib at %q (ok %v); the proof needs the pinned cache to hold it at 1.0.0", lib.Metadata.Version, ok)
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "helm/consumer-recipe:1.0.0", KindHint: closure.KindRecipe}}}
	if err := closure.AugmentClosure(context.Background(), &cl, view, view.ClosureLookup(puller, ws, resolutionCacheDir, nil, nil, nil)); err != nil {
		t.Fatalf("AugmentClosure: %v", err)
	}
	want := []string{"Lib:1.1.0 <- helm/consumer-recipe:1.0.0", "Other:1.0.0 <- Lib"}
	if got := mappingRows(cl); !reflect.DeepEqual(got, want) {
		t.Errorf("mapping libraries:\n  got:  %q\n  want: %q", got, want)
	}
	for _, lib := range cl.MappingLibraries {
		if lib.URI == "Lib" && (lib.ResolutionPath != closure.ResolutionPathRegistry || lib.Digest != fileDigest(served)) {
			t.Errorf("Lib resolved as %+v; want 1.1.0 from the registry, with the served bytes' digest %s", lib, fileDigest(served))
		}
	}
	if asked, want := puller.requests(), []string{"Lib:1.1.0", "Other"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("the registry was asked for %q; want %q", asked, want)
	}
}

// TestAugmentFollowsRaisedRegistryLibraryVersion is TestAugmentFollowsRaisedLibraryVersion
// with Lib2, Zed and Wye served by a registry rather than a catalog directory: the
// discovery holds Lib2 1.0.0 from its first fetch, and the later requirement
// raising Lib2 to 1.1.0 makes the raised version readable in its place, so Lib2
// 1.1.0's body is read. The closure holds Wye, consumed by Lib2, and holds no Zed,
// which only Lib2 1.0.0 referenced; Lib2 1.1.0 is readable once the walk is done.
func TestAugmentFollowsRaisedRegistryLibraryVersion(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	mapping := augmentLibraryKinds()[1]
	writeFixture(t, dir, "recipe-a.yaml", importingRecipe("recipe-a", "Lib:1.0.0#/mappings/X"))
	writeFixture(t, dir, "recipe-b.yaml", importingRecipe("recipe-b", "Lib2:1.0.0#/mappings/X"))
	writeFixture(t, dir, "lib.yaml", libraryDocument(mapping.kind, "Lib", "1.0.0", mapping.referencing("Lib2:1.1.0")))
	puller := &servingLibraryPuller{t: t, libraries: map[string]string{
		"Lib2:1.0.0": libraryDocument(mapping.kind, "Lib2", "1.0.0", mapping.referencing("Zed")),
		"Lib2:1.1.0": libraryDocument(mapping.kind, "Lib2", "1.1.0", mapping.referencing("Wye")),
		"Zed:1.0.0":  libraryDocument(mapping.kind, "Zed", "1.0.0", mapping.leaf),
		"Wye:1.0.0":  libraryDocument(mapping.kind, "Wye", "1.0.0", mapping.leaf),
	}}

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, puller)
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "helm/recipe-a:1.0.0", KindHint: closure.KindRecipe},
		{URI: "helm/recipe-b:1.0.0", KindHint: closure.KindRecipe},
	}}
	if err := closure.AugmentClosure(context.Background(), &cl, view, view.ClosureLookup(puller, ws, resolutionCacheDir, nil, nil, nil)); err != nil {
		t.Fatalf("AugmentClosure: %v", err)
	}
	want := []string{
		"Lib:1.0.0 <- helm/recipe-a:1.0.0",
		"Lib2:1.1.0 <- Lib,helm/recipe-b:1.0.0",
		"Wye:1.0.0 <- Lib2",
	}
	if got := mappingRows(cl); !reflect.DeepEqual(got, want) {
		t.Errorf("mapping libraries:\n  got:  %q\n  want: %q\n  registry asked: %q", got, want, puller.requests())
	}
	if lib, ok := view.MappingLibrary("Lib2:1.1.0"); !ok || lib.Metadata.Version != "1.1.0" {
		t.Errorf("after the walk the view reads Lib2:1.1.0 as %q (ok %v); want the raised 1.1.0", lib.Metadata.Version, ok)
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
// references Other and Lib 1.1.0 references Third; a consumer whose floor is 1.0.0
// selects Lib 1.1.0, the highest version the floor admits, and reaches Third and
// not Other, on every library axis — where a descent reading the floor's own
// version would read the lower version's body.
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
		if want := "Lib:1.1.0 Third:1.0.0"; strings.Join(got, " ") != want {
			t.Errorf("%s: the closure holds %v; want %s — the libraries Lib 1.1.0 references, and not those of Lib 1.0.0",
				k.kind, got, want)
		}
	}
}

// TestAugmentFollowsRaisedLibraryVersion: a library whose selected version a later
// round of the discovery raises is descended at the raised version, and the
// references its earlier version made leave the closure. Recipe B imports Lib2
// with the floor 1.0.0 and Recipe A imports Lib, whose body imports Lib2 with the
// floor 1.1.0 — a requirement the discovery meets only once it has read Lib's
// body, after Lib2 1.0.0 was selected and descended. Lib2 1.0.0 imports Zed and
// Lib2 1.1.0 imports Wye: the closure holds Lib2 at 1.1.0 and Wye, consumed by
// Lib2, and holds no Zed, which only the version no longer selected referenced.
func TestAugmentFollowsRaisedLibraryVersion(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	mapping := augmentLibraryKinds()[1]
	writeFixture(t, dir, "recipe-a.yaml", importingRecipe("recipe-a", "Lib:1.0.0#/mappings/X"))
	writeFixture(t, dir, "recipe-b.yaml", importingRecipe("recipe-b", "Lib2:1.0.0#/mappings/X"))
	writeFixture(t, dir, "lib.yaml", libraryDocument(mapping.kind, "Lib", "1.0.0", mapping.referencing("Lib2:1.1.0")))
	writeFixture(t, dir, "lib2-1.0.0.yaml", libraryDocument(mapping.kind, "Lib2", "1.0.0", mapping.referencing("Zed")))
	writeFixture(t, dir, "lib2-1.1.0.yaml", libraryDocument(mapping.kind, "Lib2", "1.1.0", mapping.referencing("Wye")))
	writeFixture(t, dir, "zed.yaml", libraryDocument(mapping.kind, "Zed", "1.0.0", mapping.leaf))
	writeFixture(t, dir, "wye.yaml", libraryDocument(mapping.kind, "Wye", "1.0.0", mapping.leaf))

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, nil)
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "helm/recipe-a:1.0.0", KindHint: closure.KindRecipe},
		{URI: "helm/recipe-b:1.0.0", KindHint: closure.KindRecipe},
	}}
	if err := closure.AugmentClosure(context.Background(), &cl, view, view.ClosureLookup(nil, ws, resolutionCacheDir, nil, nil, nil)); err != nil {
		t.Fatalf("AugmentClosure: %v", err)
	}
	want := []string{
		"Lib:1.0.0 <- helm/recipe-a:1.0.0",
		"Lib2:1.1.0 <- Lib,helm/recipe-b:1.0.0",
		"Wye:1.0.0 <- Lib2",
	}
	if got := mappingRows(cl); !reflect.DeepEqual(got, want) {
		t.Errorf("mapping libraries:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestAugmentHaltsOnAResolvedLibraryItCannotRead: a library the resolution
// resolved at a version the view cannot read at that version halts the discovery
// with the unresolvable-reference condition naming the library at that version,
// rather than ending the descent through it without a word. The lockfile pins Lib
// at 1.1.0 to bytes its cache slot no longer holds, so the view folds no copy of
// Lib, while the cache tier answers the reference from that slot; Lib 1.1.0's body
// imports Other, which would otherwise be missing from the closure unannounced.
// The halt says no copy at that version could be held, and why: the slot's refusal
// names the digest the lockfile pins.
func TestAugmentHaltsOnAResolvedLibraryItCannotRead(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := mkCatalogDir(t, filepath.Join(ws, "catalog"))
	mapping := augmentLibraryKinds()[1]
	writeFixture(t, dir, "recipe.yaml", importingRecipe("consumer-recipe", "Lib:1.1.0#/mappings/X"))
	writeFixture(t, dir, "other.yaml", libraryDocument(mapping.kind, "Other", "1.0.0", mapping.leaf))
	body := libraryDocument(mapping.kind, "Lib", "1.1.0", mapping.referencing("Other"))
	pin := writePinnedSlot(t, ws, resolutionCacheDir, "Lib:1.1.0", oci.MediaTypeMappingLibrary, []byte(body))
	pin.Digest = fileDigest("other bytes")

	view := NewLazyLookup(ws, resolutionCacheDir, []string{dir}, nil)
	view.SetPins(Pins{{Kind: mapping.kind, URI: "Lib"}: pin})
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "helm/consumer-recipe:1.0.0", KindHint: closure.KindRecipe}}}
	err := closure.AugmentClosure(context.Background(), &cl, view, view.ClosureLookup(nil, ws, resolutionCacheDir, nil, nil, nil))
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
		t.Fatalf("AugmentClosure: %v; want the unresolvable-reference condition (closure %q)", err, mappingRows(cl))
	}
	if want := `library "Lib:1.1.0": no copy at that version could be held: `; !strings.Contains(err.Error(), want) {
		t.Errorf("the halt %q does not say %q", err.Error(), want)
	}
	if !strings.Contains(err.Error(), pin.Digest) {
		t.Errorf("the halt %q does not carry the slot's refusal naming the pinned digest %s", err.Error(), pin.Digest)
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
