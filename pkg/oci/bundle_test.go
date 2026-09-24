// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	mustWrite(t, filepath.Join(dir, "recipe.yaml"), []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: helm/mariadb-bitnami\n  version: 1.0.0\n"))
	mustWrite(t, filepath.Join(dir, "source", "Chart.yaml"), []byte("name: mariadb\nversion: 25.0.8\n"))
	mustWrite(t, filepath.Join(dir, "source", "values.yaml"), []byte("primary:\n  replicaCount: 1\n"))

	bundleBytes, mediaType, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"))
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
// fixture reaches the arm, the other two as the cases a name check would refuse.
func TestBundleJudgesNoDirectoryName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	library := func(version string) []byte {
		return []byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: core/base-types\n  version: " +
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
		data, mediaType, err := oci.Bundle("SchemaLibrary", filepath.Join(tc.dir, "library.yaml"))
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
	mustWrite(t, filepath.Join(flat, "server.yaml"), []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: server\n  version: 1.0.0\n"))
	archive, mediaType, err := oci.Bundle("Recipe", filepath.Join(flat, "server.yaml"))
	if err != nil {
		t.Fatalf("a Recipe in a directory named for nothing must bundle; got %v", err)
	}
	if mediaType != oci.MediaTypeRecipe {
		t.Errorf("flat Recipe mediaType %q", mediaType)
	}
	if files := readTarGz(t, archive); len(files) != 1 || files["server.yaml"] == nil {
		t.Errorf("the flat Recipe's archive must carry its document; carries %v", files)
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

	first, _, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"))
	if err != nil {
		t.Fatalf("first Bundle: %v", err)
	}
	second, _, err := oci.Bundle("Recipe", filepath.Join(dir, "recipe.yaml"))
	if err != nil {
		t.Fatalf("second Bundle: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two Bundle invocations against the same input produced different bytes (size %d vs %d)", len(first), len(second))
	}
}

// TestBundleBareKindsWhereverTheySit covers the bare-YAML publishable kinds: each
// bundles as its document unchanged, under its own mediatype, both in the
// catalog's per-version layout and in a directory whose name is not its version.
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
		{"ArtifactsLibrary", "library.yaml", oci.MediaTypeArtifactsLibrary},
	}
	body := func(kind string) []byte {
		return []byte("apiVersion: aggrega.ai/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/in-layout", func(t *testing.T) {
			ver := filepath.Join(t.TempDir(), "1.0.0")
			if err := os.MkdirAll(ver, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			mustWrite(t, filepath.Join(ver, tc.file), body(tc.kind))
			data, mt, err := oci.Bundle(tc.kind, filepath.Join(ver, tc.file))
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
			data, mt, err := oci.Bundle(tc.kind, filepath.Join(ver, tc.file))
			if err != nil {
				t.Fatalf("off-layout %s must bundle: a directory's name is not judged; got %v", tc.kind, err)
			}
			if mt != tc.mt || !bytes.Equal(data, body(tc.kind)) {
				t.Errorf("off-layout %s bundled as %q with other bytes than its document", tc.kind, mt)
			}
		})
	}
}

// TestBundlePluginIsItsDocumentWhereverItSits proves the Plugin arm returns the
// verbatim entity YAML under the Plugin entity mediatype both in the catalog's
// per-version layout and in a directory whose name is not its version, with a
// Component in that same directory as the contrast that the second answer is the
// arm's rather than something Plugin-specific.
func TestBundlePluginIsItsDocumentWhereverItSits(t *testing.T) {
	t.Parallel()
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\nspec:\n  plugin: \"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0\"\n")

	// In-layout: the parent dir equals metadata.version, and the bare-YAML arm
	// returns the entity file unchanged.
	inLayout := filepath.Join(t.TempDir(), "1.0.0")
	if err := os.MkdirAll(inLayout, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	inPath := filepath.Join(inLayout, "plugin.yaml")
	mustWrite(t, inPath, body)

	data, mt, err := oci.Bundle("Plugin", inPath)
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
	offData, offMT, err := oci.Bundle("Plugin", offPath)
	if err != nil {
		t.Fatalf("off-layout Plugin must bundle; got %v", err)
	}
	if offMT != oci.MediaTypePlugin || !bytes.Equal(offData, body) {
		t.Errorf("off-layout Plugin bundled as %q with other bytes than its document", offMT)
	}

	// The contrast: a Component in the same directory answers the same way, so
	// the Plugin answer above is the bare arm's and not something Plugin-specific.
	compBody := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
	compPath := filepath.Join(dir, "component.yaml")
	mustWrite(t, compPath, compBody)
	compData, _, err := oci.Bundle("Component", compPath)
	if err != nil {
		t.Fatalf("Component off-layout must bundle; got %v", err)
	}
	if !bytes.Equal(compData, compBody) {
		t.Errorf("Component off-layout bundled other bytes than its document")
	}
}

// TestBundleCoversEveryMediaTypeKind converts Bundle's default arm from an
// assertion into a check. Bundle gates on the mediatype table and then dispatches
// on its own switch, so a kind can sit in the table with no matching case: the
// gate lets it through and the switch drops it on ErrUnknownEntityKind. That is
// the one shape where the two homes disagree, and it is invisible until somebody
// pushes that kind — which is how ArtifactsLibrary came to be publishable by the
// table and unbundleable in fact for as long as it was.
//
// So this walks the enumeration rather than a hand-listed set: every kind the
// transport resolves a mediatype for must reach an arm. The assertion is
// deliberately narrow — that the kind is NOT rejected as unknown, and that the
// returned mediatype is the one the table resolves. It says nothing about WHICH
// arm, because that is a per-kind judgement about the entity's shape and belongs
// in the per-kind tests above; a check that guessed would either forbid a
// legitimate archive kind or wave a mis-filed one through.
//
// The non-emptiness guard is calibration: an empty enumeration would make the
// loop body run zero times and the test pass while checking nothing, and a
// vacuous pass reads exactly like a real one.
func TestBundleCoversEveryMediaTypeKind(t *testing.T) {
	t.Parallel()
	kinds := oci.MediaTypeKinds()
	if len(kinds) == 0 {
		t.Fatal("the transport resolves no kinds at all, so the loop below asserts nothing")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			// The catalog's per-version layout for every kind, so the fixture is
			// the shape a published entity is authored in.
			ver := filepath.Join(t.TempDir(), "1.0.0")
			if err := os.MkdirAll(ver, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			path := filepath.Join(ver, "entity.yaml")
			mustWrite(t, path, []byte("apiVersion: aggrega.ai/v1alpha1\nkind: "+kind+
				"\nmetadata:\n  name: demo/x\n  version: 1.0.0\n"))

			data, mt, err := oci.Bundle(kind, path)
			if errors.Is(err, oci.ErrUnknownEntityKind()) {
				t.Fatalf("kind %q resolves a mediatype but Bundle has no case for it: it is publishable "+
					"by the mediatype table and unbundleable in fact. Add an arm to Bundle's switch — "+
					"bare YAML for a self-contained entity, tar.gz for one whose directory "+
					"carries co-located source.", kind)
			}
			if err != nil {
				t.Fatalf("kind %q: Bundle: %v", kind, err)
			}
			want, err := oci.MediaTypeForKind(kind)
			if err != nil {
				t.Fatalf("kind %q came from MediaTypeKinds but MediaTypeForKind rejects it: %v", kind, err)
			}
			if mt != want {
				t.Errorf("kind %q mediatype = %q; want %q", kind, mt, want)
			}
			if len(data) == 0 {
				t.Errorf("kind %q bundled to zero bytes", kind)
			}
		})
	}
}

// TestMediaTypeForKindPlugin proves MediaTypeForKind resolves the Plugin
// entity mediatype, so the kind is in the publishable set the bundle dispatch
// gates on.
func TestMediaTypeForKindPlugin(t *testing.T) {
	t.Parallel()
	mt, err := oci.MediaTypeForKind("Plugin")
	if err != nil {
		t.Fatalf("MediaTypeForKind(Plugin): %v", err)
	}
	if mt != oci.MediaTypePlugin {
		t.Errorf("MediaTypeForKind(Plugin) = %q; want %q", mt, oci.MediaTypePlugin)
	}
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
