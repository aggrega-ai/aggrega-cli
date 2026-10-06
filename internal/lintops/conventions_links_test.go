// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestTheConventionsWalkDoesNotReadALinkLeadingOut drives the walk the
// conventions check reports from, over a catalog directory inside the workspace
// holding the one entity a row places, and reads back whether the walk returned it.
//
// The walk is the catalog's own, so it reads what a lookup over the same directory
// reads: an entity behind a link leading out of the workspace — a link to its file,
// or to a directory holding it — is not read, and the walk halts naming the link
// rather than returning a list that reads as complete without it; one behind a
// link leading elsewhere inside it is read. The control row returns the entity from
// a plain file, which is what makes a halting row's halt the link's doing.
func TestTheConventionsWalkDoesNotReadALinkLeadingOut(t *testing.T) {
	const entity = "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: example/probe\n" +
		"  version: \"1.0.0\"\nspec:\n  inject: [\"env\"]\n"
	for _, row := range []struct {
		name string
		// lay places the entity and returns the link a halting row names, or "".
		lay       func(t *testing.T, ws, outside, at string) string
		wantFound bool
	}{
		{
			name:      "control: the entity in a plain file",
			lay:       func(t *testing.T, ws, outside, at string) string { mustWriteFile(t, at, entity); return "" },
			wantFound: true,
		},
		{
			name: "the entity behind a link leading out of the workspace halts the walk",
			lay: func(t *testing.T, ws, outside, at string) string {
				mustWriteFile(t, filepath.Join(outside, "probe.yaml"), entity)
				conventionsLink(t, filepath.Join(outside, "probe.yaml"), at)
				return at
			},
		},
		{
			name: "the entity in a directory behind a link leading out of the workspace halts the walk",
			lay: func(t *testing.T, ws, outside, at string) string {
				if err := os.MkdirAll(filepath.Join(outside, "kept"), 0o755); err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, filepath.Join(outside, "kept", "probe.yaml"), entity)
				link := filepath.Join(filepath.Dir(at), "kept")
				conventionsLink(t, filepath.Join(outside, "kept"), link)
				return link
			},
		},
		{
			name: "the entity behind a link leading elsewhere inside the workspace",
			lay: func(t *testing.T, ws, outside, at string) string {
				if err := os.MkdirAll(filepath.Join(ws, "elsewhere"), 0o755); err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, filepath.Join(ws, "elsewhere", "probe.yaml"), entity)
				conventionsLink(t, filepath.Join(ws, "elsewhere", "probe.yaml"), at)
				return ""
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
			halting := row.lay(t, ws, outside, at)
			files, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{cat}, nil, "")
			if halting != "" {
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read-leads-out-of-workspace halt, got %v (files: %+v)", err, files)
				}
				if !strings.Contains(err.Error(), "the link "+halting+" leads to ") {
					t.Errorf("the halt does not name the link %s: %v", halting, err)
				}
				return
			}
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
