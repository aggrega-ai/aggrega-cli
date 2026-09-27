// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// followedTooFar is what pagingSource returns once it has served more pages than
// any row below needs. It is the fixture's own liveness bound: a walk that keeps
// following a listing that does not advance meets it instead of running for ever,
// so a missing guard fails a row rather than hanging the run.
var followedTooFar = errors.New("the listing was followed past the fixture's bound; the walk did not stop on its own")

const pagingSourceBound = 200

// pagingSource is an in-memory index source whose repository listing is served
// page by page, the way the registry client hands pages to the enumeration. With
// cycle set, the listing starts again from its first page after its last, which
// is the shape of a registry whose next link leads back to where it has been.
//
// It counts every repository it is asked to open, so a row can assert which
// repositories the walk read and that none was read twice.
type pagingSource struct {
	pages   [][]string
	cycle   bool
	targets map[string]oras.ReadOnlyTarget
	opened  map[string]int
	served  int
	// onPage, when set, runs after each page is handed over; onOpen before each
	// repository is opened. A stop row uses them to withdraw the walk at a chosen
	// point.
	onPage func()
	onOpen func()
}

func (p *pagingSource) Repositories(ctx context.Context, _ string, fn func(repos []string) error) error {
	for i := 0; ; i++ {
		if i >= len(p.pages) {
			if !p.cycle || len(p.pages) == 0 {
				return nil
			}
		}
		if p.served >= pagingSourceBound {
			return followedTooFar
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		page := p.pages[i%len(p.pages)]
		p.served++
		if err := fn(slices.Clone(page)); err != nil {
			return err
		}
		if p.onPage != nil {
			p.onPage()
		}
	}
}

func (p *pagingSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	if p.onOpen != nil {
		p.onOpen()
	}
	p.opened[repo]++
	tgt, ok := p.targets[repo]
	if !ok {
		return nil, fmt.Errorf("no repository %s in the fixture", repo)
	}
	return tgt, nil
}

func (p *pagingSource) ConditionalTags(ctx context.Context, repo, _ string) ([]string, string, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, false, err
	}
	tgt, ok := p.targets[repo].(*taggedStore)
	if !ok {
		return nil, "", false, false, fmt.Errorf("no repository %s in the fixture", repo)
	}
	return slices.Clone(tgt.tags), "", false, false, nil
}

// taggedStore is an in-memory repository that also lists its own tags, which the
// bare in-memory store cannot.
type taggedStore struct {
	*memory.Store
	tags []string
}

func (s *taggedStore) Tags(_ context.Context, _ string, fn func(tags []string) error) error {
	return fn(s.tags)
}

// publishedComponent packs one component version through the publish path's own
// packer, so the walk reads back a manifest shaped the way a publication shapes
// one.
func publishedComponent(t *testing.T, repo string) *taggedStore {
	t.Helper()
	store := &taggedStore{Store: memory.New(), tags: []string{"1.0.0"}}
	ann, err := oci.SearchableMetadata{Kind: "Component", Description: repo}.Annotations()
	if err != nil {
		t.Fatalf("annotations for %s: %v", repo, err)
	}
	if _, err := oci.Push(context.Background(), store.Store, "1.0.0",
		"application/vnd.aggrega.component.v1alpha1+yaml", []byte("# "+repo+"\n"), ann); err != nil {
		t.Fatalf("pack %s: %v", repo, err)
	}
	return store
}

// newPagingSource builds a source serving pages, with a published component
// behind every repository name the pages carry.
func newPagingSource(t *testing.T, pages [][]string, cycle bool) *pagingSource {
	t.Helper()
	src := &pagingSource{pages: pages, cycle: cycle, targets: map[string]oras.ReadOnlyTarget{}, opened: map[string]int{}}
	for _, page := range pages {
		for _, repo := range page {
			if _, ok := src.targets[repo]; !ok {
				src.targets[repo] = publishedComponent(t, repo)
			}
		}
	}
	return src
}

