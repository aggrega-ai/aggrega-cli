// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package externalinputs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestStoreWritesAreHeldToTheWorkspace drives the one write the external-input
// store performs, through a service composed over a workspace's own cache, with
// the workspace's cache directory being, one row at a time, a plain directory and
// a link leading out.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been written there.
func TestStoreWritesAreHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	rows := []struct {
		name    string
		link    bool
		refused bool
	}{
		{name: "control: a cache directory inside"},
		{name: "a cache directory that is a link leading out", link: true, refused: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			if row.link {
				if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(ws, cacheDir)); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			s := NewFrozen(ws, cacheDir, nil)

			err := s.writeBlob(strings.Repeat("a", 64), []byte("input"))
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a blob the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			entries, rerr := os.ReadDir(outside)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 0 {
				t.Errorf("something was written outside the workspace: %v", entries)
			}
		})
	}
}

// TestStoreWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt drives the
// same write through a service composed over a cache directory the configuration
// names climbing out of the workspace, one shape per row beside a control writing
// plainly beneath it.
//
// The directory is a root of its own: an inputs subtree beneath it that is a link
// leading out of it is refused, and the place the link leads to is left as it was;
// the directory itself being a link is honoured where it was named, and the blob is
// written where it leads.
func TestStoreWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	hexDigest := strings.Repeat("a", 64)
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	rows := []struct {
		name    string
		layout  func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: a cache directory outside the workspace", layout: func(*testing.T, string, string) {}},
		{name: "an inputs subtree that is a link leading out of it is refused", refused: true, layout: func(t *testing.T, cache, elsewhere string) {
			link(t, elsewhere, filepath.Join(cache, "inputs"))
		}},
		{name: "the cache directory itself a link is honoured where it was named", layout: func(t *testing.T, cache, elsewhere string) {
			link(t, elsewhere, cache)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			row.layout(t, filepath.Join(ws, cacheDir), elsewhere)
			s := NewFrozen(ws, cacheDir, nil)

			err := s.writeBlob(hexDigest, []byte("input"))
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if entries, rerr := os.ReadDir(elsewhere); rerr != nil || len(entries) != 0 {
					t.Errorf("something was written where the link leads: %v (%v)", entries, rerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a blob its root holds: %v", err)
			}
			if got, rerr := os.ReadFile(filepath.Join(StoreRoot(ws, cacheDir), "sha256", hexDigest)); rerr != nil || string(got) != "input" {
				t.Errorf("the store does not hold what was written: %q (%v)", got, rerr)
			}
		})
	}
}
