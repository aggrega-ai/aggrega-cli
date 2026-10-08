// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package explainops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// TestTheWalkThroughIsSaidToBeUnreadBehindALink drives the read-only page read an
// explanation takes its walk-through from, over a workspace with no bundle cached
// and over one whose artifact cache directory is a link leading out, a bundle kept
// behind it. The first is told the bundle is not cached; the second that it is not
// read, naming the link — not that it is missing, which would send the reader to
// fetch a bundle they may already hold.
func TestTheWalkThroughIsSaidToBeUnreadBehindALink(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const ref = "reg.example.invalid/docs/aggrega-docs"
	const version = "1.0.0"
	rows := []struct {
		name   string
		layout func(t *testing.T, ws, elsewhere string) string
		want   string
	}{
		{name: "control: no bundle cached", layout: func(*testing.T, string, string) string { return "" },
			want: "is not cached for this workspace"},
		{name: "a bundle behind a cache directory that is a link leading out", want: "is not read",
			layout: func(t *testing.T, ws, elsewhere string) string {
				slot := filepath.Join(elsewhere, "docs-bundles", pathsegment.Encode(ref), pathsegment.Encode(version))
				if err := os.MkdirAll(slot, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"blob", "manifest"} {
					if err := os.WriteFile(filepath.Join(slot, name), []byte("kept "+name), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(ws, cacheDir)
				if err := os.Symlink(elsewhere, link); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
				return link
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, elsewhere := t.TempDir(), t.TempDir()
			link := row.layout(t, ws, elsewhere)

			_, err := ReadPages(ws, cacheDir, ref, version)
			if err == nil {
				t.Fatal("the read answered pages it cannot have")
			}
			if !strings.Contains(err.Error(), row.want) {
				t.Errorf("the reason does not say %q:\n%v", row.want, err)
			}
			if link != "" {
				if strings.Contains(err.Error(), "is not cached") {
					t.Errorf("a bundle behind a link is said to be absent:\n%v", err)
				}
				if !strings.Contains(err.Error(), link) {
					t.Errorf("the reason does not name the link %s:\n%v", link, err)
				}
			}
		})
	}
}
