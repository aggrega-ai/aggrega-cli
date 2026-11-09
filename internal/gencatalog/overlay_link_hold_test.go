// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gencatalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheOverlaysBeneathAnOverlayDirectoryAreHeldToTheRootItPicks drives a
// generation from the embedded guests — the entry the command line, the language
// server and the assistant tool all reach — over an overlay directory inside the
// workspace and one named outside it, with the first guest's overlay laid, one row at
// a time, as a plain file (the controls), as a link, and inside a directory link.
//
// Every read beneath the overlay directory is held to the root that directory picks:
// the workspace for one inside it, and the overlay directory itself for one named
// outside it, so a link beneath a directory named outside is held to that directory
// even where it leads into the workspace. A link leading out of the root is not read:
// the generation halts with the read hold's condition naming the link, writes nothing,
// and leaves the place the link leads to as it was — passing it over would generate
// without the overlay. A link leading elsewhere inside the root is read where it
// leads, which the row's entity proves by carrying the limit only that overlay
// authors. An overlay directory that is itself a link leading out of the workspace is
// honoured where it was named, a root of its own.
func TestTheOverlaysBeneathAnOverlayDirectoryAreHeldToTheRootItPicks(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Skip("no guests embedded in this build")
	}
	first := uris[0]
	leaf := first[strings.LastIndex(first, "/")+1:]
	// linkedPages is the limit only an overlay read through a link authors; every
	// other overlay carries the 4096 writeEmbeddedOverlays authors.
	const linkedPages = 1234

	type layout struct {
		ws, outside, elsewhere string
	}
	// overlaysWithout lays every overlay but the first guest's into dir.
	overlaysWithout := func(t *testing.T, dir string) {
		t.Helper()
		writeEmbeddedOverlays(t, dir)
		if err := os.Remove(filepath.Join(dir, leaf+".yaml")); err != nil {
			t.Fatal(err)
		}
	}
	// linkedOverlay writes the first guest's overlay, carrying linkedPages, at path.
	linkedOverlay := func(t *testing.T, path string) {
		t.Helper()
		writeFixture(t, path, "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: "+leaf+
			"\nspec:\n  runtime:\n    maxMemoryPages: "+strconv.Itoa(linkedPages)+"\n    timeoutMs: 300000\n")
	}
	rows := []struct {
		name string
		// arrange lays the row out and returns the overlay directory named, and the
		// link a refusal must name — "" for a row that generates.
		arrange func(t *testing.T, l layout) (overlayDir, refusedLink string)
		// pages is the limit the first guest's entity carries where the row generates.
		pages uint32
	}{
		{
			name: "control: an overlay directory inside the workspace",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.ws, "overlays")
				writeEmbeddedOverlays(t, dir)
				return dir, ""
			},
			pages: 4096,
		},
		{
			name: "beneath an overlay directory inside the workspace an overlay linking out of the workspace is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.ws, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(l.elsewhere, leaf+".yaml"))
				at := filepath.Join(dir, leaf+".yaml")
				link(t, filepath.Join(l.elsewhere, leaf+".yaml"), at)
				return dir, at
			},
		},
		{
			name: "beneath an overlay directory inside the workspace an overlay linking elsewhere inside it is read where it leads",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.ws, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(l.ws, "kept", leaf+".yaml"))
				link(t, filepath.Join("..", "kept", leaf+".yaml"), filepath.Join(dir, leaf+".yaml"))
				return dir, ""
			},
			pages: linkedPages,
		},
		{
			name: "beneath an overlay directory inside the workspace a directory link leading out of the workspace is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.ws, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(l.elsewhere, "more", leaf+".yaml"))
				at := filepath.Join(dir, "more")
				link(t, filepath.Join(l.elsewhere, "more"), at)
				return dir, at
			},
		},
		{
			name: "control: an overlay directory named outside the workspace",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.outside, "overlays")
				writeEmbeddedOverlays(t, dir)
				return dir, ""
			},
			pages: 4096,
		},
		{
			name: "beneath an overlay directory named outside the workspace an overlay linking out of it is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.outside, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(l.elsewhere, leaf+".yaml"))
				at := filepath.Join(dir, leaf+".yaml")
				link(t, filepath.Join(l.elsewhere, leaf+".yaml"), at)
				return dir, at
			},
		},
		{
			name: "beneath an overlay directory named outside the workspace an overlay linking elsewhere inside it is read where it leads",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.outside, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(dir, "store", leaf+".overlay"))
				link(t, filepath.Join("store", leaf+".overlay"), filepath.Join(dir, leaf+".yaml"))
				return dir, ""
			},
			pages: linkedPages,
		},
		{
			name: "beneath an overlay directory named outside the workspace an overlay linking into the workspace is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				dir := filepath.Join(l.outside, "overlays")
				overlaysWithout(t, dir)
				linkedOverlay(t, filepath.Join(l.ws, "kept", leaf+".yaml"))
				at := filepath.Join(dir, leaf+".yaml")
				link(t, filepath.Join(l.ws, "kept", leaf+".yaml"), at)
				return dir, at
			},
		},
		{
			name: "an overlay directory that is itself a link leading out of the workspace is a root of its own",
			arrange: func(t *testing.T, l layout) (string, string) {
				target := filepath.Join(l.outside, "overlays")
				overlaysWithout(t, target)
				linkedOverlay(t, filepath.Join(target, "store", leaf+".overlay"))
				link(t, filepath.Join("store", leaf+".overlay"), filepath.Join(target, leaf+".yaml"))
				dir := filepath.Join(l.ws, "overlays")
				link(t, target, dir)
				return dir, ""
			},
			pages: linkedPages,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			l := layout{ws: t.TempDir(), outside: t.TempDir(), elsewhere: t.TempDir()}
			overlayDir, refusedLink := row.arrange(t, l)
			before := listing(t, l.elsewhere)
			out := t.TempDir()

			var stdout bytes.Buffer
			err := Run(context.Background(), Options{
				FromEmbedded: true, OutputDir: out, OverlayPath: overlayDir, WorkspaceRoot: l.ws,
			}, &stdout)

			if after := listing(t, l.elsewhere); after != before {
				t.Errorf("the place outside changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if refusedLink != "" {
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read hold's refusal naming %s, got %v", refusedLink, err)
				}
				if !strings.Contains(err.Error(), refusedLink) {
					t.Errorf("the refusal does not name the link %s: %v", refusedLink, err)
				}
				if !strings.Contains(err.Error(), "overlay directory") {
					t.Errorf("the refusal does not name the remedy an overlay has: %v", err)
				}
				if n := entitiesBeneath(t, out); n != 0 {
					t.Errorf("a refused generation wrote %d entities", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a generation whose overlays its root holds: %v", err)
			}
			path := filepath.Join(out, filepath.FromSlash(first), embeddedGuestVersion(t, first), genCatalogPluginFilename)
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("no entity generated for %s: %v", first, rerr)
			}
			var entity types.Plugin
			if uerr := yaml.Unmarshal(raw, &entity); uerr != nil {
				t.Fatalf("parse %s: %v", path, uerr)
			}
			if entity.Spec.Runtime == nil || entity.Spec.Runtime.MaxMemoryPages != row.pages {
				t.Errorf("%s carries runtime %+v; want maxMemoryPages %d from the overlay the row lays", first, entity.Spec.Runtime, row.pages)
			}
		})
	}
}
