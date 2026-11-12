// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

// TestASourceSlotIsReadOnlyFromWhereItsWriteIsHeld drives the read a render makes of
// a pinned source artifact through its one entry, over a slot laid out one way per
// row: plainly inside the workspace; behind a cache directory that is a link leading
// out; with the archive, the metadata or the manifest a link leading out; and beneath
// a cache directory the configuration names outside the workspace, plainly and with
// the archive linking out of it.
//
// A refused row is held to the read hold's condition, to the link it names, and to
// the slot: nothing is read, so nothing is laid out from it. A file of the slot
// linking out of a real slot is the shape no write refusal would otherwise catch —
// the layout writes inside the slot — which is why the read is held on its own.
func TestASourceSlotIsReadOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	const kind = "helm-oci-chart"
	const url = "oci://r/c"
	const version = "1.0.0"
	blob := buildTGZ(t, map[string]string{"Chart.yaml": "apiVersion: v2\nname: c\nversion: 1.0.0\n"})
	digest := digestOf(blob)

	type place struct{ ws, cacheDir, cache, elsewhere string }
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
		linkOrSkipTree(t, kept, at)
		return at
	}
	seed := func(t *testing.T, p place) string {
		t.Helper()
		return seedSlot(t, p.ws, p.cacheDir, kind, url, version, blob, digest)
	}
	rows := []struct {
		name     string
		cacheDir string
		layout   func(t *testing.T, p place) string
	}{
		{"control: the slot inside the workspace", ".aggrega/cache", func(t *testing.T, p place) string {
			seed(t, p)
			return ""
		}},
		{"a cache directory that is a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			linkOrSkipTree(t, p.elsewhere, p.cache)
			seed(t, p)
			return p.cache
		}},
		{"the archive a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			return moveOut(t, p, seed(t, p), "blob.tgz")
		}},
		{"the metadata a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			return moveOut(t, p, seed(t, p), "metadata.yaml")
		}},
		{"the manifest a link leading out", ".aggrega/cache", func(t *testing.T, p place) string {
			return moveOut(t, p, seed(t, p), cacheslot.ManifestFile)
		}},
		{"control: a cache directory the configuration names outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			seed(t, p)
			return ""
		}},
		{"the archive linking out of a cache directory named outside the workspace", "../shared-cache", func(t *testing.T, p place) string {
			return moveOut(t, p, seed(t, p), "blob.tgz")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			p := place{ws: ws, cacheDir: row.cacheDir, cache: filepath.Join(ws, row.cacheDir), elsewhere: t.TempDir()}
			link := row.layout(t, p)
			lf := fixtureLockfile(kind, url, version, digest)

			extracted, err := sourcecache.LookupAndExtract(lf, kind, url, "", "", version, "", ws, row.cacheDir)
			if link == "" {
				if err != nil {
					t.Fatalf("refused a slot its root holds: %v", err)
				}
				if _, serr := os.Stat(filepath.Join(extracted, "Chart.yaml")); serr != nil {
					t.Errorf("the archive was not laid out: %v", serr)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			if errors.Is(err, sourcecache.ErrFetchNotFound()) || errors.Is(err, sourcecache.ErrFetchIntegrity()) {
				t.Errorf("the refusal must be returned as itself, not as a miss or a mismatch: %v", err)
			}
			if extracted != "" {
				t.Errorf("a refused read handed back a tree: %s", extracted)
			}
			if !strings.Contains(err.Error(), link) {
				t.Errorf("the refusal does not name the link %s: %v", link, err)
			}
		})
	}
}
