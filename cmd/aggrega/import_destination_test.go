// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Where `aggrega import` writes its drafts, driven through the real command tree
// — the root command, its workspace resolution and its flags — with the import
// seam pointed at the demo importer guest.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// importDestinationLayout is one row's fixture: a workspace with a sub-folder,
// and beside it another folder, a directory outside the workspace and a place a
// link can lead to. Every path is absolute, and every one of them sits beneath
// parent, so one fingerprint of parent sees everything a run could touch.
type importDestinationLayout struct {
	parent, root, sub, other, outside, elsewhere, source string
}

// layImportDestination builds the fixture a row runs in.
func layImportDestination(t *testing.T) importDestinationLayout {
	t.Helper()
	parent := t.TempDir()
	l := importDestinationLayout{
		parent:    parent,
		root:      filepath.Join(parent, "ws"),
		other:     filepath.Join(parent, "other"),
		outside:   filepath.Join(parent, "outside"),
		elsewhere: filepath.Join(parent, "elsewhere"),
		source:    filepath.Join(parent, "demo-source.txt"),
	}
	l.sub = filepath.Join(l.root, "sub")
	for _, dir := range []string{l.sub, l.other, l.outside, l.elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	writeFixture(t, filepath.Join(l.root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, l.source, demoSourceBody)
	return l
}

// importPreambleLead opens the first line `aggrega import` writes on its
// commentary stream; the directory the drafted paths are relative to follows it.
const importPreambleLead = "read as the demo format, with drafted paths relative to "

// TestWhereAnImportWritesItsDrafts runs `aggrega import` through the real command
// tree from the workspace root, from a sub-folder of it, and from another folder
// naming the workspace with --workspace-root, with and without --output-dir, and
// reads where the drafts landed off the content digests of the whole fixture.
//
// The destination is a literal on every row. Naming none, the drafts land beneath
// the workspace root wherever the command runs, and the first line names that
// root. A relative --output-dir resolves against the folder the command runs in,
// as every typed path does. An absolute one outside the workspace is honoured
// where it was named, and a link beneath it leading out of it is refused with
// nothing written anywhere.
//
// 🔴 PLANTED DEFECTS:
//   - have outputDirFor (internal/importops/import.go) return "." rather than
//     rw.Root() for an empty name: every row naming no destination reddens except
//     the one run at the root itself, which then lands right by coincidence and
//     still names "." on its first line, so it reddens too.
//   - have the command hand OutputDir "." again (cmd/aggrega/import.go, the
//     importops.Options literal): the rows naming no destination from a
//     sub-folder and from another folder redden, their drafts landing in the
//     folder the command runs in; the row run at the root itself lands right,
//     since "." resolves to the root there and the first line names it in full.
//   - resolve a relative --output-dir against the workspace root instead
//     (internal/importops/import.go, have outputDirFor join rw.Root() with the
//     named directory rather than asking pathscope.DirectoryPath): the sub-folder
//     relative row reddens, its drafts landing in ws/drafts rather than
//     ws/sub/drafts.
//
// Not parallel: it points the shared import seam, a package-level variable, and
// moves the process working directory.
func TestWhereAnImportWritesItsDrafts(t *testing.T) {
	pointImportSeamAtDemoGuest(t)
	withCLIRegistrations(t, nil)
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")

	for _, row := range []struct {
		name string
		// from returns the folder the command runs in.
		from func(l importDestinationLayout) string
		// args returns the command line after the binary name.
		args func(l importDestinationLayout) []string
		// arrange prepares anything the row needs beyond the shared fixture.
		arrange func(t *testing.T, l importDestinationLayout)
		// wantRoot returns the directory the first line must name.
		wantRoot func(l importDestinationLayout) string
		// wantChanges is every change the run makes beneath the fixture, relative
		// to it, as fingerprintDifferences words it.
		wantChanges []string
		// reported says --dry-run is passed: standard output carries the drafts.
		reported bool
		// refused says the run must be refused with the write-leads-out halt.
		refused bool
	}{
		{
			name:        "naming no destination at the workspace root",
			from:        func(l importDestinationLayout) string { return l.root },
			args:        func(l importDestinationLayout) []string { return []string{"import", l.source, "--format", "demo"} },
			wantRoot:    func(l importDestinationLayout) string { return l.root },
			wantChanges: []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"},
		},
		{
			name:        "naming no destination from a sub-folder of the workspace",
			from:        func(l importDestinationLayout) string { return l.sub },
			args:        func(l importDestinationLayout) []string { return []string{"import", l.source, "--format", "demo"} },
			wantRoot:    func(l importDestinationLayout) string { return l.root },
			wantChanges: []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"},
		},
		{
			name: "naming no destination from another folder, the workspace named with --workspace-root",
			from: func(l importDestinationLayout) string { return l.other },
			args: func(l importDestinationLayout) []string {
				return []string{"--workspace-root", l.root, "import", l.source, "--format", "demo"}
			},
			wantRoot:    func(l importDestinationLayout) string { return l.root },
			wantChanges: []string{"ws/" + importops.RecordFileName + " was created", "ws/demo.yaml was created"},
		},
		{
			name: "naming no destination under --dry-run from a sub-folder",
			from: func(l importDestinationLayout) string { return l.sub },
			args: func(l importDestinationLayout) []string {
				return []string{"import", l.source, "--format", "demo", "--dry-run"}
			},
			wantRoot: func(l importDestinationLayout) string { return l.root },
			reported: true,
		},
		{
			name: "a relative --output-dir at the workspace root",
			from: func(l importDestinationLayout) string { return l.root },
			args: func(l importDestinationLayout) []string {
				return []string{"import", l.source, "--format", "demo", "--output-dir", "drafts"}
			},
			wantRoot: func(l importDestinationLayout) string { return filepath.Join(l.root, "drafts") },
			wantChanges: []string{"ws/drafts was created", "ws/drafts/" + importops.RecordFileName + " was created",
				"ws/drafts/demo.yaml was created"},
		},
		{
			name: "a relative --output-dir from a sub-folder resolves against that folder",
			from: func(l importDestinationLayout) string { return l.sub },
			args: func(l importDestinationLayout) []string {
				return []string{"import", l.source, "--format", "demo", "--output-dir", "drafts"}
			},
			wantRoot: func(l importDestinationLayout) string { return filepath.Join(l.sub, "drafts") },
			wantChanges: []string{"ws/sub/drafts was created",
				"ws/sub/drafts/" + importops.RecordFileName + " was created", "ws/sub/drafts/demo.yaml was created"},
		},
		{
			name: "an absolute --output-dir outside the workspace is honoured where it was named",
			from: func(l importDestinationLayout) string { return l.sub },
			args: func(l importDestinationLayout) []string {
				return []string{"import", l.source, "--format", "demo", "--output-dir", l.outside}
			},
			wantRoot:    func(l importDestinationLayout) string { return l.outside },
			wantChanges: []string{"outside/" + importops.RecordFileName + " was created", "outside/demo.yaml was created"},
		},
		{
			name: "a link beneath an --output-dir outside the workspace that leads out of it is refused",
			from: func(l importDestinationLayout) string { return l.sub },
			args: func(l importDestinationLayout) []string {
				return []string{"import", l.source, "--format", "demo", "--output-dir", l.outside}
			},
			arrange: func(t *testing.T, l importDestinationLayout) {
				target := filepath.Join(l.elsewhere, "target.yaml")
				writeFixture(t, target, "left as it was\n")
				if err := os.Symlink(target, filepath.Join(l.outside, "demo.yaml")); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			},
			refused: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			l := layImportDestination(t)
			if row.arrange != nil {
				row.arrange(t, l)
			}
			t.Chdir(row.from(l))
			before := treeFingerprint(t, l.parent)

			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(row.args(l))
			_, err := root.ExecuteContextC(context.Background())
			changes := fingerprintDifferences(before, treeFingerprint(t, l.parent))

			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v\nstderr:\n%s", err, stderr.String())
				}
				if len(changes) != 0 {
					t.Errorf("a refused import changed the fixture: %v", changes)
				}
				return
			}
			if err != nil {
				t.Fatalf("import: %v\nstderr:\n%s", err, stderr.String())
			}
			if !slices.Equal(changes, row.wantChanges) {
				t.Errorf("the import changed the fixture as %v; want exactly %v", changes, row.wantChanges)
			}

			var named []string
			for _, line := range strings.Split(stderr.String(), "\n") {
				if dir, ok := strings.CutPrefix(line, importPreambleLead); ok {
					named = append(named, dir)
				}
			}
			switch {
			case len(named) != 1:
				t.Errorf("the commentary stream names %d root(s) %v; want exactly one\n%s", len(named), named, stderr.String())
			case !filepath.IsAbs(named[0]):
				t.Errorf("the first line names %q; want the full path of %s", named[0], row.wantRoot(l))
			case !sameDirectory(t, named[0], row.wantRoot(l)):
				t.Errorf("the first line names %s; want %s", named[0], row.wantRoot(l))
			}

			if row.reported {
				docs, derr := authoredcontent.Decode(stdout.Bytes())
				if derr != nil {
					t.Fatalf("decode the reported drafts: %v\n%s", derr, stdout.String())
				}
				if len(docs) != 2 || docs[0].Path != "demo.yaml" || docs[0].Content != demoEchoBody ||
					docs[1].Path != importops.RecordFileName {
					t.Errorf("the reported drafts are %+v; want demo.yaml carrying the demo body, then the record", docs)
				}
				return
			}
			if want := "wrote demo.yaml\nwrote " + importops.RecordFileName + "\n"; stdout.String() != want {
				t.Errorf("the standard output stream carried %q; want %q", stdout.String(), want)
			}
		})
	}
}
