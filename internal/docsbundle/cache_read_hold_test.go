// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docsbundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// errNoRegistryHere is what the test's acquisition target answers, so a row that
// reaches for a registry is told apart from one that never does.
var errNoRegistryHere = errors.New("the test reaches no registry")

// TestAcquisitionAsksNoRegistryForABundleTheReadHoldRefuses drives the acquisition
// every explanation and documentation resource runs first, over a bundle that is
// absent, one cached inside the workspace, and one behind a cache directory that is a
// link leading out. The absent bundle is fetched for; the cached one is left as it
// is; the one behind the link is refused with the read hold, naming the link, before
// any registry is asked — a presence probe that read it as absent would fetch into
// the same place.
func TestAcquisitionAsksNoRegistryForABundleTheReadHoldRefuses(t *testing.T) {
	const ref = "reg.example.invalid/docs/aggrega-docs"
	const version = "1.0.0"
	const cacheDir = ".aggrega/cache"
	seedBundle := func(t *testing.T, slot string) {
		t.Helper()
		if err := os.MkdirAll(slot, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"blob", "manifest"} {
			if err := os.WriteFile(filepath.Join(slot, name), []byte("cached "+name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows := []struct {
		name   string
		layout func(t *testing.T, ws, elsewhere string) string
		// asked is whether the row reaches for a registry.
		asked bool
	}{
		{name: "control: no bundle cached is fetched for", asked: true, layout: func(*testing.T, string, string) string { return "" }},
		{name: "control: a bundle cached inside the workspace is left as it is", layout: func(t *testing.T, ws, _ string) string {
			seedBundle(t, filepath.Join(docscache.DocsBundlesRoot(ws, cacheDir), pathsegment.Encode(ref), pathsegment.Encode(version)))
			return ""
		}},
		{name: "a bundle behind a cache directory that is a link leading out", layout: func(t *testing.T, ws, elsewhere string) string {
			seedBundle(t, filepath.Join(elsewhere, "docs-bundles", pathsegment.Encode(ref), pathsegment.Encode(version)))
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
			want := row.layout(t, ws, elsewhere)
			asked := false
			target := func(workspaceconfig.Config, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
				asked = true
				return nil, nil, errNoRegistryHere
			}

			acquired, err := EnsureCached(context.Background(), workspaceconfig.Config{}, ws, cacheDir, ref, version, target, nil)
			if asked != row.asked {
				t.Errorf("reached for a registry: %v, want %v", asked, row.asked)
			}
			if acquired {
				t.Errorf("reported an acquisition the test's target cannot make")
			}
			switch {
			case want != "":
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read hold's refusal, got %v", err)
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the link %s: %v", want, err)
				}
			case row.asked:
				if !errors.Is(err, errNoRegistryHere) {
					t.Fatalf("want the fetch's own answer, got %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("refused a bundle the workspace holds: %v", err)
				}
			}
		})
	}
}
