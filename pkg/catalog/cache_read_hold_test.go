// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// cacheReadLibrary is the SchemaLibrary the cache-read tests cache, and the
// reference a closure resolves it by.
const (
	cacheReadLibraryURI     = "hub.aggrega.ai/schemalibrary/core/CacheRead:1.0.0"
	cacheReadLibraryVersion = "1.0.0"
)

var cacheReadLibraryBody = []byte("apiVersion: aggrega.ai/v1alpha1\n" +
	"kind: SchemaLibrary\nmetadata:\n  name: CacheRead\n  version: 1.0.0\n" +
	"spec:\n  schemas:\n    Storage: {}\n")

// cacheReadPlace is a workspace, its cache directory as configured, that
// directory joined to the workspace, and a directory outside both.
type cacheReadPlace struct{ ws, cacheDir, cache, elsewhere string }

// cacheReadSlotLayouts are the layouts of one cached library's slot the
// cache-read tests drive: plainly inside the workspace; behind a cache directory
// that is a link leading out; with the slot's content or its manifest a link
// leading out; with the content a link leading out of the cache directory to
// elsewhere inside the workspace — refused too, the cache directory being the root
// of everything beneath it — and one leading elsewhere inside the cache directory;
// and beneath a cache directory the configuration names outside the workspace,
// plainly and with the content linking out of it. Each lays the slot out and
// returns the link a refusal must name, or "" for a layout that reads.
func cacheReadSlotLayouts() []struct {
	name     string
	cacheDir string
	layout   func(t *testing.T, p cacheReadPlace) string
} {
	seed := func(t *testing.T, p cacheReadPlace) string {
		t.Helper()
		seedVerifiedEntitySlot(t, p.ws, p.cacheDir, cacheReadLibraryURI, cacheReadLibraryVersion, oci.MediaTypeSchemaLibrary, cacheReadLibraryBody)
		slot, err := oci.CachePath(p.ws, p.cacheDir, cacheReadLibraryURI, cacheReadLibraryVersion)
		if err != nil {
			t.Fatal(err)
		}
		return slot
	}
	// moveTo replaces the slot's file name with a link to a copy of it kept in dir,
	// and returns the link.
	moveTo := func(t *testing.T, dir, slot, name string) string {
		t.Helper()
		at := filepath.Join(slot, name)
		data, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		kept := filepath.Join(dir, name)
		if err := os.WriteFile(kept, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(at); err != nil {
			t.Fatal(err)
		}
		linkForCacheRead(t, kept, at)
		return at
	}
	moveOut := func(t *testing.T, p cacheReadPlace, slot, name string) string {
		t.Helper()
		return moveTo(t, p.elsewhere, slot, name)
	}
	return []struct {
		name     string
		cacheDir string
		layout   func(t *testing.T, p cacheReadPlace) string
	}{
		{"control: the slot inside the workspace", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			seed(t, p)
			return ""
		}},
		{"a cache directory that is a link leading out", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			// The held writer refuses to write through the link, so the slot is laid
			// out in a plain cache directory first, and that directory is then moved
			// to where the link leads.
			seed(t, p)
			kept := filepath.Join(p.elsewhere, "cache")
			if err := os.Rename(p.cache, kept); err != nil {
				t.Fatal(err)
			}
			linkForCacheRead(t, kept, p.cache)
			return p.cache
		}},
		{"the slot's content a link leading out", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			return moveOut(t, p, seed(t, p), "blob")
		}},
		{"the slot's manifest a link leading out", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			return moveOut(t, p, seed(t, p), "manifest")
		}},
		{"the slot's content a link leading out of the cache directory, elsewhere inside the workspace", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			return moveTo(t, filepath.Join(p.ws, "kept"), seed(t, p), "blob")
		}},
		{"control: the slot's content a link leading elsewhere inside the cache directory", ".aggrega/cache", func(t *testing.T, p cacheReadPlace) string {
			moveTo(t, filepath.Join(p.cache, "kept"), seed(t, p), "blob")
			return ""
		}},
		{"control: a cache directory the configuration names outside the workspace", "../shared-cache", func(t *testing.T, p cacheReadPlace) string {
			seed(t, p)
			return ""
		}},
		{"the slot's content linking out of a cache directory named outside the workspace", "../shared-cache", func(t *testing.T, p cacheReadPlace) string {
			return moveOut(t, p, seed(t, p), "blob")
		}},
	}
}

