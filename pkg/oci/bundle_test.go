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

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "recipe.yaml"), []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: helm/mariadb-bitnami\n"))
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatalf("mkdir source/: %v", err)
	}
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
		t.Fatal("expected ErrLibraryVersionMismatch; got nil")
	}
	if !errors.Is(err, oci.ErrLibraryVersionMismatch) {
		t.Errorf("error should wrap ErrLibraryVersionMismatch; got %v", err)
	}
}

func TestBundleDeterminism(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "recipe.yaml"), []byte("kind: Recipe\n"))
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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

func TestBundleAggregateReturnsUnknownKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "aggregate.yaml"), []byte("kind: Aggregate\n"))
	_, _, err := oci.Bundle("Aggregate", filepath.Join(dir, "aggregate.yaml"))
	if !errors.Is(err, oci.ErrUnknownEntityKind) {
		t.Errorf("Aggregate should NOT be a bundled kind; got %v", err)
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
