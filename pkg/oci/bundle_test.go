// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestPullPushTarGzRecipeRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The catalog's per-version layout — recipe.yaml under a <version>/ directory
	// named for its metadata.version — which Bundle packages but does not require.
	dir := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatalf("mkdir source/: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "recipe.yaml"), []byte("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: helm/mariadb-bitnami\n  version: 1.0.0\n"))
	mustWrite(t, filepath.Join(dir, "source", "Chart.yaml"), []byte("name: mariadb\nversion: 25.0.8\n"))
	mustWrite(t, filepath.Join(dir, "source", "values.yaml"), []byte("primary:\n  replicaCount: 1\n"))

	bundleBytes, mediaType, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"),
		[]string{"source/Chart.yaml", "source/values.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if mediaType != oci.MediaTypeRecipe {
		t.Errorf("mediaType: got %q; want %q", mediaType, oci.MediaTypeRecipe)
	}

	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", mediaType, bundleBytes, nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	got, err := oci.Pull(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	files := readTarGz(t, got.Bytes)
	wantPaths := []string{"recipe.yaml", "source/Chart.yaml", "source/values.yaml"}
	gotPaths := make([]string, 0, len(files))
	for k := range files {
		gotPaths = append(gotPaths, k)
	}
	sort.Strings(gotPaths)
	if !equalStrings(gotPaths, wantPaths) {
		t.Errorf("archive paths.\n  got:  %v\n  want: %v", gotPaths, wantPaths)
	}
	if string(files["source/values.yaml"]) != "primary:\n  replicaCount: 1\n" {
		t.Errorf("inner file content drift: %q", files["source/values.yaml"])
	}
}

// TestBundleJudgesNoDirectoryName holds Bundle to packaging the entity it is
// given wherever that entity sits: a directory's name is a catalog convention,
// not an input to the artifact. A library in a directory named for its version,
// one in a directory named for a different version, and a Recipe in a directory
// named for nothing at all each bundle — the first as the control that the
// fixture reaches the packing, the other two as the cases a name check would
// refuse. The flat Recipe carries nothing, so it publishes as its document.
func TestBundleJudgesNoDirectoryName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	library := func(version string) []byte {
		return []byte("apiVersion: aggrega.ai/v1beta1\nkind: SchemaLibrary\nmetadata:\n  name: core/base-types\n  version: " +
			version + "\nspec:\n  schemas: {}\n")
	}

	for _, tc := range []struct {
		name, dir, declared string
	}{
		{"named for its version", filepath.Join(root, "schemalibrary", "base-types", "1.0.0"), "1.0.0"},
		{"named for another version", filepath.Join(root, "schemalibrary", "base-types", "1.5.0"), "2.0.0"},
	} {
		if err := os.MkdirAll(tc.dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := library(tc.declared)
		mustWrite(t, filepath.Join(tc.dir, "library.yaml"), body)
		data, mediaType, err := oci.Bundle("SchemaLibrary", filepath.Join(tc.dir, "library.yaml"), nil)
		if err != nil {
			t.Fatalf("%s: a library must bundle wherever it sits; got %v", tc.name, err)
		}
		if mediaType != oci.MediaTypeSchemaLibrary {
			t.Errorf("%s: mediaType %q", tc.name, mediaType)
		}
		if !bytes.Equal(data, body) {
			t.Errorf("%s: a library bundles as its document unchanged", tc.name)
		}
	}

	flat := filepath.Join(root, "catalog", "recipes")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	recipe := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: server\n  version: 1.0.0\n")
	mustWrite(t, filepath.Join(flat, "server.yaml"), recipe)
	data, mediaType, err := oci.Bundle("Recipe", filepath.Join(flat, "server.yaml"), nil)
	if err != nil {
		t.Fatalf("a Recipe in a directory named for nothing must bundle; got %v", err)
	}
	if want, _ := oci.MediaTypeFor("Recipe", false); mediaType != want {
		t.Errorf("flat Recipe mediaType %q; want %q", mediaType, want)
	}
	if !bytes.Equal(data, recipe) {
		t.Errorf("a Recipe carrying nothing publishes as its document unchanged; got %d bytes", len(data))
	}
}

