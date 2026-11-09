// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// guestVersionToken stands, in a row's expected changes below, for the version a
// generated Plugin entity's guest reports about its own build. It is the one
// segment of a destination no argument and no default decides, so it is read
// from the guest rather than written into the row.
const guestVersionToken = "<version>"

// TestEveryWritingCommandWritesWhereItsDestinationSays is the census of the
// commands that write a file at a destination the call does not spell out in
// full, each run through the real command tree from a sub-folder of a fresh
// workspace, with the change it makes to the whole fixture written out as a
// literal.
//
// One row per destination rule:
//   - `aggrega import` naming no destination writes beneath the workspace root;
//   - a configuration write with --workspace rewrites the configuration file at
//     the workspace root;
//   - `aggrega plugin gen-catalog --output-dir out` writes beneath out in the
//     folder the command runs in, since a path you type resolves there;
//   - `aggrega pin` of a relative entity path writes the entity.lock beside the
//     entity;
//   - `aggrega skills install` naming no --global writes the assistant's skill
//     folder beneath the workspace root.
//
// A command added later that writes by default into the folder it runs in has a
// row to land in here, and the row it gets says so in the open.
//
// 🔴 PLANTED DEFECTS:
//   - join the workspace configuration destination to the process's folder
//     (pkg/workspaceconfig/writetarget.go, workspaceConfigDestination: path :=
//     filepath.Join(".", ConfigFilename)): the configuration row reddens, the
//     file landing in ws/sub — which shows the table reads the real tree.
//   - have outputDirFor (internal/importops/import.go) return "." for an empty
//     name: the import row reddens, its draft landing in ws/sub.
//
// Not parallel: it points the shared import seam, a package-level variable, and
// moves the process working directory.
func TestEveryWritingCommandWritesWhereItsDestinationSays(t *testing.T) {
	pointImportSeamAtDemoGuest(t)
	withCLIRegistrations(t, nil)
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")

	// The guest a catalog generation reads, and the overlay naming it. Both sit
	// outside every row's workspace, so neither is part of what a row changes.
	linter, ok := embedded.Lookup(embedded.PublishingLinterURI)
	if !ok {
		t.Fatal("this binary embeds no publishing linter, so the catalog-generation row has no guest to read")
	}
	inputs := t.TempDir()
	linterPath := filepath.Join(inputs, "linter.wasm")
	if err := os.WriteFile(linterPath, linter, 0o644); err != nil {
		t.Fatalf("write the linter guest: %v", err)
	}
	overlayPath := filepath.Join(inputs, "overlay.yaml")
	writeFixture(t, overlayPath, "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: publishing-linter\n"+
		"spec:\n  runtime:\n    maxMemoryPages: 4096\n    timeoutMs: 300000\n")
	sourcePath := filepath.Join(inputs, "demo-source.txt")
	writeFixture(t, sourcePath, demoSourceBody)
	version := embeddedGuestVersion(t, embedded.PublishingLinterURI)

	for _, row := range []struct {
		name string
		args []string
		// wantChanges is every change the run makes beneath the workspace's
		// parent, as fingerprintDifferences words it.
		wantChanges []string
	}{
		{
			name:        "import naming no destination",
			args:        []string{"import", sourcePath, "--format", "demo"},
			wantChanges: []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"},
		},
		{
			name:        "a configuration write with --workspace",
			args:        []string{"--offline", "config", "init", "--workspace", "--force"},
			wantChanges: []string{"ws/.aggrega-config.yaml was modified"},
		},
		{
			name: "plugin gen-catalog with a relative --output-dir",
			args: []string{"plugin", "gen-catalog", "--plugin", linterPath, "--overlay", overlayPath, "--output-dir", "out"},
			wantChanges: []string{
				"ws/sub/out was created",
				"ws/sub/out/linter was created",
				"ws/sub/out/linter/publishing-linter was created",
				"ws/sub/out/linter/publishing-linter/<version> was created",
				"ws/sub/out/linter/publishing-linter/<version>/plugin.yaml was created",
			},
		},
		{
			name:        "pin of a relative entity path",
			args:        []string{"pin", "../catalog/local/recipe.yaml"},
			wantChanges: []string{"ws/catalog/local/entity.lock was created"},
		},
		{
			name: "skills install for a project",
			args: []string{"skills", "install", "--agent", "claude-code"},
			wantChanges: []string{
				"ws/.claude was created",
				"ws/.claude/skills was created",
				"ws/.claude/skills/aggrega was created",
				"ws/.claude/skills/aggrega/SKILL.md was created",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "ws")
			sub := filepath.Join(root, "sub")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatalf("create %s: %v", sub, err)
			}
			writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
			writeFixture(t, filepath.Join(root, "catalog", "local", "recipe.yaml"),
				"apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: local\n  version: \"1.0.0\"\n"+
					"spec:\n  capability: helm\n  implements: local\n  source: ./chart\n")
			writeFixture(t, filepath.Join(root, "catalog", "local", "chart", "Chart.yaml"),
				"apiVersion: v2\nname: local\nversion: 1.0.0\n")

			t.Chdir(sub)
			before := treeFingerprint(t, parent)

			cmd := newRootCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(row.args)
			if _, err := cmd.ExecuteContextC(context.Background()); err != nil {
				t.Fatalf("aggrega %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(row.args, " "), err,
					stdout.String(), stderr.String())
			}

			want := make([]string, 0, len(row.wantChanges))
			for _, change := range row.wantChanges {
				want = append(want, strings.ReplaceAll(change, guestVersionToken, version))
			}
			slices.Sort(want)
			if got := fingerprintDifferences(before, treeFingerprint(t, parent)); !slices.Equal(got, want) {
				t.Errorf("aggrega %s changed the fixture as %v; want exactly %v", strings.Join(row.args, " "), got, want)
			}
		})
	}
}