// listingRows is the enumeration's table: each row is a listing a registry can
// serve and exactly what the walk must make of it.
//
// A row either names the repositories the walk returns (want, in ascending
// order, each once) or the repository the refusal names as delivered a second
// time (refusedAt; "" with refused set for a listing whose pages name nothing).
var listingRows = []struct {
	name      string
	pages     [][]string
	cycle     bool
	want      []string
	refused   bool
	refusedAt string
}{
	{
		name:  "a listing that advances is read to its end",
		pages: [][]string{{"component/core/api", "component/core/web"}, {"recipe/helm/api"}},
		want:  []string{"component/core/api", "component/core/web", "recipe/helm/api"},
	},
	{
		name:      "a listing that hands back the same page for ever is refused at the first repeat",
		pages:     [][]string{{"component/core/api", "component/core/web"}},
		cycle:     true,
		refused:   true,
		refusedAt: "component/core/api",
	},
	{
		name:      "a listing that repeats one repository on a later page is refused at it",
		pages:     [][]string{{"component/core/api", "component/core/web"}, {"component/core/web", "recipe/helm/api"}},
		refused:   true,
		refusedAt: "component/core/web",
	},
	{
		name:      "a full page then an empty page whose link starts the listing again is refused",
		pages:     [][]string{{"component/core/api", "recipe/helm/api"}, {}},
		cycle:     true,
		refused:   true,
		refusedAt: "component/core/api",
	},
	{
		name:    "empty pages that link onward for ever are refused",
		pages:   [][]string{{}},
		cycle:   true,
		refused: true,
	},
	{
		name:      "a repository named twice on one page is refused at it",
		pages:     [][]string{{"component/core/api", "component/core/api"}},
		refused:   true,
		refusedAt: "component/core/api",
	},
	{
		name:  "an empty registry answers one empty page and is not refused",
		pages: [][]string{{}},
		want:  nil,
	},
	{
		name:  "one empty page followed by more repositories is not refused",
		pages: [][]string{{"component/core/api"}, {}, {"recipe/helm/api"}},
		want:  []string{"component/core/api", "recipe/helm/api"},
	},
}

// checkRefusalText asserts msg is the listing refusal's sentence, naming what
// the row says the listing repeated. It reads text because the host's own error
// travels as text on the walk's result; the enumeration's table additionally
// matches the error itself.
func checkRefusalText(t *testing.T, msg, refusedAt string) {
	t.Helper()
	if !strings.HasPrefix(msg, errRepositoryListingRepeated.Error()) {
		t.Fatalf("want the repeated-listing refusal; got %q", msg)
	}
	if refusedAt != "" && !strings.Contains(msg, "named "+refusedAt+" a second time") {
		t.Errorf("the refusal must name the repository delivered twice (%s); got %q", refusedAt, msg)
	}
	if refusedAt == "" && !strings.Contains(msg, "two pages in a row named no repository") {
		t.Errorf("the refusal must say the listing named nothing twice in a row; got %q", msg)
	}
	if !strings.Contains(msg, "the walk stopped rather than follow the listing round again") {
		t.Errorf("the refusal must say the walk stopped rather than loop; got %q", msg)
	}
}

// TestTheEnumerationEndsAListingThatDoesNotAdvance is the enumeration's table,
// driven through the primitive every caller shares.
//
// A planted defect makes the refusing rows fail: with the repeat check removed
// from EnumerateRepositories, the cycling rows run into the fixture's own bound,
// the enumeration returns the repeated names as a result, and each row asserting
// a refusal reports a nil error and a list with duplicates.
func TestTheEnumerationEndsAListingThatDoesNotAdvance(t *testing.T) {
	t.Parallel()
	for _, row := range listingRows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			src := newPagingSource(t, row.pages, row.cycle)
			got, err := EnumerateRepositories(t.Context(), src, "", CatalogScope)
			if row.refused {
				if !errors.Is(err, errRepositoryListingRepeated) {
					t.Fatalf("want the repeated-listing refusal; got %v", err)
				}
				checkRefusalText(t, err.Error(), row.refusedAt)
				if got != nil {
					t.Errorf("a refused listing yields no repositories; got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnumerateRepositories: %v", err)
			}
			if !slices.Equal(got, row.want) {
				t.Errorf("repositories = %v; want %v", got, row.want)
			}
		})
	}
}

