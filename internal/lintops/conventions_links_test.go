// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheConventionsWalkDoesNotReadALinkLeadingOut drives the walk the
// conventions check reports from, over a catalog directory inside the workspace
// holding the one entity a row places, and reads back whether the walk returned it.
//
// The walk is the catalog's own, so it reads what a lookup over the same directory
// reads: an entity behind a link leading out of the workspace is not read and not
// returned, and one behind a link leading elsewhere inside it is. The control row
// returns the entity from a plain file, which is what makes the second row's
// absence the link's doing.
func TestTheConventionsWalkDoesNotReadALinkLeadingOut(t *testing.T) {
	const entity = "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: example/probe\n" +
		"  version: \"1.0.0\"\nspec:\n  inject: [\"env\"]\n"
	for _, row := range []struct {
		name      string
		lay       func(t *testing.T, ws, outside, at string)
		wantFound bool
	}{
		{
			name:      "control: the entity in a plain file",
			lay:       func(t *testing.T, ws, outside, at string) { mustWriteFile(t, at, entity) },
			wantFound: true,
		},
		{
			name: "the entity behind a link leading out of the workspace",
			lay: func(t *testing.T, ws, outside, at string) {
				mustWriteFile(t, filepath.Join(outside, "probe.yaml"), entity)
				conventionsLink(t, filepath.Join(outside, "probe.yaml"), at)
			},
		},
		{
			name: "the entity behind a link leading elsewhere inside the workspace",
			lay: func(t *testing.T, ws, outside, at string) {
				if err := os.MkdirAll(filepath.Join(ws, "elsewhere"), 0o755); err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, filepath.Join(ws, "elsewhere", "probe.yaml"), entity)
				conventionsLink(t, filepath.Join(ws, "elsewhere", "probe.yaml"), at)
			},
			wantFound: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			cat := filepath.Join(ws, "cat")
			if err := os.MkdirAll(cat, 0o755); err != nil {
				t.Fatal(err)
			}
			at := filepath.Join(cat, "probe.yaml")
			row.lay(t, ws, outside, at)
			files, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{cat}, nil, "")
			if err != nil {
				t.Fatalf("walkCatalogEntityFiles: %v", err)
			}
			found := false
			for _, f := range files {
				if f.Path == at && f.Name == "example/probe" {
					found = true
				}
			}
			if found != row.wantFound {
				t.Errorf("the walk returned %s: %v, want %v (files: %+v)", at, found, row.wantFound, files)
			}
		})
	}
}

func conventionsLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
