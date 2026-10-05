// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestADocumentationBundleIsHeldToTheRootItsCacheDirectoryPicks drives the one
// write a documentation bundle is cached by, over the cache directory a workspace
// configures, one shape per row beside a control caching into the default
// directory inside the workspace.
//
// A cache directory inside the workspace is held to the workspace root, so the
// directory being a link leading out of the workspace is refused. One the
// configuration names climbing out of the workspace is a root of its own: a
// docs-bundles subtree beneath it that is a link leading out of it is refused, and
// the directory itself being a link is honoured where it was named. A refused row
// is held to the place the link leads to being left as it was.
func TestADocumentationBundleIsHeldToTheRootItsCacheDirectoryPicks(t *testing.T) {
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
		name     string
		cacheDir string
		layout   func(t *testing.T, cache, elsewhere string)
		refused  bool
	}{
		{name: "control: the default cache directory inside the workspace", cacheDir: ".aggrega/cache"},
		{name: "a cache directory inside that is a link leading out of the workspace is refused", cacheDir: ".aggrega/cache", refused: true,
			layout: func(t *testing.T, cache, elsewhere string) { link(t, elsewhere, cache) }},
		{name: "control: a cache directory outside the workspace", cacheDir: "../shared-cache"},
		{name: "a docs-bundles subtree beneath a cache directory outside that is a link leading out of it is refused", cacheDir: "../shared-cache", refused: true,
			layout: func(t *testing.T, cache, elsewhere string) { link(t, elsewhere, filepath.Join(cache, "docs-bundles")) }},
		{name: "a cache directory outside that is itself a link is honoured where it was named", cacheDir: "../shared-cache",
			layout: func(t *testing.T, cache, elsewhere string) { link(t, elsewhere, cache) }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			store := memory.New()
			if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
				buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
				t.Fatalf("Push: %v", err)
			}
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			if row.layout != nil {
				row.layout(t, filepath.Join(ws, row.cacheDir), elsewhere)
			}

			_, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, ws, row.cacheDir, nil)
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
				t.Fatalf("refused a bundle its root holds: %v", err)
			}
			if data, _, rerr := docscache.ReadPage(ws, row.cacheDir, testRef, testVersion, componentPage); rerr != nil || string(data) != componentBody {
				t.Errorf("the cached bundle does not serve the page: %q (%v)", data, rerr)
			}
		})
	}
}
