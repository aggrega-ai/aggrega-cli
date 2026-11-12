// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gitrepo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestAFetchedRepositorysLinksAreHeldToItsCheckout drives a pull of a git source —
// the adapter's Resolve, which clones, checks out and archives — over a repository
// that commits links, one shape per row beside a control holding none.
//
// The checkout is a root of its own. A link it carries leading out of it — to a file
// or a directory outside, from anywhere in the repository, or the module
// subdirectory itself being such a link — halts the pull with the read hold,
// naming the link and where it leads, and nothing is cached: the file outside never
// reaches an archive. A link leading inside the checkout is archived as what it
// leads to: a file's bytes under the link's name, a directory walked where it leads
// with its entries named beneath the link, and a module subdirectory that is such a
// link walked where it leads. A directory link leading back into a directory the
// walk is already reading through it has no end to archive and halts the pull.
func TestAFetchedRepositorysLinksAreHeldToItsCheckout(t *testing.T) {
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker.txt")
	const markerText = "a file outside every repository"
	if err := os.WriteFile(marker, []byte(markerText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "dir", "Chart.yaml"), []byte(markerText), 0o644); err != nil {
		t.Fatal(err)
	}
	chart := func(prefix string) map[string]string {
		return map[string]string{
			prefix + "/Chart.yaml":  "name: probe\n",
			prefix + "/values.yaml": "replicas: 1\n",
		}
	}
	with := func(files map[string]string, more map[string]string) map[string]string {
		for k, v := range more {
			files[k] = v
		}
		return files
	}
	rows := []struct {
		name   string
		files  map[string]string
		links  map[string]string
		subdir string
		// want is the archive's members, each a file with its bytes or a directory
		// ending in "/"; empty for a refused row.
		want []string
		// link is the link a refusal must name and leadsTo where it leads; loop marks
		// the refusal of a link round a loop.
		link, leadsTo string
		loop          bool
	}{
		{name: "control: a module subdirectory holding no link", files: chart("chart"), subdir: "chart",
			want: []string{"Chart.yaml=name: probe\n", "values.yaml=replicas: 1\n"}},
		{name: "a file link leading out of the checkout halts the pull", files: chart("chart"), subdir: "chart",
			links: map[string]string{"chart/secret.txt": climbTo(marker)}, link: "chart/secret.txt", leadsTo: marker},
		{name: "a file link inside the module subdirectory is archived as its bytes", files: chart("chart"), subdir: "chart",
			links: map[string]string{"chart/extra.yaml": "values.yaml"},
			want:  []string{"Chart.yaml=name: probe\n", "extra.yaml=replicas: 1\n", "values.yaml=replicas: 1\n"}},
		{name: "a file link to elsewhere in the checkout, outside the module subdirectory, is archived as its bytes",
			files: with(chart("chart"), map[string]string{"shared/extra.yaml": "from: shared\n"}), subdir: "chart",
			links: map[string]string{"chart/extra.yaml": "../shared/extra.yaml"},
			want:  []string{"Chart.yaml=name: probe\n", "extra.yaml=from: shared\n", "values.yaml=replicas: 1\n"}},
		{name: "a module subdirectory that is a link inside the checkout is walked where it leads", files: chart("charts/real"),
			subdir: "charts/app", links: map[string]string{"charts/app": "real"},
			want: []string{"Chart.yaml=name: probe\n", "values.yaml=replicas: 1\n"}},
		{name: "a module subdirectory that is a link leading out of the checkout halts the pull", files: chart("charts/real"),
			subdir: "charts/app", links: map[string]string{"charts/app": climbTo(filepath.Join(outside, "dir"))},
			link: "charts/app", leadsTo: filepath.Join(outside, "dir")},
		{name: "a directory link inside the checkout is walked where it leads, named beneath the link",
			files: with(chart("chart"), map[string]string{"shared/templates/svc.yaml": "kind: Service\n"}), subdir: "chart",
			links: map[string]string{"chart/templates": "../shared/templates"},
			want:  []string{"Chart.yaml=name: probe\n", "templates/", "templates/svc.yaml=kind: Service\n", "values.yaml=replicas: 1\n"}},
		{name: "a directory link leading out of the checkout halts the pull", files: chart("chart"), subdir: "chart",
			links: map[string]string{"chart/linked": climbTo(filepath.Join(outside, "dir"))},
			link:  "chart/linked", leadsTo: filepath.Join(outside, "dir")},
		{name: "a directory link round a loop halts the pull", files: chart("chart"), subdir: "chart",
			links: map[string]string{"chart/loop": ".."}, link: "chart/loop", loop: true},
		{name: "the whole repository: a file link leading out of the checkout halts the pull", files: chart("chart"),
			links: map[string]string{"leak.txt": climbTo(marker)}, link: "leak.txt", leadsTo: marker},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fixture := buildFixtureRepoWithLinks(t, row.files, row.links)
			ws := t.TempDir()
			a := &adapter{clone: cloneFromFixture(fixture)}
			got, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
				Kind:          Kind,
				URL:           "file://" + fixture,
				Ref:           "HEAD",
				ModuleSubdir:  row.subdir,
				WorkspaceRoot: ws,
				CacheDir:      ".aggrega/cache",
			})
			if row.link == "" {
				if err != nil {
					t.Fatalf("refused a repository whose links stay inside it: %v", err)
				}
				if members := archiveMembers(t, got.CachePath); strings.Join(members, "\n") != strings.Join(row.want, "\n") {
					t.Errorf("the archive holds\n  %s\nwant\n  %s", strings.Join(members, "\n  "), strings.Join(row.want, "\n  "))
				}
				return
			}
			if err == nil {
				t.Fatalf("archived a repository whose link %s %s", row.link, map[bool]string{true: "loops", false: "leads out"}[row.loop])
			}
			if row.loop {
				if errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Errorf("a loop inside the checkout leads nowhere out of it, yet was refused as one: %v", err)
				}
				if !strings.Contains(err.Error(), "leads back into a directory this walk is already reading") {
					t.Errorf("the refusal does not say the link loops: %v", err)
				}
			} else {
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read hold's refusal, got %v", err)
				}
				leadsTo, err2 := filepath.EvalSymlinks(row.leadsTo)
				if err2 != nil {
					t.Fatal(err2)
				}
				for _, says := range []string{"the link " + row.link + " leads to " + leadsTo, "committed in the repository"} {
					if !strings.Contains(err.Error(), says) {
						t.Errorf("the refusal does not say %q:\n%v", says, err)
					}
				}
			}
			if files := filesBeneath(t, ws); len(files) != 0 {
				t.Errorf("a refused pull cached %v", files)
			}
		})
	}
}

// climbTo is a relative link target that climbs to the filesystem root from any
// depth a checkout can sit at and then names the absolute path p, the shape a
// committed link reaching a file outside the repository takes.
func climbTo(p string) string {
	return strings.Repeat("../", 40) + strings.TrimPrefix(filepath.ToSlash(p), "/")
}

// buildFixtureRepoWithLinks is buildFixtureRepo with links committed beside the
// files, each at its path and pointing at its target as written.
func buildFixtureRepoWithLinks(t *testing.T, files, links map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range links {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.FromSlash(target), full); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("AddGlob: %v", err)
	}
	if _, err := wt.Commit("fixture", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "t@example.com", When: time.Unix(0, 0).UTC()},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir
}

// archiveMembers lists the archive at path: each file with its bytes, each
// directory with a trailing slash, sorted.
func archiveMembers(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			out = append(out, h.Name)
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name+"="+string(body))
	}
	sort.Strings(out)
	return out
}

// filesBeneath lists every file beneath dir.
func filesBeneath(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
