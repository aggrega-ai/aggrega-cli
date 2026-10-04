// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestMissingDirectoryRecognisesTheLoadsRefusalOfADirectoryThatIsNotThere holds
// the recogniser to the refusal the catalog load really raises, over real
// directories: each row drives the load or the directory check, never a
// hand-built chain, so a change to how the walk composes its refusal fails here
// rather than silently making the recogniser stop answering.
//
// The first row is the positive control on the walk: a directory that exists is
// walked and refuses nothing. The two rows after it are the one case the
// recogniser answers yes for, reached the two ways a surface reaches it. The last
// two each carry the filesystem's own "does not exist" and must still answer no:
// a document inside a directory that exists, and the filesystem's report with no
// catalog load around it.
func TestMissingDirectoryRecognisesTheLoadsRefusalOfADirectoryThatIsNotThere(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// refusal returns the directory the recogniser must name, or "" where it
		// must answer no, and the failure the row hands it.
		refusal func(t *testing.T, root string) (string, error)
	}{
		{name: "a directory that exists is walked and refuses nothing",
			refusal: func(t *testing.T, root string) (string, error) {
				err := catalog.NewLazyLookup(root, "", []string{existingDir(t, root)}, nil).CheckDirectories()
				if err != nil {
					t.Fatalf("the directory check refused a directory that exists: %v", err)
				}
				return "", err
			}},
		{name: "a listed directory that does not exist, refused by the directory check",
			refusal: func(t *testing.T, root string) (string, error) {
				missing := filepath.Join(root, "no-such-catalog")
				return missing, catalog.NewLazyLookup(root, "", []string{existingDir(t, root), missing}, nil).CheckDirectories()
			}},
		{name: "the same directory refused by the tolerant whole-catalog load, behind a wrap of the caller's own",
			refusal: func(t *testing.T, root string) (string, error) {
				missing := filepath.Join(root, "no-such-catalog")
				_, _, err := catalog.LoadPassOneTolerant(root, "", existingDir(t, root), missing)
				if err == nil {
					t.Fatal("the whole-catalog load walked a directory that does not exist")
				}
				return missing, fmt.Errorf("catalog load: %w", err)
			}},
		{name: "a document that is not there, inside a directory that is, is not a missing directory",
			refusal: func(t *testing.T, root string) (string, error) {
				dir := existingDir(t, root)
				if err := os.Symlink(filepath.Join(root, "nowhere.yaml"), filepath.Join(dir, "dangling.yaml")); err != nil {
					t.Skipf("this filesystem cannot hold a link to a document that is not there: %v", err)
				}
				_, err := catalog.LoadPassOne(root, "", dir)
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("want the load to stop on the document that is not there; got %v", err)
				}
				return "", err
			}},
		{name: "the filesystem's report alone, with no catalog load around it, is not the load's refusal",
			refusal: func(t *testing.T, root string) (string, error) {
				_, err := os.Stat(filepath.Join(root, "no-such-catalog"))
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("want the filesystem to report the path is not there; got %v", err)
				}
				return "", err
			}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, err := tc.refusal(t, t.TempDir())
			got, ok := catalog.MissingDirectory(err)
			if ok != (want != "") || got != want {
				t.Errorf("MissingDirectory = (%q, %v); want (%q, %v)\nover: %v", got, ok, want, want != "", err)
			}
		})
	}
}

// existingDir creates a catalog directory holding nothing beneath root and
// returns it.
func existingDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	return dir
}
