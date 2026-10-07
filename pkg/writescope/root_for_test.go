// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package writescope_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestRootForPicksTheWorkspaceOrTheNamedDirectory drives the one answer to which
// root a composed write is held to, one shape of directory per row beside a
// control that lies plainly inside the workspace.
//
// Which side of the workspace a directory lies on is judged on disk, so a
// directory spelled inside the workspace that is a link leading out is outside it
// and is its own root, and a directory spelled elsewhere that is a link into the
// workspace is inside it.
func TestRootForPicksTheWorkspaceOrTheNamedDirectory(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	mustMkdir(t, filepath.Join(ws, "real"))
	mustMkdir(t, filepath.Join(outside, "named"))
	mustLink(t, filepath.Join(outside, "named"), filepath.Join(ws, "linked-out"))
	intoWorkspace := filepath.Join(t.TempDir(), "into-workspace")
	mustLink(t, filepath.Join(ws, "real"), intoWorkspace)

	rows := []struct {
		name   string
		ws     string
		anchor string
		want   string
	}{
		{name: "control: a directory inside the workspace", ws: ws, anchor: filepath.Join(ws, "real"), want: ws},
		{name: "a directory outside the workspace is its own root", ws: ws, anchor: filepath.Join(outside, "named"), want: filepath.Join(outside, "named")},
		{name: "a directory spelled inside that is a link leading out is its own root", ws: ws, anchor: filepath.Join(ws, "linked-out"), want: filepath.Join(ws, "linked-out")},
		{name: "a directory spelled elsewhere that is a link into the workspace is the workspace's", ws: ws, anchor: intoWorkspace, want: ws},
		{name: "a directory not made yet inside the workspace is the workspace's", ws: ws, anchor: filepath.Join(ws, "real", "new"), want: ws},
		{name: "a directory not made yet outside the workspace is its own root", ws: ws, anchor: filepath.Join(outside, "new"), want: filepath.Join(outside, "new")},
		{name: "no directory named is the workspace's", ws: ws, anchor: "", want: ws},
		{name: "with no workspace the directory is the root", ws: "", anchor: filepath.Join(outside, "named"), want: filepath.Join(outside, "named")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := writescope.RootFor(row.ws, row.anchor); got != row.want {
				t.Fatalf("RootFor(%q, %q) = %q, want %q", row.ws, row.anchor, got, row.want)
			}
		})
	}
}

