// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// memTagTarget is an in-memory repository that can also list its own tags, which
// is the capability the tag read requires and the bare in-memory store does not
// provide.
type memTagTarget struct {
	*memory.Store
	tags []string
}

func (m *memTagTarget) Tags(_ context.Context, _ string, fn func(tags []string) error) error {
	return fn(m.tags)
}

func (m *memTagTarget) tagList() []string { return m.tags }

// fakeIndexSource serves a fixed repository set from memory, so an index refresh
// runs without a network.
type fakeIndexSource struct {
	repos   []string
	targets map[string]oras.ReadOnlyTarget
}

func (f fakeIndexSource) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	return fn(f.repos)
}

func (f fakeIndexSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	return f.targets[repo], nil
}

// ConditionalTags always answers with the current tag set and no validator, so
// every repository takes the full-read path. The conditional-revalidation
// behaviour is covered where the walk itself is; what this fixture is for is
// proving the refresh reaches disk.
func (f fakeIndexSource) ConditionalTags(_ context.Context, repo, _ string) ([]string, string, bool, bool, error) {
	tgt, ok := f.targets[repo]
	if !ok || tgt == nil {
		return nil, "", false, false, os.ErrNotExist
	}
	lister, ok := tgt.(interface{ tagList() []string })
	if !ok {
		return nil, "", false, false, os.ErrNotExist
	}
	return lister.tagList(), "", false, false, nil
}

// seedComponentRepo packs a real catalog Component manifest per tag with the
// searchable annotations a publish stamps, so the refresh reads back annotations
// the actual publish path produced rather than a hand-built map.
func seedComponentRepo(t *testing.T, tags []string, meta oci.SearchableMetadata) *memTagTarget {
	t.Helper()
	ctx := context.Background()
	target := &memTagTarget{Store: memory.New()}
	const mediaType = "application/vnd.aggrega.component.v1beta1+yaml"
	ann, err := meta.Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	for _, tag := range tags {
		// Vary content per tag so content-address dedupe does not collapse the
		// per-version manifests into one.
		if _, err := oci.Push(ctx, target.Store, tag, mediaType, []byte("# component "+tag+"\n"), ann); err != nil {
			t.Fatalf("Push(%q): %v", tag, err)
		}
		target.tags = append(target.tags, tag)
	}
	return target
}

// TestRegistryUpdateOnTheDaemonWritesAnIndexTheSearchThenReads is the
// invocation proof for the refresh, and it is deliberately end-to-end across two
// methods rather than an assertion about the first one's return value.
//
// Asserting the answer's record count would pass on a refresh that computed an
// index and wrote nothing, which is precisely the failure that matters: the next
// search would then answer from a snapshot that was never refreshed and nothing
// would say so. So the effect is read back two ways — from the file on disk,
// which is what every surface's search opens, and through the search method
// itself.
//
// The index root the answer reports is checked against where the file actually
// landed, because that field is this operation's whole scope statement: it is
// what says the refresh changed nothing outside this workspace.
func TestRegistryUpdateOnTheDaemonWritesAnIndexTheSearchThenReads(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const host = "hub.aggrega.test"

	labels := componentFixtureLabels()
	src := fakeIndexSource{
		repos: []string{"component/core/api-server"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/api-server": seedComponentRepo(t, []string{"1.0.0", "1.2.0", "1.1.0"}, oci.SearchableMetadata{
				Description: "Stateless HTTP API server",
				Category:    "compute",
				Kind:        "Component",
				Labels:      labels,
			}),
		},
	}
	prev := registryops.IndexTarget
	registryops.IndexTarget = func(workspaceconfig.Config, string) (registryops.IndexSource, error) { return src, nil }
	t.Cleanup(func() { registryops.IndexTarget = prev })

	srv, _, _ := registryWriteFixture(t, "workspace: {}\ndefault_registry: "+host+"\nregistries:\n  - host: "+host+"\n")

	resp := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
	var res registryUpdateResult
	decodeDaemonResult(t, resp, &res)

	if !res.RegistriesConfigured {
		t.Fatalf("the fixture configures one registry; got %+v", res)
	}
	if len(res.Hosts) != 1 || res.Hosts[0].Host != host {
		t.Fatalf("expected one host %q in the answer; got %+v", host, res.Hosts)
	}
	if res.Hosts[0].Indexed != 1 {
		t.Errorf("indexed: got %d, want 1", res.Hosts[0].Indexed)
	}
	if !res.ReachedRegistries {
		t.Error("the refresh must state that it reached registries; nothing else here does")
	}

	// The scope statement is a claim about a filesystem location, so it is checked
	// against the filesystem rather than against itself.
	if res.IndexRoot == "" {
		t.Fatal("the answer carries no index root, so it makes no scope statement at all")
	}
	if !filepath.IsAbs(res.IndexRoot) {
		t.Errorf("indexRoot must be a path a client can reason about; got %q", res.IndexRoot)
	}
	rel, err := filepath.Rel(srv.workspaceRoot, res.IndexRoot)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || (len(rel) > 2 && rel[:3] == "../") {
		t.Errorf("the refresh claims a workspace-local write, but %q is not inside %q", res.IndexRoot, srv.workspaceRoot)
	}
	entries, err := os.ReadDir(res.IndexRoot)
	if err != nil {
		t.Fatalf("the answer named an index root nothing was written to: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the refresh reported a record and wrote no file; a later search would answer from a stale snapshot")
	}

	// Read the effect back through the search method, which did not perform the
	// refresh and opens the files rather than sharing any state with it.
	srv.resolvedWorkspace = reresolve(t, srv)
	searchResp := callDaemonMethod(t, srv, "aggrega/search", map[string]any{})
	var found registrySearchResult
	decodeDaemonResult(t, searchResp, &found)
	if len(found.Records) != 1 {
		t.Fatalf("the search must find the entity the refresh recorded; got %d record(s): %+v", len(found.Records), found.Records)
	}
	if got := found.Records[0].Version; got != "1.2.0" {
		t.Errorf("version: got %q, want the highest strict-SemVer tag 1.2.0", got)
	}
	if got := found.Records[0].Description; got != "Stateless HTTP API server" {
		t.Errorf("description did not survive the refresh: got %q", got)
	}
}

