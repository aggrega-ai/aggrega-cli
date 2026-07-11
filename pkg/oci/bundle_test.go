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

	// Per-version layout: recipe.yaml sits under a <version>/ dir matching its
	// metadata.version (the generalized per-version-subdir contract).
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
	if _, err := oci.Push(ctx, store, "1.0.0", mediaType, bundleBytes); err != nil {
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

func TestPullPushLibraryPerVersionSubdirEnforced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	good := filepath.Join(root, "schema-library", "core", "base-types", "1.0.0")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(good, "library.yaml"),
		[]byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: core/base-types\n  version: 1.0.0\nspec:\n  schemas: {}\n"))

	_, mediaType, err := oci.Bundle("SchemaLibrary", filepath.Join(good, "library.yaml"))
	if err != nil {
		t.Fatalf("good library Bundle: %v", err)
	}
	if mediaType != oci.MediaTypeSchemaLibrary {
		t.Errorf("mediaType: %q", mediaType)
	}

	bad := filepath.Join(root, "schema-library", "core", "base-types", "1.5.0")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(bad, "library.yaml"),
		[]byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: core/base-types\n  version: 2.0.0\nspec:\n  schemas: {}\n"))

	_, _, err = oci.Bundle("SchemaLibrary", filepath.Join(bad, "library.yaml"))
	if err == nil {
		t.Fatal("expected ErrEntityVersionPathMismatch; got nil")
	}
	if !errors.Is(err, oci.ErrEntityVersionPathMismatch) {
		t.Errorf("error should wrap ErrEntityVersionPathMismatch; got %v", err)
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

// TestBundleBareKindsPerVersionSubdir covers the bare-YAML publishable kinds
// (Component / Feature / Aggregate), which the per-version-subdir
// check did NOT cover before this change. Each must bundle in-layout (the entity
// file's parent dir equals metadata.version) and fail off-layout with
// ErrEntityVersionPathMismatch.
func TestBundleBareKindsPerVersionSubdir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, file string
		mt         string
	}{
		{"Component", "component.yaml", oci.MediaTypeComponent},
		{"Feature", "feature.yaml", oci.MediaTypeFeature},
		{"Aggregate", "aggregate.yaml", oci.MediaTypeAggregate},
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
			if _, _, err := oci.Bundle(tc.kind, filepath.Join(ver, tc.file)); !errors.Is(err, oci.ErrEntityVersionPathMismatch) {
				t.Errorf("off-layout %s should wrap ErrEntityVersionPathMismatch; got %v", tc.kind, err)
			}
		})
	}
}

// TestBundlePluginNoVersionSubdir proves the Plugin arm: it bundles to
// bare YAML under the Plugin entity mediatype, and the per-version
// subdirectory guard does NOT apply — a Plugin entity authored OUTSIDE a
// version-named directory bundles cleanly, where every version-subdir kind
// would halt with ErrEntityVersionPathMismatch. The version comes from
// metadata.version; the repository location is the socket-path.
func TestBundlePluginNoVersionSubdir(t *testing.T) {
	t.Parallel()
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\nspec:\n  plugin: \"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0\"\n")
	// Authored at a path whose parent dir is NOT the version (here the kind name)
	// — a Component here would fail the version-subdir guard.
	dir := filepath.Join(t.TempDir(), "wasm-plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "frp-step-patch.yaml")
	mustWrite(t, path, body)

	data, mt, err := oci.Bundle("Plugin", path)
	if err != nil {
		t.Fatalf("Plugin should bundle without a version subdir; got %v", err)
	}
	if mt != oci.MediaTypePlugin {
		t.Errorf("Plugin mediatype = %q; want %q", mt, oci.MediaTypePlugin)
	}
	if string(data) != string(body) {
		t.Errorf("Plugin bundle should be the verbatim entity YAML")
	}

	// Confirm the contrast: a Component at the same off-layout path DOES halt.
	compBody := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: demo/x\n  version: 1.0.0\n")
	compPath := filepath.Join(dir, "component.yaml")
	mustWrite(t, compPath, compBody)
	if _, _, err := oci.Bundle("Component", compPath); !errors.Is(err, oci.ErrEntityVersionPathMismatch) {
		t.Errorf("Component off-layout should still wrap ErrEntityVersionPathMismatch; got %v", err)
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