// indexSnapshot reads every file a host's index consists of — the per-repository
// records and the freshness sibling — keyed by path.
func indexSnapshot(t *testing.T, root, cacheDir, host string) map[string]string {
	t.Helper()
	base := oci.IndexBasePath(root, cacheDir, host)
	out := map[string]string{}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read the index directory of %s: %v", host, err)
	}
	for _, e := range entries {
		data, rerr := os.ReadFile(filepath.Join(base, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		out[e.Name()] = string(data)
	}
	stamp, err := os.ReadFile(base + ".updated")
	if err != nil {
		t.Fatalf("read the freshness sibling of %s: %v", host, err)
	}
	out[".updated"] = string(stamp)
	return out
}

// writePriorIndex lays down an earlier index for host through the product's own
// writer, holding one record the listing under test does not carry, so a later
// write is visible as that record disappearing.
func writePriorIndex(t *testing.T, root, cacheDir, host string) map[string]string {
	t.Helper()
	prior := HostIndex{Host: host, Records: []registryindex.Record{{
		URI:     "oci://" + host + "/component/core/earlier",
		Name:    "component/core/earlier",
		Version: "1.0.0",
		Kind:    "Component",
	}}}
	if err := WriteHostIndex(root, cacheDir, prior, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatalf("write the earlier index: %v", err)
	}
	return indexSnapshot(t, root, cacheDir, host)
}

// withIndexSources points the index-target seam at one source per host for the
// duration of the test.
func withIndexSources(t *testing.T, sources map[string]IndexSource) {
	t.Helper()
	prev := IndexTarget
	IndexTarget = func(_ workspaceconfig.Config, host string) (IndexSource, error) {
		src, ok := sources[host]
		if !ok {
			return nil, fmt.Errorf("no source for %s in the fixture", host)
		}
		return src, nil
	}
	t.Cleanup(func() { IndexTarget = prev })
}

// TestTheIndexWalkRefusesAListingThatDoesNotAdvanceAndKeepsItsIndex drives the
// same table through the index build, over the index-source seam, with a second,
// well-behaved registry beside the one under test.
//
// For a listing that advances, every repository is read exactly once and the
// index is written. For one that does not, the registry is named in the failed
// list with the refusal as its own error, not one repository is read, its
// earlier index is left byte for byte as it was, and the other registry is still
// indexed.
//
// Two planted defects make it fail: with the repeat check removed, the cycling
// rows read the same repositories over and over and write an index; with the
// refusal no longer returned before the write, the earlier index is replaced.
func TestTheIndexWalkRefusesAListingThatDoesNotAdvanceAndKeepsItsIndex(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const listing = "listing.example.test"
	const steady = "steady.example.test"

	for _, row := range listingRows {
		t.Run(row.name, func(t *testing.T) {
			src := newPagingSource(t, row.pages, row.cycle)
			steadySrc := newPagingSource(t, [][]string{{"component/core/steady"}}, false)
			withIndexSources(t, map[string]IndexSource{listing: src, steady: steadySrc})

			rw := indexProbeWorkspace(t, listing, steady)
			root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
			before := writePriorIndex(t, root, cacheDir, listing)

			res, err := Update(t.Context(), rw, UpdateOptions{Now: func() time.Time {
				return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
			}}, io.Discard)

			var listed, other *HostUpdate
			for i := range res.Hosts {
				switch res.Hosts[i].Host {
				case listing:
					listed = &res.Hosts[i]
				case steady:
					other = &res.Hosts[i]
				}
			}
			if listed == nil || other == nil {
				t.Fatalf("both registries must be reported; got %+v", res.Hosts)
			}
			if other.Err != "" || other.Indexed != 1 {
				t.Errorf("the well-behaved registry must still be indexed; got %+v", *other)
			}

			if row.refused {
				if err == nil {
					t.Error("a walk with a refused registry must return an error, so the exit status is non-zero")
				}
				if !slices.Equal(res.Failed, []string{listing}) {
					t.Errorf("failed = %v; want exactly [%s]", res.Failed, listing)
				}
				checkRefusalText(t, listed.Err, row.refusedAt)
				if len(src.opened) != 0 {
					t.Errorf("a refused registry has no repository read at all; opened %v", src.opened)
				}
				if after := indexSnapshot(t, root, cacheDir, listing); !mapsEqual(after, before) {
					t.Errorf("the refused registry's earlier index changed:\nbefore %v\nafter  %v", before, after)
				}
				return
			}

			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if listed.Err != "" || len(res.Failed) != 0 {
				t.Fatalf("a listing that advances must be indexed; got %+v, failed %v", *listed, res.Failed)
			}
			var opened []string
			for repo, n := range src.opened {
				if n != 1 {
					t.Errorf("repository %s was read %d times; want once", repo, n)
				}
				opened = append(opened, repo)
			}
			slices.Sort(opened)
			if !slices.Equal(opened, row.want) {
				t.Errorf("repositories read = %v; want %v", opened, row.want)
			}
			if listed.Indexed != len(row.want) {
				t.Errorf("indexed = %d; want %d", listed.Indexed, len(row.want))
			}
		})
	}
}

// TestAStoppedIndexWalkKeepsTheEarlierIndex withdraws the walk at the two points
// where a stop can land inside one registry — during the listing, and during the
// repository reads after it — and asserts the registry's earlier index is left
// byte for byte, the walk reports the stop, and the registry is reported as not
// indexed.
//
// Planted defects make the rows fail: with the stop no longer checked before the
// write, the reads row writes what the stop cut short as the registry's index and
// the earlier record disappears; with the enumeration handing back what it had
// gathered when the stop landed, the listing row goes on to open repositories it
// was never meant to reach.
func TestAStoppedIndexWalkKeepsTheEarlierIndex(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const host = "stopped.example.test"

	for _, row := range []struct {
		name string
		arm  func(src *pagingSource, stop context.CancelFunc)
		// reads says whether the stop lands after repository reads began; a stop
		// during the listing must leave every repository unopened.
		reads bool
	}{
		{
			name: "a stop during the listing",
			arm: func(src *pagingSource, stop context.CancelFunc) {
				src.onPage = stop
			},
		},
		{
			name: "a stop during the repository reads",
			arm: func(src *pagingSource, stop context.CancelFunc) {
				src.onOpen = stop
			},
			reads: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			src := newPagingSource(t, [][]string{{"component/core/api", "component/core/web"}, {"recipe/helm/api"}}, false)
			withIndexSources(t, map[string]IndexSource{host: src})

			rw := indexProbeWorkspace(t, host)
			root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
			before := writePriorIndex(t, root, cacheDir, host)

			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			row.arm(src, stop)

			res, err := Update(ctx, rw, UpdateOptions{}, io.Discard)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("a stopped walk must report the stop; got %v", err)
			}
			if len(res.Hosts) != 1 || res.Hosts[0].Err == "" {
				t.Errorf("the registry being walked when the stop landed must be reported as not indexed; got %+v", res.Hosts)
			}
			if row.reads && len(src.opened) == 0 {
				t.Fatal("the fixture never reached the repository reads, so this row does not exercise a stop there")
			}
			if !row.reads && len(src.opened) != 0 {
				t.Errorf("a walk stopped during the listing must open no repository; opened %v", src.opened)
			}
			if after := indexSnapshot(t, root, cacheDir, host); !mapsEqual(after, before) {
				t.Errorf("the stopped registry's earlier index changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
