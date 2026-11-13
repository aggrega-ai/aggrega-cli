// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// snapshotFixture is the parity fixture: a small catalog tree, and the tar stream
// the catalog publisher's script wrote for it.
const (
	snapshotFixtureTree  = "testdata/snapshot/tree"
	snapshotPublisherTar = "testdata/snapshot/publisher.tar"
)

// TestTheSnapshotWriterWritesTheTarThePublisherWrites holds the writer here to
// the catalog publisher's script, which cannot call it and writes the archive by
// the layout restated beside it: the tar stream WriteSnapshotTar writes for the
// fixture tree is, byte for byte, the one the script wrote for the same tree. The
// tree carries what the layout's rules turn on: an empty file, a file of exactly
// one block, a name opening with a capital letter, a file beside a directory of
// the same stem (dir-a.txt and dir/), and a name longer than 100 bytes, split into
// prefix and name.
//
// The committed tar was written by aggrega-hub's scripts/publish-catalog-snapshot.sh
// over a git repository holding the tree at aggrega-catalog/: run it with
// --dry-run --repository <that repository> --tar-out <this fixture's tar> to write
// it again after a deliberate change to the layout, here and there both.
//
// 🔴 PLANTED DEFECTS, each of which this test fails on:
//   - carry the files' modes rather than 0644 (snapshotFileMode = 0o755);
//   - drop the sort in snapshotEntryNames: the walk lists dir/x.txt before
//     dir-a.txt, the bytewise order the other way round;
//   - stamp a modification time (ModTime: time.Now()).
func TestTheSnapshotWriterWritesTheTarThePublisherWrites(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile(snapshotPublisherTar)
	if err != nil {
		t.Fatalf("read the publisher's tar: %v", err)
	}
	var got bytes.Buffer
	if err := WriteSnapshotTar(&got, snapshotFixtureTree); err != nil {
		t.Fatalf("WriteSnapshotTar: %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("the writer's tar of %s (%d bytes) is not the publisher's (%d bytes); first difference at byte %d",
			snapshotFixtureTree, got.Len(), len(want), firstDifferingByte(got.Bytes(), want))
	}
}

// firstDifferingByte returns the offset of the first byte a and b differ at, or
// the shorter length where one is a prefix of the other.
func firstDifferingByte(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// TestASnapshotArchiveIsAFunctionOfTheTreeAlone writes the archive of trees
// holding the same files, laid down differently, and holds every archive to the
// first: the same tree twice; the files created in the opposite order; and the
// files given other permission bits and other modification times.
//
// 🔴 PLANTED DEFECTS: carry each file's mode from disk (Mode: int64(info.Mode()))
// and the third row fails; stamp each file's modification time and the second and
// third rows fail.
func TestASnapshotArchiveIsAFunctionOfTheTreeAlone(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"component/core/a/1.0.0/component.yaml": "kind: Component\n",
		"recipe/compose/a/1.0.0/recipe.yaml":    "kind: Recipe\n",
		"recipe/compose/a/1.0.0/fragment.yaml":  "services: {}\n",
		"z-last.txt":                            "last\n",
	}
	order := []string{"component/core/a/1.0.0/component.yaml", "recipe/compose/a/1.0.0/fragment.yaml",
		"recipe/compose/a/1.0.0/recipe.yaml", "z-last.txt"}
	lay := func(t *testing.T, names []string, mode os.FileMode, stamp bool) string {
		t.Helper()
		dir := t.TempDir()
		for i, name := range names {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(files[name]), mode); err != nil {
				t.Fatal(err)
			}
			if stamp {
				when := time.Date(2001, 2, 3, 4, 5, i, 0, time.UTC)
				if err := os.Chtimes(p, when, when); err != nil {
					t.Fatal(err)
				}
			}
		}
		return dir
	}
	reversed := make([]string, len(order))
	for i, name := range order {
		reversed[len(order)-1-i] = name
	}
	archiveOf := func(t *testing.T, dir string) []byte {
		t.Helper()
		var b bytes.Buffer
		if err := WriteSnapshot(&b, dir); err != nil {
			t.Fatalf("WriteSnapshot(%s): %v", dir, err)
		}
		return b.Bytes()
	}
	want := archiveOf(t, lay(t, order, 0o644, false))
	for _, tc := range []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{"the same tree written again", func(t *testing.T) string { return lay(t, order, 0o644, false) }},
		{"the files created in the opposite order", func(t *testing.T) string { return lay(t, reversed, 0o644, true) }},
		{"other permission bits and times", func(t *testing.T) string { return lay(t, order, 0o755, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveOf(t, tc.dir(t)); !bytes.Equal(got, want) {
				t.Errorf("the archive differs from the first one's (%d bytes against %d)", len(got), len(want))
			}
		})
	}
}

// TestASnapshotRefusesWhatItCannotCarry writes the archive of trees each holding
// one thing the layout cannot carry, and asserts the writer refuses it naming the
// file, rather than packing it or quietly leaving it out: a link, a name holding a
// character other than printable ASCII, and a name of one path segment longer
// than 100 bytes, which no split into prefix and name can hold. A tree holding
// none of them is written.
func TestASnapshotRefusesWhatItCannotCarry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		lay     func(t *testing.T, dir string)
		refused string
	}{
		{
			name: "a tree holding no such file",
			lay:  func(t *testing.T, dir string) { writeSnapshotFile(t, dir, "a/b.yaml") },
		},
		{
			name: "a link",
			lay: func(t *testing.T, dir string) {
				writeSnapshotFile(t, dir, "a/b.yaml")
				if err := os.Symlink(filepath.Join(dir, "a", "b.yaml"), filepath.Join(dir, "a", "link.yaml")); err != nil {
					t.Skipf("this file system makes no links: %v", err)
				}
			},
			refused: "link.yaml",
		},
		{
			name:    "a name holding a character other than printable ASCII",
			lay:     func(t *testing.T, dir string) { writeSnapshotFile(t, dir, "a/café.yaml") },
			refused: "café.yaml",
		},
		{
			name:    "a name no split into prefix and name can hold",
			lay:     func(t *testing.T, dir string) { writeSnapshotFile(t, dir, "a/"+strings.Repeat("n", 101)) },
			refused: strings.Repeat("n", 101),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tc.lay(t, dir)
			var b bytes.Buffer
			err := WriteSnapshotTar(&b, dir)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("WriteSnapshotTar refused a tree it can carry: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("WriteSnapshotTar wrote a tree holding %s", tc.refused)
			}
			if !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("the refusal does not name %s: %v", tc.refused, err)
			}
		})
	}
}

// writeSnapshotFile writes a small file at the slash-separated name beneath dir.
func writeSnapshotFile(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("kind: Component\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
