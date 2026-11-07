// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// staleLock is a lock whose one pin the step would change, so a write through a
// link to it is visible as a change to the file the link leads to.
const staleLock = "version: 1\nlocalSourceDigests:\n  - path: chart/Chart.yaml\n    digest: sha256:0000000000000000000000000000000000000000000000000000000000000000\n"

// writeLocalRecipe authors, in dir, the Recipe the hold tests pin: one whose only
// input is a local chart beside it, so the step resolves nothing over a network.
func writeLocalRecipe(t *testing.T, dir string) string {
	t.Helper()
	recipe := filepath.Join(dir, "local", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("local", "  source: ./chart\n", false))
	writeFile(t, filepath.Join(dir, "local", "chart", "Chart.yaml"), "apiVersion: v2\nname: local\nversion: 1.0.0\n")
	return recipe
}

// TestALockBesideAnEntityInANamedDirectoryIsHeldToTheRootThatDirectoryPicks drives
// the step's write and its report over a Recipe found in a catalog directory,
// one shape of directory and lock per row beside a control that pins a Recipe in
// a plain named directory outside the workspace.
//
// A named directory outside the workspace is a root of its own, so a lock that is
// a link leading out of it is refused and one leading elsewhere inside it is not
// held. A named directory reached through a link leading out of the workspace is
// followed where it was named — but the same directory, where nobody named it, is
// the workspace's, and a lock beneath it is refused as leading out. A named
// directory inside the workspace holds its locks to the workspace root, so a lock
// leading elsewhere in the workspace is not held.
//
// The link standing for a directory sits above it rather than at it: the catalog
// walk does not descend a directory that is itself a link, so no entity would be
// found beneath one for its lock to be judged.
//
// A lock that is a link and is not held is the hold's verdict alone, and the step
// writes it where the link leads. The lock is then one of the files the entity
// carries, and every later reading of those files — a pull's digest of the entity,
// a publication — refuses a link among them, so such a lock never completes a pull.
// The rows say what the step's write does, because that is the write the hold
// judges; that the link is then refused is the entity's own files' rule, proven
// where those files are read.
func TestALockBesideAnEntityInANamedDirectoryIsHeldToTheRootThatDirectoryPicks(t *testing.T) {
	type layout struct{ ws, outside, elsewhere string }
	rows := []struct {
		name string
		// arrange lays out the row and returns the catalog directory, whether it is
		// named, the file the lock is a link to ("" for none), and where the lock
		// must land when the row is not refused.
		arrange func(t *testing.T, l layout) (cat string, named bool, target, landsAt string)
		refused bool
	}{
		{
			name: "control: a lock beside an entity in a named directory outside the workspace",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				cat := filepath.Join(l.outside, "cat")
				recipe := writeLocalRecipe(t, cat)
				return cat, true, "", recipepin.LockPath(recipe)
			},
		},
		{
			name: "a lock that is a link leading out of a named directory outside the workspace is refused",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				cat := filepath.Join(l.outside, "cat")
				recipe := writeLocalRecipe(t, cat)
				target := filepath.Join(l.elsewhere, "entity.lock")
				writeFile(t, target, staleLock)
				linkOrSkip(t, target, recipepin.LockPath(recipe))
				return cat, true, target, ""
			},
			refused: true,
		},
		{
			name: "a lock that is a link to elsewhere inside a named directory outside the workspace is not held, and the step writes where it leads",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				cat := filepath.Join(l.outside, "cat")
				recipe := writeLocalRecipe(t, cat)
				target := filepath.Join(cat, "kept", "entity.lock")
				writeFile(t, target, staleLock)
				linkOrSkip(t, target, recipepin.LockPath(recipe))
				return cat, true, target, target
			},
		},
		{
			name: "a named directory reached through a link leading out of the workspace is followed",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				linkedTo := filepath.Join(l.outside, "sub")
				writeLocalRecipe(t, filepath.Join(linkedTo, "cat"))
				linkOrSkip(t, linkedTo, filepath.Join(l.ws, "sub"))
				return filepath.Join(l.ws, "sub", "cat"), true, "", filepath.Join(linkedTo, "cat", "local", recipepin.LockFileName)
			},
		},
		{
			name: "the same directory where nobody named it is the workspace's and is refused",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				linkedTo := filepath.Join(l.outside, "sub")
				writeLocalRecipe(t, filepath.Join(linkedTo, "cat"))
				linkOrSkip(t, linkedTo, filepath.Join(l.ws, "sub"))
				return filepath.Join(l.ws, "sub", "cat"), false, filepath.Join(linkedTo, "cat", "local", recipepin.LockFileName), ""
			},
			refused: true,
		},
		{
			name: "a lock that is a link beneath a named directory inside the workspace leading elsewhere inside it is not held, and the step writes where it leads",
			arrange: func(t *testing.T, l layout) (string, bool, string, string) {
				cat := filepath.Join(l.ws, "catalog")
				recipe := writeLocalRecipe(t, cat)
				target := filepath.Join(l.ws, "other", "entity.lock")
				writeFile(t, target, staleLock)
				linkOrSkip(t, target, recipepin.LockPath(recipe))
				return cat, true, target, target
			},
		},
	}
	for _, row := range rows {
		for _, report := range []bool{false, true} {
			name := row.name + " (write)"
			if report {
				name = row.name + " (report)"
			}
			t.Run(name, func(t *testing.T) {
				l := layout{ws: t.TempDir(), outside: t.TempDir(), elsewhere: t.TempDir()}
				cat, named, target, landsAt := row.arrange(t, l)
				before, _ := os.ReadFile(target)
				in := PlanInputs{
					View:          catalog.NewLazyLookup(l.ws, "", []string{cat}, nil),
					Entities:      recipes("helm/local:1.0.0"),
					Resolver:      &recordingResolver{},
					WorkspaceRoot: l.ws,
					ForWriting:    true,
				}
				if named {
					in.NamedDirs = []string{cat}
				}
				plan, err := Compute(in)
				if err != nil {
					t.Fatalf("Compute: %v", err)
				}
				if report {
					_, _, err = plan.Report()
				} else {
					_, err = plan.Write()
				}

				if row.refused {
					if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
						t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
					}
					after, aerr := os.ReadFile(target)
					switch {
					case before == nil && !os.IsNotExist(aerr):
						t.Errorf("a lock was created at %s", target)
					case before != nil && string(after) != string(before):
						t.Errorf("the lock at %s was rewritten:\n%s", target, after)
					}
					return
				}
				if err != nil {
					t.Fatalf("refused a lock its root holds: %v", err)
				}
				if report {
					return
				}
				after, err := os.ReadFile(landsAt)
				if err != nil {
					t.Fatalf("the lock did not land at %s: %v", landsAt, err)
				}
				if string(after) == staleLock {
					t.Errorf("the lock at %s still holds the stale pins", landsAt)
				}
			})
		}
	}
}