// TestRegistryUpdateOnTheDaemonReportsWhatItCouldNotRead is the invocation proof
// for the skip account, and it is here because nothing anywhere exercised it.
//
// A repository the walk cannot read is skipped rather than fatal, so one
// unreadable repository does not abandon the index. That posture is only safe if
// the caller is TOLD, and on a surface with nobody watching the prose notice
// reaches no reader — so the skip has to travel as a value. This drives one
// unreadable repository alongside a readable one and asserts three things at
// once: the readable one was indexed, the unreadable one is named in the answer
// with a reason, and the walk did not fail.
//
// Asserting only the record count would pass on a walk that silently dropped the
// unreadable repository, which is the shape that makes a short index read as a
// complete one.
func TestRegistryUpdateOnTheDaemonReportsWhatItCouldNotRead(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const host = "partial.aggrega.test"

	src := failingRepoSource{
		fakeIndexSource: fakeIndexSource{
			repos: []string{"component/core/readable", "component/core/unreadable"},
			targets: map[string]oras.ReadOnlyTarget{
				"component/core/readable": seedComponentRepo(t, []string{"2.0.0"}, oci.SearchableMetadata{
					Description: "the repository that reads",
					Category:    "compute",
					Kind:        "Component",
					Labels:      componentFixtureLabels(),
				}),
			},
		},
		failing: "component/core/unreadable",
	}
	prev := registryops.IndexTarget
	registryops.IndexTarget = func(workspaceconfig.Config, string) (registryops.IndexSource, error) { return src, nil }
	t.Cleanup(func() { registryops.IndexTarget = prev })

	srv, _, _ := registryWriteFixture(t, "workspace: {}\ndefault_registry: "+host+"\nregistries:\n  - host: "+host+"\n")

	resp := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
	var res registryUpdateResult
	decodeDaemonResult(t, resp, &res)

	if len(res.Hosts) != 1 {
		t.Fatalf("expected one host in the answer; got %+v", res.Hosts)
	}
	hu := res.Hosts[0]
	if hu.Indexed != 1 {
		t.Errorf("the readable repository must still be indexed: indexed = %d, want 1", hu.Indexed)
	}
	if len(hu.Skipped) != 1 {
		t.Fatalf("the unreadable repository must be named in the answer; got skipped = %+v", hu.Skipped)
	}
	if hu.Skipped[0].Repo != "component/core/unreadable" {
		t.Errorf("skipped repo: got %q, want component/core/unreadable", hu.Skipped[0].Repo)
	}
	if hu.Skipped[0].Reason == "" {
		t.Error("a skip with no reason tells a caller nothing it can act on")
	}
	if len(res.Failed) != 0 {
		t.Errorf("one unreadable repository must not fail the host: failed = %v", res.Failed)
	}
	t.Logf("skipped: %+v", hu.Skipped)
}

