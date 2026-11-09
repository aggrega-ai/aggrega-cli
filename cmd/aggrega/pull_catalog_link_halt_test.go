// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestAPullHaltsOnALinkLeadingOutOfACatalogDirectory drives a whole pull over a
// Release whose catalog search stack holds, beside the fixture's own catalog, a
// second directory outside the workspace, and reads back whether the pull wrote its
// lockfile or halted.
//
// The control row puts an unrelated entity in that directory; the other makes the
// same entity a link leading out of it. A pull resolves and pins what the build will
// read, and its lookup passes such a link over unread, so a pull that did not halt
// on it could pin another entity of the name it stands for: it halts naming the
// link, and writes no lockfile.
func TestAPullHaltsOnALinkLeadingOutOfACatalogDirectory(t *testing.T) {
	const unrelated = "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: example/unrelated\n" +
		"  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: server\nspec: {}\n"
	for _, row := range []struct {
		name   string
		linked bool
	}{
		{name: "control: an unrelated entity in a second catalog directory"},
		{name: "the same entity a link leading out of that directory", linked: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			release := authoringFixture(t, dir)
			extra, elsewhere := t.TempDir(), t.TempDir()
			entity := filepath.Join(extra, "unrelated.yaml")
			if row.linked {
				writeFixture(t, filepath.Join(elsewhere, "unrelated.yaml"), unrelated)
				if err := os.Symlink(filepath.Join(elsewhere, "unrelated.yaml"), entity); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			} else {
				writeFixture(t, entity, unrelated)
			}
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: t.TempDir()})

			err := pullRun(context.Background(), pullConfig{ws: rw, ReleasePath: release,
				CatalogDirs: []string{filepath.Join(dir, "catalog"), extra}})

			_, statErr := os.Stat(lockfile.PathFor(release))
			if !row.linked {
				if err != nil {
					t.Fatalf("the control did not pull: %v", err)
				}
				if statErr != nil {
					t.Fatalf("the control wrote no lockfile: %v", statErr)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), "the link "+entity+" leads to ") {
				t.Errorf("the halt must name the link %s: %v", entity, err)
			}
			if statErr == nil {
				t.Errorf("a lockfile was written by a pull that halted")
			}
		})
	}
}
