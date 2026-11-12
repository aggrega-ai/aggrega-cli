// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestEntityFileNamesTheDocumentWhereItIsLaidOut pins the name EntityFile gives
// the entity document: the entity file's own name at the root of an archive,
// whatever the author called it, and SingleDocumentFileName for a document
// published on its own. It also pins that the document is the one EntityYAML
// returns for the same bytes, and that a failure is the same named failure, so a
// reader laying an artifact out and a reader parsing it cannot disagree.
func TestEntityFileNamesTheDocumentWhereItIsLaidOut(t *testing.T) {
	t.Parallel()
	// archiveOf packs every file in files — the entity file named among them
	// included, so the result is an archive even where the entity file is the only
	// one — which is how a fixture reaches the archive reading whatever it holds.
	archiveOf := func(t *testing.T, files map[string][]byte, entityFile, kind string) []byte {
		t.Helper()
		ver := filepath.Join(t.TempDir(), "1.0.0")
		carried := make([]string, 0, len(files))
		for rel, body := range files {
			p := filepath.Join(ver, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			mustWrite(t, p, body)
			carried = append(carried, rel)
		}
		published, _, err := oci.Bundle(kind, filepath.Join(ver, entityFile), carried)
		if err != nil {
			t.Fatalf("Bundle: %v", err)
		}
		return published
	}
	recipe := entityBody("Recipe", "helm/mariadb")
	component := entityBody("Component", "demo/compute/server")

	rows := []struct {
		name      string
		published func(t *testing.T) []byte
		wantName  string
		wantDoc   []byte
		wantErr   error
	}{
		{
			name: "archive, conventional entity file name",
			published: func(t *testing.T) []byte {
				return archiveOf(t, map[string][]byte{"recipe.yaml": recipe, "chart/Chart.yaml": []byte("name: c\n")}, "recipe.yaml", "Recipe")
			},
			wantName: "recipe.yaml",
			wantDoc:  recipe,
		},
		{
			name: "archive, an entity file named anything else",
			published: func(t *testing.T) []byte {
				return archiveOf(t, map[string][]byte{"mariadb-recipe.yml": recipe}, "mariadb-recipe.yml", "Recipe")
			},
			wantName: "mariadb-recipe.yml",
			wantDoc:  recipe,
		},
		{
			name:      "a document published on its own",
			published: func(*testing.T) []byte { return component },
			wantName:  oci.SingleDocumentFileName,
			wantDoc:   component,
		},
		{
			name: "archive carrying no entity document",
			published: func(t *testing.T) []byte {
				return archiveOf(t, map[string][]byte{"values.yaml": []byte("replicas: 1\n")}, "values.yaml", "Recipe")
			},
			wantErr: oci.ErrBundleNoEntityDocument(),
		},
		{
			name: "archive carrying two entity documents",
			published: func(t *testing.T) []byte {
				return archiveOf(t, map[string][]byte{
					"a.yaml": recipe,
					"b.yaml": entityBody("Recipe", "helm/other"),
				}, "a.yaml", "Recipe")
			},
			wantErr: oci.ErrBundleAmbiguousEntityDocument(),
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			published := row.published(t)
			name, doc, err := oci.EntityFile(published)
			yamlDoc, yamlErr := oci.EntityYAML(published)
			if row.wantErr != nil {
				if !errors.Is(err, row.wantErr) {
					t.Fatalf("EntityFile error = %v; want %v", err, row.wantErr)
				}
				if !errors.Is(yamlErr, row.wantErr) {
					t.Errorf("EntityYAML error = %v; want the same condition EntityFile names", yamlErr)
				}
				if name != "" || doc != nil {
					t.Errorf("a refusal returned a name %q and %d document bytes; want neither", name, len(doc))
				}
				return
			}
			if err != nil || yamlErr != nil {
				t.Fatalf("EntityFile err = %v, EntityYAML err = %v; want neither", err, yamlErr)
			}
			if name != row.wantName {
				t.Errorf("name = %q; want %q", name, row.wantName)
			}
			if !bytes.Equal(doc, row.wantDoc) {
				t.Errorf("document = %q; want %q", doc, row.wantDoc)
			}
			if !bytes.Equal(doc, yamlDoc) {
				t.Errorf("EntityFile and EntityYAML returned different documents for the same bytes")
			}
		})
	}
}

// TestSingleDocumentFileNameIsThePublishedTitle ties the layout name to the name
// a generic registry client writes a single-document publication under, so the
// two cannot drift: the title stamped on a Component's content descriptor is
// asserted elsewhere, and this names the same string.
func TestSingleDocumentFileNameIsThePublishedTitle(t *testing.T) {
	t.Parallel()
	if oci.SingleDocumentFileName != "entity.yaml" {
		t.Errorf("SingleDocumentFileName = %q; want the single-document title entity.yaml", oci.SingleDocumentFileName)
	}
}
