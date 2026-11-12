// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gencatalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestGeneratedEntitiesAreHeldToTheRootTheirOutputDirectoryPicks drives the
// generation over the shapes an output directory takes against a workspace, one
// fault per row beside a control writing into a plain directory outside the
// workspace.
//
// An output directory outside the workspace is a root of its own, so a link
// beneath it leading out of it is refused and the place it leads to is left as
// it was. An output directory that is itself a link is followed where it was
// named. An output directory inside the workspace holds what is written beneath
// it to the workspace, so a link leading elsewhere in the workspace is followed
// and one leading out of it is refused. A run that reports rather than writes
// halts where the write would.
//
// The links stand at the directory the generation composes first beneath the
// output directory — the socket directory of the first generated entity — which
// is read off the composition itself rather than spelled here, so the rows hold
// whichever guests are embedded.
func TestGeneratedEntitiesAreHeldToTheRootTheirOutputDirectoryPicks(t *testing.T) {
	if len(embedded.URIs()) == 0 {
		t.Skip("no guests embedded in this build")
	}
	overlays := t.TempDir()
	writeEmbeddedOverlays(t, overlays)
	socket := firstSocketDirectory(t, overlays)

	type layout struct {
		ws, outside, elsewhere string
	}
	rows := []struct {
		name string
		// arrange lays out the row's directories and returns the output directory
		// named, and where the first entity must land when the row is not refused.
		arrange func(t *testing.T, l layout) (outputDir, landsIn string)
		reports bool
		refused bool
	}{
		{
			name: "control: an output directory outside the workspace is written beneath",
			arrange: func(t *testing.T, l layout) (string, string) {
				out := filepath.Join(l.outside, "gen")
				mkdir(t, out)
				return out, out
			},
		},
		{
			name: "a link beneath an output directory outside the workspace leading out of it is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				out := filepath.Join(l.outside, "gen")
				mkdir(t, out)
				link(t, l.elsewhere, filepath.Join(out, socket))
				return out, ""
			},
			refused: true,
		},
		{
			name: "a report over a link leading out of the output directory is refused where the write would be",
			arrange: func(t *testing.T, l layout) (string, string) {
				out := filepath.Join(l.outside, "gen")
				mkdir(t, out)
				link(t, l.elsewhere, filepath.Join(out, socket))
				return out, ""
			},
			reports: true,
			refused: true,
		},
		{
			name: "an output directory that is itself a link leading out of the workspace is followed where it was named",
			arrange: func(t *testing.T, l layout) (string, string) {
				target := filepath.Join(l.outside, "target")
				mkdir(t, target)
				out := filepath.Join(l.ws, "gen-linked")
				link(t, target, out)
				return out, target
			},
		},
		{
			name: "beneath an output directory inside the workspace a link leading elsewhere inside it is followed",
			arrange: func(t *testing.T, l layout) (string, string) {
				shared := filepath.Join(l.ws, "shared")
				mkdir(t, shared)
				out := filepath.Join(l.ws, "gen")
				mkdir(t, out)
				link(t, shared, filepath.Join(out, socket))
				return out, shared
			},
		},
		{
			name: "beneath an output directory inside the workspace a link leading out of the workspace is refused",
			arrange: func(t *testing.T, l layout) (string, string) {
				out := filepath.Join(l.ws, "gen")
				mkdir(t, out)
				link(t, l.elsewhere, filepath.Join(out, socket))
				return out, ""
			},
			refused: true,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			l := layout{ws: t.TempDir(), outside: t.TempDir(), elsewhere: t.TempDir()}
			if err := os.WriteFile(filepath.Join(l.elsewhere, "kept.txt"), []byte("kept\n"), 0o644); err != nil {
				t.Fatalf("write the sentinel: %v", err)
			}
			outputDir, landsIn := row.arrange(t, l)
			before := listing(t, l.elsewhere)

			opts := Options{FromEmbedded: true, OutputDir: outputDir, OverlayPath: overlays, WorkspaceRoot: l.ws}
			if row.reports {
				opts.Delivery = authoredcontent.DeliveryFor(true)
			}
			var out bytes.Buffer
			err := Run(context.Background(), opts, &out)

			if after := listing(t, l.elsewhere); after != before {
				t.Errorf("the place the link leads to changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if n := entitiesBeneath(t, l.outside) + entitiesBeneath(t, l.ws); n != 0 {
					t.Errorf("a refused generation left %d entities on disk", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a generation its root holds: %v", err)
			}
			if entitiesBeneath(t, landsIn) == 0 {
				t.Errorf("no entity was written beneath %s", landsIn)
			}
		})
	}
}

// firstSocketDirectory returns the directory the generation composes first
// beneath an output directory: the leading segment of the first entity's place.
func firstSocketDirectory(t *testing.T, overlays string) string {
	t.Helper()
	out := t.TempDir()
	docs, err := composeFromEmbedded(context.Background(), Options{FromEmbedded: true, OutputDir: out, OverlayPath: overlays})
	if err != nil || len(docs) == 0 {
		t.Fatalf("compose the embedded entities: %d documents, %v", len(docs), err)
	}
	rel, err := filepath.Rel(out, docs[0].Path)
	if err != nil {
		t.Fatalf("place %s beneath %s: %v", docs[0].Path, out, err)
	}
	return strings.Split(filepath.ToSlash(rel), "/")[0]
}

// entitiesBeneath counts the generated entities beneath dir, links followed by the
// walk only where dir itself is one.
func entitiesBeneath(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir+string(filepath.Separator), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == genCatalogPluginFilename {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return n
}

// listing names every entry beneath dir, links not followed, with its kind and
// size, so two listings differ exactly where something was written or removed.
func listing(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(&b, "%s %s %d\n", rel, info.Mode().Type(), info.Size())
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return b.String()
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
