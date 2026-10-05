// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// layoutChart is the archive content the layout tests lay out: files at two depths,
// so a row can plant at a file's place, a directory's place and beside both.
var layoutChart = map[string]string{
	"chart/Chart.yaml":           "apiVersion: v2\nname: layout\nversion: 1.0.0\n",
	"chart/values.yaml":          "replicas: 1\n",
	"chart/templates/a.yaml":     "kind: ConfigMap\n",
	"chart/templates/b.yaml":     "kind: Secret\n",
	"chart/templates/sub/c.yaml": "kind: Service\n",
}

// TestRegenerateExtractedDiscardsWhatWasPlanted is the planted-content matrix: each row
// changes the archive's tree one way by hand, and the next regeneration leaves the tree
// holding exactly the archive's files again, as regular files in real directories, with
// nothing written through a planted link. The equal-size row is the one a comparison by
// size alone would pass, and the link rows are the ones a comparison following links
// would pass.
func TestRegenerateExtractedDiscardsWhatWasPlanted(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		plant func(t *testing.T, tree, outside string)
	}{
		{"a file edited to other bytes of the same size", func(t *testing.T, tree, _ string) {
			mustWrite(t, filepath.Join(tree, "chart", "values.yaml"), "replicas: 9\n")
		}},
		{"a file edited to a different size", func(t *testing.T, tree, _ string) {
			mustWrite(t, filepath.Join(tree, "chart", "templates", "a.yaml"), "kind: ConfigMap\nplanted: true\n")
		}},
		{"a file added beside the carried ones", func(t *testing.T, tree, _ string) {
			mustWrite(t, filepath.Join(tree, "chart", "templates", "planted.yaml"), "kind: Planted\n")
		}},
		{"a directory of files added", func(t *testing.T, tree, _ string) {
			mustWrite(t, filepath.Join(tree, "planted", "deep", "x.yaml"), "kind: Planted\n")
		}},
		{"a carried file removed", func(t *testing.T, tree, _ string) {
			if err := os.Remove(filepath.Join(tree, "chart", "templates", "b.yaml")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a directory where a carried file belongs", func(t *testing.T, tree, _ string) {
			p := filepath.Join(tree, "chart", "values.yaml")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(p, "inner.yaml"), "kind: Planted\n")
		}},
		{"a file where a carried directory belongs", func(t *testing.T, tree, _ string) {
			p := filepath.Join(tree, "chart", "templates", "sub")
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, p, "kind: Planted\n")
		}},
		{"a link to identical bytes where a carried file belongs", func(t *testing.T, tree, outside string) {
			target := filepath.Join(outside, "values.yaml")
			mustWrite(t, target, layoutChart["chart/values.yaml"])
			p := filepath.Join(tree, "chart", "values.yaml")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, target, p)
		}},
		{"a link to an identical directory where a carried directory belongs", func(t *testing.T, tree, outside string) {
			for _, rel := range []string{"a.yaml", "b.yaml", "sub/c.yaml"} {
				mustWrite(t, filepath.Join(outside, "templates", filepath.FromSlash(rel)), layoutChart["chart/templates/"+rel])
			}
			p := filepath.Join(tree, "chart", "templates")
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, filepath.Join(outside, "templates"), p)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			slot := t.TempDir()
			outside := t.TempDir()
			archive := buildTGZ(t, layoutChart)
			tree, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if err != nil {
				t.Fatalf("first regeneration: %v", err)
			}
			row.plant(t, tree, outside)
			before := treeFiles(t, outside)

			again, err := sourcecache.RegenerateExtracted("", "", slot, archive)
			if err != nil {
				t.Fatalf("regeneration after the plant: %v", err)
			}
			if again != tree {
				t.Fatalf("the regeneration moved the tree from %q to %q", tree, again)
			}
			assertTreeIs(t, tree, layoutChart)
			if after := treeFiles(t, outside); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Errorf("the regeneration wrote through a planted link: outside the tree %v became %v", before, after)
			}
		})
	}
}

// TestConcurrentReadsOfOneSourceSlotNeverMeetAMissingTree drives one source-artifact
// slot through its build-time read from many readers at once. Two properties are
// checked, and the first fails deterministically on a read that empties the tree and
// extracts it again:
//
//   - A complete read of the slot leaves every file the previous read laid out in
//     place, as the same file and never rewritten, while a reader holds it open: a
//     read that already finds the archive's bytes writes nothing, so a reader holding
//     the tree is never disturbed by another read.
//   - Readers that go on reading the tree they were handed while other readers read
//     the same slot again always find every file whole. This is the overlap a Recipe's
//     render has with a later read of the same slot, and the shape that fails when a
//     rebuild opens a window.
func TestConcurrentReadsOfOneSourceSlotNeverMeetAMissingTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const cacheDir = ".aggrega/cache"
	blob := buildTGZ(t, layoutChart)
	digest := digestOf(blob)
	seedSlot(t, root, cacheDir, "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)
	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", digest)
	read := func() (string, error) {
		return sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "", "", "1.0.0", "", root, cacheDir)
	}

	first, err := read()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	held := holdFiles(t, first, layoutChart)
	second, err := read()
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second != first {
		t.Fatalf("a second read of the same slot handed back %q; the first was handed %q", second, first)
	}
	assertStillHeld(t, held)

	const readers, rebuilders, passes = 8, 8, 25
	failures := hammerOneSlot(t, readers, rebuilders, passes, read, layoutChart)
	for _, f := range failures {
		t.Error(f)
	}
}

