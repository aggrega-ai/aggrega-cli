// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gitrepo

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// TestAModuleSubdirectoryThatClimbsIsRefusedBeforeAnythingIsFetched drives a pull of
// a git source — the adapter's Resolve — whose module subdirectory climbs above the
// checkout as written, one spelling per row beside controls that stay inside it.
//
// The checkouts are made in a temporary directory holding, beside them, the
// directory such a value names once joined to a checkout: x, with one readable and
// one unreadable file. A value that climbs is refused with the refusal its cache
// slot gives it, quoting the value under its key, before anything is fetched: the
// clone is never asked for, no checkout is made, nothing is cached, and nothing
// beside the checkout is read — a read of the unreadable file would have failed the
// pull with that read's error instead. A backslash is read as a separator, as a
// Windows join reads one, so a value climbing through backslashes is refused on
// every platform. A value that climbs and comes back down inside, and one written
// with a leading separator, are the module subdirectories they name.
func TestAModuleSubdirectoryThatClimbsIsRefusedBeforeAnythingIsFetched(t *testing.T) {
	fixture := buildFixtureRepoWithLinks(t, map[string]string{
		"chart/Chart.yaml":  "name: probe\n",
		"chart/values.yaml": "replicas: 1\n",
	}, nil)
	tmp := t.TempDir()
	beside := filepath.Join(tmp, "x")
	if err := os.MkdirAll(beside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beside, "Chart.yaml"), []byte("name: beside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beside, "unreadable.txt"), []byte("beside the checkout\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(beside, "unreadable.txt"), 0o644) })
	t.Setenv("TMPDIR", tmp)

	for _, row := range []struct {
		name, subdir string
		// refused marks a value the pull must refuse before fetching; want is the
		// archive's members for one it takes.
		refused bool
		want    []string
	}{
		{name: "control: a module subdirectory inside the checkout", subdir: "chart",
			want: []string{"Chart.yaml=name: probe\n", "values.yaml=replicas: 1\n"}},
		{name: "control: a value that climbs and comes back down inside", subdir: "chart/../chart",
			want: []string{"Chart.yaml=name: probe\n", "values.yaml=replicas: 1\n"}},
		{name: "control: a leading separator is dropped as a join drops it", subdir: "/chart",
			want: []string{"Chart.yaml=name: probe\n", "values.yaml=replicas: 1\n"}},
		{name: "a value climbing above the checkout", subdir: "../x", refused: true},
		{name: "a value climbing above the checkout after a name", subdir: "chart/../../x", refused: true},
		{name: "a value climbing behind a leading separator", subdir: "/../x", refused: true},
		{name: "a value climbing through a backslash", subdir: `..\x`, refused: true},
		{name: "a value climbing through backslashes after a name", subdir: `chart\..\..\x`, refused: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			clones := 0
			fromFixture := cloneFromFixture(fixture)
			a := &adapter{clone: func(ctx context.Context, destDir, url string, c *http.Client, auth transport.AuthMethod) (*git.Repository, error) {
				clones++
				return fromFixture(ctx, destDir, url, c, auth)
			}}
			ws := t.TempDir()
			got, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
				Kind:          Kind,
				URL:           "file://" + fixture,
				Ref:           "HEAD",
				ModuleSubdir:  row.subdir,
				WorkspaceRoot: ws,
				CacheDir:      ".aggrega/cache",
			})
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a module subdirectory inside the checkout: %v", err)
				}
				if members := archiveMembers(t, got.CachePath); strings.Join(members, "\n") != strings.Join(row.want, "\n") {
					t.Errorf("the archive holds\n  %s\nwant\n  %s", strings.Join(members, "\n  "), strings.Join(row.want, "\n  "))
				}
				return
			}
			if !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
				t.Fatalf("want the cache-location refusal, got %v", err)
			}
			if want := "module_subdir " + strconv.Quote(row.subdir); !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not quote the value under its key, %s:\n%v", want, err)
			}
			if clones != 0 {
				t.Errorf("the repository was fetched %d time(s) for a module subdirectory refused as written", clones)
			}
			if entries, rerr := os.ReadDir(tmp); rerr != nil {
				t.Fatal(rerr)
			} else {
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), "aggrega-git-repo-") {
						t.Errorf("a checkout was made for a module subdirectory refused as written: %s", e.Name())
					}
				}
			}
			if files := filesBeneath(t, ws); len(files) != 0 {
				t.Errorf("a refused pull cached %v", files)
			}
		})
	}
}

