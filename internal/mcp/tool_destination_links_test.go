// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAToolWritingThroughALinkLeadingOutIsRefused drives the tools that write
// where their call names — the catalog generation's output directory, the pin's
// entity and the structured edit's document — and the insertion-point tool, over
// one real root holding a linked directory leading out (shared), one staying
// inside (inner), and a link to a document outside (linked-entity.yaml), and reads
// back whether each call was refused, in what words, where the generation was
// pointed, and what the directory outside the root holds afterwards.
//
// Over a listener a destination is judged by the deepest part of it that exists,
// so a directory not made yet beneath a link leading out is refused as the link
// itself is. On the process's own streams a path is taken as it is named, so the
// generation is pointed where the call names, a link leading out included: the row
// pinning that is the transport's rule, not a gap. The edit and the insertion
// point confine their document on both transports by their own check, and that
// check is held to the link judgement on both.
//
// The generation runs through a stand-in that records where it was pointed and
// writes nothing, so a refused call is one the stand-in never saw. The first row
// is the control, a directory not made yet inside the root, over a listener.
func TestAToolWritingThroughALinkLeadingOutIsRefused(t *testing.T) {
	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, machineWide, "registries: []\n")
	t.Setenv(workspaceconfig.EnvConfigOverride, machineWide)

	const (
		listenerLeadsOut = "leads outside that root through a link."
		editLeadsOut     = "the document named leads outside the workspace through a link, and an edit is only applied " +
			"to a document the workspace carries"
		insertionLeadsOut = "the document named leads outside the workspace through a link, and a position is only " +
			"reported for a document the workspace carries"
		admittedToTheEdit = "the edit named is not one this capability applies"
	)
	generation := func(outputDir string) map[string]any {
		return map[string]any{"from_embedded": true, "overlay": "overlays", "output_dir": outputDir}
	}
	rows := []struct {
		name      string
		transport transportMode
		tool      string
		args      map[string]any
		// generatedInto is where the generation must have been pointed, relative to
		// the root; empty where the call names no generation or is refused.
		generatedInto string
		// want is a fragment the answer carries: the refusal, or the next stage's
		// answer to an admitted call; empty where the call simply succeeds.
		want    string
		refused bool
	}{
		{
			name:          "over a listener a generation into a directory not made yet inside the workspace is admitted",
			transport:     transportHTTP,
			tool:          "gen-catalog",
			args:          generation("generated"),
			generatedInto: "generated",
		},
		{
			name:      "over a listener a generation into a linked directory leading out is refused, quoting the value sent",
			transport: transportHTTP,
			tool:      "gen-catalog",
			args:      generation("shared"),
			want:      `gen-catalog's 'output_dir' must name a path inside the workspace this server serves, written relative to that workspace's root; "shared" ` + listenerLeadsOut,
			refused:   true,
		},
		{
			name:      "over a listener a generation into a directory not made yet beneath a link leading out is refused",
			transport: transportHTTP,
			tool:      "gen-catalog",
			args:      generation("shared/new"),
			want:      `"shared/new" ` + listenerLeadsOut,
			refused:   true,
		},
		{
			name:          "over a listener a generation into a directory not made yet beneath a link inside is admitted",
			transport:     transportHTTP,
			tool:          "gen-catalog",
			args:          generation("inner/new"),
			generatedInto: "inner/new",
		},
		{
			name:      "over a listener a pin of an entity reached through a link leading out is refused",
			transport: transportHTTP,
			tool:      "pin",
			args:      map[string]any{"entity": "linked-entity.yaml"},
			want:      `"linked-entity.yaml" ` + listenerLeadsOut,
			refused:   true,
		},
		{
			name:      "over a listener an edit of a document reached through a link leading out is refused",
			transport: transportHTTP,
			tool:      editDocumentToolName,
			args:      map[string]any{"file": "linked-entity.yaml", "edit": "no-such-edit"},
			want:      `"linked-entity.yaml" ` + listenerLeadsOut,
			refused:   true,
		},
		{
			name:      "on the process's own streams an edit of a document inside the workspace is admitted",
			transport: transportStdio,
			tool:      editDocumentToolName,
			args:      map[string]any{"file": "entity.yaml", "edit": "no-such-edit"},
			want:      admittedToTheEdit,
		},
		{
			name:      "on the process's own streams an edit of a document reached through a link leading out is refused",
			transport: transportStdio,
			tool:      editDocumentToolName,
			args:      map[string]any{"file": "linked-entity.yaml", "edit": "no-such-edit"},
			want:      editLeadsOut,
			refused:   true,
		},
		{
			name:      "on the process's own streams an insertion point in a document inside the workspace is reported",
			transport: transportStdio,
			tool:      "insertion-point",
			args:      map[string]any{"file": "entity.yaml", "container": []string{"metadata"}},
		},
		{
			name:      "on the process's own streams an insertion point in a document reached through a link leading out is refused",
			transport: transportStdio,
			tool:      "insertion-point",
			args:      map[string]any{"file": "linked-entity.yaml", "container": []string{"metadata"}},
			want:      insertionLeadsOut,
			refused:   true,
		},
		{
			name:          "on the process's own streams a generation is pointed where the call names, a link leading out included",
			transport:     transportStdio,
			tool:          "gen-catalog",
			args:          generation("shared/new"),
			generatedInto: "shared/new",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeLinkedDirsFile(t, filepath.Join(outside, "shared", "kept.txt"), "kept\n")
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-entity.yaml"), linksTestComponent("outside-only-component"))
			writeLinkedDirsFile(t, filepath.Join(root, "entity.yaml"), linksTestComponent("inside-component"))
			writeLinkedDirsFile(t, filepath.Join(root, "real", "kept.txt"), "kept\n")
			linkLinkedDirs(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
			linkLinkedDirs(t, filepath.Join(root, "real"), filepath.Join(root, "inner"))
			linkLinkedDirs(t, filepath.Join(outside, "outside-entity.yaml"), filepath.Join(root, "linked-entity.yaml"))
			writeLinkedDirsFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")

			s := &server{out: io.Discard, logw: io.Discard, startDir: root, transport: row.transport}
			pointedAt := ""
			s.runGenCatalog = func(_ context.Context, opts gencatalog.Options, _ io.Writer) error {
				pointedAt = opts.OutputDir
				return nil
			}
			before := outsideListing(t, outside)

			res := callToolOn(t, s, listenerRequest(), row.tool, row.args)
			text := res.Content[0].Text

			if after := outsideListing(t, outside); after != before {
				t.Errorf("the directory outside the root changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if row.refused {
				if !res.IsError {
					t.Fatalf("%s was admitted; want it refused with %q:\n%s", row.tool, row.want, text)
				}
				if pointedAt != "" {
					t.Errorf("a refused generation was pointed at %s", pointedAt)
				}
			} else if res.IsError && row.want == "" {
				t.Fatalf("%s was refused; want it admitted: %s", row.tool, text)
			}
			if row.want != "" && !strings.Contains(text, row.want) {
				t.Errorf("the answer reads\n  %s\nwant it to carry\n  %s", text, row.want)
			}
			if row.generatedInto != "" {
				if want := filepath.Join(root, filepath.FromSlash(row.generatedInto)); pointedAt != want {
					t.Errorf("the generation was pointed at %q; want %q", pointedAt, want)
				}
			}
			if row.transport == transportHTTP && strings.Contains(text, outside) {
				t.Errorf("the answer names %s, a place a caller reaching a listener is not told: %s", outside, text)
			}
		})
	}
}

// outsideListing lists every entry under dir, links not followed, with what each
// is and how large, one per line, so two listings taken before and after a call
// differ exactly where something was written.
func outsideListing(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
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