// TestACacheWriteIsHeldToTheRootItsCacheDirectoryPicks drives the one answer to
// which root a write beneath the artifact cache is held to, and the judgement a
// writer of the cache then makes against it, one shape of cache directory per row
// beside a control writing beneath the default directory.
//
// The cache directory is the root of everything beneath it, the default as much as
// one the configuration names outside the workspace: a class root beneath it that
// is a link leading out of it is refused — elsewhere inside the workspace as much
// as out of it — and one leading elsewhere inside it is followed. A cache directory
// spelled inside the workspace is followed to wherever it leads inside the
// workspace, and refused, held to the workspace root, where it leads out of it. One
// the configuration spells climbing out of the workspace is honoured where it was
// named, followed if it is a link, wherever it leads.
func TestACacheWriteIsHeldToTheRootItsCacheDirectoryPicks(t *testing.T) {
	parent := t.TempDir()
	ws := filepath.Join(parent, "ws")
	elsewhere := t.TempDir()
	mustMkdir(t, filepath.Join(ws, ".aggrega", "cache", "entities"))
	mustMkdir(t, filepath.Join(ws, "kept"))
	mustMkdir(t, filepath.Join(ws, "inside-cache"))
	mustLink(t, filepath.Join(ws, "kept"), filepath.Join(ws, "inside-cache", "entities"))
	mustMkdir(t, filepath.Join(ws, "within-cache", "kept-entities"))
	mustLink(t, "kept-entities", filepath.Join(ws, "within-cache", "entities"))
	mustMkdir(t, filepath.Join(ws, "state", "cache", "entities"))
	mustLink(t, filepath.Join(ws, "state", "cache"), filepath.Join(ws, "linked-in-cache"))
	mustMkdir(t, filepath.Join(elsewhere, "linked-target"))
	mustLink(t, filepath.Join(elsewhere, "linked-target"), filepath.Join(ws, "linked-cache"))
	mustMkdir(t, filepath.Join(parent, "shared", "entities"))
	mustMkdir(t, filepath.Join(parent, "shared-out"))
	mustMkdir(t, filepath.Join(elsewhere, "out-target"))
	mustLink(t, filepath.Join(elsewhere, "out-target"), filepath.Join(parent, "shared-out", "entities"))
	mustMkdir(t, filepath.Join(elsewhere, "real-cache", "entities"))
	mustLink(t, filepath.Join(elsewhere, "real-cache"), filepath.Join(parent, "shared-link"))
	mustMkdir(t, filepath.Join(ws, "back"))
	mustLink(t, filepath.Join(ws, "back"), filepath.Join(parent, "back-in"))
	mustMkdir(t, filepath.Join(parent, "shared-into", "entities"))
	mustLink(t, filepath.Join(ws, "kept"), filepath.Join(parent, "shared-into", "inputs"))

	rows := []struct {
		name     string
		ws       string
		cacheDir string
		class    string
		wantRoot string
		refused  bool
	}{
		{name: "control: the default cache directory inside the workspace is its own root", ws: ws, cacheDir: ".aggrega/cache", wantRoot: filepath.Join(ws, ".aggrega/cache")},
		{name: "a class root linked out of a cache directory inside, elsewhere inside the workspace, is refused", ws: ws, cacheDir: "inside-cache", wantRoot: filepath.Join(ws, "inside-cache"), refused: true},
		{name: "a class root linked elsewhere inside the cache directory is followed", ws: ws, cacheDir: "within-cache", wantRoot: filepath.Join(ws, "within-cache")},
		{name: "a cache directory spelled inside that is a link elsewhere inside the workspace is followed", ws: ws, cacheDir: "linked-in-cache", wantRoot: filepath.Join(ws, "linked-in-cache")},
		{name: "a cache directory spelled inside that is a link leading out of the workspace is refused", ws: ws, cacheDir: "linked-cache", wantRoot: ws, refused: true},
		{name: "a cache directory climbing out of the workspace is its own root", ws: ws, cacheDir: "../shared", wantRoot: filepath.Join(parent, "shared")},
		{name: "a class root beneath a cache directory outside that is a link leading out of it is refused", ws: ws, cacheDir: "../shared-out", wantRoot: filepath.Join(parent, "shared-out"), refused: true},
		{name: "a class root beneath a cache directory outside that is a link into the workspace is refused", ws: ws, cacheDir: "../shared-into", class: "inputs", wantRoot: filepath.Join(parent, "shared-into"), refused: true},
		{name: "a cache directory outside that is itself a link is followed where it was named", ws: ws, cacheDir: "../shared-link", wantRoot: filepath.Join(parent, "shared-link")},
		{name: "a cache directory climbing out by a longer spelling is its own root", ws: ws, cacheDir: "inner/../../shared", wantRoot: filepath.Join(ws, "inner/../../shared")},
		{name: "a cache directory climbing out that is a link back into the workspace is its own root", ws: ws, cacheDir: "../back-in", wantRoot: filepath.Join(parent, "back-in")},
		{name: "with no workspace nothing is held", ws: "", cacheDir: "../shared-out", wantRoot: ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := writescope.RootForCache(row.ws, row.cacheDir)
			if root != row.wantRoot {
				t.Fatalf("RootForCache(%q, %q) = %q, want %q", row.ws, row.cacheDir, root, row.wantRoot)
			}
			class := row.class
			if class == "" {
				class = "entities"
			}
			path := filepath.Join(ws, row.cacheDir, class, "slot", "blob")
			err := writescope.CheckWrite(root, path)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a cache write its root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), "which must stay inside "+row.wantRoot) {
				t.Errorf("the refusal must name %s as the directory the path must stay inside:\n%v", row.wantRoot, err)
			}
		})
	}
}