// TestAFetchedRepositorysRefusalNamesTheLinkByItsPlaceInTheRepository drives a pull
// of a git source over repositories committing a link leading out of the checkout —
// a file link, a directory link, the module subdirectory itself, and a file at the
// repository's top with no module subdirectory — and reads the refusal as both
// surfaces show it: the message, and the source location the command line prints
// as the refusal's head.
//
// The checkout is a temporary directory removed once the pull halts, so a refusal
// naming it names nothing a reader can open. The refusal names the link by its
// place in the repository, and the checkout by the repository and the revision the
// source names — the authored reference with the commit it resolved to, or the
// commit alone where the source names no reference — and where the link leads as
// the place on the machine it is. The temporary checkout appears nowhere in it.
func TestAFetchedRepositorysRefusalNamesTheLinkByItsPlaceInTheRepository(t *testing.T) {
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker.txt")
	if err := os.WriteFile(marker, []byte("a file outside every repository"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	chart := func(prefix string) map[string]string {
		return map[string]string{prefix + "/Chart.yaml": "name: probe\n", prefix + "/values.yaml": "replicas: 1\n"}
	}
	rows := []struct {
		name          string
		files         map[string]string
		links         map[string]string
		subdir, ref   string
		link, leadsTo string
		fixture       string
	}{
		{name: "a file link, the reference named", files: chart("chart"), subdir: "chart", ref: "HEAD",
			links: map[string]string{"chart/secret.txt": climbTo(marker)}, link: "chart/secret.txt", leadsTo: marker},
		{name: "a file link, no reference named", files: chart("chart"), subdir: "chart",
			links: map[string]string{"chart/secret.txt": climbTo(marker)}, link: "chart/secret.txt", leadsTo: marker},
		{name: "a directory link", files: chart("chart"), subdir: "chart", ref: "HEAD",
			links: map[string]string{"chart/linked": climbTo(filepath.Join(outside, "dir"))},
			link:  "chart/linked", leadsTo: filepath.Join(outside, "dir")},
		{name: "the module subdirectory itself", files: chart("charts/real"), subdir: "charts/app", ref: "HEAD",
			links: map[string]string{"charts/app": climbTo(filepath.Join(outside, "dir"))},
			link:  "charts/app", leadsTo: filepath.Join(outside, "dir")},
		{name: "the whole repository", files: chart("chart"), ref: "HEAD",
			links: map[string]string{"leak.txt": climbTo(marker)}, link: "leak.txt", leadsTo: marker},
	}
	// Every repository is built before the checkouts' temporary directory is set, so
	// none lies beneath it and a refusal naming the repository names nothing there.
	for i := range rows {
		rows[i].fixture = buildFixtureRepoWithLinks(t, rows[i].files, rows[i].links)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := row.fixture
			repo, err := git.PlainOpen(fixture)
			if err != nil {
				t.Fatal(err)
			}
			head, err := repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			url := "file://" + fixture
			revision := "at commit " + head.Hash().String()
			if row.ref != "" {
				revision = "at " + row.ref + " (commit " + head.Hash().String() + ")"
			}
			a := &adapter{clone: cloneFromFixture(fixture)}
			_, err = a.Resolve(context.Background(), sourceresolver.SourceSpec{
				Kind:          Kind,
				URL:           url,
				Ref:           row.ref,
				ModuleSubdir:  row.subdir,
				WorkspaceRoot: t.TempDir(),
				CacheDir:      ".aggrega/cache",
			})
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			leadsTo, err2 := filepath.EvalSymlinks(row.leadsTo)
			if err2 != nil {
				t.Fatal(err2)
			}
			msg := err.Error()
			for _, says := range []string{
				"reading " + row.link + ",",
				"which must stay inside the repository " + url + " " + revision + ",",
				"the link " + row.link + " leads to " + leadsTo + ")",
			} {
				if !strings.Contains(msg, says) {
					t.Errorf("the refusal does not say %q:\n%s", says, msg)
				}
			}
			for _, checkout := range []string{tmp, "aggrega-git-repo-"} {
				if strings.Contains(msg, checkout) {
					t.Errorf("the refusal names the temporary checkout (%q), gone once the pull halts:\n%s", checkout, msg)
				}
			}
			var located *cerrors.CompilerError
			if !errors.As(err, &located) {
				t.Fatalf("the refusal carries no source location: %v", err)
			}
			if got := located.Source().File; got != row.link {
				t.Errorf("the refusal's source location — the command line's head — is %q, want the link's place in the repository %q", got, row.link)
			}
		})
	}
}
