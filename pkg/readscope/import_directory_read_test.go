// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package readscope_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestCheckImportDirectoryReadHoldsAReadToTheDirectoryNamed drives the entry point
// for a directory an import was named, over a directory that lies INSIDE a
// workspace, one fault per row beside controls: a file in the directory, a link to
// a file elsewhere inside the directory, a link to a file elsewhere in the
// workspace, and a directory link leading out of the workspace altogether.
//
// The third row is the one that tells this entry point from a read held to the
// workspace: the directory named is a root of its own wherever it lies, so a link
// leaving it for another place in the same workspace is refused here, where the
// same path read beneath a catalog directory of that workspace is read. Its verdict
// is otherwise CheckRead's against the directory — it is the one judgement, not a
// second one — and a refusal names the link, the directory, and the remedy an
// imported directory has rather than a catalog directory's.
func TestCheckImportDirectoryReadHoldsAReadToTheDirectoryNamed(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(workspace, "flows")
	mustWrite(t, filepath.Join(dir, "a.probe.json"))
	mustWrite(t, filepath.Join(dir, "kept", "b.probe.json"))
	mustWrite(t, filepath.Join(workspace, "elsewhere", "c.probe.json"))
	mustWrite(t, filepath.Join(outside, "more", "d.probe.json"))
	mustLinkBeneath(t, filepath.Join(dir, "kept", "b.probe.json"), filepath.Join(dir, "b.probe.json"))
	mustLinkBeneath(t, filepath.Join(workspace, "elsewhere", "c.probe.json"), filepath.Join(dir, "c.probe.json"))
	mustLinkBeneath(t, filepath.Join(outside, "more"), filepath.Join(dir, "more"))
	for _, row := range []struct {
		name string
		path string
		// link and leadsTo are what a refusal names, both empty for a row that reads.
		link, leadsTo string
	}{
		{name: "control: a file in the directory", path: filepath.Join(dir, "a.probe.json")},
		{name: "control: a link to a file elsewhere inside the directory", path: filepath.Join(dir, "b.probe.json")},
		{name: "a link to a file elsewhere in the workspace",
			path: filepath.Join(dir, "c.probe.json"),
			link: filepath.Join(dir, "c.probe.json"), leadsTo: filepath.Join(workspace, "elsewhere", "c.probe.json")},
		{name: "a directory link leading out",
			path: filepath.Join(dir, "more"),
			link: filepath.Join(dir, "more"), leadsTo: filepath.Join(outside, "more")},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckImportDirectoryRead(dir, row.path)
			if general := readscope.CheckRead(dir, row.path); (general == nil) != (err == nil) {
				t.Fatalf("the verdict must be CheckRead's against the directory; CheckRead %v, "+
					"CheckImportDirectoryRead %v", general, err)
			}
			if row.link == "" {
				if err != nil {
					t.Fatalf("refused a read the directory holds: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			for _, want := range []string{
				"the link " + row.link + " leads to " + resolved(t, row.leadsTo),
				"which must stay inside " + dir,
				"A directory you import is read only from inside itself",
				"keep the file itself in the directory rather than a link to it",
				"import the directory the link leads to",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "as a catalog directory") {
				t.Errorf("listing a catalog directory reaches no imported file, so the remedy must not offer one:\n%v", err)
			}
		})
	}
	// The workspace holds the third row's link: read beneath the workspace, it is
	// inside. Only the directory being its own root refuses it.
	if err := readscope.CheckRead(workspace, filepath.Join(dir, "c.probe.json")); err != nil {
		t.Errorf("the workspace holds a link to elsewhere inside it; the control read against the workspace "+
			"refused it: %v", err)
	}
	if err := readscope.CheckImportDirectoryRead("", filepath.Join(dir, "c.probe.json")); err != nil {
		t.Errorf("with no root nothing is held; got %v", err)
	}
}
