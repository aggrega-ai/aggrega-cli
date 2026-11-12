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

// TestCheckReferenceHoldsAReferenceToItsRoot drives the judgement over one
// workspace beside a directory outside it, one fault per row beside controls.
//
// A reference differs from a read beneath a directory read as a whole in the row
// that matters most here: a reference written to climb out of its root is refused
// though no link carries it, because it is the author's words rather than a
// listing. The root is the workspace for a document inside it, and the document's
// own directory for one named from outside the workspace — so a reference that
// stays beside such a document passes, one that climbs out of its directory is
// refused, and a reference written further along its chain is held to that same
// directory rather than to the directory it is resolved from. With no workspace
// root nothing is held.
func TestCheckReferenceHoldsAReferenceToItsRoot(t *testing.T) {
	base := t.TempDir()
	ws, outside := filepath.Join(base, "ws"), filepath.Join(base, "outside")
	named := filepath.Join(outside, "releases")
	mustWrite(t, filepath.Join(ws, "aggregate.yaml"))
	mustWrite(t, filepath.Join(outside, "aggregate.yaml"))
	mustWrite(t, filepath.Join(named, "release.yaml"))
	mustWrite(t, filepath.Join(named, "aggregate.yaml"))
	mustWrite(t, filepath.Join(named, "base.yaml"))
	mustWrite(t, filepath.Join(named, "envs", "mid.yaml"))
	mustLink(t, outside, filepath.Join(ws, "shared"))

	in := func(target string) readscope.Reference {
		return readscope.Reference{
			Document: filepath.Join(ws, "release.yaml"), Anchor: ws, From: ws, Key: ".spec.aggregate",
			Value: target, Target: filepath.Join(ws, target),
		}
	}
	fromNamed := func(document, target string) readscope.Reference {
		from := filepath.Dir(document)
		return readscope.Reference{
			Document: document, Anchor: named, From: from, Key: ".spec.extends",
			Value: target, Target: filepath.Join(from, target),
		}
	}
	insideTheWorkspace := "outside the workspace " + ws
	insideTheNamedDirectory := "outside " + named + ", the directory of a document named outside the workspace " + ws
	rows := []struct {
		name      string
		root      string
		ref       readscope.Reference
		refused   bool
		mustStay  string
		link      string
		remedyFor string
	}{
		{name: "control: a file beside the document", root: ws, ref: in("./aggregate.yaml")},
		{name: "control: a file not there yet beneath the workspace", root: ws, ref: in("./later/aggregate.yaml")},
		{name: "a reference climbing out as written", root: ws, ref: in("../outside/aggregate.yaml"), refused: true,
			mustStay: insideTheWorkspace, remedyFor: "A document in the workspace reaches only files inside " + ws},
		{name: "a reference climbing out to a file not there", root: ws, ref: in("../outside/missing.yaml"), refused: true,
			mustStay: insideTheWorkspace, remedyFor: "A document in the workspace reaches only files inside " + ws},
		{name: "a reference a link carries out", root: ws, ref: in("./shared/aggregate.yaml"), refused: true,
			mustStay: insideTheWorkspace, link: filepath.Join(ws, "shared")},
		{name: "control: a document named outside the workspace, its reference beside it", root: ws,
			ref: fromNamed(filepath.Join(named, "release.yaml"), "./aggregate.yaml")},
		{name: "a document named outside the workspace, its reference climbing out of its own directory", root: ws,
			ref: fromNamed(filepath.Join(named, "release.yaml"), "../aggregate.yaml"), refused: true,
			mustStay:  insideTheNamedDirectory,
			remedyFor: "reaches only files inside its own directory " + named},
		{name: "control: a link of a chain named outside the workspace, held to the chain's directory, not its own", root: ws,
			ref: fromNamed(filepath.Join(named, "envs", "mid.yaml"), "../base.yaml")},
		{name: "a link of a chain named outside the workspace, climbing out of the chain's directory", root: ws,
			ref: fromNamed(filepath.Join(named, "envs", "mid.yaml"), "../../aggregate.yaml"), refused: true,
			mustStay: insideTheNamedDirectory},
		{name: "held: no workspace root", root: "", ref: in("../outside/aggregate.yaml")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckReference(row.root, row.ref)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a reference nothing holds out: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReferenceLeadsOutOfWorkspace()) {
				t.Fatalf("want the reference-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0324" {
				t.Errorf("short code = %q, want E0324", code)
			}
			wants := []string{
				row.ref.Key + ": " + row.ref.Value,
				"written in " + row.ref.Document,
				"resolved from " + row.ref.From,
				"which leads to " + filepath.Clean(row.ref.Target),
				row.mustStay,
			}
			if row.remedyFor != "" {
				wants = append(wants, row.remedyFor)
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q:\n%v", want, err)
				}
			}
			if row.link != "" && !strings.Contains(err.Error(), "the link "+row.link+" leads to "+resolved(t, outside)) {
				t.Errorf("the refusal must name the link %s and where it leads:\n%v", row.link, err)
			}
		})
	}
}