// TestALockBesideANamedEntityIsHeldToTheEntitysOwnDirectory drives the step a
// caller runs over an entity it names by path, whose directory is the one the
// caller named: beside an entity outside the workspace the lock is held to that
// directory, so a lock that is a link leading out of it is refused, and an entity
// named through a link leading out of the workspace is followed where it was
// named. One fault per row beside a control pinning an entity outside the
// workspace.
func TestALockBesideANamedEntityIsHeldToTheEntitysOwnDirectory(t *testing.T) {
	type layout struct{ ws, outside, elsewhere string }
	rows := []struct {
		name    string
		arrange func(t *testing.T, l layout) (entity, target, landsAt string)
		dryRun  bool
		refused bool
	}{
		{
			name: "control: an entity outside the workspace is pinned beside itself",
			arrange: func(t *testing.T, l layout) (string, string, string) {
				recipe := writeLocalRecipe(t, l.outside)
				return recipe, "", recipepin.LockPath(recipe)
			},
		},
		{
			name: "a lock that is a link leading out of the entity's directory is refused",
			arrange: func(t *testing.T, l layout) (string, string, string) {
				recipe := writeLocalRecipe(t, l.outside)
				target := filepath.Join(l.elsewhere, "entity.lock")
				writeFile(t, target, staleLock)
				linkOrSkip(t, target, recipepin.LockPath(recipe))
				return recipe, target, ""
			},
			refused: true,
		},
		{
			name: "a report over a lock leading out of the entity's directory is refused where the write would be",
			arrange: func(t *testing.T, l layout) (string, string, string) {
				recipe := writeLocalRecipe(t, l.outside)
				target := filepath.Join(l.elsewhere, "entity.lock")
				writeFile(t, target, staleLock)
				linkOrSkip(t, target, recipepin.LockPath(recipe))
				return recipe, target, ""
			},
			dryRun:  true,
			refused: true,
		},
		{
			name: "an entity named through a link leading out of the workspace is followed",
			arrange: func(t *testing.T, l layout) (string, string, string) {
				writeLocalRecipe(t, l.outside)
				linkOrSkip(t, l.outside, filepath.Join(l.ws, "linked"))
				return filepath.Join(l.ws, "linked", "local", "recipe.yaml"), "", filepath.Join(l.outside, "local", recipepin.LockFileName)
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			l := layout{ws: t.TempDir(), outside: t.TempDir(), elsewhere: t.TempDir()}
			rw := resolvedWorkspace(t, l.ws)
			entity, target, landsAt := row.arrange(t, l)
			before, _ := os.ReadFile(target)

			_, err := Pin(context.Background(), Options{Workspace: rw, EntityPath: entity, DryRun: row.dryRun})

			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if after, _ := os.ReadFile(target); string(after) != string(before) {
					t.Errorf("the lock at %s was rewritten:\n%s", target, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a lock its root holds: %v", err)
			}
			if _, err := os.Stat(landsAt); err != nil {
				t.Errorf("the lock did not land at %s: %v", landsAt, err)
			}
		})
	}
}
