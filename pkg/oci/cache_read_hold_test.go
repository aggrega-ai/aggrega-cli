// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestASlotIsReadOnlyFromWhereItsWriteIsHeld drives the slot reads every cached
// entity and plugin is read through — the content, the manifest, and a file of the
// slot by name — over a cache laid out one way per row: plainly inside the
// workspace; behind a cache directory that is a link leading out; with the slot's
// file itself a link leading out, a link leading out of the cache directory to
// elsewhere inside the workspace — refused too, the cache directory being the root
// of everything beneath it — or a link leading elsewhere inside the cache
// directory; and beneath a cache directory the configuration names outside the
// workspace, plainly, with a file linking out of that directory, and with the
// directory itself a link.
//
// A refused row is held to the condition, to the link it names, and to the bytes:
// a refusal hands back nothing it read, and is never taken for a miss, so no
// caller fetches around it.
func TestASlotIsReadOnlyFromWhereItsWriteIsHeld(t *testing.T) {
	const uri = "oci://hub.example.invalid/component/demo/api"
	const version = "1.0.0"
	readers := []struct {
		name string
		file string
		read func(ws, cacheDir string) ([]byte, error)
	}{
		{"ReadCached", "blob", func(ws, cacheDir string) ([]byte, error) { return ReadCached(ws, cacheDir, "Component", uri, version) }},
		{"ReadCachedManifest", "manifest", func(ws, cacheDir string) ([]byte, error) {
			return ReadCachedManifest(ws, cacheDir, "Component", uri, version)
		}},
		{"ReadCachedFile", "library.yaml", func(ws, cacheDir string) ([]byte, error) {
			return ReadCachedFile(ws, cacheDir, "Component", uri, version, "library.yaml")
		}},
	}
	type place struct{ ws, cacheDir, cache, elsewhere string }
	rows := []struct {
		name     string
		cacheDir string
		// layout lays the slot out for the file the reader reads, and returns the link
		// a refusal must name, or "" for a row that reads.
		layout func(t *testing.T, p place, file string) string
		// missing marks a row whose slot holds nothing, which is a miss.
		missing bool
	}{
		{name: "control: a slot inside the workspace", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place, file string) string {
			seedSlotFile(t, slotDir(t, p.ws, p.cacheDir, uri, version), file)
			return ""
		}},
		{name: "control: an empty cache is a miss", cacheDir: ".aggrega/cache", missing: true, layout: func(*testing.T, place, string) string {
			return ""
		}},
		{name: "a cache directory that is a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place, file string) string {
			linkCacheDir(t, p.elsewhere, p.cache)
			seedSlotFile(t, slotDir(t, p.ws, p.cacheDir, uri, version), file)
			return p.cache
		}},
		{name: "a slot's file that is a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place, file string) string {
			dir := slotDir(t, p.ws, p.cacheDir, uri, version)
			mustMkdir(t, dir)
			seedSlotFile(t, p.elsewhere, file)
			linkCacheDir(t, filepath.Join(p.elsewhere, file), filepath.Join(dir, file))
			return filepath.Join(dir, file)
		}},
		{name: "a slot's file that is a link leading out of the cache directory, elsewhere inside the workspace", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place, file string) string {
			dir := slotDir(t, p.ws, p.cacheDir, uri, version)
			mustMkdir(t, dir)
			kept := filepath.Join(p.ws, "kept")
			seedSlotFile(t, kept, file)
			linkCacheDir(t, filepath.Join(kept, file), filepath.Join(dir, file))
			return filepath.Join(dir, file)
		}},
		{name: "control: a slot's file that is a link leading elsewhere inside the cache directory", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place, file string) string {
			dir := slotDir(t, p.ws, p.cacheDir, uri, version)
			mustMkdir(t, dir)
			kept := filepath.Join(p.cache, "kept")
			seedSlotFile(t, kept, file)
			linkCacheDir(t, filepath.Join(kept, file), filepath.Join(dir, file))
			return ""
		}},
		{name: "control: a cache directory the configuration names outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place, file string) string {
			seedSlotFile(t, slotDir(t, p.ws, p.cacheDir, uri, version), file)
			return ""
		}},
		{name: "a file linking out of a cache directory named outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place, file string) string {
			dir := slotDir(t, p.ws, p.cacheDir, uri, version)
			mustMkdir(t, dir)
			seedSlotFile(t, p.elsewhere, file)
			linkCacheDir(t, filepath.Join(p.elsewhere, file), filepath.Join(dir, file))
			return filepath.Join(dir, file)
		}},
		{name: "control: a cache directory named outside the workspace that is itself a link, read where it was named", cacheDir: "../shared-cache", layout: func(t *testing.T, p place, file string) string {
			linkCacheDir(t, p.elsewhere, p.cache)
			seedSlotFile(t, slotDir(t, p.ws, p.cacheDir, uri, version), file)
			return ""
		}},
	}
	for _, reader := range readers {
		for _, row := range rows {
			t.Run(reader.name+"/"+row.name, func(t *testing.T) {
				ws := filepath.Join(t.TempDir(), "ws")
				mustMkdir(t, ws)
				p := place{ws: ws, cacheDir: row.cacheDir, cache: filepath.Join(ws, row.cacheDir), elsewhere: t.TempDir()}
				link := row.layout(t, p, reader.file)

				got, err := reader.read(ws, row.cacheDir)
				switch {
				case row.missing:
					if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrFetchNotFound()) {
						t.Fatalf("an empty slot must read as a miss; got %v", err)
					}
				case link == "":
					if err != nil {
						t.Fatalf("refused a slot its root holds: %v", err)
					}
					if string(got) != slotContent(reader.file) {
						t.Errorf("read %q, want the slot's %q", got, slotContent(reader.file))
					}
				default:
					if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
						t.Fatalf("want the read hold's refusal, got %v (bytes %q)", err, got)
					}
					if errors.Is(err, ErrFetchNotFound()) {
						t.Errorf("the refusal must not read as a miss: %v", err)
					}
					if got != nil {
						t.Errorf("a refused read handed back %q", got)
					}
					if !strings.Contains(err.Error(), link) {
						t.Errorf("the refusal does not name the link %s: %v", link, err)
					}
				}
			})
		}
	}
}

// slotDir is the slot for uri@version beneath the workspace's cache directory.
func slotDir(t *testing.T, ws, cacheDir, uri, version string) string {
	t.Helper()
	dir, err := CachePath(ws, cacheDir, "Component", uri, version)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// seedSlotFile writes the file named name, holding its content, in dir.
func seedSlotFile(t *testing.T, dir, name string) {
	t.Helper()
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(slotContent(name)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// slotContent is what a seeded file named name holds.
func slotContent(name string) string { return "the cached " + name }

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
