// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// typedReleaseByName is the authoring fixture's Release, naming its Aggregate and
// its Manifest by name rather than by path, so a pull of it looks for them among
// the documents beside the Release.
const typedReleaseByName = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`

// TestAPullOfATypedReleaseThatIsALinkResolvesTheNamesBesideIt drives a whole pull of
// a Release named on the command line over the authoring fixture, the Release being,
// row by row, a plain file, a link leading out of the workspace, and a plain file
// beside a neighbouring Release that is a link leading out.
//
// A path typed on the command line is honoured as typed, so a Release named through
// a link is read where its name leads and is not judged again when the pull meets it
// beside its Aggregate and its Manifest: the names resolve beside the place it was
// named, and the lockfile is written there, inside the workspace, with nothing
// written where the link leads. A neighbouring Release that is a link leading out is
// workspace content the pull meets on its own account, and halts it naming that link.
func TestAPullOfATypedReleaseThatIsALinkResolvesTheNamesBesideIt(t *testing.T) {
	for _, row := range []struct {
		name string
		// linked makes the typed Release a link to a copy outside the workspace.
		linked bool
		// sibling lays a neighbouring Release that is a link leading out.
		sibling bool
	}{
		{name: "control: the typed Release a plain file"},
		{name: "the typed Release a link leading out of the workspace", linked: true},
		{name: "a neighbouring Release a link leading out halts the pull", sibling: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			t.Chdir(root)
			authoringFixture(t, root)
			releasePath := filepath.Join(root, "release-named.yaml")
			link := func(target, at string) {
				if err := os.Symlink(target, at); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			if row.linked {
				writeFixture(t, filepath.Join(outside, "release-copy.yaml"), typedReleaseByName)
				link(filepath.Join(outside, "release-copy.yaml"), releasePath)
			} else {
				writeFixture(t, releasePath, typedReleaseByName)
			}
			if row.sibling {
				writeFixture(t, filepath.Join(outside, "sibling-copy.yaml"), typedReleaseByName)
				link(filepath.Join(outside, "sibling-copy.yaml"), filepath.Join(root, "sibling.yaml"))
			}

			err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: releasePath,
				CatalogDirs: []string{filepath.Join(root, "catalog")},
			})
			if row.sibling {
				if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
				}
				if want := "the link " + filepath.Join(root, "sibling.yaml") + " leads to "; !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name the neighbouring link:\n%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the pull refused a Release named on the command line: %v", err)
			}
			if _, serr := os.Stat(filepath.Join(root, "release-named.lock")); serr != nil {
				t.Errorf("no lockfile beside the Release as named: %v", serr)
			}
			if _, serr := os.Stat(filepath.Join(outside, "release-copy.lock")); !os.IsNotExist(serr) {
				t.Errorf("a lockfile was written where the link leads (stat: %v)", serr)
			}
		})
	}
}
