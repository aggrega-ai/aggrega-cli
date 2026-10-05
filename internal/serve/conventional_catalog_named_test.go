// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestEachReleaseReadsTheConventionalCatalogAsItsOwnStackNamesIt asks the daemon
// for the lookup two Releases of one workspace are answered from, where both stacks
// spell the same single directory — the workspace's conventional `catalog`, a link
// leading out of the workspace — and only one Release's configuration lists it.
//
// The Release that lists it reads it where it leads and resolves the entity behind
// it; the Release that does not reads it as the workspace's own and halts on the
// link. The two stacks are equal as lists of directories, so a daemon keeping one
// lookup per list would answer both from whichever was asked first: one of the two
// would read the other's terms. Each row asks in its own order, so neither order
// can pass by accident.
//
// The control row is a plain `catalog` directory: both Releases resolve the entity,
// which is what makes a halting Release's halt the link's doing.
func TestEachReleaseReadsTheConventionalCatalogAsItsOwnStackNamesIt(t *testing.T) {
	t.Parallel()
	const probe = "example/probe"
	const component = "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: " + probe +
		"\n  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: server\nspec: {}\n"
	const config = "workspace:\n  releases:\n    listing.yaml:\n      catalog_dirs:\n        - ./catalog\n"
	for _, row := range []struct {
		name         string
		linked       bool
		unlistedLast bool
	}{
		{name: "control: a plain conventional directory serves both Releases"},
		{name: "a linked conventional directory, the Release naming nothing asked first", linked: true},
		{name: "a linked conventional directory, the Release listing it asked first", linked: true, unlistedLast: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			conventional := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
			place := conventional
			if row.linked {
				place = filepath.Join(t.TempDir(), "kept-elsewhere")
				if err := os.MkdirAll(place, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(place, conventional); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			writeFixtureFile(t, filepath.Join(place, "probe.yaml"), component)
			unlisted, listing := filepath.Join(ws, "plain.yaml"), filepath.Join(ws, "listing.yaml")
			writeFixtureFile(t, unlisted, "kind: Release\n")
			writeFixtureFile(t, listing, "kind: Release\n")
			writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), config)

			var out bytes.Buffer
			srv := catalogDirsServer(t, ws, &out)
			for _, release := range []string{unlisted, listing} {
				if dirs := srv.catalogDirsFor(release); !slices.Equal(dirs, []string{conventional}) {
					t.Fatalf("precondition: %s reads %v; want the conventional directory alone, so both stacks spell one list", release, dirs)
				}
			}

			order := []string{unlisted, listing}
			if row.unlistedLast {
				order = []string{listing, unlisted}
			}
			for _, release := range order {
				// The session lookup, and the request view a request resolves through,
				// which carries the named part over from it.
				for which, lookup := range map[string]*catalog.LazyLookup{
					"the session lookup": srv.catalogFor(release),
					"its request view":   srv.catalogFor(release).ForRequest(nil),
				} {
					linkErr := lookup.CheckLinks()
					_, resolved := lookup.Component(probe)
					if !row.linked || release == listing {
						if linkErr != nil {
							t.Errorf("%s, %s: halted on a directory its stack names or that leads nowhere outside: %v", release, which, linkErr)
						}
						if !resolved {
							t.Errorf("%s, %s: %s did not resolve from %s", release, which, probe, place)
						}
						continue
					}
					if !errors.Is(linkErr, readscope.ErrReadLeadsOutOfWorkspace()) {
						t.Errorf("%s, %s: want the read-leads-out-of-workspace halt for the unnamed conventional directory, got %v", release, which, linkErr)
					}
					if resolved {
						t.Errorf("%s, %s: %s resolved through a link the Release's stack does not name", release, which, probe)
					}
				}
			}
			if row.linked && srv.catalogFor(unlisted) == srv.catalogFor(listing) {
				t.Error("the two Releases are answered from one lookup although only one of them names the directory")
			}
		})
	}
}