// newCacheReadPlace makes a workspace and a directory outside it for one row.
func newCacheReadPlace(t *testing.T, cacheDir string) cacheReadPlace {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return cacheReadPlace{ws: ws, cacheDir: cacheDir, cache: filepath.Join(ws, cacheDir), elsewhere: t.TempDir()}
}

// assertCacheReadRefused holds err to the read hold's condition, returned as
// itself and naming the link, rather than to a miss.
func assertCacheReadRefused(t *testing.T, err error, link string) {
	t.Helper()
	if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
		t.Fatalf("want the read hold's refusal, got %v", err)
	}
	if errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("the refusal must not read as a miss: %v", err)
	}
	if !strings.Contains(err.Error(), link) {
		t.Errorf("the refusal does not name the link %s: %v", link, err)
	}
}

// TestTheClosuresCacheTierReadsASlotOnlyFromWhereItsWriteIsHeld drives the
// closure's cache tier — the read a pull's reuse check and every compile's closure
// walk make of a cached entity — through the closure lookup a compile resolves
// with, over each slot layout. A refused layout halts the resolution with the read
// hold, naming the link: it is not a miss, which would send the resolution on to
// the registry tier to fetch into the same slot.
func TestTheClosuresCacheTierReadsASlotOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	axis := schemaLibraryAxisForCacheRead(t)
	for _, row := range cacheReadSlotLayouts() {
		t.Run(row.name, func(t *testing.T) {
			p := newCacheReadPlace(t, row.cacheDir)
			link := row.layout(t, p)

			view := NewLazyLookup(p.ws, p.cacheDir, nil, nil)
			lookup := view.ClosureLookup(nil, p.ws, p.cacheDir, nil, nil, nil)
			res, err := lookup.ResolveLibrary(context.Background(), axis, cacheReadLibraryURI)
			if link == "" {
				if err != nil {
					t.Fatalf("refused a slot its root holds: %v", err)
				}
				if res.ResolutionPath != closure.ResolutionPathCache {
					t.Errorf("resolved through %q, want the cache tier", res.ResolutionPath)
				}
				return
			}
			assertCacheReadRefused(t, err, link)
		})
	}
}

// TestTheBuildsPinGateHaltsOnASlotTheReadHoldRefuses drives the build's integrity
// gate over a pinned slot, for each slot layout. A refused layout halts the gate
// with the read hold, naming the link, rather than passing the pin over as one no
// slot holds.
func TestTheBuildsPinGateHaltsOnASlotTheReadHoldRefuses(t *testing.T) {
	for _, row := range cacheReadSlotLayouts() {
		t.Run(row.name, func(t *testing.T) {
			p := newCacheReadPlace(t, row.cacheDir)
			link := row.layout(t, p)

			// The content pin the slot was seeded under; no manifest pin, so the gate
			// holds the content to the manifest beside it and to this pin alone.
			view := NewLazyLookup(p.ws, p.cacheDir, nil, nil)
			view.SetPins(map[string]Pin{cacheReadLibraryURI: {
				ResolvedVersion: cacheReadLibraryVersion,
				Digest:          rowDigestOf(cacheReadLibraryBody),
			}})
			err := view.VerifyPinnedIntegrity()
			if link == "" {
				if err != nil {
					t.Fatalf("the gate refused a slot its root holds: %v", err)
				}
				return
			}
			assertCacheReadRefused(t, err, link)
		})
	}
}

