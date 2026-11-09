// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestArchiveEntryNamesAreTheNamesTheArchiveHolds holds the names
// oci.ArchiveEntryNames reports against the entries of the archive oci.Bundle packs
// from the same input, read back with an archive reader of this test's own rather
// than through the package's.
//
// Each row is a shape the composition must get right: the document's own file
// name is among the names whatever it is called, a carried path is named once even
// when it is carried twice, the document named among what it carries is named once,
// and an entity carrying nothing publishes as its document alone, which no archive
// holds, so it names nothing. The order is the composition's — the document first,
// then each carried path as given — while the archive stores its entries sorted, so
// the two are compared as the order the function promises and as the set the
// archive holds.
func TestArchiveEntryNamesAreTheNamesTheArchiveHolds(t *testing.T) {
	rows := []struct {
		name     string
		document string
		carried  []string
		want     []string
	}{
		{
			name:     "the document's own name first, then each carried path",
			document: "con.yaml",
			carried:  []string{"aux/x.yaml", "report."},
			want:     []string{"con.yaml", "aux/x.yaml", "report."},
		},
		{
			name:     "a path carried twice is one entry",
			document: "component.yaml",
			carried:  []string{"schema.yaml", "schema.yaml"},
			want:     []string{"component.yaml", "schema.yaml"},
		},
		{
			name:     "the document among what it carries is one entry",
			document: "component.yaml",
			carried:  []string{"component.yaml", "schema.yaml"},
			want:     []string{"component.yaml", "schema.yaml"},
		},
		{
			name:     "a document carrying nothing publishes alone and names nothing",
			document: "nul.yaml",
			carried:  nil,
			want:     nil,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			entityPath := filepath.Join(dir, row.document)
			writeArchiveFixture(t, entityPath, "apiVersion: aggrega.ai/v1beta1\nkind: Component\n")
			for _, rel := range row.carried {
				writeArchiveFixture(t, filepath.Join(dir, filepath.FromSlash(rel)), "content of "+rel+"\n")
			}

			got := oci.ArchiveEntryNames(entityPath, row.carried)
			if !reflect.DeepEqual(got, row.want) {
				t.Fatalf("ArchiveEntryNames = %q, want %q", got, row.want)
			}

			published, _, err := oci.Bundle("Component", entityPath, row.carried)
			if err != nil {
				t.Fatalf("Bundle: %v", err)
			}
			if row.want == nil {
				if oci.IsTarGzBundle(published) {
					t.Fatal("an entity carrying nothing was packed as an archive, so naming nothing is not what it publishes")
				}
				return
			}
			held := tarNames(t, published)
			wantSet := append([]string(nil), row.want...)
			sort.Strings(wantSet)
			if !reflect.DeepEqual(held, wantSet) {
				t.Errorf("the archive holds %q; ArchiveEntryNames reports %q", held, row.want)
			}
		})
	}
}

// writeArchiveFixture writes content at path, creating its directory.
func writeArchiveFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// tarNames reads every regular entry name a gzipped tar holds, sorted.
func tarNames(t *testing.T, blob []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if hdr.Typeflag == tar.TypeReg {
			names = append(names, hdr.Name)
		}
	}
	sort.Strings(names)
	return names
}
