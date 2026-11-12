// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// TestTheDirectoryRuleReadsNoNeighbourThroughALinkLeadingOut drives every
// command-line moment that asks the directory rule — the pin step and a
// publication, dry run or not — over a Recipe whose neighbour is a link to an
// ENTITY outside the workspace. Read through its link, that neighbour would be
// named by the shared-directory refusal; each moment instead halts with the read
// hold naming the link, in one diagnostic, and writes no lock. The control lays
// the same entity inside the workspace and links it from the same place, and every
// moment then reads it and refuses the directory, which is what proves the halt is
// the link's and not the layout's.
func TestTheDirectoryRuleReadsNoNeighbourThroughALinkLeadingOut(t *testing.T) {
	for _, tc := range []struct {
		name     string
		leadsOut bool
	}{
		{name: "the neighbour leads out of the workspace", leadsOut: true},
		{name: "control: the neighbour leads to a place inside the workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			t.Chdir(root)
			writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
			recipePath := filepath.Join(root, "catalog", "recipes", "server", "recipe.yaml")
			writeFixture(t, recipePath, strings.Replace(neighbourRecipe, "name: neighbour", "name: server", 1))
			target := filepath.Join(root, "elsewhere", "neighbour.yaml")
			if tc.leadsOut {
				target = filepath.Join(outside, "neighbour.yaml")
			}
			writeFixture(t, target, neighbourRecipe)
			if err := os.Symlink(target, filepath.Join(filepath.Dir(recipePath), "neighbour.yaml")); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			push := func(dryRun bool) error {
				return pushRun(context.Background(), pushConfig{Workspace: ws, EntityPath: recipePath, Registry: "example.com",
					PushTarget: memory.New(), DryRun: dryRun, Out: io.Discard})
			}
			moments := []struct {
				name string
				run  func() error
			}{
				{"aggrega pin", func() error { return pinRun(context.Background(), io.Discard, ws, recipePath, false) }},
				{"aggrega pin --dry-run", func() error { return pinRun(context.Background(), io.Discard, ws, recipePath, true) }},
				{"aggrega push --dry-run", func() error { return push(true) }},
				{"aggrega push", func() error { return push(false) }},
			}
			var first string
			for _, m := range moments {
				err := m.run()
				var got string
				if tc.leadsOut {
					if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) || errors.Is(err, entitypin.ErrEntitySharesItsDirectory()) {
						t.Fatalf("%s must halt with the read hold, having read nothing through the link; got %v", m.name, err)
					}
					if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
						t.Errorf("%s: short code %q; want E0321", m.name, code)
					}
					var ce *cerrors.CompilerError
					if !errors.As(err, &ce) {
						t.Fatalf("%s: the halt carries no composed diagnostic: %v", m.name, err)
					}
					got = ce.Error()
					for _, want := range []string{"the link " + filepath.Join(filepath.Dir(recipePath), "neighbour.yaml") + " leads to ",
						"which must stay inside " + root, "A file beside an entity"} {
						if !strings.Contains(got, want) {
							t.Errorf("%s: the halt must carry %q:\n%s", m.name, want, got)
						}
					}
				} else {
					got = directoryRuleDiagnostic(t, m.name, err)
					if !strings.Contains(got, `beside it: Recipe "neighbour" in neighbour.yaml`) {
						t.Errorf("%s: the neighbour read inside the workspace must be named:\n%s", m.name, got)
					}
				}
				if first == "" {
					first = got
				} else if got != first {
					t.Errorf("%s gave a different diagnostic from the first moment;\n--- first ---\n%s\n--- %s ---\n%s", m.name, first, m.name, got)
				}
			}
			if _, err := os.Stat(recipepin.LockPath(recipePath)); !os.IsNotExist(err) {
				t.Errorf("a refused moment wrote the lock; stat err = %v", err)
			}
		})
	}
}