// TestTheCachedLibrariesARemedyNamesAreReadOnlyFromInsideTheCache drives the
// listing a failed resolution's remedy draws from the cache: a library document
// kept inside is named; one that is a link leading out, and every one behind a
// cache directory that is a link leading out, is passed over unread — the remedy
// only enriches a halt the resolution makes on its own account. The cache
// directory is the root of everything beneath it, so an entity subtree that is a
// link leading out of it — elsewhere inside the workspace too — is passed over the
// same way, while one leading elsewhere inside the cache directory is a link the
// hold admits and is read where it leads, as every other read of the cache reads
// it; walked as the link alone it would name nothing.
func TestTheCachedLibrariesARemedyNamesAreReadOnlyFromInsideTheCache(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	library := func(name string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: " + name + "\n  version: 1.0.0\n"
	}
	put := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		name   string
		layout func(t *testing.T, p cacheReadPlace)
		want   []string
	}{
		{"control: a library document inside the cache is named", func(t *testing.T, p cacheReadPlace) {
			put(t, filepath.Join(p.cache, "entities", "inside", "1.0.0", "library.yaml"), library("Inside"))
		}, []string{`SchemaLibrary "Inside"`}},
		{"a library document that is a link leading out is passed over", func(t *testing.T, p cacheReadPlace) {
			put(t, filepath.Join(p.cache, "entities", "inside", "1.0.0", "library.yaml"), library("Inside"))
			put(t, filepath.Join(p.elsewhere, "library.yaml"), library("Outside"))
			linkForCacheRead(t, filepath.Join(p.elsewhere, "library.yaml"), filepath.Join(p.cache, "entities", "outside", "1.0.0", "library.yaml"))
		}, []string{`SchemaLibrary "Inside"`}},
		{"every document behind a cache directory that is a link leading out is passed over", func(t *testing.T, p cacheReadPlace) {
			linkForCacheRead(t, p.elsewhere, p.cache)
			put(t, filepath.Join(p.cache, "entities", "outside", "1.0.0", "library.yaml"), library("Outside"))
		}, nil},
		{"an entity subtree that is a link leading elsewhere inside the cache directory is read where it leads", func(t *testing.T, p cacheReadPlace) {
			put(t, filepath.Join(p.cache, "kept-entities", "inside", "1.0.0", "library.yaml"), library("Inside"))
			linkForCacheRead(t, "kept-entities", filepath.Join(p.cache, "entities"))
		}, []string{`SchemaLibrary "Inside"`}},
		{"an entity subtree that is a link leading out of the cache directory, elsewhere inside the workspace, is passed over", func(t *testing.T, p cacheReadPlace) {
			put(t, filepath.Join(p.ws, "kept", "entities", "inside", "1.0.0", "library.yaml"), library("Inside"))
			linkForCacheRead(t, filepath.Join("..", "..", "kept", "entities"), filepath.Join(p.cache, "entities"))
		}, nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			p := newCacheReadPlace(t, cacheDir)
			row.layout(t, p)
			view := NewLazyLookup(p.ws, cacheDir, nil, nil)
			got := view.unverifiedCachedSchemaLibraries()
			if fmt.Sprint(got) != fmt.Sprint(row.want) {
				t.Errorf("named %v, want %v", got, row.want)
			}
		})
	}
}

// schemaLibraryAxisForCacheRead is the library axis SchemaLibraries resolve on.
func schemaLibraryAxisForCacheRead(t *testing.T) closure.LibraryAxis {
	t.Helper()
	for _, axis := range closure.LibraryAxes() {
		if axis.Kind == "SchemaLibrary" {
			return axis
		}
	}
	t.Fatal("no library axis resolves SchemaLibraries")
	return closure.LibraryAxis{}
}

// linkForCacheRead makes at a symbolic link to target, creating the directory
// holding it, or skips where links are not available.
func linkForCacheRead(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
