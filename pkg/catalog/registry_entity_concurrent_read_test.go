// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// concurrentComponentDoc is a Component published as its document alone, the shape
// whose concurrent resolution from one pinned slot the per-request views exercise.
const concurrentComponentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/compute/shared
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: hello }
`

// concurrentSlotRow is one kind of pinned slot the concurrent-read tests drive: how
// it is published, how a view resolves it, and which laid-out files a reader of it
// goes on reading after the resolution.
type concurrentSlotRow struct {
	name string
	uri  string
	kind string
	// publish writes the entity into dir and returns its document's path.
	publish func(t *testing.T, dir string) string
	// resolve resolves the entity through view and returns the directory a reader
	// reads from and the files it reads there, relative to that directory. tree is
	// the slot's layout for the pinned artifact. It reports false when the view does
	// not resolve the entity; it calls nothing on a test, so readers and resolvers
	// running on their own goroutines can call it.
	resolve func(view *LazyLookup, tree string) (string, map[string]string, bool)
}

var concurrentSlotRows = []concurrentSlotRow{
	{
		name: "recipe carrying a chart",
		uri:  "helm/carried:1.0.0",
		kind: "Recipe",
		publish: func(t *testing.T, dir string) string {
			writeTree(t, dir, carriedChart)
			writeTree(t, dir, map[string]string{"recipe.yaml": carriedRecipeDoc})
			return filepath.Join(dir, "recipe.yaml")
		},
		resolve: func(view *LazyLookup, _ string) (string, map[string]string, bool) {
			r, ok := view.RecipeByURI("helm/carried:1.0.0")
			source, isString := r.Spec.Source.(string)
			if !ok || !isString {
				return "", nil, false
			}
			files := map[string]string{}
			for rel, body := range carriedChart {
				files[strings.TrimPrefix(rel, "chart/")] = body
			}
			return source, files, true
		},
	},
	{
		name: "component published as its document",
		uri:  "acme/compute/shared:1.0.0",
		kind: "Component",
		publish: func(t *testing.T, dir string) string {
			writeTree(t, dir, map[string]string{"component.yaml": concurrentComponentDoc})
			return filepath.Join(dir, "component.yaml")
		},
		resolve: func(view *LazyLookup, tree string) (string, map[string]string, bool) {
			c, ok := view.Component("acme/compute/shared:1.0.0")
			if !ok || c.Metadata.Version != "1.0.0" {
				return "", nil, false
			}
			return tree, map[string]string{oci.SingleDocumentFileName: concurrentComponentDoc}, true
		},
	},
}

// TestConcurrentReadsOfOnePinnedSlotNeverMeetAMissingLayout drives one pinned entity
// slot from many per-request views at once, the way concurrent requests read one
// workspace cache. Two properties are checked, and the first fails deterministically
// on a read that empties the layout and lays it out again:
//
//   - A complete resolution by a second view leaves every file the first view's
//     resolution laid out in place, as the same file and never rewritten, while the
//     first view holds it open: a read that already finds the artifact's bytes writes
//     nothing, so a view still reading its layout — a Recipe's render reads its chart
//     well after the resolution that laid it out — is never disturbed by another
//     view's read.
//   - Views that go on reading the layout they resolved while other views resolve the
//     same slot always find every file whole, and every one of those resolutions
//     succeeds.
func TestConcurrentReadsOfOnePinnedSlotNeverMeetAMissingLayout(t *testing.T) {
	for _, row := range concurrentSlotRows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			pin := seedPinnedSlot(t, root, row.kind, row.publish(t, t.TempDir()), row.uri, "1.0.0")
			pins := map[string]Pin{row.uri: pin}
			tree := sourcecache.ExtractedTree(oci.CachePath(root, layoutCacheDir, row.uri, "1.0.0"), pin.Digest)
			session := NewLazyLookup(root, layoutCacheDir, nil, nil)

			dir, files, ok := row.resolve(session.ForRequest(pins), tree)
			if !ok {
				t.Fatalf("the first view did not resolve the pinned %s", row.kind)
			}
			held := holdLaidOut(t, dir, files)
			if _, _, ok := row.resolve(session.ForRequest(pins), tree); !ok {
				t.Fatalf("a second view did not resolve the pinned %s", row.kind)
			}
			for _, problem := range stillHeld(held) {
				t.Error(problem)
			}

			const readers, resolvers, passes = 8, 8, 20
			var (
				mu       sync.Mutex
				problems []string
				ready    sync.WaitGroup
				done     sync.WaitGroup
			)
			report := func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				problems = append(problems, fmt.Sprintf(format, args...))
			}
			release := make(chan struct{})
			ready.Add(readers)
			done.Add(readers + resolvers)
			for r := 0; r < readers; r++ {
				go func() {
					defer done.Done()
					dir, files, ok := row.resolve(session.ForRequest(pins), tree)
					ready.Done()
					if !ok {
						report("reader: a view did not resolve the pinned %s", row.kind)
						return
					}
					<-release
					for i := 0; i < passes; i++ {
						for rel, body := range files {
							got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
							if err != nil {
								report("reader: %s in the layout it resolved could not be read while other views resolved the slot: %v", rel, err)
								continue
							}
							if string(got) != body {
								report("reader: %s in the layout it resolved held %q while other views resolved the slot", rel, got)
							}
						}
					}
				}()
			}
			for r := 0; r < resolvers; r++ {
				go func() {
					defer done.Done()
					<-release
					for i := 0; i < passes; i++ {
						if _, _, ok := row.resolve(session.ForRequest(pins), tree); !ok {
							report("resolver: a view did not resolve the pinned %s while other views read the slot", row.kind)
						}
					}
				}()
			}
			ready.Wait()
			close(release)
			done.Wait()
			for _, p := range problems {
				t.Error(p)
			}
		})
	}
}

// TestPinnedSlotReplacedUnderAReaderKeepsTheReadersLayout replaces a pinned slot's
// artifact — a re-pull of the same version after the registry moved its tag — while a
// view that resolved the previous artifact is still reading its layout. The second
// artifact is laid out in a tree of its own, so the first view's layout keeps the files
// its pin vouched for, as the same files, and never picks up the second artifact's. A
// read that lays every artifact of a slot out in one place fails here whatever its
// timing: the first view's files are replaced by the second artifact's.
func TestPinnedSlotReplacedUnderAReaderKeepsTheReadersLayout(t *testing.T) {
	t.Parallel()
	const uri = "helm/carried:1.0.0"
	root := t.TempDir()
	session := NewLazyLookup(root, layoutCacheDir, nil, nil)
	resolveSource := func(pin Pin) (string, bool) {
		r, ok := session.ForRequest(map[string]Pin{uri: pin}).RecipeByURI(uri)
		source, isString := r.Spec.Source.(string)
		return source, ok && isString
	}

	first := t.TempDir()
	writeTree(t, first, carriedChart)
	writeTree(t, first, map[string]string{
		"recipe.yaml":                        carriedRecipeDoc,
		"chart/templates/only-in-first.yaml": "kind: ConfigMap\n",
	})
	firstPin := seedPinnedSlot(t, root, "Recipe", filepath.Join(first, "recipe.yaml"), uri, "1.0.0")
	firstSource, ok := resolveSource(firstPin)
	if !ok {
		t.Fatal("the first artifact did not resolve")
	}
	held := holdLaidOut(t, firstSource, map[string]string{
		"Chart.yaml":                   carriedChart["chart/Chart.yaml"],
		"templates/banner.yaml":        carriedChart["chart/templates/banner.yaml"],
		"templates/only-in-first.yaml": "kind: ConfigMap\n",
	})

	second := t.TempDir()
	writeTree(t, second, map[string]string{
		"recipe.yaml":                         carriedRecipeDoc,
		"chart/Chart.yaml":                    carriedChart["chart/Chart.yaml"],
		"chart/templates/banner.yaml":         "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: carried\ndata:\n  carried-by: the-replacement-chart\n",
		"chart/templates/only-in-second.yaml": "kind: Secret\n",
	})
	secondPin := seedPinnedSlot(t, root, "Recipe", filepath.Join(second, "recipe.yaml"), uri, "1.0.0")
	if secondPin.Digest == firstPin.Digest {
		t.Fatal("the replacement artifact has the first one's digest; the test replaces nothing")
	}
	secondSource, ok := resolveSource(secondPin)
	if !ok {
		t.Fatal("the replacement artifact did not resolve")
	}

	if secondSource == firstSource {
		t.Errorf("both artifacts were laid out at %q; a reader of the first would read the second", firstSource)
	}
	for _, problem := range stillHeld(held) {
		t.Error(problem)
	}
	if _, err := os.Stat(filepath.Join(firstSource, "templates", "only-in-second.yaml")); !os.IsNotExist(err) {
		t.Errorf("the first artifact's layout picked up a file only the replacement carries (stat err = %v)", err)
	}
	got, err := os.ReadFile(filepath.Join(secondSource, "templates", "banner.yaml"))
	if err != nil || !strings.Contains(string(got), "the-replacement-chart") {
		t.Errorf("the replacement's layout does not hold the replacement chart: %q (err %v)", got, err)
	}
}

// laidOutSentinel is the modification time a held file is stamped with. No write of
// the file keeps it, so finding it afterwards shows the file was not rewritten.
var laidOutSentinel = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// laidOutFile is a laid-out file a reader holds open, stamped with laidOutSentinel, so
// a later check can tell whether the same file is still in place.
//
// Holding the file open is what makes the identity check discriminating: a file
// removed and written again can otherwise reuse the removed file's identity on
// filesystems that recycle it at once, and compare equal to it. An open file's
// identity cannot be recycled while it is held; on a platform that refuses to remove
// an open file, a read that tries to fails instead.
type laidOutFile struct {
	path string
	body string
	info os.FileInfo
}

// holdLaidOut stamps, opens and records every file of files inside dir as it stands
// now. The files stay open until the test ends.
func holdLaidOut(t *testing.T, dir string, files map[string]string) []laidOutFile {
	t.Helper()
	out := make([]laidOutFile, 0, len(files))
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Chtimes(p, laidOutSentinel, laidOutSentinel); err != nil {
			t.Fatalf("%s cannot be stamped: %v", rel, err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("%s is missing from the layout: %v", rel, err)
		}
		t.Cleanup(func() { _ = f.Close() })
		fi, err := f.Stat()
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		out = append(out, laidOutFile{path: p, body: body, info: fi})
	}
	return out
}

// stillHeld reports every held file that is gone, was replaced by another file, was
// rewritten, or no longer holds its bytes.
func stillHeld(held []laidOutFile) []string {
	var problems []string
	for _, h := range held {
		now, err := os.Lstat(h.path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s was removed by a later read of the same slot: %v", h.path, err))
			continue
		}
		if !os.SameFile(h.info, now) {
			problems = append(problems, fmt.Sprintf("%s was replaced by a later read of the same slot although it already held the artifact's bytes", h.path))
		}
		if !now.ModTime().Equal(laidOutSentinel) {
			problems = append(problems, fmt.Sprintf("%s was rewritten by a later read of the same slot although it already held the artifact's bytes (modified %v)", h.path, now.ModTime()))
		}
		if got, err := os.ReadFile(h.path); err != nil || string(got) != h.body {
			problems = append(problems, fmt.Sprintf("%s holds %q after a later read (err %v); want %q", h.path, got, err, h.body))
		}
	}
	return problems
}
