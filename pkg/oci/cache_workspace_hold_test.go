// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestEntitySlotWritesAreHeldToTheWorkspace drives the one write every file of an
// entity slot goes through over a workspace whose artifact cache directory is, one
// row at a time, a plain directory, a link leading out, and a link pointing
// inside.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been written there.
func TestEntitySlotWritesAreHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	rows := []struct {
		name    string
		layout  func(t *testing.T, ws, outside string)
		refused bool
	}{
		{name: "control: a cache directory inside", layout: func(*testing.T, string, string) {}},
		{name: "a cache directory that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) {
			linkCacheDir(t, outside, filepath.Join(ws, cacheDir))
		}},
		{name: "a cache directory that is a link pointing inside", layout: func(t *testing.T, ws, _ string) {
			target := filepath.Join(ws, "shared-cache")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			linkCacheDir(t, target, filepath.Join(ws, cacheDir))
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			row.layout(t, ws, outside)

			err := WriteCached(ws, cacheDir, "oci://hub.example.invalid/component/demo/api", "1.0.0", []byte("blob"))
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a slot the workspace holds: %v", err)
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

func linkCacheDir(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