// TestAWriteBeneathANamedDirectoryIsHeldToTheRootItsDirectoryPicks drives the
// judgement a writer makes once it has asked RootFor, over the shapes the rule
// distinguishes: beneath a directory outside the workspace, a link leading out of
// that directory is refused, wherever it leads — into the workspace too; the
// directory itself being a link is followed; beneath a directory inside the
// workspace, a link is judged against the workspace root, so one leading elsewhere
// in the workspace is followed and one leading out is refused. One fault per row,
// beside a control writing plainly beneath a directory outside the workspace.
//
// A refused row is asserted against the condition, the link it names, and the root
// it names as the one the path must stay inside, because a refusal naming only
// the workspace would mislead a reader whose directory lies outside it.
func TestAWriteBeneathANamedDirectoryIsHeldToTheRootItsDirectoryPicks(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	elsewhere := t.TempDir()
	named := filepath.Join(outside, "named")
	mustMkdir(t, filepath.Join(named, "real"))
	mustMkdir(t, filepath.Join(ws, "real", "sibling"))
	mustMkdir(t, filepath.Join(ws, "inner"))
	mustMkdir(t, filepath.Join(outside, "target"))
	mustLink(t, elsewhere, filepath.Join(named, "out-link"))
	mustLink(t, filepath.Join(ws, "real"), filepath.Join(named, "into-workspace"))
	mustLink(t, filepath.Join(outside, "target"), filepath.Join(ws, "linked-out"))
	mustLink(t, filepath.Join(ws, "real", "sibling"), filepath.Join(ws, "inner", "in-link"))
	mustLink(t, elsewhere, filepath.Join(ws, "inner", "out-link"))
	reachedThroughALink := filepath.Join(t.TempDir(), "workspace-link")
	mustLink(t, filepath.Join(ws, "inner"), reachedThroughALink)

	rows := []struct {
		name    string
		anchor  string
		path    string
		refused bool
		link    string
		root    string
	}{
		{name: "control: a file beneath a directory outside the workspace", anchor: named, path: filepath.Join(named, "real", "x.yaml")},
		{name: "a link beneath an outside directory leading out of it is refused", anchor: named, path: filepath.Join(named, "out-link", "x.yaml"), refused: true, link: filepath.Join(named, "out-link"), root: named},
		{name: "a link beneath an outside directory leading into the workspace is refused", anchor: named, path: filepath.Join(named, "into-workspace", "x.yaml"), refused: true, link: filepath.Join(named, "into-workspace"), root: named},
		{name: "a named directory that is itself a link is followed", anchor: filepath.Join(ws, "linked-out"), path: filepath.Join(ws, "linked-out", "x.yaml")},
		{name: "beneath a directory inside the workspace a link leading elsewhere inside it is followed", anchor: filepath.Join(ws, "inner"), path: filepath.Join(ws, "inner", "in-link", "x.yaml")},
		{name: "beneath a directory inside the workspace a link leading out of the workspace is refused", anchor: filepath.Join(ws, "inner"), path: filepath.Join(ws, "inner", "out-link", "x.yaml"), refused: true, link: filepath.Join(ws, "inner", "out-link"), root: ws},
		{name: "beneath a directory reached through a link into the workspace a link leading out is refused", anchor: reachedThroughALink, path: filepath.Join(reachedThroughALink, "out-link", "x.yaml"), refused: true, link: filepath.Join(reachedThroughALink, "out-link"), root: ws},
		{name: "beneath a directory not made yet outside the workspace nothing can lead out", anchor: filepath.Join(outside, "new"), path: filepath.Join(outside, "new", "a", "x.yaml")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := writescope.RootFor(ws, row.anchor)
			err := writescope.CheckWrite(root, row.path)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a write its root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), "the link "+row.link+" leads to ") {
				t.Errorf("the refusal must name the link %s:\n%v", row.link, err)
			}
			if !strings.Contains(err.Error(), "which must stay inside "+row.root) {
				t.Errorf("the refusal must name %s as the directory the path must stay inside:\n%v", row.root, err)
			}
		})
	}
}
