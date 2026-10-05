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