// hammerOneSlot starts readers that each read the slot once and then read every file
// of the tree they were handed, passes times; once every reader holds its tree,
// rebuilders read the same slot again, passes times each, while the readers read. It
// returns every failure, one line each.
func hammerOneSlot(t *testing.T, readers, rebuilders, passes int, read func() (string, error), want map[string]string) []string {
	t.Helper()
	var (
		mu       sync.Mutex
		failures []string
		held     sync.WaitGroup
		done     sync.WaitGroup
	)
	fail := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, fmt.Sprintf(format, args...))
	}
	release := make(chan struct{})
	held.Add(readers)
	done.Add(readers + rebuilders)
	for r := 0; r < readers; r++ {
		go func() {
			defer done.Done()
			tree, err := read()
			held.Done()
			if err != nil {
				fail("reader: the read itself failed: %v", err)
				return
			}
			<-release
			for i := 0; i < passes; i++ {
				for rel, body := range want {
					got, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(rel)))
					if err != nil {
						fail("reader: %s in the tree it was handed could not be read while another read ran: %v", rel, err)
						continue
					}
					if string(got) != body {
						fail("reader: %s in the tree it was handed held %q while another read ran", rel, got)
					}
				}
			}
		}()
	}
	for b := 0; b < rebuilders; b++ {
		go func() {
			defer done.Done()
			<-release
			for i := 0; i < passes; i++ {
				if _, err := read(); err != nil {
					fail("rebuilder: a read of the slot failed while other reads ran: %v", err)
				}
			}
		}()
	}
	held.Wait()
	close(release)
	done.Wait()
	return failures
}

// heldSentinel is the modification time a held file is stamped with. No write of the
// file keeps it, so finding it afterwards shows the file was not rewritten.
var heldSentinel = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// heldFile is a laid-out file a reader holds open, stamped with heldSentinel, so a
// later check can tell whether the same file is still in place.
//
// Holding the file open is what makes the identity check discriminating: a file
// removed and written again can otherwise reuse the removed file's identity on
// filesystems that recycle it at once, and compare equal to it. An open file's
// identity cannot be recycled while it is held; on a platform that refuses to remove
// an open file, a read that tries to fails instead.
type heldFile struct {
	path string
	body string
	info os.FileInfo
}

// holdFiles stamps, opens and records every file of want inside tree as it stands
// now. The files stay open until the test ends.
func holdFiles(t *testing.T, tree string, want map[string]string) []heldFile {
	t.Helper()
	out := make([]heldFile, 0, len(want))
	for rel, body := range want {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.Chtimes(p, heldSentinel, heldSentinel); err != nil {
			t.Fatalf("%s cannot be stamped: %v", rel, err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("%s is missing from the tree: %v", rel, err)
		}
		t.Cleanup(func() { _ = f.Close() })
		fi, err := f.Stat()
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		out = append(out, heldFile{path: p, body: body, info: fi})
	}
	return out
}

// assertStillHeld checks that each held file is still the same file, never rewritten,
// holding the same bytes.
func assertStillHeld(t *testing.T, held []heldFile) {
	t.Helper()
	for _, h := range held {
		now, err := os.Lstat(h.path)
		if err != nil {
			t.Errorf("%s was removed by a later read of the same slot: %v", h.path, err)
			continue
		}
		if !os.SameFile(h.info, now) {
			t.Errorf("%s was replaced by a later read of the same slot although it already held the archive's bytes", h.path)
		}
		if !now.ModTime().Equal(heldSentinel) {
			t.Errorf("%s was rewritten by a later read of the same slot although it already held the archive's bytes (modified %v)", h.path, now.ModTime())
		}
		if got, err := os.ReadFile(h.path); err != nil || string(got) != h.body {
			t.Errorf("%s holds %q after a later read (err %v); want %q", h.path, got, err, h.body)
		}
	}
}

// assertTreeIs checks that tree holds exactly want: every file, as a regular file with
// its bytes, and nothing else.
func assertTreeIs(t *testing.T, tree string, want map[string]string) {
	t.Helper()
	got := treeFiles(t, tree)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the tree holds\n  %v\nwant exactly\n  %v", got, want)
	}
}

// treeFiles returns every regular file below dir, keyed by its slash-separated
// place, with its content. A link or any other non-regular entry is reported under
// its place with a marker, so a test comparing against the files it expects sees it.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			out[rel] = "<not a regular file>"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustSymlink plants a link, skipping the row where the platform does not let this
// process create one.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform does not let the test create a link: %v", err)
	}
}
