// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
)

// TestABundleRefusesALinkWhateverListItIsHanded holds the archive itself to the
// link rule, independently of the walk that names what it carries: a carried path
// that is a symbolic link to a file is refused under the one link condition rather
// than packed with the link's own permission bits and the target's bytes. The
// positive control carries the same bytes as a regular file and packs.
//
// A bundle that read through the link would pack it — as 0755, since a link's
// permission bits on the common filesystems carry every execute bit — and this
// test would see no error.
func TestABundleRefusesALinkWhateverListItIsHanded(t *testing.T) {
	rows := []struct {
		name    string
		asLink  bool
		refused bool
	}{
		{name: "a carried regular file packs", asLink: false, refused: false},
		{name: "a carried link to a file outside is refused", asLink: true, refused: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "secret.txt")
			if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			doc := filepath.Join(dir, "recipe.yaml")
			if err := os.WriteFile(doc, []byte("kind: Recipe\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			carried := filepath.Join(dir, "carried.txt")
			if row.asLink {
				if err := os.Symlink(outside, carried); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(carried, []byte("outside\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Bundle("Recipe", doc, []string{"carried.txt"})
			switch {
			case row.refused && !errors.Is(err, sourcewalk.ErrSymbolicLinkInEntityFiles()):
				t.Fatalf("Bundle = %v; want the symbolic-link refusal", err)
			case !row.refused && err != nil:
				t.Fatalf("Bundle over a regular file: %v", err)
			}
		})
	}
}