// TestRegistryUpdateOnTheDaemonReusesAnUnchangedRepository drives the refresh
// twice against unchanged registry state and asserts the second run writes the
// same record and skips nothing.
//
// This fixture records no listing validator, so the second run lists the tags in
// full and compares the selected version's digest instead, keeping the facets it
// read before; it offers no referrer revalidation, so each version is read again.
// The two runs must still agree byte for byte.
func TestRegistryUpdateOnTheDaemonReusesAnUnchangedRepository(t *testing.T) {
	// Not parallel: it points the process-wide index-target seam.
	const host = "reuse.aggrega.test"

	src := fakeIndexSource{
		repos: []string{"component/core/stable"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/stable": seedComponentRepo(t, []string{"1.0.0", "1.1.0"}, oci.SearchableMetadata{
				Description: "unchanged between runs",
				Category:    "compute",
				Kind:        "Component",
				Labels:      componentFixtureLabels(),
			}),
		},
	}
	prev := registryops.IndexTarget
	registryops.IndexTarget = func(workspaceconfig.Config, string) (registryops.IndexSource, error) { return src, nil }
	t.Cleanup(func() { registryops.IndexTarget = prev })

	srv, _, _ := registryWriteFixture(t, "workspace: {}\ndefault_registry: "+host+"\nregistries:\n  - host: "+host+"\n")

	first := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
	var res1 registryUpdateResult
	decodeDaemonResult(t, first, &res1)
	if len(res1.Hosts) != 1 || res1.Hosts[0].Indexed != 1 {
		t.Fatalf("the first run must index the repository; got %+v", res1.Hosts)
	}
	firstFile := indexFileBytes(t, res1.IndexRoot)

	second := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
	var res2 registryUpdateResult
	decodeDaemonResult(t, second, &res2)
	if len(res2.Hosts) != 1 || res2.Hosts[0].Indexed != 1 {
		t.Fatalf("the second run must index the repository; got %+v", res2.Hosts)
	}
	if len(res2.Hosts[0].Skipped) != 0 {
		t.Errorf("nothing changed, so nothing may be skipped: %+v", res2.Hosts[0].Skipped)
	}

	// Byte-identical records across two runs against the same state is the
	// determinism the index writer's own contract states, and reuse is how it is
	// reached without re-reading. Comparing the bytes is what makes this a
	// statement about the file rather than about the answer.
	if got := indexFileBytes(t, res2.IndexRoot); got != firstFile {
		t.Errorf("two runs against unchanged state produced different records:\nfirst:\n%s\nsecond:\n%s", firstFile, got)
	}
}

// failingRepoSource is a source one of whose repositories cannot be opened, so
// the walk's skip-and-continue posture is exercised rather than described.
type failingRepoSource struct {
	fakeIndexSource
	failing string
}

func (f failingRepoSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	if repo == f.failing {
		return nil, errUnreadableRepository
	}
	return f.fakeIndexSource.Repository(repo)
}

func (f failingRepoSource) ConditionalTags(ctx context.Context, repo, ifNoneMatch string) ([]string, string, bool, bool, error) {
	if repo == f.failing {
		return nil, "", false, false, errUnreadableRepository
	}
	return f.fakeIndexSource.ConditionalTags(ctx, repo, ifNoneMatch)
}

// errUnreadableRepository stands in for whatever a registry returns for a
// repository this caller cannot read. The walk's posture does not depend on
// which failure it is, so the fixture states one and the assertion reads the
// reason rather than matching it.
var errUnreadableRepository = errors.New("this repository cannot be read")

// indexFileBytes reads the single per-repository index file under root, failing
// when the count is anything but one — a comparison over the wrong number of
// files would compare something other than the record.
func indexFileBytes(t *testing.T, root string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the index root: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one per-repository index file under %s; got %v", root, files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read the index file: %v", err)
	}
	return string(data)
}

// reresolve re-runs the workspace resolution the fixture built, so a method
// called after a configuration or cache write reads the tree as it now stands
// rather than as the session found it.
func reresolve(t *testing.T, srv *Server) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, err := workspaceconfig.Resolve(srv.workspaceRoot, srv.workspaceRoot, nil)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	return rw
}

// componentFixtureLabels is the authored label map the component fixture
// carries: a category label plus a non-category one, in author order. Both are
// needed for the fixture to be a shape the publish path can actually produce —
// the category facet is derived from the category label, so a manifest cannot
// carry the facet without carrying the map.
func componentFixtureLabels() *types.OrderedMap[string] {
	labels := types.NewOrderedMap[string]()
	labels.Set("component.aggrega.ai/type", "server")
	labels.Set("component.aggrega.ai/category", "compute")
	return labels
}
