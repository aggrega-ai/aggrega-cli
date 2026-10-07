// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestACachedBundleIsReadOnlyFromWhereItsWriteIsHeld drives the two reads of a cached
// documentation bundle — the presence probe an acquisition asks first, and the page
// read every explanation and documentation resource makes — over a bundle cached one
// way per row: plainly inside the workspace; behind a cache directory that is a link
// leading out; with the bundle's content or its manifest a link leading out; and
// beneath a cache directory the configuration names outside the workspace, plainly
// and with the content linking out of it.
//
// A refused row is held to the read hold's condition and to the link it names, on
// both reads: the probe does not report such a bundle absent, which would send the
// acquisition to fetch into the same place, and the page read serves nothing.
func TestACachedBundleIsReadOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
		buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	type place struct{ ws, cacheDir, cache, elsewhere string }
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	cache := func(t *testing.T, ws, cacheDir string) string {
		t.Helper()
		slot, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, ws, cacheDir, nil)
		if err != nil {
			t.Fatalf("ResolveAndCache: %v", err)
		}
		return slot
	}
	// moveOut replaces the slot's file name with a link to a copy of it kept in
	// elsewhere, and returns the link.
	moveOut := func(t *testing.T, p place, slot, name string) string {
		t.Helper()
		at := filepath.Join(slot, name)
		data, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		kept := filepath.Join(p.elsewhere, name)
		if err := os.WriteFile(kept, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(at); err != nil {
			t.Fatal(err)
		}
		link(t, kept, at)
		return at
	}
	rows := []struct {
		name     string
		cacheDir string
		layout   func(t *testing.T, p place) string
	}{
		{"control: the bundle cached inside the workspace", ".aggrega/cache", func(t *testing.T, p place) string {
			cache(t, p.ws, p.cacheDir)
			return ""
		}},
		{"a cache directory that is a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			// Cached where the link will lead, through a workspace of its own, then
			// reached through the link.
			cache(t, p.elsewhere, ".")
			link(t, p.elsewhere, p.cache)
			return p.cache
		}},
		{"the bundle's content a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			return moveOut(t, p, cache(t, p.ws, p.cacheDir), "blob")
		}},
		{"the bundle's manifest a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			return moveOut(t, p, cache(t, p.ws, p.cacheDir), "manifest")
		}},
		{"control: a cache directory the configuration names outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			cache(t, p.ws, p.cacheDir)
			return ""
		}},
		{"the content linking out of a cache directory named outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			return moveOut(t, p, cache(t, p.ws, p.cacheDir), "blob")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			p := place{ws: ws, cacheDir: row.cacheDir, cache: filepath.Join(ws, row.cacheDir), elsewhere: t.TempDir()}
			want := row.layout(t, p)

			cached, cerr := docscache.Cached(ws, row.cacheDir, testRef, testVersion)
			data, _, rerr := docscache.ReadPage(ws, row.cacheDir, testRef, testVersion, componentPage)
			if want == "" {
				if cerr != nil || !cached {
					t.Errorf("the presence probe answered (%v, %v), want a cached bundle", cached, cerr)
				}
				if rerr != nil || string(data) != componentBody {
					t.Errorf("the page read answered %q (%v), want the page", data, rerr)
				}
				return
			}
			// The manifest is not what the presence probe looks at, so it is held
			// only where the page read opens it.
			if !strings.HasSuffix(want, "manifest") {
				if !errors.Is(cerr, readscope.ErrReadLeadsOutOfWorkspace()) || cached {
					t.Errorf("the presence probe answered (%v, %v), want the read hold's refusal", cached, cerr)
				} else if !strings.Contains(cerr.Error(), want) {
					t.Errorf("the probe's refusal does not name the link %s: %v", want, cerr)
				}
			}
			if !errors.Is(rerr, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("the page read answered %q (%v), want the read hold's refusal", data, rerr)
			}
			if errors.Is(rerr, docscache.ErrFetchNotFound()) || data != nil {
				t.Errorf("the refusal must serve nothing and not read as a miss: %q (%v)", data, rerr)
			}
			if !strings.Contains(rerr.Error(), want) {
				t.Errorf("the page read's refusal does not name the link %s: %v", want, rerr)
			}
		})
	}
}
