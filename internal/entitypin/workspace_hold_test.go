// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestTheEntityLockIsHeldToTheWorkspace drives the pin step's write and its
// report over one Recipe with a local source inside the workspace's catalog, whose
// entity.lock is, one row at a time, absent, a link to a stale lock outside, and a
// link leading nowhere outside.
//
// A refused row is held to the file the link leads to as well as to the
// condition: a stale lock outside must still be stale, and a lock that was never
// there must still not be there, because the step writes beside the entity and a
// write through either link lands outside the workspace.
func TestTheEntityLockIsHeldToTheWorkspace(t *testing.T) {
	const stale = "version: 1\nlocalSourceDigests:\n  - path: chart/Chart.yaml\n    digest: sha256:0000000000000000000000000000000000000000000000000000000000000000\n"
	rows := []struct {
		name    string
		layout  func(t *testing.T, lock, outside string) string
		refused bool
	}{
		{name: "control: no lock yet", layout: func(*testing.T, string, string) string { return "" }},
		{name: "a lock that is a link to a lock outside", refused: true, layout: func(t *testing.T, lock, outside string) string {
			target := filepath.Join(outside, "entity.lock")
			writeFile(t, target, stale)
			linkOrSkip(t, target, lock)
			return target
		}},
		{name: "a lock that is a link leading nowhere outside", refused: true, layout: func(t *testing.T, lock, outside string) string {
			target := filepath.Join(outside, "absent.lock")
			linkOrSkip(t, target, lock)
			return target
		}},
	}
	for _, row := range rows {
		for _, report := range []bool{false, true} {
			name := row.name + " (write)"
			if report {
				name = row.name + " (report)"
			}
			t.Run(name, func(t *testing.T) {
				ws, outside := t.TempDir(), t.TempDir()
				cat := filepath.Join(ws, "catalog")
				recipe := filepath.Join(cat, "local", "recipe.yaml")
				writeFile(t, recipe, recipeYAML("local", "  source: ./chart\n", false))
				writeFile(t, filepath.Join(cat, "local", "chart", "Chart.yaml"), "apiVersion: v2\nname: local\nversion: 1.0.0\n")
				target := row.layout(t, recipepin.LockPath(recipe), outside)
				var before []byte
				if target != "" {
					before, _ = os.ReadFile(target)
				}

				plan, err := Compute(PlanInputs{
					View:          catalog.NewLazyLookup(ws, "", []string{cat}, nil),
					URIs:          []string{"helm/local:1.0.0"},
					Resolver:      &recordingResolver{},
					WorkspaceRoot: ws,
					ForWriting:    true,
				})
				if err != nil {
					t.Fatalf("Compute: %v", err)
				}
				if report {
					_, _, err = plan.Report()
				} else {
					_, err = plan.Write()
				}

				if !row.refused {
					if err != nil {
						t.Fatalf("refused a lock the workspace holds: %v", err)
					}
					return
				}
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				after, aerr := os.ReadFile(target)
				switch {
				case before == nil && !os.IsNotExist(aerr):
					t.Errorf("a lock was created outside the workspace at %s", target)
				case before != nil && string(after) != string(before):
					t.Errorf("the lock outside the workspace was rewritten:\n%s", after)
				}
			})
		}
	}
}

func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
