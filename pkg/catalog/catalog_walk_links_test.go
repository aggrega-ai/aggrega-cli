// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// walkLinksComponent is the one entity a row lays, named so that reading it shows
// up by name.
func walkLinksComponent(name string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: server\nspec: {}\n"
}

// TestACatalogWalkDoesNotReadALinkLeadingOutOfItsRoot drives every reader of a
// catalog directory — the whole-catalog load a build runs, the lookup's check of
// its directories, the check a compile asks of it, its per-name resolution and its
// listing — over one layout per row, and reads back what each found and what each
// halted on.
//
// Each row lays a workspace and a directory outside it, and puts the one entity,
// example/probe, where the row says. A row whose entity sits behind a link leading
// out of the root its catalog directory is held to expects every reader that a
// compile depends on to halt naming that link, and the lookup to have read nothing
// through it: the entity neither resolves nor is listed. A row whose link leads
// inside, or whose catalog directory is itself a link somebody named, expects the
// entity found and nothing halted.
//
// The first row is the control: the entity in a plain catalog directory is found by
// every reader, which is what makes a row's "not found" the link's doing.
func TestACatalogWalkDoesNotReadALinkLeadingOutOfItsRoot(t *testing.T) {
	const probe = "example/probe"
	rows := []struct {
		name string
		// lay puts the entity in place and returns the catalog directories to read,
		// given the workspace root and the directory outside it.
		lay func(t *testing.T, ws, outside string) []string
		// haltAt is the link a halt must name, relative to the workspace (or, where
		// it starts with "outside/", to the directory outside); empty where nothing
		// halts.
		haltAt string
		found  bool
		// named hands every reader the directories lay returned as the part of the
		// stack somebody named; otherwise the readers are handed none.
		named bool
	}{
		{
			name: "control: the entity in a catalog directory inside the workspace",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(ws, "cat", "probe.yaml"), walkLinksComponent(probe))
				return []string{filepath.Join(ws, "cat")}
			},
			found: true,
		},
		{
			name: "a document that is a link leading out of the workspace is not read, and halts the load",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(outside, "probe.yaml"), filepath.Join(ws, "cat", "probe.yaml"))
				return []string{filepath.Join(ws, "cat")}
			},
			haltAt: "cat/probe.yaml",
		},
		{
			name: "a directory that is a link leading out of the workspace is not entered, and halts the load",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "shelf", "probe.yaml"), walkLinksComponent(probe))
				writeWalkFile(t, filepath.Join(ws, "cat", "readme.txt"), "a catalog directory\n")
				walkLink(t, filepath.Join(outside, "shelf"), filepath.Join(ws, "cat", "shelf"))
				return []string{filepath.Join(ws, "cat")}
			},
			haltAt: "cat/shelf",
		},
		{
			name: "a document that is a link leading inside the workspace is read where the walk meets it",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(ws, "elsewhere", "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(ws, "elsewhere", "probe.yaml"), filepath.Join(ws, "cat", "probe.yaml"))
				return []string{filepath.Join(ws, "cat")}
			},
			found: true,
		},
		{
			name: "a link leading out to a file no catalog walk reads halts nothing",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(ws, "cat", "probe.yaml"), walkLinksComponent(probe))
				writeWalkFile(t, filepath.Join(outside, "notes.txt"), "notes\n")
				walkLink(t, filepath.Join(outside, "notes.txt"), filepath.Join(ws, "cat", "notes.txt"))
				return []string{filepath.Join(ws, "cat")}
			},
			found: true,
		},
		{
			name: "a named catalog directory that is itself a link leading out is read where it leads",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "cat", "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(outside, "cat"), filepath.Join(ws, "vendored"))
				return []string{filepath.Join(ws, "vendored")}
			},
			found: true,
		},
		{
			name: "the conventional catalog directory a link leading out, named by nobody, is not read, and halts the load",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "cat", "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(outside, "cat"), filepath.Join(ws, "catalog"))
				return []string{filepath.Join(ws, "catalog")}
			},
			haltAt: "catalog",
		},
		{
			name: "the conventional catalog directory a link leading out, named, is read where it leads",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "cat", "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(outside, "cat"), filepath.Join(ws, "catalog"))
				return []string{filepath.Join(ws, "catalog")}
			},
			named: true,
			found: true,
		},
		{
			name: "beneath the conventional catalog directory named, a link leading out of where it leads halts the load",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "elsewhere", "probe.yaml"), walkLinksComponent(probe))
				writeWalkFile(t, filepath.Join(outside, "cat", "readme.txt"), "a catalog directory\n")
				walkLink(t, filepath.Join(outside, "elsewhere", "probe.yaml"), filepath.Join(outside, "cat", "probe.yaml"))
				walkLink(t, filepath.Join(outside, "cat"), filepath.Join(ws, "catalog"))
				return []string{filepath.Join(ws, "catalog")}
			},
			named:  true,
			haltAt: "catalog/probe.yaml",
		},
		{
			name: "beneath a named catalog directory outside the workspace, a link leading out of it halts the load",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(outside, "elsewhere", "probe.yaml"), walkLinksComponent(probe))
				walkLink(t, filepath.Join(outside, "elsewhere", "probe.yaml"), filepath.Join(outside, "cat", "probe.yaml"))
				return []string{filepath.Join(outside, "cat")}
			},
			haltAt: "outside/cat/probe.yaml",
		},
		{
			name: "a document that is a link leading nowhere is neither read nor a halt",
			lay: func(t *testing.T, ws, outside string) []string {
				writeWalkFile(t, filepath.Join(ws, "cat", "readme.txt"), "a catalog directory\n")
				walkLink(t, filepath.Join(outside, "missing.yaml"), filepath.Join(ws, "cat", "probe.yaml"))
				return []string{filepath.Join(ws, "cat")}
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			outside := t.TempDir()
			dirs := row.lay(t, ws, outside)
			wantLink := ""
			switch {
			case strings.HasPrefix(row.haltAt, "outside/"):
				wantLink = filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(row.haltAt, "outside/")))
			case row.haltAt != "":
				wantLink = filepath.Join(ws, filepath.FromSlash(row.haltAt))
			}

			var named []string
			if row.named {
				named = dirs
			}

			// The readers a compile depends on: the build's whole-catalog load, the
			// lookup's check of its directories, and the check a compile asks first.
			lookup := catalog.NewLazyLookup(ws, "", dirs, nil, named...)
			_, _, loadErr := catalog.LoadPassOneTolerantWithNamedDirs(ws, "", named, dirs...)
			for _, halt := range []struct {
				reader string
				err    error
			}{
				{"the whole-catalog load", loadErr},
				{"CheckDirectories", lookup.CheckDirectories()},
				{"CheckLinks", lookup.CheckLinks()},
			} {
				requireWalkHalt(t, halt.reader, halt.err, wantLink, outside)
			}

			// The lookup's own readers find what they may read and nothing through a
			// link leading out.
			_, resolved := catalog.NewLazyLookup(ws, "", dirs, nil, named...).Component(probe)
			listed := slices.Contains(catalog.NewLazyLookup(ws, "", dirs, nil, named...).EntityNames("Component"), probe)
			if resolved != row.found || listed != row.found {
				t.Errorf("%s: resolved %v, listed %v; want %v for both — a link leading out was read, or one the "+
					"root holds was not", probe, resolved, listed, row.found)
			}
		})
	}
}

// requireWalkHalt asserts what one reader answered: nil where wantLink is empty,
// and otherwise the read-leads-out halt naming wantLink and the place it leads.
func requireWalkHalt(t *testing.T, reader string, err error, wantLink, outside string) {
	t.Helper()
	if wantLink == "" {
		if err != nil {
			t.Errorf("%s halted where nothing leads out: %v", reader, err)
		}
		return
	}
	if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
		t.Errorf("%s: want the read-leads-out-of-workspace halt naming %s, got %v", reader, wantLink, err)
		return
	}
	if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
		t.Errorf("%s: short code = %q, want E0321", reader, code)
	}
	if !strings.Contains(err.Error(), "the link "+wantLink+" leads to ") {
		t.Errorf("%s: the halt must name the link %s:\n%v", reader, wantLink, err)
	}
	if !strings.Contains(err.Error(), outside) {
		t.Errorf("%s: the halt must name where the link leads, beneath %s:\n%v", reader, outside, err)
	}
}

func writeWalkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func walkLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(at), err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
