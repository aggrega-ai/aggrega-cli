// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package exampleops

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
)

// TestScaffoldExampleRequestFormReadsEachFormAndRefusesMixedFields pins the one
// statement of which fields go together: each form read from its own fields, and
// every mixture refused naming the fields it mixed. A positive row per form keeps
// each refusal row failing on its own mixture rather than on a request no form
// would accept.
func TestScaffoldExampleRequestFormReadsEachFormAndRefusesMixedFields(t *testing.T) {
	type req struct {
		kind, name, example, dir string
		list                     bool
	}
	for _, tc := range []struct {
		scenario string
		req      req
		want     Form
		refuses  []string
	}{
		{scenario: "a kind drafts", req: req{kind: "component"}, want: FormDraft},
		{scenario: "a kind and a name draft", req: req{kind: "component", name: "api"}, want: FormDraft},
		{scenario: "nothing at all is the drafting form, refused by each surface for its kind", want: FormDraft},
		{scenario: "list lists", req: req{list: true}, want: FormList},
		{scenario: "an example writes", req: req{example: "demo"}, want: FormWrite},
		{scenario: "an example and a directory write", req: req{example: "demo", dir: "here"}, want: FormWrite},
		{scenario: "a blank example is no example", req: req{example: "  ", kind: "component"}, want: FormDraft},
		{scenario: "a kind with an example", req: req{kind: "component", example: "demo"}, refuses: []string{"`kind`", "`example`"}},
		{scenario: "list with an example", req: req{list: true, example: "demo"}, refuses: []string{"`list`", "`example`"}},
		{scenario: "list with a kind", req: req{list: true, kind: "component"}, refuses: []string{"`list`", "`kind`"}},
		{scenario: "a name with an example", req: req{name: "api", example: "demo"}, refuses: []string{"`name`", "`dir`"}},
		{scenario: "a name with list", req: req{name: "api", list: true}, refuses: []string{"`name`"}},
		{scenario: "a directory with no example", req: req{dir: "here"}, refuses: []string{"`dir`", "`example`"}},
		{scenario: "a directory beside a kind", req: req{kind: "component", dir: "here"}, refuses: []string{"`dir`"}},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			got, err := RequestForm(tc.req.kind, tc.req.name, tc.req.example, tc.req.dir, tc.req.list)
			if tc.refuses == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got != tc.want {
					t.Fatalf("read as form %d, want %d", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("a mixed request was read as form %d", got)
			}
			for _, field := range tc.refuses {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("the refusal does not name %s: %v", field, err)
				}
			}
		})
	}
}

// TestScaffoldExampleCheckNameRefusesAPath pins that a name a surface would
// compose a directory from is refused as no example before it can be judged as a
// path, and that a plain directory name passes.
func TestScaffoldExampleCheckNameRefusesAPath(t *testing.T) {
	if err := CheckName("redis-cache"); err != nil {
		t.Fatalf("a plain name was refused: %v", err)
	}
	for _, name := range []string{"", ".", "..", "../x", "a/b", "/abs"} {
		if err := CheckName(name); !errors.Is(err, docscache.ErrPageNotFound()) {
			t.Errorf("%q answered %v, want the page-not-found refusal", name, err)
		}
	}
}

// TestScaffoldExampleReportNamesTheDirectoryAndTheNextCommands pins the account a
// reader is shown of an example written, both with the page's commands and
// without them.
func TestScaffoldExampleReportNamesTheDirectoryAndTheNextCommands(t *testing.T) {
	with := Written{Name: "demo", Dir: "my-demo", Files: []string{"README.md", "a.yaml"}, Next: []string{"# Compose", "", "aggrega build"}}
	want := "Wrote the example demo into my-demo (2 files).\n\nNext:\n\n  cd my-demo\n  # Compose\n\n  aggrega build\n" +
		"\nIts page, my-demo/README.md, explains what it shows.\n"
	if got := with.Report(); got != want {
		t.Errorf("the account reads\n%q\nwant\n%q", got, want)
	}
	without := Written{Name: "demo", Dir: "d", Files: []string{"README.md"}}
	want = "Wrote the example demo into d (1 file).\n\nNext:\n\n  cd d\n\nIts page, d/README.md, says how to build it.\n"
	if got := without.Report(); got != want {
		t.Errorf("the account without commands reads\n%q\nwant\n%q", got, want)
	}
}