func TestBundleDeterminism(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "2.0.0")
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "recipe.yaml"), []byte("kind: Recipe\nmetadata:\n  version: 2.0.0\n"))
	mustWrite(t, filepath.Join(dir, "source", "Chart.yaml"), []byte("name: x\n"))

	first, _, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"), []string{"source/Chart.yaml"})
	if err != nil {
		t.Fatalf("first Bundle: %v", err)
	}
	// The same files named in another order: the archive is a function of the set.
	second, _, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"), []string{"source/Chart.yaml", "recipe.yaml"})
	if err != nil {
		t.Fatalf("second Bundle: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two Bundle invocations against the same input produced different bytes (size %d vs %d)", len(first), len(second))
	}
}

// TestBundleBareKindsWhereverTheySit covers entities carrying nothing beside
// their document: each bundles as its document unchanged, under its kind's
// document mediatype, both in the catalog's per-version layout and in a directory
// whose name is not its version.
func TestBundleBareKindsWhereverTheySit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, file string
		mt         string
	}{
		{"Component", "component.yaml", oci.MediaTypeComponent},
		{"Feature", "feature.yaml", oci.MediaTypeFeature},
		{"Aggregate", "aggregate.yaml", oci.MediaTypeAggregate},
		{"WorkloadLibrary", "library.yaml", oci.MediaTypeWorkloadLibrary},
		{"ArtifactLibrary", "library.yaml", oci.MediaTypeArtifactLibrary},
	}
	body := func(kind string) []byte {
		return []byte("apiVersion: aggrega.ai/v1beta1\nkind: " + kind + "\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/in-layout", func(t *testing.T) {
			ver := filepath.Join(t.TempDir(), "1.0.0")
			if err := os.MkdirAll(ver, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			mustWrite(t, filepath.Join(ver, tc.file), body(tc.kind))
			data, mt, err := oci.Bundle(tc.kind, filepath.Join(ver, tc.file), nil)
			if err != nil {
				t.Fatalf("in-layout %s should bundle; got %v", tc.kind, err)
			}
			if mt != tc.mt {
				t.Errorf("%s mediatype = %q; want %q", tc.kind, mt, tc.mt)
			}
			if len(data) == 0 {
				t.Errorf("%s bundle bytes empty", tc.kind)
			}
		})
		t.Run(tc.kind+"/off-layout", func(t *testing.T) {
			ver := filepath.Join(t.TempDir(), "9.9.9") // parent dir != metadata.version
			if err := os.MkdirAll(ver, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			mustWrite(t, filepath.Join(ver, tc.file), body(tc.kind))
			data, mt, err := oci.Bundle(tc.kind, filepath.Join(ver, tc.file), nil)
			if err != nil {
				t.Fatalf("off-layout %s must bundle: a directory's name is not judged; got %v", tc.kind, err)
			}
			if mt != tc.mt || !bytes.Equal(data, body(tc.kind)) {
				t.Errorf("off-layout %s bundled as %q with other bytes than its document", tc.kind, mt)
			}
		})
	}
}

// TestBundlePluginIsItsDocumentWhereverItSits proves a Plugin carrying nothing
// bundles as the verbatim entity YAML under the Plugin entity mediatype both in
// the catalog's per-version layout and in a directory whose name is not its
// version, with a Component in that same directory as the contrast that the
// second answer is the shape rule's rather than something Plugin-specific.
func TestBundlePluginIsItsDocumentWhereverItSits(t *testing.T) {
	t.Parallel()
	body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\nspec:\n  plugin: \"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0\"\n")

	// In-layout: the parent dir equals metadata.version, and a publication
	// carrying nothing is the entity file unchanged.
	inLayout := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(inLayout, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	inPath := filepath.Join(inLayout, "plugin.yaml")
	mustWrite(t, inPath, body)

	data, mt, err := oci.Bundle("Plugin", inPath, nil)
	if err != nil {
		t.Fatalf("in-layout Plugin should bundle; got %v", err)
	}
	if mt != oci.MediaTypePlugin {
		t.Errorf("Plugin mediatype = %q; want %q", mt, oci.MediaTypePlugin)
	}
	if string(data) != string(body) {
		t.Errorf("Plugin bundle should be the verbatim entity YAML")
	}

	// Off-layout: the parent dir is not the version, and nothing about that is
	// judged here.
	dir := filepath.Join(t.TempDir(), "wasm-plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	offPath := filepath.Join(dir, "plugin.yaml")
	mustWrite(t, offPath, body)
	offData, offMT, err := oci.Bundle("Plugin", offPath, nil)
	if err != nil {
		t.Fatalf("off-layout Plugin must bundle; got %v", err)
	}
	if offMT != oci.MediaTypePlugin || !bytes.Equal(offData, body) {
		t.Errorf("off-layout Plugin bundled as %q with other bytes than its document", offMT)
	}

	// The contrast: a Component in the same directory answers the same way, so
	// the Plugin answer above is the shape rule's and not something
	// Plugin-specific.
	compBody := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
	compPath := filepath.Join(dir, "component.yaml")
	mustWrite(t, compPath, compBody)
	compData, _, err := oci.Bundle("Component", compPath, nil)
	if err != nil {
		t.Fatalf("Component off-layout must bundle; got %v", err)
	}
	if !bytes.Equal(compData, compBody) {
		t.Errorf("Component off-layout bundled other bytes than its document")
	}
}

// TestEveryKindTakesTheShapeWhatItCarriesDecides is the shape rule over the whole
// kind set, read from the mediatype table rather than a list written here: an
// entity of any kind carrying nothing publishes as its document, byte for byte,
// under the kind's document mediatype, and the same entity carrying one file
// publishes as an archive under the kind's archive mediatype.
//
// Each half is the other's control. A packer that archived everything passes the
// second half for every kind and fails the first; one that kept a per-kind
// answer passes whichever half its kind was filed under and fails the other.
//
// The non-emptiness guard is calibration: an empty enumeration would make the
// loop body run zero times and the test pass while checking nothing, and a
// vacuous pass reads exactly like a real one.
func TestEveryKindTakesTheShapeWhatItCarriesDecides(t *testing.T) {
	t.Parallel()
	kinds := oci.MediaTypeKinds()
	if len(kinds) == 0 {
		t.Fatal("the transport resolves no kinds at all, so the loop below asserts nothing")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ver := filepath.Join(t.TempDir(), "1.0.0")
			if err := os.MkdirAll(ver, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			path := filepath.Join(ver, "entity.yaml")
			body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: " + kind +
				"\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
			mustWrite(t, path, body)
			mustWrite(t, filepath.Join(ver, "carried.yaml"), []byte("carried: true\n"))

			document, documentType, err := oci.Bundle(kind, path, nil)
			if err != nil {
				t.Fatalf("kind %q carrying nothing: Bundle: %v", kind, err)
			}
			wantDocument, err := oci.MediaTypeFor(kind, false)
			if err != nil {
				t.Fatalf("kind %q came from MediaTypeKinds but MediaTypeFor rejects it: %v", kind, err)
			}
			if documentType != wantDocument || !bytes.Equal(document, body) {
				t.Errorf("kind %q carrying nothing published as %q (%d bytes); want its document unchanged under %q",
					kind, documentType, len(document), wantDocument)
			}

			archive, archiveType, err := oci.Bundle(kind, path, []string{"carried.yaml"})
			if err != nil {
				t.Fatalf("kind %q carrying a file: Bundle: %v", kind, err)
			}
			wantArchive, _ := oci.MediaTypeFor(kind, true)
			if archiveType != wantArchive || !oci.IsTarGzBundle(archive) {
				t.Errorf("kind %q carrying a file published as %q; want an archive under %q", kind, archiveType, wantArchive)
			}
			files := readTarGz(t, archive)
			if len(files) != 2 || !bytes.Equal(files["entity.yaml"], body) || files["carried.yaml"] == nil {
				t.Errorf("kind %q archive carries %d entries; want the document and the one carried file", kind, len(files))
			}
		})
	}
}

// TestBundleReadsOnlyWhatItIsNamed holds the packer to the files it is handed. A
// note sitting beside the entity is neither carried nor able to change the shape;
// a named file is carried; and a name that is not a file inside the entity's
// directory is refused rather than packed.
func TestBundleReadsOnlyWhatItIsNamed(t *testing.T) {
	t.Parallel()
	ver := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(filepath.Join(ver, "chart"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: helm/x\n  version: 1.0.0\n")
	path := filepath.Join(ver, "recipe.yaml")
	mustWrite(t, path, body)
	mustWrite(t, filepath.Join(ver, "chart", "Chart.yaml"), []byte("name: x\n"))
	mustWrite(t, filepath.Join(ver, "NOTES.md"), []byte("a note nothing declares\n"))

	document, _, err := oci.Bundle("Recipe", path, nil)
	if err != nil || !bytes.Equal(document, body) {
		t.Fatalf("a note beside a Recipe carrying nothing changed what it publishes (err %v, %d bytes)", err, len(document))
	}
	archive, _, err := oci.Bundle("Recipe", path, []string{"chart/Chart.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	files := readTarGz(t, archive)
	if _, stray := files["NOTES.md"]; stray || len(files) != 2 || files["chart/Chart.yaml"] == nil {
		t.Errorf("archive carries %v; want recipe.yaml and chart/Chart.yaml alone", keysOf(files))
	}

	for _, bad := range []string{"../outside.yaml", "/etc/hostname", "chart/../chart/Chart.yaml", "missing.yaml"} {
		if _, _, err := oci.Bundle("Recipe", path, []string{bad}); err == nil {
			t.Errorf("carried %q was packed; a name that is not a file inside the directory must be refused", bad)
		}
	}
}

// TestMediaTypesReadBackToTheirKindAndShape holds the table's two directions to
// one another: every media type MediaTypeFor derives reads back to the kind and
// shape it was derived from, and one naming no kind reads back to nothing.
func TestMediaTypesReadBackToTheirKindAndShape(t *testing.T) {
	t.Parallel()
	for _, kind := range oci.MediaTypeKinds() {
		for _, archive := range []bool{false, true} {
			mt, err := oci.MediaTypeFor(kind, archive)
			if err != nil {
				t.Fatalf("MediaTypeFor(%q, %v): %v", kind, archive, err)
			}
			gotKind, gotArchive, ok := oci.KindOfMediaType(mt)
			if !ok || gotKind != kind || gotArchive != archive {
				t.Errorf("KindOfMediaType(%q) = %q, %v, %v; want %q, %v, true", mt, gotKind, gotArchive, ok, kind, archive)
			}
		}
		if !oci.IsPublishableKind(kind) {
			t.Errorf("kind %q is enumerated but not publishable", kind)
		}
	}
	if mt, _ := oci.MediaTypeFor("Plugin", false); mt != oci.MediaTypePlugin {
		t.Errorf("MediaTypeFor(Plugin, document) = %q; want %q", mt, oci.MediaTypePlugin)
	}
	for _, other := range []string{oci.MediaTypeWasm, oci.MediaTypeDocs, oci.MediaTypeSignatureBundle, ""} {
		if kind, _, ok := oci.KindOfMediaType(other); ok {
			t.Errorf("KindOfMediaType(%q) = %q; a media type naming no entity kind must read back to nothing", other, kind)
		}
	}
	for _, kind := range []string{"Release", "Manifest", "NotARealKind"} {
		if oci.IsPublishableKind(kind) {
			t.Errorf("kind %q reads as publishable", kind)
		}
		if _, err := oci.MediaTypeFor(kind, false); !errors.Is(err, oci.ErrUnknownEntityKind()) {
			t.Errorf("MediaTypeFor(%q) = %v; want ErrUnknownEntityKind", kind, err)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTarGz(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = body
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
