// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package editops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
)

// TestAnEditIsHeldToTheWorkspaceWhereItsDocumentLeads applies one edit to a
// document named each way the one judgement of inside tells apart, over a real
// workspace root beside a directory outside it, and reads back whether the edit
// was applied or refused, and what the outside directory holds afterwards.
//
// An edit reads its document before it writes it back, so a document that leads
// out of the workspace through a link — a link to a file outside, or a directory
// on the way linked out — is a file the workspace does not carry: it is refused,
// whichever spelling names it and whichever delivery is asked for, with nothing
// read or written. This is the edit's one confinement, which the command line, the
// language server and the assistant tools all reach. The first row is the control.
func TestAnEditIsHeldToTheWorkspaceWhereItsDocumentLeads(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// file names the document, given the root.
		file   func(root string) string
		report bool
		// refused says the edit must be refused as leading out of the workspace.
		refused bool
	}{
		{name: "control: a document inside the workspace is edited",
			file: func(string) string { return "storefront.aggregate.yaml" }},
		{name: "a document named by its absolute path inside the workspace is edited",
			file: func(root string) string { return filepath.Join(root, "storefront.aggregate.yaml") }},
		{name: "a document reached through a link that stays inside the workspace is edited",
			file: func(string) string { return "inner/storefront.aggregate.yaml" }},
		{name: "a document that is a link leading out of the workspace is refused",
			file: func(string) string { return "linked.aggregate.yaml" }, refused: true},
		{name: "a document named by its absolute path through a link leading out is refused",
			file: func(root string) string { return filepath.Join(root, "linked.aggregate.yaml") }, refused: true},
		{name: "a document inside a directory linked out of the workspace is refused",
			file: func(string) string { return "shared/storefront.aggregate.yaml" }, refused: true},
		{name: "a document leading out is refused when the edit is only reported, nothing being read",
			file: func(string) string { return "linked.aggregate.yaml" }, report: true, refused: true},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			for _, dir := range []string{root, filepath.Join(root, "real"), filepath.Join(outside, "shared")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("make %s: %v", dir, err)
				}
				if err := os.WriteFile(filepath.Join(dir, "storefront.aggregate.yaml"), []byte(authoredAggregate), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			if err := os.WriteFile(filepath.Join(outside, "outside.aggregate.yaml"), []byte(authoredAggregate), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			for target, at := range map[string]string{
				filepath.Join(outside, "outside.aggregate.yaml"): filepath.Join(root, "linked.aggregate.yaml"),
				filepath.Join(outside, "shared"):                 filepath.Join(root, "shared"),
				filepath.Join(root, "real"):                      filepath.Join(root, "inner"),
			} {
				if err := os.Symlink(target, at); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			before := outsideListing(t, outside)

			delivery := authoredcontent.DeliveryFor(row.report)
			res, err := Apply(t.Context(), root, Options{
				File:     row.file(root),
				Kind:     KindSetValue,
				Node:     []string{"spec", "components", "web"},
				Name:     "probe",
				Value:    "held",
				Delivery: delivery,
			})

			if after := outsideListing(t, outside); after != before {
				t.Errorf("the directory outside the workspace changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if row.refused {
				if !errors.Is(err, errFileLeadsOut) {
					t.Fatalf("want the leads-out refusal; got %v", err)
				}
				if len(res.Documents) != 0 || len(res.Written) != 0 {
					t.Errorf("a refused edit produced a document: %+v", res)
				}
				if info, lerr := os.Lstat(filepath.Join(root, "linked.aggregate.yaml")); lerr != nil || info.Mode()&fs.ModeSymlink == 0 {
					t.Errorf("the link standing in the workspace was replaced (lstat: %v)", lerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("the edit was refused: %v", err)
			}
			if len(res.Written) != 1 {
				t.Errorf("want the edited document written once; got %v", res.Written)
			}
		})
	}
}

// outsideListing lists every entry beneath dir with its type and bytes, so a
// listing taken before and after an edit differs exactly where something changed.
func outsideListing(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString(rel + " " + d.Type().String())
		if d.Type().IsRegular() {
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			b.WriteString(" " + string(data))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return b.String()
}
